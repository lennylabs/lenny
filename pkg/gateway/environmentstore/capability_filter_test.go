// SPDX-License-Identifier: MIT

package environmentstore_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/environment"
	"github.com/lennylabs/lenny/pkg/gateway/environmentstore"
)

// spec: §10.6 lines 588-607 — capability-based tool filtering for
// mcpRuntimeFilters and connectorSelector.

func TestPermitCapabilities_spec_10_6_607(t *testing.T) {
	// Matches the §10.6 mcpRuntimeFilters example:
	//   allowedCapabilities: [read, execute]
	//   deniedCapabilities:  [write, delete, admin]
	allowed := []string{"read", "execute"}
	denied := []string{"write", "delete", "admin"}

	cases := []struct {
		name      string
		toolCaps  []string
		allowed   []string
		denied    []string
		permitted bool
		blocked   string
	}{
		{"read-only-permitted", []string{"read"}, allowed, denied, true, ""},
		{"read-execute-permitted", []string{"read", "execute"}, allowed, denied, true, ""},
		{"write-denied", []string{"write"}, allowed, denied, false, "write"},
		{"destructive-denied-on-delete", []string{"write", "delete"}, allowed, denied, false, "write"},
		{"admin-denied", []string{"admin"}, allowed, denied, false, "admin"},
		{"outside-allowlist-denied", []string{"read", "network"}, allowed, denied, false, "network"},
		{"no-caps-permitted", nil, allowed, denied, true, ""},
		{"deny-wins-over-allow", []string{"read", "admin"}, allowed, denied, false, "admin"},

		// An empty allow-list imposes no allow restriction; only the
		// deny-list filters.
		{"empty-allowlist-permits-unlisted", []string{"network"}, nil, denied, true, ""},
		{"empty-allowlist-still-denies", []string{"delete"}, nil, denied, false, "delete"},

		// No filter at all permits everything (the no-policy case).
		{"no-filter-permits", []string{"admin"}, nil, nil, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, blocked := environmentstore.PermitCapabilities(tc.toolCaps, tc.allowed, tc.denied)
			if ok != tc.permitted {
				t.Fatalf("permitted = %v, want %v", ok, tc.permitted)
			}
			if blocked != tc.blocked {
				t.Fatalf("blockedBy = %q, want %q", blocked, tc.blocked)
			}
		})
	}
}

func TestConnectorSelectorPermitTool_spec_10_6_599(t *testing.T) {
	cs := environmentstore.ConnectorSelector{
		Selector:            environment.Selector{MatchLabels: map[string]string{"team": "security"}},
		AllowedCapabilities: []string{"read", "search", "network"},
		DeniedCapabilities:  []string{"write", "delete", "execute", "admin"},
	}
	if ok, _ := cs.PermitTool([]string{"read"}); !ok {
		t.Fatalf("read tool should be permitted")
	}
	if ok, blocked := cs.PermitTool([]string{"write"}); ok || blocked != "write" {
		t.Fatalf("write tool should be denied on write, got ok=%v blocked=%q", ok, blocked)
	}

	if !cs.Admits("github", map[string]string{"team": "security"}) {
		t.Fatalf("connector with team=security label should be admitted by the selector")
	}
	if cs.Admits("github", map[string]string{"team": "platform"}) {
		t.Fatalf("connector with non-matching label should not be admitted")
	}
}

func TestMCPRuntimeFilterFor_spec_10_6_607(t *testing.T) {
	env := environmentstore.Environment{
		Name:     "security-team",
		TenantID: "acme",
		MCPRuntimeFilters: []environmentstore.MCPRuntimeFilter{
			{
				RuntimeSelector:     environment.Selector{MatchLabels: map[string]string{"category": "execution"}},
				AllowedCapabilities: []string{"read", "execute"},
				DeniedCapabilities:  []string{"write", "delete", "admin"},
			},
		},
	}

	f, ok := env.MCPRuntimeFilterFor("code-runner", "mcp", map[string]string{"category": "execution"})
	if !ok {
		t.Fatalf("runtime with category=execution should match the filter")
	}
	if permit, blocked := f.PermitTool([]string{"write"}); permit || blocked != "write" {
		t.Fatalf("write tool should be denied, got permit=%v blocked=%q", permit, blocked)
	}
	if permit, _ := f.PermitTool([]string{"read", "execute"}); !permit {
		t.Fatalf("read/execute tool should be permitted")
	}

	if _, ok := env.MCPRuntimeFilterFor("scanner", "mcp", map[string]string{"category": "analysis"}); ok {
		t.Fatalf("runtime with non-matching label should not match any filter")
	}
}
