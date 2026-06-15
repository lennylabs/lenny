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
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// spec: §7.2 / §8.8 — `running → suspended` on interrupt_request +
// interrupt_acknowledged is a session-model state on the Postgres session row.
// The interrupt-suspension fact (SuspendedAt + SuspendedReason) lives on the
// session row, not on Sandbox.status.conditions: the gateway lost
// sandboxes/status RBAC and is no longer a Sandbox.status writer (§4.6.3), so
// the gateway writes no Sandbox condition for the suspend. The pod stays in
// the coarse `claimed` phase (the WarmPoolController is the sole Sandbox.status
// writer). F-6.2.12.

func suspendStubAdapter(t *testing.T, status adapterv1.InterruptResponse_Status) (*stubAdapterServer, *adapterclient.Client) {
	t.Helper()
	stub := &stubAdapterServer{status: status}
	cl := dialStubAdapter(t, stub)
	return stub, cl
}

func TestInterruptRecordsSuspendedConditionOnRow_spec_7_2(t *testing.T) {
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
	// spec: §7.2 / §8.8 — the acknowledged interrupt stamps the Suspended
	// session-condition fact on the row with the InterruptAcknowledged reason.
	if row.SuspendedAt.IsZero() {
		t.Errorf("F-6.2.12: SuspendedAt not stamped on the session row")
	}
	if row.SuspendedReason != "InterruptAcknowledged" {
		t.Errorf("F-6.2.12: SuspendedReason = %q, want InterruptAcknowledged", row.SuspendedReason)
	}
	// spec: §4.6.3 — the gateway writes no Sandbox.status field; the pod stays
	// in the coarse `claimed` phase and no condition is written.
	sb := getSandbox(t, cluster, "sbx-att")
	if sb.Status.Phase != string(state.Claimed) {
		t.Errorf("F-6.2.13: Sandbox.status.phase = %q, want claimed (suspended is a session-model state)", sb.Status.Phase)
	}
	if len(sb.Status.Conditions) != 0 {
		t.Errorf("§4.6.3: gateway must write no Sandbox condition; got %+v", sb.Status.Conditions)
	}
}

// spec: §7.2 line 169 — adapter-forced suspended (INTERRUPT_TIMEOUT) stamps the
// Suspended session-condition fact with the InterruptTimeout reason so the
// session history distinguishes the forced suspend from an acknowledged one.
func TestInterruptTimeoutRecordsSuspendedConditionOnRow_spec_7_2(t *testing.T) {
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
	row, _ := store.Get(context.Background(), "acme", "sess_to")
	if row.State != session.StateSuspended {
		t.Errorf("row state = %q, want suspended", row.State)
	}
	if row.SuspendedAt.IsZero() {
		t.Errorf("F-6.2.12: SuspendedAt not stamped on the session row")
	}
	if row.SuspendedReason != "InterruptTimeout" {
		t.Errorf("F-6.2.12: SuspendedReason = %q, want InterruptTimeout", row.SuspendedReason)
	}
	// spec: §4.6.3 — the pod stays in the coarse `claimed` phase; no condition.
	sb := getSandbox(t, cluster, "sbx-to")
	if sb.Status.Phase != string(state.Claimed) {
		t.Errorf("F-6.2.13 timeout path: Sandbox.status.phase = %q, want claimed", sb.Status.Phase)
	}
	if len(sb.Status.Conditions) != 0 {
		t.Errorf("§4.6.3: gateway must write no Sandbox condition; got %+v", sb.Status.Conditions)
	}
}

// spec: §4.7 STATUS_BUSY — the row stays running; no Suspended fact is stamped
// on the row and the Sandbox stays in the coarse `claimed` phase.
func TestInterruptBusyLeavesRowRunning_spec_7_2(t *testing.T) {
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
	row, _ := store.Get(context.Background(), "acme", "sess_busy")
	if row.State != session.StateRunning {
		t.Errorf("BUSY must leave the row running; got %q", row.State)
	}
	// BUSY does not suspend the session, so no Suspended fact is stamped.
	if !row.SuspendedAt.IsZero() {
		t.Errorf("BUSY must not stamp SuspendedAt; got %v", row.SuspendedAt)
	}
	sb := getSandbox(t, cluster, "sbx-bz")
	if sb.Status.Phase != string(state.Claimed) {
		t.Errorf("BUSY must not flip Sandbox.status.phase; got %q, want claimed", sb.Status.Phase)
	}
	if len(sb.Status.Conditions) != 0 {
		t.Errorf("§4.6.3: gateway must write no Sandbox condition; got %+v", sb.Status.Conditions)
	}
}

// spec: the no-binding fallback path must still flip the session row and stamp
// the Suspended fact even when there is no Sandbox to update. F-6.2.12.
func TestInterruptSuspendNoBindingStampsRow_spec_7_2(t *testing.T) {
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
	// Even with no pod binding the Suspended fact is stamped on the row: the
	// fact is a session-model field, independent of the Sandbox.
	if row.SuspendedAt.IsZero() {
		t.Errorf("F-6.2.12: SuspendedAt not stamped in row-only mode")
	}
	if row.SuspendedReason != "InterruptAcknowledged" {
		t.Errorf("F-6.2.12: SuspendedReason = %q, want InterruptAcknowledged", row.SuspendedReason)
	}
}

// getSandbox reads a Sandbox by name from the envtest cluster.
func getSandbox(t *testing.T, c client.Client, name string) lennyv1.Sandbox {
	t.Helper()
	var sb lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: podTestNS, Name: name}, &sb); err != nil {
		t.Fatalf("get sandbox %s: %v", name, err)
	}
	return sb
}

// Smoke: enforce that the stub adapter dial helper is still callable —
// without this Go cleanly elides the import for the `grpc` and
// `insecure` packages on a refactor.
func TestSuspendInterruptHelpersImported_smoke(t *testing.T) {
	_ = grpc.NewServer()
	_ = insecure.NewCredentials()
}
