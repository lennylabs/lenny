//go:build integration

// SPDX-License-Identifier: MIT

// Tier-4 integration test for the §25.10 configuration-drift
// reconciliation surface: POST /v1/admin/drift/reconcile with
// confirm:true applying the desired state through the gateway admin API.
//
// This test is currently a placeholder. Reconciliation is specified to
// apply desired state by calling admin API PUT endpoints via
// GatewayClient, but no production ResourceApplier is wired into
// cmd/lenny-ops (deps.go never calls Service.SetApplier), so a
// confirm:true reconcile fails closed with DRIFT_RECONCILE_UNAVAILABLE.
// Exercising the real gateway-side RBAC/validation/audit interaction,
// the per-resource operation_progressed event, and the drift.resource_-
// reconciled audit event requires building and wiring a GatewayClient-
// backed ResourceApplier (a product-code change), plus a harness that
// boots cmd/lenny-ops with --gateway-url against a real cmd/lenny-gateway
// so the reconcile PUTs land on a live admin API. Until that lands the
// assertions below cannot run, so the test skips rather than assert a
// path that is unreachable in the current binary.
package tier4_integration_test

import "testing"

// TestOpsDriftReconcileAgainstGatewayE2E boots cmd/lenny-ops wired to a
// real cmd/lenny-gateway, seeds a drifted pool, and drives POST
// /v1/admin/drift/reconcile with confirm:true. It asserts the drifted
// pool converges to the desired state through the gateway admin API and
// that the reconcile emits a drift.resource_reconciled audit event and
// an operation_progressed event for each reconciled resource.
//
// spec: 25.10 (Reconciliation) — "POST /v1/admin/drift/reconcile calls
// admin API PUT endpoints via GatewayClient to apply the desired state.
// Each call goes through full RBAC, validation, and audit on the gateway
// side." and "The operation_progressed event fires on every resource
// reconciliation.", with drift.resource_reconciled among the §25.10
// Audit Events.
// diagnosis: a failure means the confirm:true reconcile path did not
// apply desired state through the real gateway admin API — the pool did
// not converge, the gateway-side RBAC/validation/audit was not
// exercised, or the per-resource operation_progressed and
// drift.resource_reconciled events did not fire.
func TestOpsDriftReconcileAgainstGatewayE2E(t *testing.T) {
	t.Skip("reconcile-against-gateway needs a production GatewayClient-backed ResourceApplier wired into cmd/lenny-ops (SetApplier is never called); confirm:true reconcile currently fails closed with DRIFT_RECONCILE_UNAVAILABLE")
}
