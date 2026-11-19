// SPDX-License-Identifier: MIT

package platformtools

import (
	"context"
	"errors"

	"github.com/lennylabs/lenny/pkg/gateway/seam"
)

// DenialClearer clears the recorded extension denial of a delegation
// subtree. The lease-control budget source satisfies it.
type DenialClearer interface {
	ClearSubtreeDenial(ctx context.Context, tenantID, rootSessionID, sessionID string) error
}

// FloorReader reads the deployment's elicitation floor. The elicitation
// floor provider satisfies it.
type FloorReader interface {
	Floor() string
}

// DelegationAdmin is the platform tools seam's side of the operations the
// API front's admin handlers call on it: clearing a subtree's extension
// denial and reading the elicitation floor.
type DelegationAdmin struct {
	// Denials clears a subtree's denial. Nil answers
	// ErrDelegationAdminUnwired, and the composition root registers the
	// admin route only when a budget source is wired.
	Denials DenialClearer
	// Floors reads the elicitation floor. Nil reads the empty floor.
	Floors FloorReader
}

var _ seam.DelegationAdmin = DelegationAdmin{}

// ErrDelegationAdminUnwired reports a denial clear with no budget source
// wired.
var ErrDelegationAdminUnwired = errors.New("platformtools: no delegation budget source is wired")

// ClearSubtreeDenial clears the recorded denial of budget extensions for
// the subtree.
//
// spec: GWSEAM-ClearSubtreeDenial
func (d DelegationAdmin) ClearSubtreeDenial(ctx context.Context, tenantID, rootSessionID, sessionID string) error {
	if d.Denials == nil {
		return ErrDelegationAdminUnwired
	}
	return d.Denials.ClearSubtreeDenial(ctx, tenantID, rootSessionID, sessionID)
}

// ElicitationFloor returns the deployment's elicitation floor.
//
// spec: GWSEAM-ElicitationFloor
func (d DelegationAdmin) ElicitationFloor() string {
	if d.Floors == nil {
		return ""
	}
	return d.Floors.Floor()
}
