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
	// acquires counts every Acquire call so a test can assert how many
	// times the bind funnel touched the store.
	acquires int
	// failAcquireAfter, when > 0, makes every Acquire past the Nth return a
	// transient (non-ErrHeld) error. Setting it to 1 lets the hoisted
	// at-bind acquire succeed while the follow-on self-renew acquire fails,
	// which reproduces a transient Redis blip between the hoisted acquire
	// and the binding publish on the early-commit paths.
	failAcquireAfter int
}

func newComponentLeaseStore() *componentLeaseStore {
	return &componentLeaseStore{holders: map[string]string{}}
}

func clk(tenantID, sessionID string) string { return tenantID + "/" + sessionID }

func (f *componentLeaseStore) Acquire(_ context.Context, tenantID, sessionID, holder string, _ time.Duration) (leasestore.Lease, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acquires++
	if f.failAcquireAfter > 0 && f.acquires > f.failAcquireAfter {
		return leasestore.Lease{}, errors.New("injected transient leaseStore fault")
	}
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

// spec: §4.6.1 (coordinating replica holds the lease), §10.1 (per-session
// coordination lease)
// diagnosis: a failure here means a single-call /start committed the row to
// running holding the coordination lease but published no binding, so the
// executor serves the session from an empty registry and the session is
// permanently unservable while this replica renews the lease forever. That is
// the lease-without-binding decoupling co-location removes.
//
// Regression for the hoisted-publish fix: the single-call /start acquires the
// coordination lease ahead of the running-commit, so once the row is
// committed the binding must publish unconditionally rather than through a
// second self-renew acquire. A transient leaseStore fault on that follow-on
// acquire must not drop the publish. failAcquireAfter=1 lets the hoisted
// acquire succeed and fails any later acquire; against the pre-fix code, which
// routed the already-held path back through registerBinding's acquire and
// logged the error best-effort, the row commits to running but the registry
// stays empty. The fix publishes directly, so the binding is present.
func TestSingleCallStartPublishesBindingDespiteSelfRenewFault(t *testing.T) {
	leases := newComponentLeaseStore()
	leases.failAcquireAfter = 1
	srv, registry := podBindServerWithLease(t, "sess-renew-blip", "replica-a", leases)

	body, err := json.Marshal(sessionserver.CreateAndStartRequest{RuntimeRef: "echo", UserID: "alice@acme.com"})
	if err != nil {
		t.Fatalf("marshal create-and-start body: %v", err)
	}
	rr := postSessionStep(t, srv.Handler(), "/v1/sessions/start", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("single-call start: status %d, body=%s", rr.Code, rr.Body.String())
	}

	var resp sessionserver.SessionResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.State != string(session.StateRunning) {
		t.Errorf("state = %q, want running", resp.State)
	}
	// The binding is published even though the follow-on self-renew acquire
	// faulted: a committed running row is never left without a binding.
	if _, ok := registry.Get("sess-renew-blip"); !ok {
		t.Fatal("single-call start committed to running but published no binding (lease-without-binding decoupling)")
	}
	// The lease is held by the binding replica, so lease and binding are
	// co-located on the same replica.
	got, gerr := leases.Get(context.Background(), "acme", "sess-renew-blip")
	if gerr != nil {
		t.Fatalf("coordination lease not held after bind: %v", gerr)
	}
	if got.Holder != "replica-a" {
		t.Fatalf("lease holder = %q, want replica-a (the binding replica)", got.Holder)
	}
}
