// SPDX-License-Identifier: MIT

// Package condition writes the §6.2 line-305 Sandbox `.status.conditions`
// lifecycle history that §4.6.1 names as part of the authoritative state.
// It is a leaf utility both the gateway (podsession) and the controller
// (warmpool orphan-claim GC) depend on, so it lives under pkg/sandbox to
// avoid a podclaim ↔ warmpool import cycle.
//
// spec: §6.2 line 305 (.status.conditions is authoritative); §4.6.1
// lifecycle-manager condition history (orphan claim, suspend, terminal
// disposition).
package condition

import (
	"context"
	"fmt"
	"strings"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// Sandbox lifecycle condition types written by Apply. They are the §4.6.1
// history entries operators read to learn why a Sandbox sits in a given
// §6.2 phase.
const (
	// Suspended records the §6.2 line 214 attached → suspended interrupt
	// transition. Reason distinguishes an acknowledged interrupt from a
	// forced (timeout) one.
	Suspended = "Suspended"
	// Terminated records the §6.2 lines 105-117 session-terminal
	// disposition (completed / failed / cancelled / expired).
	Terminated = "Terminated"
	// OrphanClaimReclaimed records the §4.6.1 orphan-SandboxClaim
	// collection that returned a Sandbox to idle.
	OrphanClaimReclaimed = "OrphanClaimReclaimed"
)

// Apply records a single §6.2 line-305 Sandbox status.conditions entry via
// SSA Apply. Each condition Type is owned by its own field manager
// (`lenny-sandbox-cond-<type>`), so distinct lifecycle conditions (suspend,
// terminal disposition, orphan-claim reclaim, etc.) accumulate independently
// rather than overwriting one another, and a condition write never disturbs
// the §4.6.3 WPC-owned status.phase or the gateway's own phase claim (the
// apply carries only the conditions list; every other status field is
// omitempty and therefore unclaimed). Re-applying the same Type updates that
// entry in place because status.conditions is a `listType=map` keyed by
// `type`.
//
// LastTransitionTime defaults to now and ObservedGeneration to the Sandbox's
// current generation when the caller leaves them unset; Status defaults to
// True. On success the condition is merged into sb.Status.Conditions in
// place so a caller chaining further writes sees a consistent view.
func Apply(ctx context.Context, cl client.Client, sb *lennyv1.Sandbox, cond metav1.Condition) error {
	if cond.Type == "" {
		return fmt.Errorf("sandbox/condition: empty condition type for sandbox %s", sb.Name)
	}
	if cond.Status == "" {
		cond.Status = metav1.ConditionTrue
	}
	if cond.LastTransitionTime.IsZero() {
		cond.LastTransitionTime = metav1.Now()
	}
	if cond.ObservedGeneration == 0 {
		cond.ObservedGeneration = sb.Generation
	}
	patch := &lennyv1.Sandbox{
		TypeMeta: metav1.TypeMeta{
			APIVersion: lennyv1.GroupVersion.String(),
			Kind:       "Sandbox",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      sb.Name,
			Namespace: sb.Namespace,
		},
	}
	patch.Status.Conditions = []metav1.Condition{cond}
	fieldOwner := "lenny-sandbox-cond-" + strings.ToLower(cond.Type)
	if err := cl.Status().Patch(ctx, patch, client.Apply,
		client.FieldOwner(fieldOwner), client.ForceOwnership); err != nil {
		return fmt.Errorf("sandbox/condition: apply %s on sandbox %s: %w", cond.Type, sb.Name, err)
	}
	apimeta.SetStatusCondition(&sb.Status.Conditions, cond)
	return nil
}

// TerminalReason maps a §6.2 terminal phase to the PascalCase reason
// recorded on the Terminated condition. It returns "" for a non-terminal
// phase.
func TerminalReason(phase state.State) string {
	switch phase {
	case state.Completed:
		return "Completed"
	case state.Failed:
		return "Failed"
	case state.Cancelled:
		return "Cancelled"
	case state.Expired:
		return "Expired"
	default:
		return ""
	}
}
