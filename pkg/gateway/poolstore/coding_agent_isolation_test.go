// SPDX-License-Identifier: MIT

package poolstore_test

import (
	"sort"
	"testing"

	"github.com/lennylabs/lenny/pkg/compliance"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// TestValidateCodingAgentIsolation_spec_26_2_38 exercises the §26.2 line
// 38 categorical rule: a coding-agent runtime is never permitted on
// `standard` (runc) isolation, regardless of allowStandardIsolation.
func TestValidateCodingAgentIsolation_spec_26_2_38(t *testing.T) {
	cases := []struct {
		name          string
		pool          poolstore.Pool
		isCodingAgent bool
		wantErr       bool
	}{
		{
			name:          "coding-agent + standard rejected",
			pool:          poolstore.Pool{IsolationProfile: isolation.ProfileStandard},
			isCodingAgent: true,
			wantErr:       true,
		},
		{
			name: "coding-agent + standard rejected even with allowStandardIsolation",
			pool: poolstore.Pool{
				IsolationProfile:       isolation.ProfileStandard,
				AllowStandardIsolation: true,
			},
			isCodingAgent: true,
			wantErr:       true,
		},
		{
			name:          "coding-agent + sandboxed allowed",
			pool:          poolstore.Pool{IsolationProfile: isolation.ProfileSandboxed},
			isCodingAgent: true,
			wantErr:       false,
		},
		{
			name:          "coding-agent + microvm allowed",
			pool:          poolstore.Pool{IsolationProfile: isolation.ProfileMicrovm},
			isCodingAgent: true,
			wantErr:       false,
		},
		{
			name:          "coding-agent + empty (sandboxed default) allowed",
			pool:          poolstore.Pool{},
			isCodingAgent: true,
			wantErr:       false,
		},
		{
			name:          "non-coding-agent + standard allowed by this rule",
			pool:          poolstore.Pool{IsolationProfile: isolation.ProfileStandard},
			isCodingAgent: false,
			wantErr:       false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := poolstore.ValidateCodingAgentIsolation(tc.pool, tc.isCodingAgent)
			if tc.wantErr && err == nil {
				t.Fatalf("ValidateCodingAgentIsolation(%+v, %v) = nil, want error", tc.pool, tc.isCodingAgent)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ValidateCodingAgentIsolation(%+v, %v) = %v, want nil", tc.pool, tc.isCodingAgent, err)
			}
		})
	}
}

// TestIsCodingAgentRuntime_spec_26_1 pins the four §26.1 coding-agent
// names and a couple of non-coding-agent reference runtimes.
func TestIsCodingAgentRuntime_spec_26_1(t *testing.T) {
	coding := []string{"claude-code", "gemini-cli", "codex", "cursor-cli"}
	for _, n := range coding {
		if !poolstore.IsCodingAgentRuntime(n) {
			t.Errorf("IsCodingAgentRuntime(%q) = false, want true", n)
		}
	}
	for _, n := range []string{"chat", "langgraph", "mastra", "echo", ""} {
		if poolstore.IsCodingAgentRuntime(n) {
			t.Errorf("IsCodingAgentRuntime(%q) = true, want false", n)
		}
	}
}

// TestCodingAgentSetMatchesReferenceCatalog_spec_26_1 guards the
// hardcoded coding-agent set in poolstore against drift from the
// authoritative §26 reference catalog (pkg/compliance/reference_catalog.yaml,
// `category: coding-agent`). The set is duplicated in poolstore because
// pkg/compliance pulls in `testing`; this test is the cross-check that
// keeps the duplication honest in both directions.
func TestCodingAgentSetMatchesReferenceCatalog_spec_26_1(t *testing.T) {
	cat, err := compliance.ReferenceCatalog()
	if err != nil {
		t.Fatalf("ReferenceCatalog: %v", err)
	}
	var fromCatalog []string
	for _, rt := range cat {
		if rt.Category == compliance.CategoryCodingAgent {
			fromCatalog = append(fromCatalog, rt.Name)
			if !poolstore.IsCodingAgentRuntime(rt.Name) {
				t.Errorf("catalog runtime %q is category coding-agent but poolstore.IsCodingAgentRuntime returns false", rt.Name)
			}
		}
	}
	sort.Strings(fromCatalog)
	fromPoolstore := poolstore.CodingAgentRuntimeNames()
	if len(fromCatalog) != len(fromPoolstore) {
		t.Fatalf("coding-agent set drift: catalog has %v, poolstore has %v", fromCatalog, fromPoolstore)
	}
	for i := range fromCatalog {
		if fromCatalog[i] != fromPoolstore[i] {
			t.Errorf("coding-agent set drift at %d: catalog %q != poolstore %q", i, fromCatalog[i], fromPoolstore[i])
		}
	}
}
