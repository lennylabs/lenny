// SPDX-License-Identifier: MIT

// Tier-11 documentation checks for the 0007 proposal reconciliation: the
// session-creation observability surface and the credential-materialization
// failure attribution must match the landed spec edits.
//
// These tests are NOT under a build tag; they read the repository docs
// directly and require no external infrastructure.

package tier11_docs_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// createHandlerPhases is the §16.1 lenny_session_creation_duration_seconds
// create-handler phase label set after the 0007 proposal moved credential-lease
// assignment to /finalize. The proposal dropped credential_assign and pod_assign
// and re-anchored the set to the create steps that remain.
//
// spec: §16.1 (creation-duration phases), §16.5 (SLO measurement)
var createHandlerPhases = []string{
	"auth",
	"policy",
	"credential_precheck",
	"pod_claim",
	"postgres_persist",
}

// droppedCreatePhases are the phase labels the 0007 proposal removed from the
// create-handler set because the work moved to /finalize (credential assignment)
// or is already covered by pod_claim (no separate pod-side assignment runs in the
// create window). They must not reappear as documented create-handler phases.
var droppedCreatePhases = []string{"credential_assign", "pod_assign"}

// TestSloRunbookGroupByPhaseMatchesSpec161 pins the slo-session-creation runbook
// Step-3 latency-breakdown phase enumeration to the edited §16.1 create-handler
// set. The runbook groups lenny_session_creation_duration_seconds by `phase`, so
// the enumerated phases must resolve to real phase-label values; before the 0007
// reconciliation the runbook listed claim/materialize/warmup/attach, which matched
// neither the old nor the edited metric set.
//
// spec: §16.1 (creation-duration phases), §16.5 (SLO measurement)
func TestSloRunbookGroupByPhaseMatchesSpec161(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, "docs", "runbooks", "slo-session-creation.md")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	text := string(body)

	// The runbook must enumerate every create-handler phase the metric carries.
	for _, phase := range createHandlerPhases {
		if !strings.Contains(text, "`"+phase+"`") {
			t.Errorf("slo-session-creation.md does not enumerate the §16.1 create-handler phase %q; the group-by-phase diagnosis must list the real phase-label values", phase)
		}
	}
	// The dropped phases must not be documented as create-handler phases.
	for _, phase := range droppedCreatePhases {
		if strings.Contains(text, "`"+phase+"`") {
			t.Errorf("slo-session-creation.md still lists the dropped create-handler phase %q; the 0007 proposal moved credential-lease assignment to /finalize and removed credential_assign/pod_assign from the create-handler set", phase)
		}
	}
	// The pre-reconciliation placeholder phases must be gone.
	for _, stale := range []string{"`materialize`", "`warmup`", "`attach`"} {
		if strings.Contains(text, stale) {
			t.Errorf("slo-session-creation.md still lists the stale Step-3 phase %s; replace it with the edited §16.1 create-handler set (auth, policy, credential_precheck, pod_claim, postgres_persist)", stale)
		}
	}
}

// TestTokenServiceOutageRunbookAttributesToFinalize confirms the downstream
// token-service-outage runbook attributes CREDENTIAL_MATERIALIZATION_ERROR to the
// finalize step, matching the §17.7 spec edit. The 0007 proposal moved credential
// materialization from session creation to /finalize, so the failure surfaces at
// finalize for credential-requiring sessions.
//
// spec: §17.7 (token-service runbook), §16.1 (creation-duration phases)
func TestTokenServiceOutageRunbookAttributesToFinalize(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, "docs", "runbooks", "token-service-outage.md")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	text := string(body)

	if !strings.Contains(text, "`finalize`") {
		t.Error("token-service-outage.md does not name the `finalize` step; CREDENTIAL_MATERIALIZATION_ERROR is materialized at /finalize per §17.7")
	}
	// The error code must still be the materialization error.
	if !strings.Contains(text, "CREDENTIAL_MATERIALIZATION_ERROR") {
		t.Error("token-service-outage.md no longer names CREDENTIAL_MATERIALIZATION_ERROR")
	}
	// The pre-reconciliation "New sessions return" attribution must be gone.
	if strings.Contains(text, "New sessions return `CREDENTIAL_MATERIALIZATION_ERROR`") {
		t.Error("token-service-outage.md still attributes CREDENTIAL_MATERIALIZATION_ERROR to \"New sessions\"; the 0007 proposal moved materialization to /finalize")
	}
}
