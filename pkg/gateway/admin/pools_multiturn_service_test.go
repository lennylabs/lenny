// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
)

// multiTurnServiceWarningType is the §5.2 / §3.6 audit event the gateway
// emits when a multi_turn runtime is bound to a service-mode pool.
const multiTurnServiceWarningType = "pool.multi_turn_service_no_continuity"

// mustCreateInteractionRuntime registers a runtime with the given §5.1
// interaction model so the multi_turn-on-service warning can be exercised.
func mustCreateInteractionRuntime(t *testing.T, runtimes *runtimestore.Memory, name string, interaction runtimestore.RuntimeInteraction) {
	t.Helper()
	rt := runtimestore.Runtime{Name: name}
	if interaction != "" {
		rt.Capabilities = &runtimestore.RuntimeCapabilities{
			Interaction: interaction,
			// A multi_turn runtime must declare injection support (§5.1), but
			// the warning derivation reads only the interaction model, so the
			// injection flag is set here purely to mirror a valid runtime.
			Injection: runtimestore.InjectionCapability{Supported: interaction == runtimestore.InteractionMultiTurn},
		}
	}
	if err := runtimes.Create(context.Background(), rt); err != nil {
		t.Fatalf("create runtime %s: %v", name, err)
	}
}

// findWarning returns the first emitted multi_turn-on-service warning for the
// named pool, or nil when none was emitted.
func findWarning(events []admin.AuditEvent, pool string) *admin.AuditEvent {
	for i := range events {
		if events[i].Type == multiTurnServiceWarningType && events[i].TargetResource == pool {
			return &events[i]
		}
	}
	return nil
}

// spec: §5.2 (multi_turn permitted on service mode, registration-time
// warning), §3.6 (service-mode conversationContinuity), §7.1 line 74.
// A multi_turn runtime bound to a service-mode pool is admitted but warns:
// service mode preserves no cross-message conversation continuity. A one_shot
// runtime on a service pool, and a multi_turn runtime on a session pool, both
// emit no warning.
func TestCreateServicePoolWarnsMultiTurn_spec_5_2(t *testing.T) {
	router, _, runtimes, audit := newPoolAdmin(t)
	mustCreateInteractionRuntime(t, runtimes, "mt-rt", runtimestore.InteractionMultiTurn)
	mustCreateInteractionRuntime(t, runtimes, "os-rt", runtimestore.InteractionOneShot)

	// multi_turn runtime + service mode → admitted, with the warning.
	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:          "mt-service",
		RuntimeRef:    "mt-rt",
		ExecutionMode: "service",
		MaxConcurrent: 4,
		ResourceClass: "small",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("multi_turn service pool must be admitted: status %d body=%s", rr.Code, rr.Body.String())
	}
	w := findWarning(audit.snapshot(), "mt-service")
	if w == nil {
		t.Fatalf("no %s event for mt-service; events=%+v", multiTurnServiceWarningType, audit.snapshot())
	}
	if w.Detail["runtimeRef"] != "mt-rt" || w.Detail["conversationContinuity"] != "none" {
		t.Errorf("warning detail = %+v, want runtimeRef=mt-rt / conversationContinuity=none", w.Detail)
	}

	// one_shot runtime + service mode → admitted, no warning (continuity is
	// irrelevant to a one_shot runtime).
	rr = poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:          "os-service",
		RuntimeRef:    "os-rt",
		ExecutionMode: "service",
		MaxConcurrent: 4,
		ResourceClass: "small",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("one_shot service pool must be admitted: status %d body=%s", rr.Code, rr.Body.String())
	}
	if w := findWarning(audit.snapshot(), "os-service"); w != nil {
		t.Errorf("one_shot service pool must not warn, got %+v", w)
	}

	// multi_turn runtime on a session pool → admitted, no warning (session
	// mode preserves conversation continuity).
	rr = poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:          "mt-session",
		RuntimeRef:    "mt-rt",
		ExecutionMode: "session",
		ResourceClass: "small",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("multi_turn session pool must be admitted: status %d body=%s", rr.Code, rr.Body.String())
	}
	if w := findWarning(audit.snapshot(), "mt-session"); w != nil {
		t.Errorf("multi_turn session pool must not warn, got %+v", w)
	}
}

// spec: §5.2 (registration-time warning re-evaluated on update), §3.6.
// Switching a session pool bound to a multi_turn runtime into service mode
// via PUT emits the warning, mirroring the create-path derivation.
func TestUpdatePoolToServiceWarnsMultiTurn_spec_5_2(t *testing.T) {
	router, _, runtimes, audit := newPoolAdmin(t)
	mustCreateInteractionRuntime(t, runtimes, "mt-rt", runtimestore.InteractionMultiTurn)

	// Seed a session pool on the multi_turn runtime: no warning at create.
	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:          "mt-pool",
		RuntimeRef:    "mt-rt",
		ExecutionMode: "session",
		ResourceClass: "small",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("seed session pool: status %d body=%s", rr.Code, rr.Body.String())
	}
	if w := findWarning(audit.snapshot(), "mt-pool"); w != nil {
		t.Fatalf("session pool create must not warn, got %+v", w)
	}

	// PUT executionMode: service → admitted, with the warning.
	service := "service"
	maxConcurrent := 4
	rr = poolReq(t, router.Handler(), http.MethodPut, "/v1/admin/pools/mt-pool",
		admin.UpdatePoolRequest{ExecutionMode: &service, MaxConcurrent: &maxConcurrent})
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT to service on multi_turn session pool: status %d body=%s", rr.Code, rr.Body.String())
	}
	if w := findWarning(audit.snapshot(), "mt-pool"); w == nil {
		t.Errorf("no %s event after PUT to service; events=%+v", multiTurnServiceWarningType, audit.snapshot())
	}
}
