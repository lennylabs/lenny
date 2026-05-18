// SPDX-License-Identifier: MIT

package integrity

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/audit"
)

// spec: 11.7
// diagnosis: §11.7 says a broken chain triggers the §16.5 critical
// alert path. ChainContinuityResult.Broken must report true exactly
// when the verifier stamped ChainBroken, and false for every other
// state — a verified chain or a benign state must not page on-call.
func TestChainContinuityResultBroken(t *testing.T) {
	cases := []struct {
		integrity audit.ChainIntegrity
		broken    bool
	}{
		{audit.ChainVerified, false},
		{audit.ChainBroken, true},
		{audit.ChainUnchecked, false},
		{audit.ChainRechainedPostOutage, false},
		{audit.ChainGapSuspected, false},
		{audit.ChainRedactedGDPR, false},
	}
	for _, tc := range cases {
		r := ChainContinuityResult{
			TenantID: "acme",
			Result:   audit.VerifyResult{Integrity: tc.integrity},
		}
		if r.Broken() != tc.broken {
			t.Errorf("Broken() for %q = %v, want %v", tc.integrity, r.Broken(), tc.broken)
		}
	}
}

// spec: 11.7
// diagnosis: §11.7 startup chain-continuity check walks every tenant.
// FirstBroken is the decision the gateway startup path calls — it must
// return the first broken-chain result so the §16.5 AuditChainGap
// alert fires, and nil when every tenant chain verified.
func TestFirstBroken(t *testing.T) {
	t.Run("all chains verified", func(t *testing.T) {
		results := []ChainContinuityResult{
			{TenantID: "acme", Result: audit.VerifyResult{Integrity: audit.ChainVerified}},
			{TenantID: "globex", Result: audit.VerifyResult{Integrity: audit.ChainVerified}},
		}
		if got := FirstBroken(results); got != nil {
			t.Errorf("FirstBroken = %+v, want nil when every chain verified", got)
		}
	})
	t.Run("one chain broken", func(t *testing.T) {
		results := []ChainContinuityResult{
			{TenantID: "acme", Result: audit.VerifyResult{Integrity: audit.ChainVerified}},
			{TenantID: "globex", Result: audit.VerifyResult{Integrity: audit.ChainBroken, BreakSeq: 4}},
			{TenantID: "initech", Result: audit.VerifyResult{Integrity: audit.ChainBroken, BreakSeq: 2}},
		}
		got := FirstBroken(results)
		if got == nil {
			t.Fatal("FirstBroken = nil, want the first broken chain")
		}
		if got.TenantID != "globex" {
			t.Errorf("FirstBroken tenant = %q, want globex (the first broken)", got.TenantID)
		}
		if got.Result.BreakSeq != 4 {
			t.Errorf("FirstBroken BreakSeq = %d, want 4", got.Result.BreakSeq)
		}
	})
	t.Run("empty results", func(t *testing.T) {
		if got := FirstBroken(nil); got != nil {
			t.Errorf("FirstBroken(nil) = %+v, want nil", got)
		}
	})
}
