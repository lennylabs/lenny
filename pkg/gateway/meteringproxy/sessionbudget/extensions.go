// SPDX-License-Identifier: MIT

package sessionbudget

import (
	"context"

	"github.com/lennylabs/lenny/pkg/gateway/seam"
)

// ExtendVia returns the extension seam that requests a budget extension
// through the platform tools seam's GWSEAM-ExtendForBudget. It threads both
// contexts and the tenant the enforcer holds, and maps the outcome onto the
// enforcer's tri-state. An extension does not carry a budget or consumed
// amount: budget exhaustion asks for as much headroom as the §8.6 ceiling
// allows, which the platform tools seam derives from the tree budget. A
// dispatch error, and any outcome other than granted or pending, fails
// closed to Terminal, so an unresolvable or errored extension denies and
// terminates the session rather than granting tokens. A nil ext returns a
// nil seam, the non-extendable path.
//
// spec: GWSEAM-ExtendForBudget, §8.6
func ExtendVia(ext seam.BudgetExtensions) ExtendOnExhaustion {
	if ext == nil {
		return nil
	}
	return func(reqCtx, waitCtx context.Context, tenantID, sessionID string, _, _ int64) Outcome {
		outcome, err := ext.ExtendForBudget(reqCtx, waitCtx, tenantID, sessionID)
		if err != nil {
			return Terminal
		}
		switch outcome {
		case seam.BudgetGranted:
			return Granted
		case seam.BudgetPending:
			return Pending
		default:
			return Terminal
		}
	}
}
