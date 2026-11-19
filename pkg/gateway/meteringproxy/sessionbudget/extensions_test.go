// SPDX-License-Identifier: MIT

package sessionbudget_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/meteringproxy/sessionbudget"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
)

// fixedExtension answers every extension with one outcome.
type fixedExtension struct {
	outcome        seam.BudgetExtension
	err            error
	tenant, sessID string
}

func (f *fixedExtension) ExtendForBudget(_, _ context.Context, tenantID, sessionID string) (seam.BudgetExtension, error) {
	f.tenant, f.sessID = tenantID, sessionID
	return f.outcome, f.err
}

// TestExtendViaMapsEachOutcomeAndFailsClosed checks that the extension seam
// threads the tenant and session to GWSEAM-ExtendForBudget, maps a granted
// and a pending extension onto the enforcer's outcomes, and fails a dispatch
// error and any other outcome closed to Terminal; a nil extension is the
// non-extendable path.
//
// spec: GWSEAM-ExtendForBudget
func TestExtendViaMapsEachOutcomeAndFailsClosed(t *testing.T) {
	if sessionbudget.ExtendVia(nil) != nil {
		t.Error("ExtendVia(nil) is not nil")
	}
	for _, tc := range []struct {
		outcome seam.BudgetExtension
		err     error
		want    sessionbudget.Outcome
	}{
		{seam.BudgetGranted, nil, sessionbudget.Granted},
		{seam.BudgetPending, nil, sessionbudget.Pending},
		{seam.BudgetTerminal, nil, sessionbudget.Terminal},
		{seam.BudgetExtension(99), nil, sessionbudget.Terminal},
		{seam.BudgetGranted, errors.New("dispatch failed"), sessionbudget.Terminal},
	} {
		ext := &fixedExtension{outcome: tc.outcome, err: tc.err}
		got := sessionbudget.ExtendVia(ext)(context.Background(), context.Background(), "acme", "s1", 100, 120)
		if got != tc.want {
			t.Errorf("outcome %v err %v = %v, want %v", tc.outcome, tc.err, got, tc.want)
		}
		if ext.tenant != "acme" || ext.sessID != "s1" {
			t.Errorf("extension asked for %q/%q, want acme/s1", ext.tenant, ext.sessID)
		}
	}
}
