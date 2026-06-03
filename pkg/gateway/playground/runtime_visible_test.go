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
// visible. F-27.4.1.
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
// operator values with no allowedRuntimes set exposes every runtime. F-27.4.1.
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
// language picker, not a hard-coded single language. F-27.4.4 / F-27.9.4.
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
// runtimeRef/runtimeOptions create body. F-27.4.2.
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
// F-27.4.1.
func TestPlaygroundPickerFiltersAllowedRuntimes_spec_27_5_190(t *testing.T) {
	app := readUIAsset(t, "app.js")
	for _, marker := range []string{"runtimeAllowed", "allowedRuntimes", "globMatch"} {
		if !strings.Contains(app, marker) {
			t.Errorf("app.js runtime picker must contain %q", marker)
		}
	}
}
