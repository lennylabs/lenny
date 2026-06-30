// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// stubAdapterServer is a minimal gRPC Adapter that records each
// Interrupt call and returns the configured response status, so the
// gateway interrupt path can be driven end-to-end without spinning up a
// real pod.
type stubAdapterServer struct {
	adapterv1.UnimplementedAdapterServer
	status          adapterv1.InterruptResponse_Status
	called          atomic.Int32
	lastReq         atomic.Pointer[adapterv1.InterruptRequest]
	deadlineCalled  atomic.Int32
	lastDeadlineReq atomic.Pointer[adapterv1.SignalDeadlineRequest]
}

func (s *stubAdapterServer) Interrupt(ctx context.Context, req *adapterv1.InterruptRequest) (*adapterv1.InterruptResponse, error) {
	s.called.Add(1)
	s.lastReq.Store(req)
	return &adapterv1.InterruptResponse{
		Acknowledged: s.status == adapterv1.InterruptResponse_STATUS_ACKNOWLEDGED,
		Status:       s.status,
	}, nil
}

func (s *stubAdapterServer) SignalDeadline(_ context.Context, req *adapterv1.SignalDeadlineRequest) (*adapterv1.SignalDeadlineResponse, error) {
	s.deadlineCalled.Add(1)
	s.lastDeadlineReq.Store(req)
	return &adapterv1.SignalDeadlineResponse{Delivered: true}, nil
}

// dialStubAdapter starts the supplied stub on an in-process listener
// and returns a connected adapterclient.Client + cleanup.
func dialStubAdapter(t *testing.T, stub *stubAdapterServer) *adapterclient.Client {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	adapterv1.RegisterAdapterServer(srv, stub)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.GracefulStop() })

	cl, err := adapterclient.Dial(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial stub adapter: %v", err)
	}
	t.Cleanup(func() { cl.Close() })
	return cl
}

// spec: §7.2 line 168 — interrupt_request + interrupt_acknowledged
// transitions running → suspended. The gateway must signal the adapter
// AND the row must reach suspended.
func TestInterruptSignalsAdapterAndSuspendsOnAck_spec_7_2(t *testing.T) {
	store := memstore.New()
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_int", TenantID: "acme", RuntimeRef: "echo",
		State:     session.StateRunning,
		CreatedAt: now, UpdatedAt: now,
	})
	stub := &stubAdapterServer{status: adapterv1.InterruptResponse_STATUS_ACKNOWLEDGED}
	adapter := dialStubAdapter(t, stub)
	reg := podsession.NewRegistry()
	reg.Put(&podsession.BindResult{SessionID: "sess_int", TenantID: "acme", Adapter: adapter})

	srv := sessionserver.New(store, sessionserver.Options{PodRegistry: reg})

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_int/interrupt", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if stub.called.Load() != 1 {
		t.Errorf("adapter.Interrupt called %d times, want 1", stub.called.Load())
	}
	got, _ := store.Get(context.Background(), "acme", "sess_int")
	if got.State != session.StateSuspended {
		t.Errorf("row state = %s, want suspended", got.State)
	}
	// Successful ack omits the interruptStatus field.
	if strings.Contains(rr.Body.String(), `"interruptStatus":"timeout"`) {
		t.Errorf("ack response should not advertise interrupt timeout: %s", rr.Body.String())
	}
}

// spec: §7.2 line 169 / §4.7 — deadlineMs elapsed without ack: adapter
// forces suspended, RPC returns INTERRUPT_TIMEOUT. The row STILL flips
// to suspended; the response advertises the timeout so a UI can flag it.
func TestInterruptForcesSuspendedOnTimeout_spec_7_2(t *testing.T) {
	store := memstore.New()
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_to", TenantID: "acme", RuntimeRef: "echo",
		State:     session.StateRunning,
		CreatedAt: now, UpdatedAt: now,
	})
	stub := &stubAdapterServer{status: adapterv1.InterruptResponse_STATUS_INTERRUPT_TIMEOUT}
	adapter := dialStubAdapter(t, stub)
	reg := podsession.NewRegistry()
	reg.Put(&podsession.BindResult{SessionID: "sess_to", TenantID: "acme", Adapter: adapter})

	srv := sessionserver.New(store, sessionserver.Options{PodRegistry: reg})

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_to/interrupt", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	got, _ := store.Get(context.Background(), "acme", "sess_to")
	if got.State != session.StateSuspended {
		t.Errorf("row state = %s, want suspended (§7.2 line 169 adapter-forces)", got.State)
	}

	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("parse body: %v", err)
	}
	if body["interruptStatus"] != "timeout" {
		t.Errorf("interruptStatus = %v, want \"timeout\"", body["interruptStatus"])
	}
}

// spec: §4.7 STATUS_BUSY — the adapter rejected the call because
// another op holds the per-session lock. The row stays running and the
// gateway returns 409 INTERRUPT_BUSY so the client retries.
func TestInterruptReturnsBusyAndLeavesRowRunning_spec_4_7(t *testing.T) {
	store := memstore.New()
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_busy", TenantID: "acme", RuntimeRef: "echo",
		State:     session.StateRunning,
		CreatedAt: now, UpdatedAt: now,
	})
	stub := &stubAdapterServer{status: adapterv1.InterruptResponse_STATUS_BUSY}
	adapter := dialStubAdapter(t, stub)
	reg := podsession.NewRegistry()
	reg.Put(&podsession.BindResult{SessionID: "sess_busy", TenantID: "acme", Adapter: adapter})

	srv := sessionserver.New(store, sessionserver.Options{PodRegistry: reg})

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_busy/interrupt", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"INTERRUPT_BUSY"`) {
		t.Errorf("expected INTERRUPT_BUSY in body: %s", rr.Body.String())
	}
	got, _ := store.Get(context.Background(), "acme", "sess_busy")
	if got.State != session.StateRunning {
		t.Errorf("row state = %s, want running (BUSY leaves row)", got.State)
	}
}

// spec: §15.1 + §7.2 — when the gateway holds no binding (single-replica
// dev posture or a coordinator handoff has not re-bound the session on
// this replica), the row-only transition still fires so the API is
// usable end-to-end without a pod attached.
func TestInterruptFallsBackToRowOnlyWhenNoBinding_spec_7_2(t *testing.T) {
	store := memstore.New()
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_nobind", TenantID: "acme", RuntimeRef: "echo",
		State:     session.StateRunning,
		CreatedAt: now, UpdatedAt: now,
	})

	// Wire a registry but never Put a binding.
	srv := sessionserver.New(store, sessionserver.Options{PodRegistry: podsession.NewRegistry()})

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_nobind/interrupt", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	got, _ := store.Get(context.Background(), "acme", "sess_nobind")
	if got.State != session.StateSuspended {
		t.Errorf("row state = %s, want suspended", got.State)
	}
}

// spec: §15.1 precondition — /interrupt against a non-running row
// rejects without touching the adapter.
func TestInterruptRejectsNonRunningStateBeforeAdapterCall_spec_15_1(t *testing.T) {
	store := memstore.New()
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_sus", TenantID: "acme", RuntimeRef: "echo",
		State:     session.StateSuspended, // already suspended
		CreatedAt: now, UpdatedAt: now,
	})
	stub := &stubAdapterServer{status: adapterv1.InterruptResponse_STATUS_ACKNOWLEDGED}
	adapter := dialStubAdapter(t, stub)
	reg := podsession.NewRegistry()
	reg.Put(&podsession.BindResult{SessionID: "sess_sus", TenantID: "acme", Adapter: adapter})

	srv := sessionserver.New(store, sessionserver.Options{PodRegistry: reg})

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_sus/interrupt", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 INVALID_STATE_TRANSITION; body=%s", rr.Code, rr.Body.String())
	}
	if stub.called.Load() != 0 {
		t.Errorf("adapter.Interrupt called %d times before precondition reject", stub.called.Load())
	}
}

// spec: §4.7 / §7.2 — the gateway forwards the configured deadlineMs.
// Verifies the per-call request mirrors the gateway default and the
// deadline is non-zero so a stuck runtime cannot stall the handler.
func TestInterruptForwardsDeadlineMs_spec_7_2(t *testing.T) {
	store := memstore.New()
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_ddl", TenantID: "acme", RuntimeRef: "echo",
		State:     session.StateRunning,
		CreatedAt: now, UpdatedAt: now,
	})
	stub := &stubAdapterServer{status: adapterv1.InterruptResponse_STATUS_ACKNOWLEDGED}
	adapter := dialStubAdapter(t, stub)
	reg := podsession.NewRegistry()
	reg.Put(&podsession.BindResult{SessionID: "sess_ddl", TenantID: "acme", Adapter: adapter})

	srv := sessionserver.New(store, sessionserver.Options{PodRegistry: reg})

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_ddl/interrupt", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	last := stub.lastReq.Load()
	if last == nil {
		t.Fatal("stub never observed an Interrupt call")
	}
	if last.GetDeadlineMs() <= 0 {
		t.Errorf("deadlineMs = %d, want > 0", last.GetDeadlineMs())
	}
	if last.GetMode() != adapterv1.InterruptRequest_MODE_CLEAN {
		t.Errorf("mode = %v, want MODE_CLEAN", last.GetMode())
	}
}
