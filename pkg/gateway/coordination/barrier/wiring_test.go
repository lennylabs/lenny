// SPDX-License-Identifier: MIT

package barrier_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/gateway/coordination/barrier"
	"github.com/lennylabs/lenny/pkg/gateway/coordination/coordlease"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// fakeRuntime is the minimal RuntimeProcess StartSession needs.
type fakeRuntime struct{}

func (fakeRuntime) Start(context.Context, string) error           { return nil }
func (fakeRuntime) WriteEnvelope(string, []byte) error            { return nil }
func (fakeRuntime) Interrupt(context.Context, string, bool) error { return nil }
func (fakeRuntime) Close(context.Context, string) error           { return nil }
func (fakeRuntime) Output(context.Context, string) (<-chan []byte, error) {
	ch := make(chan []byte)
	close(ch)
	return ch, nil
}

// fencedAdapter returns an in-process adapter with session "s1" started
// and fenced to generation 9, plus a client dialed to it.
func fencedAdapter(t *testing.T) (*adapter.Server, *adapterclient.Client) {
	t.Helper()
	srv := adapter.New("barrier-wiring-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.ManifestDir = t.TempDir()
	srv.Runtime = fakeRuntime{}

	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	cl, err := adapterclient.Dial("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = cl.Close() })

	ctx := context.Background()
	if err := cl.StartSession(ctx, adapterclient.StartSessionParams{SessionID: "s1", Runtime: "claude-code"}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if _, err := srv.CoordinatorFence(ctx, &adapterv1.CoordinatorFenceRequest{
		SessionId: &adapterv1.SessionId{Value: "s1"}, CoordinationGeneration: 9,
	}); err != nil {
		t.Fatalf("fence: %v", err)
	}
	return srv, cl
}

// spec: §10.1 lines 165-169 — PodDispatcher routes the barrier through the
// live in-replica connection, and under quiesce-and-hold the adapter holds
// the ack open until the gateway drives the Checkpoint stream to
// termination. With a matching generation and no gateway-driven stream,
// Send therefore holds quiescence for the whole ack window and returns the
// window's deadline rather than a self-checkpointed ack. The
// concurrent-stream happy path (the barrier opens the stream itself and
// the ack completes it) is covered at the Coordinator level in
// checkpoint_drive_test.go.
//
// Against the pre-quiesce-and-hold adapter, which self-checkpointed and
// acked immediately, Send returned a nil error before the window; this
// test would fail there because it now asserts the held-window deadline.
func TestPodDispatcherSendHoldsUntilWindow_spec_10_1_169(t *testing.T) {
	_, cl := fencedAdapter(t)

	d := &barrier.PodDispatcher{
		Conn: func(sessionID string) (*adapterclient.Client, bool) {
			if sessionID == "s1" {
				return cl, true
			}
			return nil, false
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := d.Send(ctx, barrier.Target{
		TenantID: "acme", SessionID: "s1", CoordinationGeneration: 9,
	}, "b-1")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("Send returned an ack with no gateway-driven stream; the barrier no longer holds quiescence")
	}
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("Send error = %v, want DeadlineExceeded (the held ack window closed)", err)
	}
	if elapsed < 400*time.Millisecond {
		t.Fatalf("Send returned after %s; the barrier did not hold quiescence for the window", elapsed)
	}
}

// spec: §10.1 line 165 — a generation-stale barrier (FailedPrecondition)
// is surfaced as ErrGenerationStale so Dispatch records it without
// aborting the drain.
func TestPodDispatcherSendGenerationStale_spec_10_1_165(t *testing.T) {
	_, cl := fencedAdapter(t)
	d := &barrier.PodDispatcher{
		Conn: func(string) (*adapterclient.Client, bool) { return cl, true },
	}
	_, err := d.Send(context.Background(), barrier.Target{
		TenantID: "acme", SessionID: "s1", CoordinationGeneration: 3, // stale vs fenced 9
	}, "b-1")
	if !errors.Is(err, barrier.ErrGenerationStale) {
		t.Fatalf("Send stale = %v, want ErrGenerationStale", err)
	}
}

// spec: §10.1 line 165 — a target with no live connection and no dialer
// is unreachable; the Outcome records the error and the drain continues.
func TestPodDispatcherUnreachableWithoutConnOrDial(t *testing.T) {
	d := &barrier.PodDispatcher{
		Conn: func(string) (*adapterclient.Client, bool) { return nil, false },
	}
	_, err := d.Send(context.Background(), barrier.Target{SessionID: "s1"}, "b-1")
	if err == nil {
		t.Fatal("Send with no conn and no dial = nil error, want unreachable")
	}
}

// spec: §10.1 line 165 — when no live connection is cached, the dispatcher
// dials the target's pod address.
func TestPodDispatcherDialsWhenNoLiveConn_spec_10_1_165(t *testing.T) {
	_, cl := fencedAdapter(t)
	var dialedAddr string
	d := &barrier.PodDispatcher{
		Conn: func(string) (*adapterclient.Client, bool) { return nil, false },
		Dial: func(addr string) (*adapterclient.Client, error) {
			dialedAddr = addr
			return cl, nil
		},
	}
	// gen 3 is stale → ErrGenerationStale, but the dial path is exercised.
	_, err := d.Send(context.Background(), barrier.Target{SessionID: "s1", CoordinationGeneration: 3, PodAddr: "10.0.0.5:9090"}, "b-1")
	if !errors.Is(err, barrier.ErrGenerationStale) {
		t.Fatalf("Send = %v, want ErrGenerationStale via dial", err)
	}
	if dialedAddr != "10.0.0.5:9090" {
		t.Errorf("dialed addr = %q, want 10.0.0.5:9090", dialedAddr)
	}
}

// erroringMirror always fails ListHeldByReplica so the cache fallback is
// exercised.
type erroringMirror struct{ coordlease.Store }

func (erroringMirror) ListHeldByReplica(context.Context, string) ([]coordlease.Lease, error) {
	return nil, errors.New("postgres down")
}

// spec: §10.1 line 165 — the primary barrier-target source is the
// coordination_lease mirror (source "postgres").
func TestMirrorTargetListerPostgresPrimary_spec_10_1_165(t *testing.T) {
	mirror := coordlease.NewMemoryStore(nil)
	_ = mirror.Upsert(context.Background(), coordlease.Lease{TenantID: "acme", SessionID: "s1", CoordinatorReplica: "rep-1", CoordinationGeneration: 4})
	l := &barrier.MirrorTargetLister{
		ReplicaID: "rep-1",
		Mirror:    mirror,
		PodAddr: func(sessionID string) (string, bool) {
			if sessionID == "s1" {
				return "10.0.0.1:9090", true
			}
			return "", false
		},
	}
	targets, source, err := l.Targets(context.Background())
	if err != nil {
		t.Fatalf("Targets: %v", err)
	}
	if source != barrier.SourcePostgres {
		t.Fatalf("source = %q, want postgres", source)
	}
	if len(targets) != 1 || targets[0].SessionID != "s1" || targets[0].CoordinationGeneration != 4 || targets[0].PodAddr != "10.0.0.1:9090" {
		t.Fatalf("targets = %+v, want one resolved s1 target", targets)
	}
}

// spec: §10.1 line 165 — a Postgres read failure falls back to the
// in-memory lease cache with source "cache_fallback".
func TestMirrorTargetListerCacheFallbackOnError_spec_10_1_165(t *testing.T) {
	l := &barrier.MirrorTargetLister{
		ReplicaID: "rep-1",
		Mirror:    erroringMirror{},
		Fallback: func() []barrier.Target {
			return []barrier.Target{{TenantID: "acme", SessionID: "cached", PodAddr: "10.0.0.2:9090"}}
		},
	}
	targets, source, err := l.Targets(context.Background())
	if err != nil {
		t.Fatalf("Targets: %v", err)
	}
	if source != barrier.SourceCacheFallback {
		t.Fatalf("source = %q, want cache_fallback", source)
	}
	if len(targets) != 1 || targets[0].SessionID != "cached" {
		t.Fatalf("targets = %+v, want the cached fallback target", targets)
	}
}

// spec: §10.1 line 165 — with no mirror configured the lister uses the
// cache fallback directly.
func TestMirrorTargetListerNilMirrorUsesFallback_spec_10_1_165(t *testing.T) {
	l := &barrier.MirrorTargetLister{
		ReplicaID: "rep-1",
		Fallback:  func() []barrier.Target { return []barrier.Target{{SessionID: "x"}} },
	}
	targets, source, _ := l.Targets(context.Background())
	if source != barrier.SourceCacheFallback || len(targets) != 1 {
		t.Fatalf("targets=%+v source=%q, want one target, cache_fallback", targets, source)
	}
}
