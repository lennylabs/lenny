// SPDX-License-Identifier: MIT

package bindattempt

import (
	"strings"
	"testing"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// A probe failure names the registry state behind the reclaim outcome it
// observed, with one distinct statement per outcome the reclaim-outcome rule
// defines, and names an undefined outcome as outside the contract.
//
// spec: §4.7.1 (role and gateway RPC contract)
func TestRegistryStateNamesTheEntryBehindEachReclaimOutcome_spec_4_7_1(t *testing.T) {
	cases := []struct {
		outcome adapterv1.SlotReclaimOutcome
		want    string
	}{
		{reclaimed, "held the entry the request was addressed to and removed it"},
		{superseded, "does not own"},
		{absent, "holds no entry"},
		{adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_UNSPECIFIED, "does not define"},
	}
	seen := map[string]bool{}
	for _, c := range cases {
		got := registryState(c.outcome)
		if !strings.Contains(got, c.want) {
			t.Errorf("registryState(%v) = %q, want it to state %q", c.outcome, got, c.want)
		}
		if seen[got] {
			t.Errorf("registryState(%v) = %q repeats another outcome's statement", c.outcome, got)
		}
		seen[got] = true
	}
}

// The restore pairing-rule probe sends a mid-session FinalizeWorkspace with
// no bind token that carries a restore grant, so the only half of the
// pairing rule that refuses it is the one forbidding restore_chunks on a
// mid-session request.
//
// spec: §7.1 (Derive restore semantics); §4.7.1 (role and gateway RPC contract)
func TestRestoreFinalizeProbeCarriesARestoreGrantMidSessionWithoutAToken_spec_7_1(t *testing.T) {
	req := restoreFinalizeReq(alice)
	if !req.GetMidSession() {
		t.Error("the restore probe is not mid-session")
	}
	if req.GetBindAttempt() != "" {
		t.Errorf("the restore probe carries bind_attempt %q, want none", req.GetBindAttempt())
	}
	if len(req.GetRestoreChunks()) != 1 || req.GetRestoreChunks()[0].GetUrl() == "" {
		t.Errorf("the restore probe carries restore_chunks %v, want one grant with a URL", req.GetRestoreChunks())
	}
	if req.GetSessionId().GetValue() != alice {
		t.Errorf("the restore probe addresses session %q, want %q", req.GetSessionId().GetValue(), alice)
	}
}
