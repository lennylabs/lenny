// SPDX-License-Identifier: MIT

package billingstore

import (
	"context"
	"testing"
)

// TestSessionTotals_SumsPerSession_spec_15_2_3 verifies the per-session
// token + compute totals sum only the named session's events and isolate
// other sessions. spec: §15.1 per-session usage; F-15.2.3.
func TestSessionTotals_SumsPerSession_spec_15_2_3(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	const tenant = "acme"

	for _, e := range []Event{
		{TenantID: tenant, SessionID: "s1", EventType: EventSessionCreated, TokensInput: 100, TokensOutput: 40, PodMinutes: 1.5},
		{TenantID: tenant, SessionID: "s1", EventType: EventSessionCompleted, TokensInput: 50, TokensOutput: 10, PodMinutes: 0.5},
		{TenantID: tenant, SessionID: "s2", EventType: EventSessionCreated, TokensInput: 999, TokensOutput: 999, PodMinutes: 9},
	} {
		if _, err := m.Append(ctx, e); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	got, err := m.SessionTotals(ctx, tenant, "s1")
	if err != nil {
		t.Fatalf("SessionTotals: %v", err)
	}
	if got.TokensInput != 150 || got.TokensOutput != 50 || got.PodMinutes != 2.0 || got.EventCount != 2 {
		t.Fatalf("s1 totals = %+v, want {150 50 2 2}", got)
	}

	// A session with no events returns the zero report and no error.
	empty, err := m.SessionTotals(ctx, tenant, "missing")
	if err != nil {
		t.Fatalf("SessionTotals(missing): %v", err)
	}
	if empty != (SessionUsage{}) {
		t.Fatalf("missing session totals = %+v, want zero", empty)
	}
}

// TestSessionTotals_AppliesCorrections_spec_11_2_1 verifies a §11.2.1
// billing_correction supersedes the referenced original's token figures
// rather than double-counting them. spec: §11.2.1 correction semantics;
// F-15.2.3.
func TestSessionTotals_AppliesCorrections_spec_11_2_1(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	const tenant = "acme"

	orig, err := m.Append(ctx, Event{
		TenantID: tenant, SessionID: "s1", EventType: EventSessionCompleted,
		TokensInput: 1000, TokensOutput: 200,
	})
	if err != nil {
		t.Fatalf("append original: %v", err)
	}
	// The correction supersedes the original with the accurate figures.
	if _, err := m.Append(ctx, Event{
		TenantID: tenant, SessionID: "s1", EventType: EventBillingCorrection,
		TokensInput: 100, TokensOutput: 20,
		CorrectsSequence: orig.SequenceNumber, CorrectionReasonCode: ReasonRetryOvercounting,
	}); err != nil {
		t.Fatalf("append correction: %v", err)
	}

	got, err := m.SessionTotals(ctx, tenant, "s1")
	if err != nil {
		t.Fatalf("SessionTotals: %v", err)
	}
	// Reconciliation drops the correction record and rewrites the original,
	// so the total is the corrected figure (100/20), not 1100/220.
	if got.TokensInput != 100 || got.TokensOutput != 20 || got.EventCount != 1 {
		t.Fatalf("reconciled totals = %+v, want {100 20 0 1}", got)
	}
}
