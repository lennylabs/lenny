// SPDX-License-Identifier: MIT

package checkpointer_test

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/checkpointer"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// spec: §4.4 / §7.1 — the gateway checkpointer drives a pod checkpoint
// and records the resulting WorkspaceSnapshot on the session row.

// stubRuntime is a no-op RuntimeProcess for the bufconn adapter.
type stubRuntime struct{}

func (stubRuntime) Start(context.Context, string) error           { return nil }
func (stubRuntime) WriteEnvelope(string, []byte) error            { return nil }
func (stubRuntime) Interrupt(context.Context, string, bool) error { return nil }
func (stubRuntime) Close(context.Context, string) error           { return nil }
func (stubRuntime) Output(context.Context, string) (<-chan []byte, error) {
	ch := make(chan []byte)
	close(ch)
	return ch, nil
}

// stubSink is an adapter.CheckpointSink returning a fixed checkpoint id.
type stubSink struct {
	id  string
	err error
}

func (s stubSink) SaveCheckpoint(_ context.Context, _ string, r io.Reader) (string, error) {
	_, _ = io.Copy(io.Discard, r)
	if s.err != nil {
		return "", s.err
	}
	return s.id, nil
}

// dialAdapter serves srv over an in-memory connection and returns a
// connected adapter client.
func dialAdapter(t *testing.T, srv *adapter.Server) *adapterclient.Client {
	t.Helper()
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
		t.Fatalf("dial adapter: %v", err)
	}
	t.Cleanup(func() { _ = cl.Close() })
	return cl
}

// runningSession seeds store with one running session.
func runningSession(t *testing.T, store sessionstore.Store, tenantID, sessionID string) {
	t.Helper()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID:         sessionID,
		TenantID:   tenantID,
		State:      session.StateRunning,
		RuntimeRef: "echo",
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
}

func TestCheckpointRecordsTheWorkspaceSnapshot(t *testing.T) {
	srv := adapter.New("checkpointer-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = stubRuntime{}
	srv.Checkpoints = stubSink{id: "ckpt-7"}
	client := dialAdapter(t, srv)
	if err := client.StartSession(context.Background(), "s1", "echo", nil); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	registry := podsession.NewRegistry()
	registry.Put(&podsession.BindResult{SessionID: "s1", Adapter: client})

	store := memstore.New()
	runningSession(t, store, "acme", "s1")

	when := time.Date(2026, 5, 16, 9, 0, 0, 0, time.UTC)
	cp := &checkpointer.Checkpointer{
		Sessions: store,
		Registry: registry,
		Now:      func() time.Time { return when },
	}
	if err := cp.Checkpoint(context.Background(), "acme", "s1"); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}

	row, err := store.Get(context.Background(), "acme", "s1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row.WorkspaceSnapshot == nil {
		t.Fatal("no WorkspaceSnapshot recorded on the session row")
	}
	if row.WorkspaceSnapshot.Ref != "ckpt-7" {
		t.Errorf("snapshot ref = %q, want ckpt-7", row.WorkspaceSnapshot.Ref)
	}
	if row.WorkspaceSnapshot.Source != sessionstore.WorkspaceSnapshotCheckpoint {
		t.Errorf("snapshot source = %q, want checkpoint", row.WorkspaceSnapshot.Source)
	}
	if !row.WorkspaceSnapshot.Timestamp.Equal(when) {
		t.Errorf("snapshot timestamp = %v, want %v", row.WorkspaceSnapshot.Timestamp, when)
	}
}

func TestCheckpointReturnsErrNoBindingForAnUncoordinatedSession(t *testing.T) {
	cp := &checkpointer.Checkpointer{
		Sessions: memstore.New(),
		Registry: podsession.NewRegistry(), // empty
	}
	if err := cp.Checkpoint(context.Background(), "acme", "s1"); !errors.Is(err, checkpointer.ErrNoBinding) {
		t.Errorf("error = %v, want ErrNoBinding", err)
	}
}

func TestCheckpointSurfacesAnAdapterFailure(t *testing.T) {
	srv := adapter.New("checkpointer-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = stubRuntime{}
	srv.Checkpoints = stubSink{err: errors.New("artifact store down")}
	client := dialAdapter(t, srv)
	if err := client.StartSession(context.Background(), "s1", "echo", nil); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	registry := podsession.NewRegistry()
	registry.Put(&podsession.BindResult{SessionID: "s1", Adapter: client})
	store := memstore.New()
	runningSession(t, store, "acme", "s1")

	cp := &checkpointer.Checkpointer{Sessions: store, Registry: registry}
	if err := cp.Checkpoint(context.Background(), "acme", "s1"); err == nil {
		t.Error("Checkpoint succeeded though the adapter checkpoint failed")
	}

	// The session row carries no snapshot when the checkpoint failed.
	row, err := store.Get(context.Background(), "acme", "s1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row.WorkspaceSnapshot != nil {
		t.Error("a WorkspaceSnapshot was recorded despite the checkpoint failure")
	}
}

// bindSession wires a bufconn adapter for sessionID, registers the pod
// binding, and seeds a running session row.
func bindSession(t *testing.T, registry *podsession.Registry, store sessionstore.Store, tenantID, sessionID string, sink adapter.CheckpointSink) {
	t.Helper()
	srv := adapter.New("checkpointer-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = stubRuntime{}
	srv.Checkpoints = sink
	client := dialAdapter(t, srv)
	if err := client.StartSession(context.Background(), sessionID, "echo", nil); err != nil {
		t.Fatalf("StartSession %s: %v", sessionID, err)
	}
	registry.Put(&podsession.BindResult{SessionID: sessionID, TenantID: tenantID, Adapter: client})
	runningSession(t, store, tenantID, sessionID)
}

func TestSweepCheckpointsEveryCoordinatedSession(t *testing.T) {
	registry := podsession.NewRegistry()
	store := memstore.New()
	bindSession(t, registry, store, "acme", "s1", stubSink{id: "ck-1"})
	bindSession(t, registry, store, "acme", "s2", stubSink{id: "ck-2"})

	cp := &checkpointer.Checkpointer{Sessions: store, Registry: registry}
	cp.Sweep(context.Background())

	for _, tc := range []struct{ id, ref string }{{"s1", "ck-1"}, {"s2", "ck-2"}} {
		row, err := store.Get(context.Background(), "acme", tc.id)
		if err != nil {
			t.Fatalf("Get %s: %v", tc.id, err)
		}
		if row.WorkspaceSnapshot == nil || row.WorkspaceSnapshot.Ref != tc.ref {
			t.Errorf("session %s snapshot = %+v, want ref %s", tc.id, row.WorkspaceSnapshot, tc.ref)
		}
	}
}

func TestSweepReportsPerSessionFailuresAndContinues(t *testing.T) {
	registry := podsession.NewRegistry()
	store := memstore.New()
	bindSession(t, registry, store, "acme", "ok", stubSink{id: "ck-ok"})
	bindSession(t, registry, store, "acme", "bad", stubSink{err: errors.New("store down")})

	var failed []string
	cp := &checkpointer.Checkpointer{
		Sessions: store,
		Registry: registry,
		OnError:  func(sessionID string, _ error) { failed = append(failed, sessionID) },
	}
	cp.Sweep(context.Background())

	if len(failed) != 1 || failed[0] != "bad" {
		t.Errorf("OnError saw %v, want [bad]", failed)
	}
	if row, _ := store.Get(context.Background(), "acme", "ok"); row.WorkspaceSnapshot == nil {
		t.Error("the sweep stopped after the failed session — ok was not checkpointed")
	}
}

func TestRunPeriodicallySweeps(t *testing.T) {
	registry := podsession.NewRegistry()
	store := memstore.New()
	bindSession(t, registry, store, "acme", "s1", stubSink{id: "ck-run"})

	cp := &checkpointer.Checkpointer{Sessions: store, Registry: registry, Interval: 10 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go cp.Run(ctx)

	deadline := time.After(2 * time.Second)
	for {
		if row, _ := store.Get(context.Background(), "acme", "s1"); row.WorkspaceSnapshot != nil {
			return // the periodic loop checkpointed the session.
		}
		select {
		case <-deadline:
			t.Fatal("Run did not checkpoint the session within the deadline")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestSealRecordsASealedSnapshot(t *testing.T) {
	registry := podsession.NewRegistry()
	store := memstore.New()
	bindSession(t, registry, store, "acme", "s1", stubSink{id: "seal-1"})

	when := time.Date(2026, 5, 16, 18, 0, 0, 0, time.UTC)
	cp := &checkpointer.Checkpointer{
		Sessions: store,
		Registry: registry,
		Now:      func() time.Time { return when },
	}
	if err := cp.Seal(context.Background(), "acme", "s1"); err != nil {
		t.Fatalf("Seal: %v", err)
	}

	row, err := store.Get(context.Background(), "acme", "s1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row.WorkspaceSnapshot == nil {
		t.Fatal("Seal recorded no WorkspaceSnapshot")
	}
	// A seal records source `sealed`, distinguishing the final
	// workspace from a periodic `checkpoint` snapshot.
	if row.WorkspaceSnapshot.Source != sessionstore.WorkspaceSnapshotSealed {
		t.Errorf("snapshot source = %q, want sealed", row.WorkspaceSnapshot.Source)
	}
	if row.WorkspaceSnapshot.Ref != "seal-1" {
		t.Errorf("snapshot ref = %q, want seal-1", row.WorkspaceSnapshot.Ref)
	}
	if !row.WorkspaceSnapshot.Timestamp.Equal(when) {
		t.Errorf("snapshot timestamp = %v, want %v", row.WorkspaceSnapshot.Timestamp, when)
	}
}
