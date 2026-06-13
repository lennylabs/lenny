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
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	sandboxcond "github.com/lennylabs/lenny/pkg/sandbox/condition"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// spec: §6.2 lines 82, 172, 214 — `running → suspended` on
// interrupt_request + interrupt_acknowledged is a session-model state on the
// Postgres session row, not a coarse Sandbox.status.phase value. The pod
// stays in the coarse `claimed` phase; the gateway records a Suspended
// condition so the operator-visible history mirrors the interrupt. F-6.2.13.

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
		Status:     lennyv1.SandboxStatus{Phase: string(state.Claimed), PodIP: "10.0.0.1"},
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
	// spec: §6.2 lines 82, 172 — suspended is a session-model state; the pod
	// stays in the coarse `claimed` phase.
	if sb.Status.Phase != string(state.Claimed) {
		t.Errorf("F-6.2.13: Sandbox.status.phase = %q, want claimed (suspended is a session-model state)", sb.Status.Phase)
	}
	// spec: §6.2 line 305 / §4.6.1 — the acknowledged interrupt records a
	// Suspended condition with the InterruptAcknowledged reason. F-6.2.12.
	if cond := apimeta.FindStatusCondition(sb.Status.Conditions, sandboxcond.Suspended); cond == nil {
		t.Errorf("F-6.2.12: missing %s condition; have %v", sandboxcond.Suspended, sb.Status.Conditions)
	} else if cond.Reason != "InterruptAcknowledged" {
		t.Errorf("F-6.2.12: condition reason = %q, want InterruptAcknowledged", cond.Reason)
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
		Status:     lennyv1.SandboxStatus{Phase: string(state.Claimed), PodIP: "10.0.0.2"},
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
	// spec: §6.2 lines 82, 172 — the pod stays in the coarse `claimed` phase.
	if sb.Status.Phase != string(state.Claimed) {
		t.Errorf("F-6.2.13 timeout path: Sandbox.status.phase = %q, want claimed", sb.Status.Phase)
	}
	// spec: §7.2 line 169 / §4.6.1 — the forced suspend records the
	// InterruptTimeout reason so the history distinguishes it. F-6.2.12.
	if cond := apimeta.FindStatusCondition(sb.Status.Conditions, sandboxcond.Suspended); cond == nil {
		t.Errorf("F-6.2.12: missing %s condition; have %v", sandboxcond.Suspended, sb.Status.Conditions)
	} else if cond.Reason != "InterruptTimeout" {
		t.Errorf("F-6.2.12: condition reason = %q, want InterruptTimeout", cond.Reason)
	}
}

// spec: §4.7 STATUS_BUSY — the row stays running; the Sandbox must
// therefore stay in the coarse `claimed` phase with no Suspended condition
// written (no spurious suspend).
func TestInterruptBusyLeavesSandboxClaimed_spec_6_2_214(t *testing.T) {
	store := memstore.New()
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_busy", TenantID: "acme", RuntimeRef: "echo",
		State: session.StateRunning, CreatedAt: now, UpdatedAt: now,
	})

	cluster := podBindClient(t, &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sbx-bz", Namespace: podTestNS},
		Status:     lennyv1.SandboxStatus{Phase: string(state.Claimed), PodIP: "10.0.0.3"},
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
	if sb.Status.Phase != string(state.Claimed) {
		t.Errorf("BUSY must not flip Sandbox.status.phase; got %q, want claimed", sb.Status.Phase)
	}
	// BUSY does not suspend the session, so no Suspended condition is recorded.
	if cond := apimeta.FindStatusCondition(sb.Status.Conditions, sandboxcond.Suspended); cond != nil {
		t.Errorf("BUSY must not record a Suspended condition; got %+v", cond)
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

// spec: §6.2 line 214 — the binder's Suspend method is idempotent: a
// retry re-applies the Suspended condition in place (status.conditions is a
// listType=map keyed by type) and never errors.
func TestBinderSuspendIsIdempotent_spec_6_2_214(t *testing.T) {
	cluster := podBindClient(t, &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sbx-id", Namespace: podTestNS},
		Status:     lennyv1.SandboxStatus{Phase: string(state.Claimed), PodIP: "10.0.0.4"},
	})
	binder := &podsession.Binder{Client: cluster, Namespace: podTestNS}

	for i := 0; i < 2; i++ {
		if err := binder.Suspend(context.Background(), "sbx-id", ""); err != nil {
			t.Errorf("Suspend iteration %d: %v", i, err)
		}
	}
}

// spec: §6.2 lines 82, 172 — Suspend never writes Sandbox.status.phase
// (suspended is a session-model state, not a coarse occupancy value); the
// pod's coarse phase is preserved while the Suspended condition is recorded.
func TestBinderSuspendPreservesCoarsePhase_spec_6_2_214(t *testing.T) {
	cluster := podBindClient(t, &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sbx-keep", Namespace: podTestNS},
		Status:     lennyv1.SandboxStatus{Phase: string(state.Claimed), PodIP: "10.0.0.5"},
	})
	binder := &podsession.Binder{Client: cluster, Namespace: podTestNS}

	if err := binder.Suspend(context.Background(), "sbx-keep", ""); err != nil {
		t.Errorf("Suspend: %v", err)
	}
	var sb lennyv1.Sandbox
	if err := cluster.Get(context.Background(), client.ObjectKey{Namespace: podTestNS, Name: "sbx-keep"}, &sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Status.Phase != string(state.Claimed) {
		t.Errorf("phase = %q, want claimed (unchanged; suspended is session-model)", sb.Status.Phase)
	}
	if cond := apimeta.FindStatusCondition(sb.Status.Conditions, sandboxcond.Suspended); cond == nil {
		t.Errorf("missing %s condition; have %v", sandboxcond.Suspended, sb.Status.Conditions)
	}
}

// spec: a missing Sandbox (already reclaimed) must no-op so the
// interrupt handler completes cleanly.
func TestBinderSuspendMissingSandboxIsNoOp_spec_6_2_214(t *testing.T) {
	cluster := podBindClient(t /* no sandbox */)
	binder := &podsession.Binder{Client: cluster, Namespace: podTestNS}
	if err := binder.Suspend(context.Background(), "sbx-missing", ""); err != nil {
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
