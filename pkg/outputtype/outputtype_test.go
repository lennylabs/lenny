// SPDX-License-Identifier: MIT

package outputtype_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/outputtype"
)

// spec: §15.4.1 lines 1503, 1522 — canonical registry + namespace rule.

func TestIsCanonical_spec_15_4_1(t *testing.T) {
	for _, typ := range outputtype.Canonical {
		if !outputtype.IsCanonical(typ) {
			t.Errorf("registry type %q classified non-canonical", typ)
		}
	}
	for _, typ := range []string{"heatmap", "x-acme/heatmap", "", "Text"} {
		if outputtype.IsCanonical(typ) {
			t.Errorf("non-registry type %q classified canonical", typ)
		}
	}
}

func TestIsVendorNamespaced_spec_15_4_1(t *testing.T) {
	cases := []struct {
		typ  string
		want bool
	}{
		{"x-acme/heatmap", true},
		{"x-myorg/audio-transcript", true},
		{"x-acme/", false},    // empty type name
		{"x-/heatmap", false}, // empty vendor name is malformed
		{"x-acme", false},     // no slash
		{"acme/heatmap", false},
		{"text", false},
		{"", false},
	}
	for _, c := range cases {
		if got := outputtype.IsVendorNamespaced(c.typ); got != c.want {
			t.Errorf("IsVendorNamespaced(%q) = %v, want %v", c.typ, got, c.want)
		}
	}
}

func TestUnregistered_spec_15_4_1(t *testing.T) {
	cases := []struct {
		typ  string
		want bool
	}{
		{"text", false},             // canonical
		{"execution_result", false}, // canonical
		{"x-acme/heatmap", false},   // vendor-namespaced, deliberate
		{"", false},                 // omitted, not a warning
		{"heatmap", true},           // unprefixed unknown
		{"reasoning", true},         // near-miss of reasoning_trace
		{"x-acme", true},            // malformed namespace falls through
	}
	for _, c := range cases {
		if got := outputtype.Unregistered(c.typ); got != c.want {
			t.Errorf("Unregistered(%q) = %v, want %v", c.typ, got, c.want)
		}
	}
}
