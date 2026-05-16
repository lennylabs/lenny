// SPDX-License-Identifier: MIT

package preflight_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/preflight"
)

func enabledEntry() preflight.PhaseStampEntry {
	return preflight.PhaseStampEntry{Enabled: true, EnabledAt: "2026-05-15T00:00:00Z"}
}

func TestCheckPhaseStampPassesWhenNoFlagRecorded(t *testing.T) {
	d := preflight.CheckPhaseStamp(
		map[string]bool{"llmProxy": false},
		map[string]preflight.PhaseStampEntry{},
		nil,
	)
	if !d.Passed {
		t.Errorf("check failed with an empty phase-stamp: %s", d.Reason)
	}
}

func TestCheckPhaseStampPassesWhenFlagStillEnabled(t *testing.T) {
	d := preflight.CheckPhaseStamp(
		map[string]bool{"llmProxy": true},
		map[string]preflight.PhaseStampEntry{"llmProxy": enabledEntry()},
		nil,
	)
	if !d.Passed {
		t.Errorf("check failed though the flag is still enabled: %s", d.Reason)
	}
}

func TestCheckPhaseStampFailsOnUnacknowledgedDowngrade(t *testing.T) {
	d := preflight.CheckPhaseStamp(
		map[string]bool{"llmProxy": false},
		map[string]preflight.PhaseStampEntry{"llmProxy": enabledEntry()},
		nil,
	)
	if d.Passed {
		t.Fatal("check passed an unacknowledged feature-flag downgrade")
	}
	if !strings.Contains(d.Reason, "PREFLIGHT_PHASE_STAMP_MISMATCH") {
		t.Errorf("reason %q does not carry the §17.9 error code", d.Reason)
	}
	if !strings.Contains(d.Reason, "llmProxy") {
		t.Errorf("reason %q does not name the downgraded flag", d.Reason)
	}
}

func TestCheckPhaseStampPassesWhenDowngradeAcknowledged(t *testing.T) {
	d := preflight.CheckPhaseStamp(
		map[string]bool{"llmProxy": false},
		map[string]preflight.PhaseStampEntry{"llmProxy": enabledEntry()},
		map[string]bool{"llmProxy": true},
	)
	if !d.Passed {
		t.Errorf("check failed an acknowledged downgrade: %s", d.Reason)
	}
}

func TestCheckPhaseStampIgnoresAFlagRecordedDisabled(t *testing.T) {
	// A phase-stamp entry with Enabled false is not a phase-advance
	// signal, so rendering the flag as false is not a downgrade.
	d := preflight.CheckPhaseStamp(
		map[string]bool{"compliance": false},
		map[string]preflight.PhaseStampEntry{"compliance": {Enabled: false}},
		nil,
	)
	if !d.Passed {
		t.Errorf("check failed for a flag recorded disabled: %s", d.Reason)
	}
}

func TestCheckPhaseStampFailsOnTheFirstDowngradedFlag(t *testing.T) {
	d := preflight.CheckPhaseStamp(
		map[string]bool{"llmProxy": false, "compliance": false},
		map[string]preflight.PhaseStampEntry{
			"llmProxy":   enabledEntry(),
			"compliance": enabledEntry(),
		},
		map[string]bool{"llmProxy": true}, // only llmProxy is acknowledged
	)
	if d.Passed {
		t.Fatal("check passed though compliance is downgraded without acknowledgement")
	}
	if !strings.Contains(d.Reason, "compliance") {
		t.Errorf("reason %q does not name the unacknowledged flag", d.Reason)
	}
}
