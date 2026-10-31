// SPDX-License-Identifier: MIT

package barrier_test

import (
	"context"
	"errors"
	"net"
	"strings"
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
// and fenced to generation 9, plus a client dialed to it. opts configure the
// adapter's gRPC server.
func fencedAdapter(t *testing.T, opts ...grpc.ServerOption) (*adapter.Server, *adapterclient.Client) {
	t.Helper()
	srv := adapter.New("barrier-wiring-test")
	srv.WorkspaceBase = t.TempDir()
	srv.ManifestDir = t.TempDir()
	srv.Runtime = fakeRuntime{}

	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(srv, opts...)
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

// spec: §10.1 — PodDispatcher routes the barrier through the
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
//
// The window closes on both ends of the RPC at nearly the same instant, and
// either end can finish first. The client's deadline yields DeadlineExceeded.
// The adapter's yields an ack with an empty checkpoint_ref, which the client
// returns when that ack is read before its own timer fires. Under host load
// the second outcome occurs in this test, so it accepts both and holds what
// both share: the barrier held for the window and no checkpoint was claimed
// (BUILD-GAPS F-10.1.46 records the two outcomes the gateway can observe, and
// TestPodDispatcherSendAckAtTheAdapterWindow_spec_10_1_169 drives the second).
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
	ack, err := d.Send(ctx, barrier.Target{
		TenantID: "acme", SessionID: "s1", CoordinationGeneration: 9,
	}, "b-1")
	elapsed := time.Since(start)
	if err != nil && status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("Send error = %v, want DeadlineExceeded (the held ack window closed)", err)
	}
	if ack.CheckpointRef != "" {
		t.Fatalf("Send ack carries checkpoint_ref %q with no gateway-driven stream", ack.CheckpointRef)
	}
	if elapsed < 400*time.Millisecond {
		t.Fatalf("Send returned after %s; the barrier did not hold quiescence for the window", elapsed)
	}
}

// spec: §10.1 — when the adapter's end of the window closes first, the
// adapter holds quiescence to its deadline and acks with an empty
// checkpoint_ref. The interceptor gives the handler a deadline shorter than
// the client's, which fixes the order the unloaded test leaves to the
// scheduler, so this path runs on every run.
func TestPodDispatcherSendAckAtTheAdapterWindow_spec_10_1_169(t *testing.T) {
	const window = 500 * time.Millisecond
	_, cl := fencedAdapter(t, grpc.ChainUnaryInterceptor(func(
		ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler,
	) (any, error) {
		if !strings.HasSuffix(info.FullMethod, "/CheckpointBarrier") {
			return handler(ctx, req)
		}
		ctx, cancel := context.WithTimeout(ctx, window)
		defer cancel()
		return handler(ctx, req)
	}))
	d := &barrier.PodDispatcher{
		Conn: func(string) (*adapterclient.Client, bool) { return cl, true },
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	start := time.Now()
	ack, err := d.Send(ctx, barrier.Target{
		TenantID: "acme", SessionID: "s1", CoordinationGeneration: 9,
	}, "b-1")
	if err != nil {
		t.Fatalf("Send = %v, want the ack the adapter sends at its window", err)
	}
	if ack.CheckpointRef != "" {
		t.Fatalf("Send ack carries checkpoint_ref %q with no gateway-driven stream", ack.CheckpointRef)
	}
	if elapsed := time.Since(start); elapsed < window {
		t.Fatalf("Send returned after %s; the adapter did not hold quiescence for its %s window", elapsed, window)
	}
}

// spec: §10.1.8 — a generation-stale barrier (FailedPrecondition)
// is surfaced as ErrGenerationStale so Dispatch records it without
// aborting the drain.
func TestPodDispatcherSendGenerationStale_spec_10_1_8(t *testing.T) {
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

// spec: §10.1.8 — with the co-located coordination lease the
// coordinator always reaches the pod through the held connection, so a
// target for which this replica holds no live binding is unreachable and
// the barrier has no fresh-dial fallback. The Outcome records the error
// and the drain continues.
func TestPodDispatcherUnreachableWithoutConn_spec_10_1_8(t *testing.T) {
	d := &barrier.PodDispatcher{
		Conn: func(string) (*adapterclient.Client, bool) { return nil, false },
	}
	_, err := d.Send(context.Background(), barrier.Target{SessionID: "s1"}, "b-1")
	if err == nil {
		t.Fatal("Send with no held binding = nil error, want unreachable")
	}
}

// erroringMirror always fails ListHeldByReplica so the cache fallback is
// exercised.
type erroringMirror struct{ coordlease.Store }

func (erroringMirror) ListHeldByReplica(context.Context, string) ([]coordlease.Lease, error) {
	return nil, errors.New("postgres down")
}

// spec: §10.1.8 — the primary barrier-target source is the
// coordination_lease mirror (source "postgres").
func TestMirrorTargetListerPostgresPrimary_spec_10_1_8(t *testing.T) {
	mirror := coordlease.NewMemoryStore(nil)
	_ = mirror.Upsert(context.Background(), coordlease.Lease{TenantID: "acme", SessionID: "s1", CoordinatorReplica: "rep-1", CoordinationGeneration: 4})
	l := &barrier.MirrorTargetLister{
		ReplicaID: "rep-1",
		Mirror:    mirror,
	}
	targets, source, err := l.Targets(context.Background())
	if err != nil {
		t.Fatalf("Targets: %v", err)
	}
	if source != barrier.SourcePostgres {
		t.Fatalf("source = %q, want postgres", source)
	}
	if len(targets) != 1 || targets[0].SessionID != "s1" || targets[0].CoordinationGeneration != 4 {
		t.Fatalf("targets = %+v, want one resolved s1 target", targets)
	}
}

// spec: §10.1.8 — a Postgres read failure falls back to the
// in-memory lease cache with source "cache_fallback".
func TestMirrorTargetListerCacheFallbackOnError_spec_10_1_8(t *testing.T) {
	l := &barrier.MirrorTargetLister{
		ReplicaID: "rep-1",
		Mirror:    erroringMirror{},
		Fallback: func() []barrier.Target {
			return []barrier.Target{{TenantID: "acme", SessionID: "cached"}}
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

// spec: §10.1.8 — with no mirror configured the lister uses the
// cache fallback directly.
func TestMirrorTargetListerNilMirrorUsesFallback_spec_10_1_8(t *testing.T) {
	l := &barrier.MirrorTargetLister{
		ReplicaID: "rep-1",
		Fallback:  func() []barrier.Target { return []barrier.Target{{SessionID: "x"}} },
	}
	targets, source, _ := l.Targets(context.Background())
	if source != barrier.SourceCacheFallback || len(targets) != 1 {
		t.Fatalf("targets=%+v source=%q, want one target, cache_fallback", targets, source)
	}
}
