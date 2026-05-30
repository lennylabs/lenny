// SPDX-License-Identifier: MIT

package audit

import (
	"errors"
	"testing"
	"time"
)

func TestAllChainIntegritiesIsExhaustive(t *testing.T) {
	got := AllChainIntegrities()
	if len(got) != 6 {
		t.Errorf("AllChainIntegrities() returned %d, want 6 per §11.7", len(got))
	}
	for _, c := range got {
		if !c.IsValid() {
			t.Errorf("AllChainIntegrities() returned invalid %q", c)
		}
	}
	if ChainIntegrity("bogus").IsValid() {
		t.Errorf("unknown integrity state must not be IsValid")
	}
}

func TestChainIntegrityIsAlarming(t *testing.T) {
	if !ChainBroken.IsAlarming() {
		t.Errorf("broken must alarm (AuditChainGap)")
	}
	for _, c := range []ChainIntegrity{ChainVerified, ChainUnchecked, ChainRechainedPostOutage, ChainGapSuspected, ChainRedactedGDPR} {
		if c.IsAlarming() {
			t.Errorf("%q must not alarm by itself", c)
		}
	}
}

func TestAllComplianceProfilesIsExhaustive(t *testing.T) {
	if got := len(AllComplianceProfiles()); got != 4 {
		t.Errorf("AllComplianceProfiles() returned %d, want 4 per §11.7", got)
	}
}

func TestComplianceProfileRequiresSIEM(t *testing.T) {
	if ComplianceNone.RequiresSIEM() {
		t.Errorf("none must not require SIEM")
	}
	for _, p := range []ComplianceProfile{ComplianceSOC2, ComplianceFedRAMP, ComplianceHIPAA} {
		if !p.RequiresSIEM() {
			t.Errorf("%q must require SIEM per §11.7", p)
		}
	}
}

func TestAllRetentionPresetsIsExhaustive(t *testing.T) {
	got := AllRetentionPresets()
	if len(got) != 5 {
		t.Errorf("AllRetentionPresets() returned %d, want 5 per §16.4", len(got))
	}
}

func TestPresetDaysMatchesSpec(t *testing.T) {
	cases := map[RetentionPreset]int{
		PresetSOC2:        365,
		PresetFedRAMPHigh: 1095,
		PresetHIPAA:       2190,
		PresetNIS2DORA:    1825,
		PresetCustom:      0,
	}
	for p, want := range cases {
		if got := p.PresetDays(); got != want {
			t.Errorf("%q.PresetDays() = %d, want %d", p, got, want)
		}
	}
}

func TestPresetWindowMatchesDays(t *testing.T) {
	if got := PresetSOC2.PresetWindow(); got != 365*24*time.Hour {
		t.Errorf("PresetSOC2.PresetWindow() = %v, want 365d", got)
	}
	if got := PresetCustom.PresetWindow(); got != 0 {
		t.Errorf("PresetCustom.PresetWindow() must be 0, got %v", got)
	}
}

func TestPresetCompatibilityMatrix(t *testing.T) {
	cases := []struct {
		preset RetentionPreset
		want   []ComplianceProfile
	}{
		{PresetSOC2, []ComplianceProfile{ComplianceSOC2, ComplianceFedRAMP}},
		{PresetFedRAMPHigh, []ComplianceProfile{ComplianceFedRAMP}},
		{PresetHIPAA, []ComplianceProfile{ComplianceHIPAA}},
		{PresetNIS2DORA, []ComplianceProfile{ComplianceNone, ComplianceSOC2}},
	}
	for _, c := range cases {
		got := c.preset.CompatibleProfiles()
		if len(got) != len(c.want) {
			t.Errorf("%q.CompatibleProfiles() count: want %d, got %d", c.preset, len(c.want), len(got))
		}
		seen := map[ComplianceProfile]bool{}
		for _, p := range got {
			seen[p] = true
		}
		for _, want := range c.want {
			if !seen[want] {
				t.Errorf("%q.CompatibleProfiles() missing %q", c.preset, want)
			}
		}
	}
}

func TestValidatePairingHappyPath(t *testing.T) {
	cases := []struct {
		p RetentionPreset
		c ComplianceProfile
	}{
		{PresetSOC2, ComplianceSOC2},
		{PresetSOC2, ComplianceFedRAMP}, // §16.4: soc2 preset usable with fedramp profile
		{PresetFedRAMPHigh, ComplianceFedRAMP},
		{PresetHIPAA, ComplianceHIPAA},
		{PresetNIS2DORA, ComplianceNone},
		{PresetNIS2DORA, ComplianceSOC2},
		{PresetCustom, ComplianceHIPAA},
	}
	for _, c := range cases {
		if err := ValidatePairing(c.p, c.c); err != nil {
			t.Errorf("ValidatePairing(%q, %q) = %v, want nil", c.p, c.c, err)
		}
	}
}

func TestValidatePairingMismatches(t *testing.T) {
	cases := []struct {
		p RetentionPreset
		c ComplianceProfile
	}{
		{PresetSOC2, ComplianceHIPAA},
		{PresetFedRAMPHigh, ComplianceSOC2}, // FedRAMP-high mandates fedramp profile
		{PresetHIPAA, ComplianceSOC2},
		{PresetNIS2DORA, ComplianceFedRAMP},
	}
	for _, c := range cases {
		err := ValidatePairing(c.p, c.c)
		var pe *PairingError
		if !errors.As(err, &pe) {
			t.Errorf("ValidatePairing(%q, %q) should be *PairingError, got %v", c.p, c.c, err)
		}
	}
}

func TestAllOCSFStatesIsExhaustive(t *testing.T) {
	got := AllOCSFStates()
	if len(got) != 4 {
		t.Errorf("AllOCSFStates() returned %d, want 4 per §11.7", len(got))
	}
}

func TestOCSFStateIsTerminal(t *testing.T) {
	for _, s := range []OCSFTranslationState{OCSFSucceeded, OCSFDeadLettered} {
		if !s.IsTerminal() {
			t.Errorf("%q must be terminal", s)
		}
	}
	for _, s := range []OCSFTranslationState{OCSFPending, OCSFRetryPending} {
		if s.IsTerminal() {
			t.Errorf("%q must not be terminal", s)
		}
	}
}

// spec: §12.8 line 839 — IsRegulated is true exactly for soc2, fedramp,
// and hipaa; none and the empty profile are unregulated.
func TestComplianceProfileIsRegulated(t *testing.T) {
	regulated := map[ComplianceProfile]bool{
		ComplianceSOC2:        true,
		ComplianceFedRAMP:     true,
		ComplianceHIPAA:       true,
		ComplianceNone:        false,
		ComplianceProfile(""): false,
	}
	for p, want := range regulated {
		if got := p.IsRegulated(); got != want {
			t.Errorf("ComplianceProfile(%q).IsRegulated() = %v, want %v", p, got, want)
		}
		// RequiresSIEM delegates to IsRegulated; the two must agree.
		if p.RequiresSIEM() != p.IsRegulated() {
			t.Errorf("RequiresSIEM(%q)=%v diverges from IsRegulated()=%v", p, p.RequiresSIEM(), p.IsRegulated())
		}
	}
}

// spec: §12.8 line 839 — audit.gdprRetentionDays must be at least 2190
// (6 years) under any regulated complianceProfile; the default is 2555.
func TestValidateGDPRRetentionDays(t *testing.T) {
	cases := []struct {
		name     string
		days     int
		profiles []string
		wantErr  bool
	}{
		{"default under hipaa", GDPRRetentionDefaultDays, []string{"hipaa"}, false},
		{"floor boundary under soc2", GDPRRetentionComplianceFloorDays, []string{"soc2"}, false},
		{"one below floor under hipaa", GDPRRetentionComplianceFloorDays - 1, []string{"hipaa"}, true},
		{"one below floor under fedramp", 1000, []string{"fedramp"}, true},
		{"below floor but no regulated profile", 30, []string{"none", ""}, false},
		{"below floor with mixed profiles", 100, []string{"none", "soc2"}, true},
		{"zero treated as default under hipaa", 0, []string{"hipaa"}, false},
		{"negative treated as default under hipaa", -5, []string{"hipaa"}, false},
		{"no profiles", 1, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateGDPRRetentionDays(tc.days, tc.profiles)
			if tc.wantErr != (err != nil) {
				t.Fatalf("ValidateGDPRRetentionDays(%d, %v) err=%v, wantErr=%v", tc.days, tc.profiles, err, tc.wantErr)
			}
		})
	}
}

// spec: §12.8 line 839 — the startup rejection message is verbatim so
// operators and tests can match on it.
func TestGDPRRetentionFloorErrorMessage(t *testing.T) {
	err := ValidateGDPRRetentionDays(100, []string{"hipaa"})
	if err == nil {
		t.Fatal("expected a floor error")
	}
	const want = "CONFIG_INVALID: audit.gdprRetentionDays below compliance floor"
	if err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
	var floorErr *GDPRRetentionFloorError
	if !errors.As(err, &floorErr) {
		t.Fatalf("error is not a *GDPRRetentionFloorError: %T", err)
	}
	if floorErr.Profile != "hipaa" || floorErr.FloorDays != GDPRRetentionComplianceFloorDays || floorErr.RetentionDays != 100 {
		t.Errorf("floor error fields = %+v", floorErr)
	}
}
