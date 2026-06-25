// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 §5.1 injection-gate fail-closed probe (F-5.1.20, F-MS6). The
// §5.1 mid-session injection gate consults two backing stores through
// runtimecapoverride.ResolveForTenant: the runtime registry and the
// per-tenant capability-override store. The override store holds the
// F-5.1.20 tenant-narrowing value (a tenant setting injection.supported:
// false on an otherwise injection-capable runtime). A transient read
// error from the override store must fail the gate closed with a
// retryable 503 SERVICE_UNAVAILABLE rather than admit injection against
// the un-overlaid base runtime, which still reports injection supported.
//
// This is the security-relevant fail-open vector F-MS6 closed: before the
// fix, ResolveForTenant swallowed the override-store error and returned
// the un-overlaid base runtime with err==nil, so the gate evaluated the
// injection check against the wrong runtime and admitted injection for the
// exact tenant the gate exists to narrow. The suite drives the gateway
// REST POST /v1/sessions/{id}/messages handler through sessionserver.New
// (the same wiring the gateway binary uses) so the security tier exercises
// the fail-closed boundary the way an external client reaches it. It
// complements the in-package component cases in
// pkg/gateway/sessionserver/messages_test.go, asserting the security-tier
// fail-closed posture on the F-5.1.20 override path and the registry path.
package tier9_security_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/runtimecapoverride"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/transcriptstore"
)

// failClosedRuntimeStore returns a transient (non-not-found) read error
// from Get, simulating a Postgres blip on the runtime-registry read the
// injection gate consults. Every other method delegates to the embedded
// store so a runtime can still be seeded.
type failClosedRuntimeStore struct {
	runtimestore.Store
	err error
}

func (s failClosedRuntimeStore) Get(context.Context, string) (runtimestore.Runtime, error) {
	return runtimestore.Runtime{}, s.err
}

// failClosedOverrideStore returns a transient (non-not-found) read error
// from Get, simulating a Postgres blip on the per-tenant
// capability-override read. This is the F-5.1.20 tenant-narrowing path.
type failClosedOverrideStore struct {
	runtimecapoverride.Store
	err error
}

func (s failClosedOverrideStore) Get(context.Context, string, string) (runtimestore.CapabilityOverride, bool, error) {
	return runtimestore.CapabilityOverride{}, false, s.err
}

func seedInjectionCapableRuntime(t *testing.T) runtimestore.Store {
	t.Helper()
	runtimes := runtimestore.NewMemory()
	if err := runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "chatty", Type: runtimestore.TypeAgent,
		Capabilities: &runtimestore.RuntimeCapabilities{
			Interaction: runtimestore.InteractionMultiTurn,
			Injection:   runtimestore.InjectionCapability{Supported: true},
		},
	}); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	return runtimes
}

func postInjectionMessage(t *testing.T, h http.Handler, id, tenant string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(sessionserver.MessageRequest{
		Messages: []sessionserver.MessagePayload{{Content: "hi"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+id+"/messages", bytes.NewReader(body))
	req.Header.Set("X-Lenny-Tenant-ID", tenant)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func assertInjectionGateFailedClosed(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("injection gate must fail closed with 503: got %d (body=%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "SERVICE_UNAVAILABLE") {
		t.Errorf("body must carry SERVICE_UNAVAILABLE: %s", rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "INJECTION_REJECTED") {
		t.Errorf("a transient store blip must not surface as the policy denial INJECTION_REJECTED: %s", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"retryable":true`) {
		t.Errorf("SERVICE_UNAVAILABLE must be retryable so a client retries once the store recovers: %s", rr.Body.String())
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Error("a retryable 503 SERVICE_UNAVAILABLE must carry a Retry-After header")
	}
}

// failClosedCauseRecorder captures the cause labels passed to the
// lenny_injection_gate_failclosed_total{cause} metric hook so the security
// tier can assert the §5.1 fail-closed branch records the granular
// backing-store cause as a metric, the "and metrics" half of the §15.1
// SERVICE_UNAVAILABLE observability contract.
type failClosedCauseRecorder struct{ causes []string }

func (f *failClosedCauseRecorder) inc(cause string) { f.causes = append(f.causes, cause) }

func assertFailClosedCause(t *testing.T, rec *failClosedCauseRecorder, wantCause string) {
	t.Helper()
	if len(rec.causes) != 1 {
		t.Fatalf("lenny_injection_gate_failclosed_total fired %d times, want exactly 1 (causes=%v)", len(rec.causes), rec.causes)
	}
	if rec.causes[0] != wantCause {
		t.Errorf("fail-closed metric cause=%q, want %q", rec.causes[0], wantCause)
	}
}

// diagnosis: the §5.1 injection gate admitted mid-session injection on a
// transient per-tenant override-store read error, the F-5.1.20
// tenant-narrowing path the prior ResolveForTenant error swallow hid. A
// store blip on the override read is being treated as "no override
// applied", so the gate evaluates injection support against the
// un-overlaid base runtime and admits injection for the tenant the gate
// exists to narrow. The gate must fail closed.
// spec: 5.1 (injection fail-closed, F-5.1.20 tenant injection-disable), 15.1 (SERVICE_UNAVAILABLE).
func TestInjectionGateFailsClosedOnTransientOverrideStoreError_spec_5_1_49(t *testing.T) {
	store := memstore.New()
	overrides := failClosedOverrideStore{
		Store: runtimecapoverride.NewMemory(),
		err:   errors.New("override store pg read timeout"),
	}
	rec := &failClosedCauseRecorder{}
	srv := sessionserver.New(store, sessionserver.Options{
		Executor:                   executor.NewEchoExecutor(),
		Transcripts:                transcriptstore.NewMemory(),
		Runtimes:                   seedInjectionCapableRuntime(t),
		CapabilityOverrides:        overrides,
		IncInjectionGateFailClosed: rec.inc,
	})
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "acme1", TenantID: "acme", State: session.StateRunning,
		RuntimeRef: "chatty", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	rr := postInjectionMessage(t, srv.Handler(), "acme1", "acme")
	assertInjectionGateFailedClosed(t, rr)
	// The fail-closed branch must record the granular override-store cause
	// as a metric so the F-5.1.20 path is observable beyond the log line.
	assertFailClosedCause(t, rec, "override_store")
}

// diagnosis: the §5.1 injection gate admitted injection on a transient
// runtime-registry read error instead of failing closed; the registry
// store blip is being treated as a definite "injection supported" answer.
// spec: 5.1 (injection fail-closed), 15.1 (SERVICE_UNAVAILABLE).
func TestInjectionGateFailsClosedOnTransientRegistryError_spec_5_1(t *testing.T) {
	store := memstore.New()
	runtimes := failClosedRuntimeStore{
		Store: seedInjectionCapableRuntime(t),
		err:   errors.New("registry store pg read timeout"),
	}
	srv := sessionserver.New(store, sessionserver.Options{
		Executor:    executor.NewEchoExecutor(),
		Transcripts: transcriptstore.NewMemory(),
		Runtimes:    runtimes,
	})
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "s1", TenantID: "acme", State: session.StateRunning,
		RuntimeRef: "chatty", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	rr := postInjectionMessage(t, srv.Handler(), "s1", "acme")
	assertInjectionGateFailedClosed(t, rr)
}
