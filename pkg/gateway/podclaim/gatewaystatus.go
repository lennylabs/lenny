// SPDX-License-Identifier: MIT

package podclaim

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/admission/ownership"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// ApplyGatewayPhase writes Sandbox.status.phase via SSA Apply under the
// §4.6.3 gateway field manager and ForceOwnership. The §4.6.3 ownership
// table records status.phase as WPC-owned by default; the gateway claims
// it for the duration of the session-mode lifecycle (idle → claimed →
// receiving_uploads → finalizing_workspace → running_setup →
// starting_session → attached → completed/failed/cancelled) and the
// §5.2 slot-mode lifecycle (idle → slot_active → idle). ForceOwnership
// is the spec-intended cross-manager override that lets a write succeed
// even though the default-owning manager (WarmPoolController) still
// claims the field.
//
// On success sb.Status.Phase is updated in place. The apply preserves
// the in-memory ActiveSlots and TenantID values: those are gateway-owned
// status fields that must not be cleared by a phase-only write.
//
// Drain handoff: this helper is NOT used by the binder.drain or
// slotbinder.drainSandbox paths. The drain step writes phase=draining
// expressly to hand control back to the WPC, which then drives
// draining → terminated. SSA + ForceOwnership would leave the gateway
// holding status.phase indefinitely and the WPC's drain → terminated
// apply would conflict (the WPC reconciler does not use ForceOwnership
// per §4.6.3 "never force-conflicts" guidance for WPC/PSC). Direct
// Update is the documented yield mechanism: per the Kubernetes SSA
// spec, a non-Apply request removes other managers' claims on the
// fields it writes, so the WPC can re-claim status.phase to write
// terminated. See drain() in podsession/binder.go.
//
// spec: §4.6.3 ownership table; §5.2 slot-reservation gateway-side
// transitions; §6.2 lines 83-94 setup chain; §6.2 lines 105-117 terminal
// dispositions.
func ApplyGatewayPhase(ctx context.Context, cl client.Client, sb *lennyv1.Sandbox, phase state.State) error {
	patch := gatewayStatusPatch(sb.Name, sb.Namespace,
		string(phase), sb.Status.ActiveSlots, sb.Status.TenantID)
	if err := cl.Status().Patch(ctx, patch, client.Apply,
		client.FieldOwner(string(ownership.Gateway)),
		client.ForceOwnership); err != nil {
		return fmt.Errorf("podclaim: apply gateway phase %s on sandbox %s: %w", phase, sb.Name, err)
	}
	sb.Status.Phase = string(phase)
	return nil
}
