// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/gateway/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// spec: §6.2 line 214 — `running → suspended` on interrupt_request +
// interrupt_acknowledged must also advance the Sandbox.status.phase
// from `attached` to `suspended` so the operator-visible phase mirrors
// the session row. F-6.2.13.

func suspendStubAdapter(t *testing.T, status adapterv1.InterruptResponse_Status) (*stubAdapterServer, *adapterclient.Client) {
	t.Helper()
	stub := &stubAdapterServer{status: status}
	cl := dialStubAdapter(t, stub)
	return stub, cl
}

func TestInterruptAdvancesSandboxToSuspended_spec_6_2_214(t *testing.T) {
	store := memstore.New()
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_sus", TenantID: "acme", RuntimeRef: "echo",
		State: session.StateRunning, CreatedAt: now, UpdatedAt: now,
	})

	cluster := podBindClient(t, &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sbx-att", Namespace: podTestNS},
		Status:     lennyv1.SandboxStatus{Phase: string(state.Attached), PodIP: "10.0.0.1"},
	})
	binder := &podsession.Binder{Client: cluster, Namespace: podTestNS}
	_, adapter := suspendStubAdapter(t, adapterv1.InterruptResponse_STATUS_ACKNOWLEDGED)
	reg := podsession.NewRegistry()
	reg.Put(&podsession.BindResult{
		SessionID: "sess_sus", TenantID: "acme",
		SandboxName: "sbx-att", Adapter: adapter,
	})

	srv := sessionserver.New(store, sessionserver.Options{
		PodBinder:   binder,
		PodRegistry: reg,
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_sus/interrupt", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	row, _ := store.Get(context.Background(), "acme", "sess_sus")
	if row.State != session.StateSuspended {
		t.Errorf("row state = %q, want suspended", row.State)
	}
	var sb lennyv1.Sandbox
	if err := cluster.Get(context.Background(), client.ObjectKey{Namespace: podTestNS, Name: "sbx-att"}, &sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Status.Phase != string(state.Suspended) {
		t.Errorf("F-6.2.13: Sandbox.status.phase = %q, want suspended", sb.Status.Phase)
	}
}

// spec: §7.2 line 169 — adapter-forced suspended (INTERRUPT_TIMEOUT)
// still produces the §6.2 Sandbox transition.
func TestInterruptAdvancesSandboxOnTimeout_spec_6_2_214(t *testing.T) {
	store := memstore.New()
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_to", TenantID: "acme", RuntimeRef: "echo",
		State: session.StateRunning, CreatedAt: now, UpdatedAt: now,
	})

	cluster := podBindClient(t, &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sbx-to", Namespace: podTestNS},
		Status:     lennyv1.SandboxStatus{Phase: string(state.Attached), PodIP: "10.0.0.2"},
	})
	binder := &podsession.Binder{Client: cluster, Namespace: podTestNS}
	_, adapter := suspendStubAdapter(t, adapterv1.InterruptResponse_STATUS_INTERRUPT_TIMEOUT)
	reg := podsession.NewRegistry()
	reg.Put(&podsession.BindResult{
		SessionID: "sess_to", TenantID: "acme",
		SandboxName: "sbx-to", Adapter: adapter,
	})

	srv := sessionserver.New(store, sessionserver.Options{PodBinder: binder, PodRegistry: reg})

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_to/interrupt", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var sb lennyv1.Sandbox
	if err := cluster.Get(context.Background(), client.ObjectKey{Namespace: podTestNS, Name: "sbx-to"}, &sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Status.Phase != string(state.Suspended) {
		t.Errorf("F-6.2.13 timeout path: Sandbox.status.phase = %q, want suspended", sb.Status.Phase)
	}
}

// spec: §4.7 STATUS_BUSY — the row stays running; the Sandbox phase
// must therefore stay attached (no spurious suspend write).
func TestInterruptBusyLeavesSandboxAttached_spec_6_2_214(t *testing.T) {
	store := memstore.New()
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_busy", TenantID: "acme", RuntimeRef: "echo",
		State: session.StateRunning, CreatedAt: now, UpdatedAt: now,
	})

	cluster := podBindClient(t, &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sbx-bz", Namespace: podTestNS},
		Status:     lennyv1.SandboxStatus{Phase: string(state.Attached), PodIP: "10.0.0.3"},
	})
	binder := &podsession.Binder{Client: cluster, Namespace: podTestNS}
	_, adapter := suspendStubAdapter(t, adapterv1.InterruptResponse_STATUS_BUSY)
	reg := podsession.NewRegistry()
	reg.Put(&podsession.BindResult{
		SessionID: "sess_busy", TenantID: "acme",
		SandboxName: "sbx-bz", Adapter: adapter,
	})

	srv := sessionserver.New(store, sessionserver.Options{PodBinder: binder, PodRegistry: reg})

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_busy/interrupt", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
	var sb lennyv1.Sandbox
	if err := cluster.Get(context.Background(), client.ObjectKey{Namespace: podTestNS, Name: "sbx-bz"}, &sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Status.Phase != string(state.Attached) {
		t.Errorf("BUSY must not flip Sandbox.status.phase; got %q, want attached", sb.Status.Phase)
	}
}

// spec: the no-binding fallback path must still flip the session row but
// MUST NOT panic when there is no Sandbox to update. F-6.2.13.
func TestInterruptSuspendNoBindingSafe_spec_6_2_214(t *testing.T) {
	store := memstore.New()
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_nb", TenantID: "acme", RuntimeRef: "echo",
		State: session.StateRunning, CreatedAt: now, UpdatedAt: now,
	})
	// No binder, no registry — row-only mode.
	srv := sessionserver.New(store, sessionserver.Options{})

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_nb/interrupt", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	row, _ := store.Get(context.Background(), "acme", "sess_nb")
	if row.State != session.StateSuspended {
		t.Errorf("row state = %q, want suspended", row.State)
	}
}

// spec: §6.2 line 214 — the binder's Suspend method must be idempotent
// when the Sandbox is already in `suspended` so a retry never errors.
func TestBinderSuspendIsIdempotent_spec_6_2_214(t *testing.T) {
	cluster := podBindClient(t, &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sbx-id", Namespace: podTestNS},
		Status:     lennyv1.SandboxStatus{Phase: string(state.Suspended), PodIP: "10.0.0.4"},
	})
	binder := &podsession.Binder{Client: cluster, Namespace: podTestNS}

	for i := 0; i < 2; i++ {
		if err := binder.Suspend(context.Background(), "sbx-id"); err != nil {
			t.Errorf("Suspend iteration %d: %v", i, err)
		}
	}
}

// spec: §6.2 — Suspend must NOT advance a Sandbox whose phase has no
// legal edge to suspended (the row is the source of truth; the Sandbox
// is the best-effort projection). The current state must be preserved.
func TestBinderSuspendInvalidEdgeIsNoOp_spec_6_2_214(t *testing.T) {
	cluster := podBindClient(t, &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sbx-bad", Namespace: podTestNS},
		Status:     lennyv1.SandboxStatus{Phase: string(state.Idle), PodIP: "10.0.0.5"},
	})
	binder := &podsession.Binder{Client: cluster, Namespace: podTestNS}

	if err := binder.Suspend(context.Background(), "sbx-bad"); err != nil {
		t.Errorf("Suspend on invalid edge must no-op, got error: %v", err)
	}
	var sb lennyv1.Sandbox
	if err := cluster.Get(context.Background(), client.ObjectKey{Namespace: podTestNS, Name: "sbx-bad"}, &sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Status.Phase != string(state.Idle) {
		t.Errorf("phase = %q, want idle (unchanged)", sb.Status.Phase)
	}
}

// spec: a missing Sandbox (already reclaimed) must no-op so the
// interrupt handler completes cleanly.
func TestBinderSuspendMissingSandboxIsNoOp_spec_6_2_214(t *testing.T) {
	cluster := podBindClient(t /* no sandbox */)
	binder := &podsession.Binder{Client: cluster, Namespace: podTestNS}
	if err := binder.Suspend(context.Background(), "sbx-missing"); err != nil {
		t.Errorf("Suspend on missing sandbox: %v", err)
	}
}

// Smoke: enforce that the stub adapter dial helper is still callable —
// without this Go cleanly elides the import for the `grpc` and
// `insecure` packages on a refactor.
func TestSuspendInterruptHelpersImported_smoke(t *testing.T) {
	_ = grpc.NewServer()
	_ = insecure.NewCredentials()
}
