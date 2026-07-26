// SPDX-License-Identifier: MIT

package playground

import (
	"io/fs"
	"strings"
	"testing"
)

// TestRuntimeVisibleGlob_spec_27_5_190 pins the §27.5 line 190
// playground.allowedRuntimes glob semantics RuntimeVisible enforces: `*` is
// the only metacharacter and matches any sequence; a pattern with no `*` is an
// exact match; the default ["*"] (applied by withDefaults) makes every runtime
// visible.
func TestRuntimeVisibleGlob_spec_27_5_190(t *testing.T) {
	cases := []struct {
		name     string
		allowed  []string
		runtime  string
		expected bool
	}{
		{"exact match", []string{"claude-code"}, "claude-code", true},
		{"exact miss", []string{"claude-code"}, "gpt-4", false},
		{"prefix glob hit", []string{"claude-*"}, "claude-code", true},
		{"prefix glob miss", []string{"claude-*"}, "gemini-pro", false},
		{"suffix glob hit", []string{"*-agent"}, "research-agent", true},
		{"infix glob hit", []string{"*code*"}, "claude-code-v2", true},
		{"star matches everything", []string{"*"}, "anything", true},
		{"multiple patterns, second hits", []string{"gpt-*", "claude-*"}, "claude-code", true},
		{"multiple patterns, none hit", []string{"gpt-*", "gemini-*"}, "claude-code", false},
		// withDefaults normalizes an empty list to ["*"]; an un-normalized
		// empty list is treated as all-visible by RuntimeVisible directly.
		{"empty list is all-visible", nil, "claude-code", true},
		{"normalized default is all-visible", []string{"*"}, "claude-code", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Config{AllowedRuntimes: tc.allowed}
			if got := c.RuntimeVisible(tc.runtime); got != tc.expected {
				t.Errorf("RuntimeVisible(%q) with allowed=%v = %v, want %v", tc.runtime, tc.allowed, got, tc.expected)
			}
		})
	}
}

// TestRuntimeVisibleWithDefaults_spec_27_2 pins that withDefaults normalizes an
// empty AllowedRuntimes to the all-visible posture, so a Config built from
// operator values with no allowedRuntimes set exposes every runtime.
func TestRuntimeVisibleWithDefaults_spec_27_2(t *testing.T) {
	c := Config{}.withDefaults()
	if len(c.AllowedRuntimes) != 1 || c.AllowedRuntimes[0] != "*" {
		t.Fatalf("withDefaults must normalize allowedRuntimes to [\"*\"], got %v", c.AllowedRuntimes)
	}
	if !c.RuntimeVisible("any-runtime") {
		t.Errorf("the default posture must expose every runtime")
	}
}

// readUIAsset returns the embedded §27.4 SPA asset content for content-level
// assertions. The bundle is plain ES2017 with no build step, so the tests
// inspect its source directly rather than executing it.
func readUIAsset(t *testing.T, name string) string {
	t.Helper()
	data, err := fs.ReadFile(assetFS, name)
	if err != nil {
		t.Fatalf("read embedded asset %q: %v", name, err)
	}
	return string(data)
}

// TestPlaygroundSDKSnippetCoversThreeLanguages_spec_27_4_item3 pins the §27.4
// item 3 requirement that "Copy as client SDK snippet" emits equivalent code in
// Go, Python, and TypeScript — the bundle must carry a template for each and a
// language picker, not a hard-coded single language.
func TestPlaygroundSDKSnippetCoversThreeLanguages_spec_27_4_item3(t *testing.T) {
	app := readUIAsset(t, "app.js")
	for _, marker := range []string{
		`lang === "python"`,
		`lang === "typescript"`,
		"// Go — lenny client SDK",
		"# Python — lenny client SDK",
		"// TypeScript — lenny client SDK",
	} {
		if !strings.Contains(app, marker) {
			t.Errorf("app.js SDK-snippet generator must contain %q", marker)
		}
	}
	// §27.9 line 256: a snippet must reference credentials via environment
	// variables only, never embed one.
	if !strings.Contains(app, "LENNY_BEARER_TOKEN") {
		t.Errorf("SDK snippets must source the bearer from an environment variable")
	}
}

// TestPlaygroundSchemaDrivenConfig_spec_27_4_item2 pins that the §27.4 item 2
// session-config screen renders a form from the runtime's runtimeOptionsSchema
// rather than a single raw JSON textarea, and posts the correct
// runtimeRef/runtimeOptions create body.
func TestPlaygroundSchemaDrivenConfig_spec_27_4_item2(t *testing.T) {
	app := readUIAsset(t, "app.js")
	for _, marker := range []string{
		"renderSchemaForm",
		"runtimeOptionsSchema",
		"runtimeRef:",     // the create body uses the §15.1 field name
		"runtimeOptions:", // schema-form output is sent as runtimeOptions
	} {
		if !strings.Contains(app, marker) {
			t.Errorf("app.js session-config must contain %q", marker)
		}
	}
	// The schema-driven form replaces the old runtime-options textarea as the
	// primary editor; the raw editor remains only as the no-schema fallback.
	if !strings.Contains(app, "renderRawOptions") {
		t.Errorf("app.js must keep a no-schema fallback editor (renderRawOptions)")
	}
}

// TestPlaygroundPickerFiltersAllowedRuntimes_spec_27_5_190 pins that the §27.4
// runtime picker consults the server-sourced playground.allowedRuntimes list,
// matching the gateway's authoritative filter a second time client-side.
func TestPlaygroundPickerFiltersAllowedRuntimes_spec_27_5_190(t *testing.T) {
	app := readUIAsset(t, "app.js")
	for _, marker := range []string{"runtimeAllowed", "allowedRuntimes", "globMatch"} {
		if !strings.Contains(app, marker) {
			t.Errorf("app.js runtime picker must contain %q", marker)
		}
	}
}

// TestPlaygroundStoresEffectiveScope_spec_27_3_1 pins the §27.3.1 → §27.4 wiring
// the SPA needs to gate the delegation-policy affordance: the in-memory state
// declares effectiveScope before the first mint, mintBearer stores the mint
// response's effectiveScope field, and a hasScope helper probes
// tools:sessions:write while honoring the tools:sessions:* and tools:* wildcards
// per the gateway scopes.Set.Matches semantics. The bundle is plain ES2017 with
// no build step, so the test inspects its source for these surfaces directly.
func TestPlaygroundStoresEffectiveScope_spec_27_3_1(t *testing.T) {
	app := readUIAsset(t, "app.js")
	// The state initializer declares effectiveScope so the §27.4 gate reads a
	// defined value before the first mint completes (proposal §3.4).
	if !strings.Contains(app, `effectiveScope: ""`) {
		t.Errorf("app.js state initializer must declare effectiveScope")
	}
	// mintBearer stores the §27.3.1 mint-response effectiveScope field.
	if !strings.Contains(app, "state.effectiveScope = body.effectiveScope") {
		t.Errorf("app.js mintBearer must store body.effectiveScope into state.effectiveScope")
	}
	// The hasScope helper gates the delegation field on the minted bearer's
	// effective scope (proposal §3.5). It probes a domain:domain-resource:action
	// target (such as tools:sessions:write), reads the stored effective scope,
	// and honors the tools:sessions:* and tools:* wildcards.
	for _, marker := range []string{
		"function hasScope(target)",              // the helper exists
		"state.effectiveScope",                   // it reads the stored scope
		`var want = target.split(":")`,           // it parses the probe target (e.g. tools:sessions:write)
		`have[1] === "*" && have[2] === "*"`,     // tools:* matches everything
		`have[2] === "*" || have[2] === want[2]`, // action wildcard (tools:sessions:*) or exact match
	} {
		if !strings.Contains(app, marker) {
			t.Errorf("app.js hasScope helper must contain %q", marker)
		}
	}
}

// TestPlaygroundGatesDelegationField_spec_27_4_item2 pins the §27.4 item 2
// delegation-policy gate and its create-payload wire-key. The field is a
// client-side visibility affordance: renderSessionConfig computes canDelegate
// from hasScope("tools:sessions:write") and emits the delegation label and
// input only when canDelegate is true (relying on el's null-child skip). The
// create payload sets the nested delegationLease.delegationPolicyRef field the
// server decodes, guarded on canDelegate so an undefined delegationField is
// never dereferenced when the affordance is hidden, and the obsolete flat
// delegationPolicyId key is gone. The bundle is plain ES2017 with no build
// step, so the test inspects its source for these surfaces directly.
func TestPlaygroundGatesDelegationField_spec_27_4_item2(t *testing.T) {
	app := readUIAsset(t, "app.js")
	// renderSessionConfig gates the delegation affordance on the minted
	// bearer's effective scope granting tools:sessions:write (proposal §3.5).
	for _, marker := range []string{
		`var canDelegate = hasScope("tools:sessions:write")`,                         // the gate is computed from hasScope
		`canDelegate ? el("label", { text: "Delegation policy (optional)" }) : null`, // label is null-skipped when ungated
		"canDelegate ? delegationField : null",                                       // input is null-skipped when ungated
	} {
		if !strings.Contains(app, marker) {
			t.Errorf("app.js renderSessionConfig must gate the delegation field: missing %q", marker)
		}
	}
	// The "(requires scope)" qualifier is dropped because visibility is now
	// gated; the field is shown only to callers who hold the scope.
	if strings.Contains(app, "requires scope") {
		t.Errorf("app.js must drop the '(requires scope)' qualifier now that visibility is gated")
	}
	// The create payload emits the nested delegationLease.delegationPolicyRef
	// key the server decodes (proposal §3.6), guarded on canDelegate and a
	// defined delegationField so the affordance-hidden path never dereferences
	// an undefined field.
	for _, marker := range []string{
		"if (canDelegate && delegationField && delegationField.value.trim())",
		"payload.delegationLease = { delegationPolicyRef: delegationField.value.trim() }",
	} {
		if !strings.Contains(app, marker) {
			t.Errorf("app.js create payload must set delegationLease.delegationPolicyRef: missing %q", marker)
		}
	}
	// The old flat top-level delegationPolicyId payload key, which the server
	// never decoded and silently dropped, must be gone.
	if strings.Contains(app, "delegationPolicyId") {
		t.Errorf("app.js must not emit the flat delegationPolicyId key; it was never decoded by the server")
	}
}
