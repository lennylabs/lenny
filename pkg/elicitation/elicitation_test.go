// SPDX-License-Identifier: MIT

package elicitation

import (
	"errors"
	"testing"
)

func TestAllModesIsExhaustive(t *testing.T) {
	if got := len(AllModes()); got != 3 {
		t.Errorf("AllModes() returned %d, want 3 per §9.2", got)
	}
}

func TestModeOrderingStrict(t *testing.T) {
	// §9.2: off < detect-only < enforce.
	if ModeOff.Rank() >= ModeDetectOnly.Rank() {
		t.Errorf("off must rank below detect-only")
	}
	if ModeDetectOnly.Rank() >= ModeEnforce.Rank() {
		t.Errorf("detect-only must rank below enforce")
	}
}

func TestAtLeast(t *testing.T) {
	if !ModeEnforce.AtLeast(ModeOff) {
		t.Errorf("enforce must be at least off")
	}
	if !ModeEnforce.AtLeast(ModeEnforce) {
		t.Errorf("enforce must be at least enforce")
	}
	if ModeOff.AtLeast(ModeDetectOnly) {
		t.Errorf("off must not be at least detect-only")
	}
}

func TestResolveEffectivePicksStricter(t *testing.T) {
	cases := []struct {
		floor    EnforcementMode
		stored   EnforcementMode
		expected EnforcementMode
	}{
		{ModeOff, ModeOff, ModeOff},
		{ModeOff, ModeDetectOnly, ModeDetectOnly},
		{ModeOff, ModeEnforce, ModeEnforce},
		{ModeDetectOnly, ModeOff, ModeDetectOnly}, // floor wins
		{ModeDetectOnly, ModeEnforce, ModeEnforce},
		{ModeEnforce, ModeOff, ModeEnforce},       // floor wins strongly
		{ModeEnforce, ModeDetectOnly, ModeEnforce},
		{ModeEnforce, ModeEnforce, ModeEnforce},
	}
	for _, c := range cases {
		got, err := ResolveEffective(c.floor, c.stored)
		if err != nil {
			t.Errorf("ResolveEffective(%q, %q): %v", c.floor, c.stored, err)
		}
		if got != c.expected {
			t.Errorf("ResolveEffective(%q, %q): want %q, got %q", c.floor, c.stored, c.expected, got)
		}
	}
}

func TestResolveEffectiveRejectsInvalidMode(t *testing.T) {
	_, err := ResolveEffective("bogus", ModeEnforce)
	var ime *InvalidModeError
	if !errors.As(err, &ime) {
		t.Errorf("expected *InvalidModeError, got %v", err)
	}
}

func TestAllDepthPoliciesIsExhaustive(t *testing.T) {
	if got := len(AllDepthPolicies()); got != 3 {
		t.Errorf("AllDepthPolicies() returned %d, want 3 per §9.2", got)
	}
}

func TestDepthPolicyAllowAll(t *testing.T) {
	for _, init := range []InitiatorType{InitiatorAgent, InitiatorConnector} {
		for d := 0; d < 5; d++ {
			if DepthAllowAll.ShouldSuppress(init, d, 0) {
				t.Errorf("allow_all must never suppress (initiator=%q, depth=%d)", init, d)
			}
		}
	}
}

func TestDepthPolicyBlockAll(t *testing.T) {
	// Depth 0 (top-level) is admitted; depth >= 1 suppressed for agents only.
	if DepthBlockAll.ShouldSuppress(InitiatorAgent, 0, 0) {
		t.Errorf("block_all must admit top-level agent elicitations")
	}
	if !DepthBlockAll.ShouldSuppress(InitiatorAgent, 1, 0) {
		t.Errorf("block_all must suppress agent elicitations at depth 1+")
	}
	// Connectors are always exempt.
	if DepthBlockAll.ShouldSuppress(InitiatorConnector, 5, 0) {
		t.Errorf("connector elicitations are always exempt from suppression")
	}
}

func TestDepthPolicySuppressAtDepth(t *testing.T) {
	const threshold = 3
	for d := 0; d < threshold; d++ {
		if DepthSuppressAtDepth.ShouldSuppress(InitiatorAgent, d, threshold) {
			t.Errorf("suppress_at_depth=%d must admit agent at depth %d", threshold, d)
		}
	}
	for d := threshold; d < threshold+3; d++ {
		if !DepthSuppressAtDepth.ShouldSuppress(InitiatorAgent, d, threshold) {
			t.Errorf("suppress_at_depth=%d must suppress agent at depth %d", threshold, d)
		}
	}
	// Connectors exempt at all depths.
	for d := 0; d < 10; d++ {
		if DepthSuppressAtDepth.ShouldSuppress(InitiatorConnector, d, threshold) {
			t.Errorf("connector at depth %d must not be suppressed", d)
		}
	}
}

func TestAllInitiatorTypesIsExhaustive(t *testing.T) {
	if got := len(AllInitiatorTypes()); got != 2 {
		t.Errorf("AllInitiatorTypes() returned %d, want 2", got)
	}
}

func TestContentDigestIsStable(t *testing.T) {
	c := Content{Message: "hello", Schema: map[string]any{"type": "string"}}
	a, err := c.Digest()
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Errorf("Digest must be stable: %q vs %q", a, b)
	}
	// Hex-encoded SHA-256 is 64 chars.
	if len(a) != 64 {
		t.Errorf("Digest length: want 64, got %d", len(a))
	}
}

func TestContentDigestCanonicalKeyOrder(t *testing.T) {
	// Same logical content, keys serialised in different orders.
	a := Content{
		Message: "hi",
		Schema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"x": "string"},
		},
	}
	b := Content{
		Message: "hi",
		Schema: map[string]any{
			"properties": map[string]any{"x": "string"},
			"type":       "object",
		},
	}
	dA, _ := a.Digest()
	dB, _ := b.Digest()
	if dA != dB {
		t.Errorf("Digest must canonicalise key order; got %q != %q", dA, dB)
	}
}

func TestContentDigestDiffersOnMutation(t *testing.T) {
	a := Content{Message: "hello"}
	b := Content{Message: "goodbye"}
	dA, _ := a.Digest()
	dB, _ := b.Digest()
	if dA == dB {
		t.Errorf("different messages must produce different digests")
	}
}

func TestContentEqualUsesDigest(t *testing.T) {
	a := Content{Message: "hi", Schema: nil}
	b := Content{Message: "hi", Schema: nil}
	c := Content{Message: "hi", Schema: map[string]any{"type": "string"}}
	if !a.Equal(b) {
		t.Errorf("identical content must Equal")
	}
	if a.Equal(c) {
		t.Errorf("different schema must not Equal")
	}
}

func TestVerifyContentAdmitsMatching(t *testing.T) {
	c := Content{Message: "hello"}
	digest, _ := c.Digest()
	if err := VerifyContent(digest, c); err != nil {
		t.Errorf("matching content should verify, got %v", err)
	}
}

func TestVerifyContentRejectsTampered(t *testing.T) {
	original := Content{Message: "approve transfer of $100"}
	digest, _ := original.Digest()
	tampered := Content{Message: "approve transfer of $10000"}
	err := VerifyContent(digest, tampered)
	var te *TamperError
	if !errors.As(err, &te) {
		t.Fatalf("expected *TamperError, got %v", err)
	}
	if te.ExpectedDigest == te.ObservedDigest {
		t.Errorf("tamper error must carry distinct digests")
	}
}

func TestVerifyContentRequiresOriginalDigest(t *testing.T) {
	if err := VerifyContent("", Content{Message: "x"}); err == nil {
		t.Errorf("empty originalDigest must error")
	}
}

func TestProvenanceValidateHappyPath(t *testing.T) {
	cases := []Provenance{
		{OriginPod: "pod-1", InitiatorType: InitiatorAgent, DelegationDepth: 0},
		{OriginPod: "pod-1", InitiatorType: InitiatorConnector, ConnectorID: "github", DelegationDepth: 2},
	}
	for _, p := range cases {
		if err := p.Validate(); err != nil {
			t.Errorf("Provenance(%+v) should validate, got %v", p, err)
		}
	}
}

func TestProvenanceValidateRejectsConnectorWithoutID(t *testing.T) {
	p := Provenance{OriginPod: "pod-1", InitiatorType: InitiatorConnector}
	if err := p.Validate(); err == nil {
		t.Errorf("connector without connector_id must be rejected")
	}
}

func TestProvenanceValidateRejectsNegativeDepth(t *testing.T) {
	p := Provenance{OriginPod: "pod-1", InitiatorType: InitiatorAgent, DelegationDepth: -1}
	if err := p.Validate(); err == nil {
		t.Errorf("negative depth must be rejected")
	}
}

func TestProvenanceValidateRejectsMissingOriginPod(t *testing.T) {
	p := Provenance{InitiatorType: InitiatorAgent}
	if err := p.Validate(); err == nil {
		t.Errorf("empty origin_pod must be rejected")
	}
}

func TestProvenanceValidateRejectsBadInitiatorType(t *testing.T) {
	p := Provenance{OriginPod: "pod-1", InitiatorType: "bogus"}
	if err := p.Validate(); err == nil {
		t.Errorf("invalid initiator_type must be rejected")
	}
}
