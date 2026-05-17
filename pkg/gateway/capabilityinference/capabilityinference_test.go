// SPDX-License-Identifier: MIT

package capabilityinference_test

import (
	"strings"
	"testing"

	ci "github.com/lennylabs/lenny/pkg/gateway/capabilityinference"
)

func ptr(b bool) *bool { return &b }

func caps(cs ...ci.Capability) []ci.Capability { return cs }

func TestInferAnnotationTable(t *testing.T) {
	cases := []struct {
		name string
		ann  ci.ToolAnnotations
		want []ci.Capability
	}{
		{
			name: "readOnlyHint true infers read",
			ann:  ci.ToolAnnotations{ReadOnlyHint: ptr(true)},
			want: caps(ci.CapRead),
		},
		{
			name: "not read-only and not destructive infers write",
			ann:  ci.ToolAnnotations{ReadOnlyHint: ptr(false), DestructiveHint: ptr(false)},
			want: caps(ci.CapWrite),
		},
		{
			name: "destructive infers write and delete",
			ann:  ci.ToolAnnotations{DestructiveHint: ptr(true)},
			want: caps(ci.CapWrite, ci.CapDelete),
		},
		{
			name: "open world adds network",
			ann:  ci.ToolAnnotations{ReadOnlyHint: ptr(false), OpenWorldHint: ptr(true)},
			want: caps(ci.CapWrite, ci.CapNetwork),
		},
		{
			name: "destructive and open world infer write, delete, network",
			ann:  ci.ToolAnnotations{DestructiveHint: ptr(true), OpenWorldHint: ptr(true)},
			want: caps(ci.CapWrite, ci.CapDelete, ci.CapNetwork),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ci.Infer(tc.ann, ci.ModeStrict)
			if !equal(got.Capabilities, tc.want) {
				t.Errorf("Infer = %v, want %v", got.Capabilities, tc.want)
			}
			if got.InferredAdminUnannotated {
				t.Error("an annotated tool must not flag InferredAdminUnannotated")
			}
		})
	}
}

func TestInferUnannotatedStrictIsAdmin(t *testing.T) {
	// §5.1: in strict mode an unannotated tool infers admin and raises
	// the warning signal.
	got := ci.Infer(ci.ToolAnnotations{}, ci.ModeStrict)
	if !equal(got.Capabilities, caps(ci.CapAdmin)) {
		t.Errorf("strict unannotated = %v, want [admin]", got.Capabilities)
	}
	if !got.InferredAdminUnannotated {
		t.Error("strict unannotated must flag InferredAdminUnannotated for the §5.1 WARN")
	}
}

func TestInferUnannotatedPermissiveIsWrite(t *testing.T) {
	// §5.1: in permissive mode an unannotated tool infers write and
	// raises no warning.
	got := ci.Infer(ci.ToolAnnotations{}, ci.ModePermissive)
	if !equal(got.Capabilities, caps(ci.CapWrite)) {
		t.Errorf("permissive unannotated = %v, want [write]", got.Capabilities)
	}
	if got.InferredAdminUnannotated {
		t.Error("permissive mode must not raise the admin-unannotated warning")
	}
}

func TestInferDefaultsInvalidMode(t *testing.T) {
	// An unrecognised mode falls back to the §5.1 strict default.
	got := ci.Infer(ci.ToolAnnotations{}, ci.Mode("bogus"))
	if !equal(got.Capabilities, caps(ci.CapAdmin)) {
		t.Errorf("invalid mode unannotated = %v, want strict default [admin]", got.Capabilities)
	}
}

func TestInferAnnotatedButInconclusiveTakesModeDefault(t *testing.T) {
	// A tool that carries an annotation but produces no capability (for
	// example destructiveHint:false alone) takes the mode default, but
	// it is not flagged as unannotated since it did annotate.
	ann := ci.ToolAnnotations{DestructiveHint: ptr(false)}
	strict := ci.Infer(ann, ci.ModeStrict)
	if !equal(strict.Capabilities, caps(ci.CapAdmin)) {
		t.Errorf("inconclusive strict = %v, want [admin]", strict.Capabilities)
	}
	if strict.InferredAdminUnannotated {
		t.Error("an annotated-but-inconclusive tool must not flag InferredAdminUnannotated")
	}
}

func TestModeIsValid(t *testing.T) {
	for _, m := range ci.AllModes() {
		if !m.IsValid() {
			t.Errorf("%q from the closed enum reports invalid", m)
		}
	}
	if ci.Mode("loose").IsValid() {
		t.Error("an unknown mode must report invalid")
	}
	if ci.DefaultMode != ci.ModeStrict {
		t.Errorf("DefaultMode = %q, want strict", ci.DefaultMode)
	}
}

func TestResolveOverrideWins(t *testing.T) {
	// §5.1: a tool with a toolCapabilityOverrides entry uses that
	// explicit set, and inference does not run.
	overrides := map[string][]ci.Capability{
		"deploy": {ci.CapExecute, ci.CapAdmin},
	}
	// The annotations would infer read, but the override wins.
	got := ci.Resolve("deploy", ci.ToolAnnotations{ReadOnlyHint: ptr(true)}, ci.ModeStrict, overrides)
	if !equal(got.Capabilities, caps(ci.CapExecute, ci.CapAdmin)) {
		t.Errorf("override Resolve = %v, want [execute admin]", got.Capabilities)
	}
	if got.InferredAdminUnannotated {
		t.Error("an overridden tool must not raise the inference warning")
	}
}

func TestResolveFallsThroughToInference(t *testing.T) {
	// A tool with no override entry is inferred from its annotations.
	got := ci.Resolve("reader", ci.ToolAnnotations{ReadOnlyHint: ptr(true)}, ci.ModeStrict, nil)
	if !equal(got.Capabilities, caps(ci.CapRead)) {
		t.Errorf("no-override Resolve = %v, want [read]", got.Capabilities)
	}
	// An unannotated, non-overridden tool still takes the strict default.
	def := ci.Resolve("mystery", ci.ToolAnnotations{}, ci.ModeStrict, map[string][]ci.Capability{"other": {ci.CapRead}})
	if !equal(def.Capabilities, caps(ci.CapAdmin)) || !def.InferredAdminUnannotated {
		t.Errorf("non-overridden unannotated Resolve = %+v, want [admin] flagged", def)
	}
}

func TestResolveOverrideDeduplicatesAndOrders(t *testing.T) {
	overrides := map[string][]ci.Capability{
		"messy": {ci.CapAdmin, ci.CapRead, ci.CapRead, ci.CapWrite},
	}
	got := ci.Resolve("messy", ci.ToolAnnotations{}, ci.ModeStrict, overrides)
	if !equal(got.Capabilities, caps(ci.CapRead, ci.CapWrite, ci.CapAdmin)) {
		t.Errorf("override Resolve = %v, want deduped read,write,admin", got.Capabilities)
	}
}

func TestValidateOverrides(t *testing.T) {
	ok := map[string][]ci.Capability{
		"a": {ci.CapRead, ci.CapWrite},
		"b": {ci.CapAdmin},
	}
	if err := ci.ValidateOverrides(ok); err != nil {
		t.Errorf("valid overrides rejected: %v", err)
	}
	if err := ci.ValidateOverrides(map[string][]ci.Capability{"": {ci.CapRead}}); err == nil {
		t.Error("an empty tool name must be rejected")
	}
	if err := ci.ValidateOverrides(map[string][]ci.Capability{"t": {"superuser"}}); err == nil {
		t.Error("an unrecognised capability must be rejected")
	}
}

func TestCapabilityIsValid(t *testing.T) {
	for _, c := range ci.AllCapabilities() {
		if !c.IsValid() {
			t.Errorf("%q from the closed enum reports invalid", c)
		}
	}
	if ci.Capability("superuser").IsValid() {
		t.Error("an unknown capability must report invalid")
	}
}

func TestWarnMessage(t *testing.T) {
	msg := ci.WarnMessage("dangerous_tool", "acme-connector")
	for _, want := range []string{"dangerous_tool", "acme-connector", "admin", "toolCapabilityOverrides"} {
		if !strings.Contains(msg, want) {
			t.Errorf("WarnMessage missing %q: %s", want, msg)
		}
	}
}

func equal(a, b []ci.Capability) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
