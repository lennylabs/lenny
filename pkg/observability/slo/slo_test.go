// SPDX-License-Identifier: MIT

package slo

import (
	"strings"
	"testing"
)

// TestStartupWarningEmittedWhenUnvalidated verifies a deployment that
// has not completed the Phase 14.5 gate (validated == false) emits the
// verbatim §16.5 provisional-SLO warning. spec: §16.5 lines 609, 623.
func TestStartupWarningEmittedWhenUnvalidated_spec_16_5_609(t *testing.T) {
	msg, emit := StartupWarning(false)
	if !emit {
		t.Fatal("StartupWarning(false) should emit the provisional-SLO warning")
	}
	if msg != ProvisionalWarning {
		t.Errorf("warning text = %q, want %q", msg, ProvisionalWarning)
	}
	// Guard the spec-mandated substrings so a reword cannot silently
	// drop the Phase 14.5 reference or the SLA-commitment prohibition.
	for _, frag := range []string{
		"SLO targets in Section 16.5 are provisional",
		"have not been validated by load testing",
		"Phase 14.5 benchmark gate is complete",
	} {
		if !strings.Contains(msg, frag) {
			t.Errorf("warning %q is missing required fragment %q", msg, frag)
		}
	}
}

// TestStartupWarningSuppressedWhenValidated verifies the warning is
// suppressed once the Phase 14.5 automation has set slo.validated.
// spec: §16.5 line 623.
func TestStartupWarningSuppressedWhenValidated_spec_16_5_623(t *testing.T) {
	msg, emit := StartupWarning(true)
	if emit {
		t.Error("StartupWarning(true) should not emit once the SLO gate is validated")
	}
	if msg != "" {
		t.Errorf("validated warning text = %q, want empty", msg)
	}
}
