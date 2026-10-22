// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// startProbe is a gRPC server interceptor on the pod's adapter. It records
// the deadline of every StartSession and, when block is set, holds the call
// until its context ends, which is a launch that outlives its bound.
type startProbe struct {
	mu        sync.Mutex
	block     bool
	deadlines []time.Time
}

func (p *startProbe) intercept(ctx context.Context, req any, info *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
	p.mu.Lock()
	if info.FullMethod == "/lenny.adapter.v1.Adapter/StartSession" {
		dl, _ := ctx.Deadline()
		p.deadlines = append(p.deadlines, dl)
	}
	block := p.block && info.FullMethod == "/lenny.adapter.v1.Adapter/StartSession"
	p.mu.Unlock()
	if block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return h(ctx, req)
}

func (p *startProbe) snapshot() []time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]time.Time(nil), p.deadlines...)
}

// launchServer builds a session server over one warm pod whose adapter runs
// behind probe, with the given launch bound.
func launchServer(t *testing.T, store sessionstore.Store, id string, maxFinalizing time.Duration, probe *startProbe) (*sessionserver.Server, *podsession.Registry, client.Client) {
	t.Helper()
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceBase = t.TempDir()
	adapterSrv.Runtime = &podBindRuntime{}
	cluster := podBindClient(
		t,
		podBindWarmPool("echo-pool", "echo-tmpl"),
		podBindTemplate("echo-tmpl", "echo", string(isolation.ProfileSandboxed)),
		podBindIdleSandbox("sbx-1", "echo-pool", "10.244.2.5"),
	)
	registry := podsession.NewRegistry()
	dial := podBindAdapterDialer(t, adapterSrv, grpc.ChainUnaryInterceptor(probe.intercept))
	srv := sessionserver.New(store, sessionserver.Options{
		DevTenantHeader:         true,
		IDFunc:                  func() string { return id },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               podBindBinder(cluster, dial),
		PodRegistry:             registry,
		AgentNamespace:          podTestNS,
		MaxFinalizing:           maxFinalizing,
	})
	return srv, registry, cluster
}

// createAndFinalize runs the first two steps of the two-step lifecycle.
func createAndFinalize(t *testing.T, h http.Handler, id string) {
	t.Helper()
	body, _ := json.Marshal(sessionserver.CreateSessionRequest{
		RuntimeRef: "echo", UserID: "alice@acme.com",
		WorkspacePlan: json.RawMessage(`{"schemaVersion": 1, "sources": []}`),
	})
	if rr := postSessionStep(t, h, "/v1/sessions", body); rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body=%s", rr.Code, rr.Body.String())
	}
	if rr := postSessionStep(t, h, "/v1/sessions/"+id+"/finalize", nil); rr.Code != http.StatusOK {
		t.Fatalf("finalize: status %d, body=%s", rr.Code, rr.Body.String())
	}
}

// assertDeadlineNear checks that the launch's deadline is bound after the
// session read, within the test's own scheduling slack.
func assertDeadlineNear(t *testing.T, deadlines []time.Time, readAt time.Time, bound time.Duration) {
	t.Helper()
	if len(deadlines) == 0 || deadlines[0].IsZero() {
		t.Fatalf("StartSession deadlines = %v, want one carrying the launch bound", deadlines)
	}
	lo, hi := readAt.Add(bound-5*time.Second), readAt.Add(bound+5*time.Second)
	if d := deadlines[0]; d.Before(lo) || d.After(hi) {
		t.Errorf("launch deadline = %s, want about %s after the session read (%s)", d, bound, readAt)
	}
}

// spec: §5.2 (Slot assignment atomicity)
//
// A /start launch runs under the launch bound, taken before the session
// read: with MaxFinalizing unset the launch's context carries a deadline 600
// seconds after the read, and a launch that blocks past a short bound ends
// with the launch error, does not commit running, and leaves no binding.
func TestStartLaunchRunsUnderTheFinalizingBound_spec_5_2(t *testing.T) {
	t.Run("the default bound is 600 seconds", func(t *testing.T) {
		probe := &startProbe{}
		srv, _, _ := launchServer(t, memstore.New(), "sess-bound-default", 0, probe)
		h := srv.Handler()
		createAndFinalize(t, h, "sess-bound-default")
		readAt := time.Now()
		if rr := postSessionStep(t, h, "/v1/sessions/sess-bound-default/start", nil); rr.Code != http.StatusOK {
			t.Fatalf("start: status %d, body=%s", rr.Code, rr.Body.String())
		}
		deadlines := probe.snapshot()
		assertDeadlineNear(t, deadlines, readAt, 600*time.Second)
	})

	t.Run("a launch past the bound fails and does not commit running", func(t *testing.T) {
		probe := &startProbe{block: true}
		store := memstore.New()
		srv, registry, cluster := launchServer(t, store, "sess-bound-expired", 500*time.Millisecond, probe)
		h := srv.Handler()
		createAndFinalize(t, h, "sess-bound-expired")
		rr := postSessionStep(t, h, "/v1/sessions/sess-bound-expired/start", nil)
		if rr.Code == http.StatusOK {
			t.Fatalf("start past the launch bound answered 200: %s", rr.Body.String())
		}
		row, err := store.Get(context.Background(), "acme", "sess-bound-expired")
		if err != nil {
			t.Fatal(err)
		}
		if row.State == session.StateRunning {
			t.Error("a launch past its bound committed running")
		}
		if _, ok := registry.Get("sess-bound-expired"); ok {
			t.Error("a launch past its bound published a binding")
		}
		if claimExists(t, cluster, "sbx-1") {
			t.Error("the failed launch's compensating release left the pod's claim")
		}
	})
}

// spec: §5.2 (Slot assignment atomicity)
//
// A delegated child's materialization runs its claim and launch under the
// same bound, taken before the primary read of the child's row.
func TestMaterializeDelegatedChildRunsUnderTheFinalizingBound_spec_5_2(t *testing.T) {
	t.Run("the default bound is 600 seconds", func(t *testing.T) {
		store := memstore.New()
		seedDelegatedChild(t, store, "child-bound", `{"schemaVersion": 1, "sources": []}`)
		probe := &startProbe{}
		srv, _, _ := launchServer(t, store, "unused", 0, probe)
		readAt := time.Now()
		if _, err := srv.MaterializeDelegatedChild(context.Background(), "acme", "child-bound"); err != nil {
			t.Fatalf("MaterializeDelegatedChild: %v", err)
		}
		deadlines := probe.snapshot()
		assertDeadlineNear(t, deadlines, readAt, 600*time.Second)
	})

	t.Run("a launch past the bound fails and does not commit running", func(t *testing.T) {
		store := memstore.New()
		seedDelegatedChild(t, store, "child-expired", `{"schemaVersion": 1, "sources": []}`)
		probe := &startProbe{block: true}
		srv, registry, cluster := launchServer(t, store, "unused", 500*time.Millisecond, probe)
		if _, err := srv.MaterializeDelegatedChild(context.Background(), "acme", "child-expired"); err == nil {
			t.Fatal("a materialization past its launch bound returned no error")
		}
		row, err := store.Get(context.Background(), "acme", "child-expired")
		if err != nil {
			t.Fatal(err)
		}
		if row.State != session.StateCreated {
			t.Errorf("child state = %q, want created", row.State)
		}
		if registry.Len() != 0 {
			t.Errorf("registry holds %d bindings, want 0", registry.Len())
		}
		if claimExists(t, cluster, "sbx-1") {
			t.Error("the failed launch's compensating release left the pod's claim")
		}
	})
}

// laggingReplicaStore answers Get with a stale non-terminal state, as a
// lagging read replica can, while its primary read returns the row as it is.
type laggingReplicaStore struct {
	*memstore.Store
	staleState session.State
}

func (s *laggingReplicaStore) Get(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	row, err := s.Store.Get(ctx, tenantID, id)
	if err == nil {
		row.State = s.staleState
	}
	return row, err
}

// countingBinder is a pod binder whose every Kubernetes read and adapter
// dial is counted, so a case can assert that no launch started.
func countingBinder(t *testing.T, calls *int) *podsession.Binder {
	t.Helper()
	var mu sync.Mutex
	count := func() {
		mu.Lock()
		defer mu.Unlock()
		*calls++
	}
	c := fake.NewClientBuilder().WithScheme(podBindScheme(t)).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			count()
			return cl.Get(ctx, key, obj, opts...)
		},
		List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			count()
			return cl.List(ctx, list, opts...)
		},
	}).Build()
	return &podsession.Binder{
		Client:    c,
		Namespace: podTestNS,
		DialAdapter: func(string) (*adapterclient.Client, error) {
			count()
			return nil, errors.New("no adapter")
		},
	}
}

// spec: §5.2 (Slot assignment atomicity)
//
// /start and a delegated child's materialization read the session row from
// the primary. A replica that still reports the row non-terminal after a
// terminal write must not admit a launch, because the reaper's terminal age
// is measured from that write: /start refuses on its precondition and the
// materialization returns DelegatedChildNotCreatedError, and no binder call
// follows either.
func TestLaunchReadsTheRowFromThePrimary_spec_5_2(t *testing.T) {
	t.Run("start", func(t *testing.T) {
		base := memstore.New()
		if err := base.Create(context.Background(), sessionstore.Session{
			ID: "sess-lagging", TenantID: "acme", UserID: "alice@acme.com", RuntimeRef: "echo",
			IsolationProfile: isolation.ProfileSandboxed, State: session.StateCancelled,
		}); err != nil {
			t.Fatal(err)
		}
		var calls int
		srv := sessionserver.New(&laggingReplicaStore{Store: base, staleState: session.StateReady}, sessionserver.Options{
			DevTenantHeader:         true,
			IDFunc:                  func() string { return "unused" },
			DefaultIsolationProfile: isolation.ProfileSandboxed,
			PodBinder:               countingBinder(t, &calls),
			PodRegistry:             podsession.NewRegistry(),
			AgentNamespace:          podTestNS,
		})
		rr := postSessionStep(t, srv.Handler(), "/v1/sessions/sess-lagging/start", nil)
		if rr.Code != http.StatusConflict {
			t.Errorf("start of a terminal row = %d (%s), want the 409 precondition refusal", rr.Code, rr.Body.String())
		}
		if calls != 0 {
			t.Errorf("binder calls = %d, want none", calls)
		}
	})

	t.Run("delegated child", func(t *testing.T) {
		base := memstore.New()
		if err := base.Create(context.Background(), sessionstore.Session{
			ID: "child-lagging", TenantID: "acme", UserID: "alice@acme.com", RuntimeRef: "echo",
			IsolationProfile: isolation.ProfileSandboxed, State: session.StateCancelled, ParentSessionID: "parent-1",
		}); err != nil {
			t.Fatal(err)
		}
		var calls int
		srv := sessionserver.New(&laggingReplicaStore{Store: base, staleState: session.StateCreated}, sessionserver.Options{
			DevTenantHeader:         true,
			IDFunc:                  func() string { return "unused" },
			DefaultIsolationProfile: isolation.ProfileSandboxed,
			PodBinder:               countingBinder(t, &calls),
			PodRegistry:             podsession.NewRegistry(),
			AgentNamespace:          podTestNS,
		})
		_, err := srv.MaterializeDelegatedChild(context.Background(), "acme", "child-lagging")
		var guard *sessionserver.DelegatedChildNotCreatedError
		if !errors.As(err, &guard) {
			t.Fatalf("MaterializeDelegatedChild = %v, want *DelegatedChildNotCreatedError", err)
		}
		if calls != 0 {
			t.Errorf("binder calls = %d, want none", calls)
		}
	})
}
