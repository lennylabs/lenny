// SPDX-License-Identifier: MIT

package billingstore

import (
	"context"
	"testing"
)

// TestEnvironmentTotals_SumsPerEnvironment_spec_15_1_840 verifies the
// environment billing rollup sums only the named environment's events,
// isolates other environments, and excludes sessions that name no
// environment. spec: §15.1 line 840; §10.6 line 663. F-15.1.3.
func TestEnvironmentTotals_SumsPerEnvironment_spec_15_1_840(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	const tenant = "acme"

	for _, e := range []Event{
		{TenantID: tenant, SessionID: "s1", EnvironmentID: "prod", EventType: EventSessionCreated, TokensInput: 100, TokensOutput: 40, PodMinutes: 1.5},
		{TenantID: tenant, SessionID: "s2", EnvironmentID: "prod", EventType: EventSessionCompleted, TokensInput: 50, TokensOutput: 10, PodMinutes: 0.5},
		{TenantID: tenant, SessionID: "s3", EnvironmentID: "staging", EventType: EventSessionCreated, TokensInput: 999, TokensOutput: 999, PodMinutes: 9},
		{TenantID: tenant, SessionID: "s4", EventType: EventSessionCreated, TokensInput: 7, TokensOutput: 7, PodMinutes: 0.1},
	} {
		if _, err := m.Append(ctx, e); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	got, err := m.EnvironmentTotals(ctx, tenant, "prod")
	if err != nil {
		t.Fatalf("EnvironmentTotals: %v", err)
	}
	if got.TokensInput != 150 || got.TokensOutput != 50 || got.PodMinutes != 2.0 || got.EventCount != 2 {
		t.Fatalf("prod totals = %+v, want {150 50 2 2}", got)
	}

	// An environment with no events returns the zero report, not an error.
	empty, err := m.EnvironmentTotals(ctx, tenant, "missing")
	if err != nil {
		t.Fatalf("EnvironmentTotals(missing): %v", err)
	}
	if empty != (SessionUsage{}) {
		t.Fatalf("missing-environment totals = %+v, want zero", empty)
	}
}

// TestEnvironmentTotals_AppliesCorrections_spec_11_2_1 verifies a
// §11.2.1 billing_correction supersedes the corrected original's figures
// even though the correction event itself carries no environment_id (the
// production commit path does not stamp one). Reconciliation matches the
// correction to the original by sequence, so the corrected original stays
// in the environment rollup with the accurate figure. spec: §11.2.1;
// §15.1 line 840. F-15.1.3.
func TestEnvironmentTotals_AppliesCorrections_spec_11_2_1(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	const tenant = "acme"

	orig, err := m.Append(ctx, Event{
		TenantID: tenant, SessionID: "s1", EnvironmentID: "prod", EventType: EventSessionCompleted,
		TokensInput: 1000, TokensOutput: 200,
	})
	if err != nil {
		t.Fatalf("append original: %v", err)
	}
	// The correction carries no environment_id, mirroring commitCorrection.
	if _, err := m.Append(ctx, Event{
		TenantID: tenant, EventType: EventBillingCorrection,
		TokensInput: 100, TokensOutput: 20,
		CorrectsSequence: orig.SequenceNumber, CorrectionReasonCode: ReasonRetryOvercounting,
	}); err != nil {
		t.Fatalf("append correction: %v", err)
	}

	got, err := m.EnvironmentTotals(ctx, tenant, "prod")
	if err != nil {
		t.Fatalf("EnvironmentTotals: %v", err)
	}
	// The corrected figure (100/20), not the double-counted 1100/220.
	if got.TokensInput != 100 || got.TokensOutput != 20 || got.EventCount != 1 {
		t.Fatalf("reconciled totals = %+v, want {100 20 0 1}", got)
	}
}

// TestEnvironmentTotals_EmptyEnvironmentMatchesNothing verifies that an
// empty environment id never aggregates the no-environment sessions: a
// session not scoped to an environment carries an empty environment id,
// and the rollup must not treat that as a real environment bucket.
// spec: §10.6 line 663 (empty for sessions not scoped to an environment).
// F-15.1.3.
func TestEnvironmentTotals_EmptyEnvironmentMatchesNothing(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	const tenant = "acme"

	if _, err := m.Append(ctx, Event{
		TenantID: tenant, SessionID: "s1", EventType: EventSessionCreated,
		TokensInput: 42, TokensOutput: 42,
	}); err != nil {
		t.Fatalf("append: %v", err)
	}

	got, err := m.EnvironmentTotals(ctx, tenant, "")
	if err != nil {
		t.Fatalf("EnvironmentTotals(\"\"): %v", err)
	}
	if got != (SessionUsage{}) {
		t.Fatalf("empty-environment totals = %+v, want zero", got)
	}
}
