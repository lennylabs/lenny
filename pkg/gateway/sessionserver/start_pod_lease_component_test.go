// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// componentLeaseStore is an in-memory leasestore.LeaseStore for the
// at-bind acquire component tests. It honours the Acquire holder fencing
// so a test can preset a live foreign holder and assert the bind funnel
// fails closed, and it is concurrency-safe because the coordination sweep
// path may touch it from another goroutine in a broader wiring.
type componentLeaseStore struct {
	mu      sync.Mutex
	holders map[string]string
}

func newComponentLeaseStore() *componentLeaseStore {
	return &componentLeaseStore{holders: map[string]string{}}
}

func clk(tenantID, sessionID string) string { return tenantID + "/" + sessionID }

func (f *componentLeaseStore) Acquire(_ context.Context, tenantID, sessionID, holder string, _ time.Duration) (leasestore.Lease, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := clk(tenantID, sessionID)
	if cur, ok := f.holders[k]; ok && cur != holder {
		return leasestore.Lease{}, leasestore.ErrHeld
	}
	f.holders[k] = holder
	return leasestore.Lease{TenantID: tenantID, SessionID: sessionID, Holder: holder}, nil
}

func (f *componentLeaseStore) Renew(_ context.Context, tenantID, sessionID, holder string, _ time.Duration) (leasestore.Lease, error) {
	return leasestore.Lease{TenantID: tenantID, SessionID: sessionID, Holder: holder}, nil
}

func (f *componentLeaseStore) Release(_ context.Context, tenantID, sessionID, holder string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := clk(tenantID, sessionID)
	if cur, ok := f.holders[k]; ok && cur == holder {
		delete(f.holders, k)
	}
	return nil
}

func (f *componentLeaseStore) Get(_ context.Context, tenantID, sessionID string) (leasestore.Lease, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := clk(tenantID, sessionID)
	h, ok := f.holders[k]
	if !ok {
		return leasestore.Lease{}, leasestore.ErrNotFound
	}
	return leasestore.Lease{TenantID: tenantID, SessionID: sessionID, Holder: h}, nil
}

func (f *componentLeaseStore) DeleteByUser(context.Context, string, string) (int, error) {
	return 0, nil
}
func (f *componentLeaseStore) DeleteByTenant(context.Context, string) (int, error) { return 0, nil }

// podBindServerWithLease mirrors podBindServer but injects the at-bind
// coordination leaseStore and this replica's ReplicaID so the component
// test drives the full two-step /start bind funnel through the HTTP
// surface with the lease gate active.
func podBindServerWithLease(t *testing.T, id, replicaID string, leases leasestore.LeaseStore) (*sessionserver.Server, *podsession.Registry) {
	t.Helper()
	wsRoot := t.TempDir()
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceRoot = wsRoot
	adapterSrv.Runtime = &podBindRuntime{}

	cluster := podBindClient(
		t,
		podBindWarmPool("echo-pool", "echo-tmpl"),
		podBindTemplate("echo-tmpl", "echo", string(isolation.ProfileSandboxed)),
		podBindIdleSandbox("sbx-1", "echo-pool", "10.244.2.5"),
	)
	registry := podsession.NewRegistry()
	binder := podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv))

	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		IDFunc:                  func() string { return id },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             registry,
		AgentNamespace:          podTestNS,
		CoordinationLeaseStore:  leases,
		ReplicaID:               replicaID,
	})
	return srv, registry
}

func createFinalizeBody(t *testing.T) []byte {
	t.Helper()
	body, err := json.Marshal(sessionserver.CreateSessionRequest{
		RuntimeRef: "echo",
		UserID:     "alice@acme.com",
		WorkspacePlan: json.RawMessage(`{
			"schemaVersion": 1,
			"sources": [{"type":"inlineFile","path":"CLAUDE.md","content":"# stored plan","mode":"0644"}]
		}`),
	})
	if err != nil {
		t.Fatalf("marshal create body: %v", err)
	}
	return body
}

// spec: §4.6.1 (coordinating replica holds the lease), §10.1 (per-session
// coordination lease)
// diagnosis: a failure here means the at-bind acquire did not run in the
// two-step /start funnel, so the lease and the binding diverged: the
// replica that holds the binding is not the lease holder from bind time.
//
// The two-step /start acquires the coordination lease for this replica at
// bind, so once the session is running the leaseStore names this replica
// as the holder and the binding is published on the same replica.
func TestTwoStepStartAcquiresCoordinationLeaseAtBind(t *testing.T) {
	leases := newComponentLeaseStore()
	srv, registry := podBindServerWithLease(t, "sess-lease-ok", "replica-a", leases)
	h := srv.Handler()

	if rr := postSessionStep(t, h, "/v1/sessions", createFinalizeBody(t)); rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body=%s", rr.Code, rr.Body.String())
	}
	if rr := postSessionStep(t, h, "/v1/sessions/sess-lease-ok/finalize", nil); rr.Code != http.StatusOK {
		t.Fatalf("finalize: status %d, body=%s", rr.Code, rr.Body.String())
	}
	rr := postSessionStep(t, h, "/v1/sessions/sess-lease-ok/start", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("start: status %d, body=%s", rr.Code, rr.Body.String())
	}

	var resp sessionserver.SessionResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.State != string(session.StateRunning) {
		t.Errorf("state = %q, want running", resp.State)
	}
	if _, ok := registry.Get("sess-lease-ok"); !ok {
		t.Fatal("registry holds no binding after the two-step start")
	}
	got, err := leases.Get(context.Background(), "acme", "sess-lease-ok")
	if err != nil {
		t.Fatalf("coordination lease not held after bind: %v", err)
	}
	if got.Holder != "replica-a" {
		t.Fatalf("lease holder = %q, want replica-a (the binding replica)", got.Holder)
	}
}

// spec: §4.6.1 (coordinating replica holds the lease), §10.1 (per-session
// coordination lease; fail-closed on a live foreign holder)
// diagnosis: a failure here means the bind funnel published a binding for
// a session a live foreign replica still coordinates, the double-bind the
// co-location fix removes.
//
// Regression for the fail-closed fix: with a live foreign holder on the
// session's lease, the two-step /start returns an error and publishes no
// binding. Against the pre-fix void registerBinding (which Put the binding
// unconditionally) the /start would succeed and the registry would hold a
// competing binding.
func TestTwoStepStartFailsClosedOnForeignLeaseHolder(t *testing.T) {
	leases := newComponentLeaseStore()
	// A live foreign replica already coordinates this session.
	if _, err := leases.Acquire(context.Background(), "acme", "sess-lease-held", "replica-b", time.Minute); err != nil {
		t.Fatalf("preset foreign holder: %v", err)
	}
	srv, registry := podBindServerWithLease(t, "sess-lease-held", "replica-a", leases)
	h := srv.Handler()

	if rr := postSessionStep(t, h, "/v1/sessions", createFinalizeBody(t)); rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body=%s", rr.Code, rr.Body.String())
	}
	if rr := postSessionStep(t, h, "/v1/sessions/sess-lease-held/finalize", nil); rr.Code != http.StatusOK {
		t.Fatalf("finalize: status %d, body=%s", rr.Code, rr.Body.String())
	}
	rr := postSessionStep(t, h, "/v1/sessions/sess-lease-held/start", nil)
	if rr.Code == http.StatusOK {
		t.Fatalf("start succeeded against a live foreign lease holder (fail-open double-bind); body=%s", rr.Body.String())
	}
	if _, ok := registry.Get("sess-lease-held"); ok {
		t.Fatal("two-step start published a binding for a session a foreign replica holds (fail-open double-bind)")
	}
	// The foreign holder still owns the lease; the failed bind did not steal it.
	got, err := leases.Get(context.Background(), "acme", "sess-lease-held")
	if err != nil {
		t.Fatalf("foreign lease vanished: %v", err)
	}
	if got.Holder != "replica-b" {
		t.Fatalf("lease holder = %q, want replica-b (unchanged)", got.Holder)
	}
}
