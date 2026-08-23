// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
	"github.com/lennylabs/lenny/pkg/sessionrecord"
)

// spec: §7.2 (per-slot inbox routing on the coordinating gateway replica),
// §5.2 (per-session multiplexing), §28.5.3 (the outbound envelope carries
// sessionId on every pod).
//
// This is the tier-2 gateway component test for the per-slot message routing
// the gateway owns. It backs the binder with an envtest cluster (the §5.2
// slot-claim path uses SSA, which the fake client does not implement), wires a
// PodExecutor over a slot-recording adapter, and drives the §15.1
// POST /v1/sessions/{id}/messages handler. The §6.4 slot is 1:1 with the
// session, so the session identifier is the address the gateway puts on the
// outbound adapter envelope; a bind that resolved no session identifier
// fails closed on the internal §7.2 dispatch invariant.

// perSlotAdapter records the address the PodExecutor stamped on each
// session's Attach binding frame and the sessionId it stamped on each
// forwarded message envelope, so a component test can assert per-session
// routing and outbound stamping. It keys captures by the Attach binding
// address observed first on each stream. The envelope decode reads the
// wire key off a struct tag, so a producer-side rename that this fake
// does not follow yields the empty string rather than a build failure.
type perSlotAdapter struct {
	adapterv1.UnimplementedAdapterServer

	mu        sync.Mutex
	bindSlots []string // the session address on each opened Attach stream, in open order
	envSlots  []string // sessionId on each forwarded envelope
}

func (a *perSlotAdapter) Attach(stream grpc.BidiStreamingServer[adapterv1.AttachRequest, adapterv1.AttachResponse]) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.bindSlots = append(a.bindSlots, first.GetSessionId().GetValue())
	a.mu.Unlock()
	for {
		req, err := stream.Recv()
		if err != nil {
			return nil //nolint:nilerr // EOF/cancel ends the capture cleanly.
		}
		if env := req.GetEnvelopeJson(); len(env) > 0 {
			var decoded struct {
				SessionID string `json:"sessionId"`
			}
			_ = json.Unmarshal(env, &decoded)
			a.mu.Lock()
			a.envSlots = append(a.envSlots, decoded.SessionID)
			a.mu.Unlock()
			_ = stream.Send(&adapterv1.AttachResponse{
				EnvelopeJson: []byte(`{"type":"response","text":"ack"}`),
			})
		}
	}
}

func (a *perSlotAdapter) snapshot() (bindSlots, envSlots []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.bindSlots...), append([]string(nil), a.envSlots...)
}

// dialPerSlotAdapter serves rec over bufconn and returns a connected client.
func dialPerSlotAdapter(t *testing.T, rec *perSlotAdapter) *adapterclient.Client {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	adapterv1.RegisterAdapterServer(gs, rec)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	cl, err := adapterclient.Dial("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial adapter: %v", err)
	}
	t.Cleanup(func() { _ = cl.Close() })
	return cl
}

// concurrentRoutingServer wires a gateway whose executor is a PodExecutor over
// an envtest-backed binder and the given registry, plus the §7.2 inbox
// coordinator so the buffered (input_required) path exercises real machinery.
func concurrentRoutingServer(t *testing.T, reg *podsession.Registry) (*sessionserver.Server, sessionstore.Store, *sessioninbox.MemoryInbox) {
	t.Helper()
	cluster := podBindClient(t, &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sbx-c", Namespace: podTestNS},
		Status:     lennyv1.SandboxStatus{Phase: string(state.Claimed), PodIP: "10.0.0.9"},
	})
	binder := &podsession.Binder{Client: cluster, Namespace: podTestNS}

	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	inbox := sessioninbox.NewMemoryInbox(10)
	coord := sessioninbox.NewCoordinator(sessioninbox.Config{
		Inbox: inbox,
		DLQ:   sessioninbox.NewDLQ(rc, 10),
	})

	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		Executor:    executor.NewPodExecutor(reg, binder),
		Transcripts: transcriptstore.NewMemory(),
		Messaging:   coord,
	})
	return srv, store, inbox
}

func seedConcurrentSession(t *testing.T, store sessionstore.Store, id string, st session.State) {
	t.Helper()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", State: st, RuntimeRef: "echo",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

// TestMessagesPerSlotRoutingOnConcurrentPod is the per-slot routing component
// test. Two sessions share one maxConcurrentSessions > 1 pod in distinct
// slots: slot 01 is running and slot 02 is in input_required. The gateway
// resolves each session to its bound slot, delivers to slot 01 (path 2) while
// slot 02 buffers (path 3), and stamps the addressed session on the
// outbound adapter envelope. A client-supplied address is ignored; the
// session the request names governs.
//
// spec: 7.2 (per-slot inbox routing), 5.2 (per-session multiplexing),
// 28.5.3 (the outbound envelope carries sessionId on every pod)
//
// diagnosis: a failure means the gateway's per-slot message routing over the
// single pod regressed — either it no longer stamps the addressed session
// outbound, it lets one slot's input_required block a sibling
// slot's delivery, or it began honoring a client-supplied address.
func TestMessagesPerSlotRoutingOnConcurrentPod(t *testing.T) {
	rec01 := &perSlotAdapter{}
	rec02 := &perSlotAdapter{}
	cl01 := dialPerSlotAdapter(t, rec01)
	cl02 := dialPerSlotAdapter(t, rec02)

	reg := podsession.NewRegistry()
	reg.Put(&podsession.BindResult{
		SessionID: "sess-slot-01", TenantID: "acme", SandboxName: "sbx-c",
		Adapter: cl01, SlotID: "slot_01",
	})
	reg.Put(&podsession.BindResult{
		SessionID: "sess-slot-02", TenantID: "acme", SandboxName: "sbx-c",
		Adapter: cl02, SlotID: "slot_02",
	})

	srv, store, inbox := concurrentRoutingServer(t, reg)
	seedConcurrentSession(t, store, "sess-slot-01", session.StateRunning)
	seedConcurrentSession(t, store, "sess-slot-02", session.StateInputRequired)

	// Slot 01 (running) delivers (path 2) even though slot 02 is blocked.
	rr := sendMessageRequest(t, srv.Handler(), "sess-slot-01", sessionserver.MessageRequest{
		Messages: []sessionserver.MessagePayload{{Role: "user", Content: sessionrecord.MessageContentFromText("to-slot-01")}},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("slot 01 status %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp01 sessionserver.MessageResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp01); err != nil {
		t.Fatalf("decode slot 01: %v", err)
	}
	if resp01.DeliveryReceipt.Status != session.DeliveryStatusDelivered {
		t.Errorf("slot 01 status = %q, want delivered (a sibling slot in input_required must not block it)", resp01.DeliveryReceipt.Status)
	}

	// Slot 02 (input_required) buffers (path 3) independently.
	rr = sendMessageRequest(t, srv.Handler(), "sess-slot-02", sessionserver.MessageRequest{
		Messages: []sessionserver.MessagePayload{{Role: "user", Content: sessionrecord.MessageContentFromText("to-slot-02")}},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("slot 02 status %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp02 sessionserver.MessageResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp02)
	if resp02.DeliveryReceipt.Status != session.DeliveryStatusQueued {
		t.Errorf("slot 02 status = %q, want queued (input_required buffers)", resp02.DeliveryReceipt.Status)
	}
	if n, _ := inbox.Len(context.Background(), "acme", "sess-slot-02"); n != 1 {
		t.Errorf("slot 02 inbox depth = %d, want 1", n)
	}

	// The gateway addressed each Attach stream by its own session and
	// stamped that session on the outbound envelope.
	bind01, env01 := rec01.snapshot()
	if len(bind01) == 0 || bind01[0] != "sess-slot-01" {
		t.Errorf("session Attach binding address = %v, want [sess-slot-01]", bind01)
	}
	if len(env01) == 0 || env01[0] != "sess-slot-01" {
		t.Errorf("slot 01 outbound envelope sessionId = %v, want sess-slot-01 stamped", env01)
	}
	// Slot 02 buffered in the gateway inbox, so it never reached the adapter
	// stream — the buffered path is a gateway concern, not an adapter one.
	if _, env02 := rec02.snapshot(); len(env02) != 0 {
		t.Errorf("slot 02 forwarded an envelope to the adapter %v; an input_required buffer must not reach the adapter", env02)
	}
}

// TestMessagesIgnoresAClientAddressAndRoutesBySession confirms a
// legitimate inbound message that carries a stray client address on a
// maxConcurrentSessions > 1 pod is routed by the session the request names
// rather than rejected, and the gateway stamps its own address outbound.
//
// spec: 7.2 (per-slot routing; the address is internal), 5.2, 28.5.3
//
// diagnosis: a failure means the gateway began honoring a client-supplied
// address or rejecting a message that carries none on a concurrent pod,
// rather than deriving the address from the session.
func TestMessagesIgnoresAClientAddressAndRoutesBySession(t *testing.T) {
	rec := &perSlotAdapter{}
	cl := dialPerSlotAdapter(t, rec)
	reg := podsession.NewRegistry()
	reg.Put(&podsession.BindResult{
		SessionID: "sess-slot-01", TenantID: "acme", SandboxName: "sbx-c",
		Adapter: cl, SlotID: "slot_01",
	})
	srv, store, _ := concurrentRoutingServer(t, reg)
	seedConcurrentSession(t, store, "sess-slot-01", session.StateRunning)

	// A raw body that carries a stray client address pointing at a
	// different session. The gateway must ignore it and route by the
	// session the request names.
	rr := sendRawMessageRequest(t, srv.Handler(), "sess-slot-01",
		`{"messages":[{"content":"hello","sessionId":"sess-slot-99"}]}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("message carrying a stray client address rejected: %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp sessionserver.MessageResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.DeliveryReceipt.Status != session.DeliveryStatusDelivered {
		t.Errorf("status = %q, want delivered", resp.DeliveryReceipt.Status)
	}
	if _, env := rec.snapshot(); len(env) == 0 || env[0] != "sess-slot-01" {
		t.Errorf("outbound envelope sessionId = %v, want the addressed sess-slot-01 (not the client sess-slot-99)", env)
	}
}

// TestMessagesAddressesTheStreamBySessionOnAPoolOfOne confirms that a bind
// with no separate slot recorded still addresses its Attach stream by the
// session identifier, which under §5.2 names the slot that session holds
// on the pod, and that the outbound envelope carries that same identifier.
// A bind on a pool of one records no separate slot, so an envelope filled
// from the bind's slot would emit no address at all on exactly the pool
// §28.5.3 exists to normalize.
//
// spec: 7.2 (per-session routing), 5.2, 28.5.3 (the outbound envelope
// carries sessionId on every pod, whatever the pool's concurrency)
//
// diagnosis: a failure means the stream or the envelope carries a separate
// slot address again, which a bind on a pool of one leaves empty and the
// adapter cannot attribute.
func TestMessagesAddressesTheStreamBySessionOnAPoolOfOne(t *testing.T) {
	rec := &perSlotAdapter{}
	cl := dialPerSlotAdapter(t, rec)
	reg := podsession.NewRegistry()
	// A bind on a pool of one: no separate slot recorded.
	reg.Put(&podsession.BindResult{
		SessionID: "sess-excl", TenantID: "acme", SandboxName: "sbx-c",
		Adapter: cl,
	})
	srv, store, _ := concurrentRoutingServer(t, reg)
	seedConcurrentSession(t, store, "sess-excl", session.StateRunning)

	rr := sendMessageRequest(t, srv.Handler(), "sess-excl", sessionserver.MessageRequest{
		Messages: []sessionserver.MessagePayload{{Role: "user", Content: sessionrecord.MessageContentFromText("hello")}},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("exclusive status %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp sessionserver.MessageResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.DeliveryReceipt.Status != session.DeliveryStatusDelivered {
		t.Errorf("exclusive status = %q, want delivered", resp.DeliveryReceipt.Status)
	}
	bind, env := rec.snapshot()
	if len(bind) == 0 || bind[0] != "sess-excl" {
		t.Errorf("Attach binding address = %v, want [sess-excl]", bind)
	}
	if len(env) == 0 || env[0] != "sess-excl" {
		t.Errorf("outbound envelope sessionId = %v, want [sess-excl]; the envelope carries the addressed session on a pool of one too", env)
	}
}
