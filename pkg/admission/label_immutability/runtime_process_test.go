// SPDX-License-Identifier: MIT

package label_immutability

import "testing"

// spec: 17.2 (item 5), 5.2 (Runtime-process lifetime)
func TestDecideImmutableLabelsHoldsTheRuntimeProcessLabel_spec_17_2(t *testing.T) {
	cases := []struct {
		name     string
		old, new map[string]string
		allowed  bool
	}{
		{"change keep to restart", map[string]string{LabelRuntimeProcess: "keep"}, map[string]string{LabelRuntimeProcess: "restart"}, false},
		{"change restart to keep", map[string]string{LabelRuntimeProcess: "restart"}, map[string]string{LabelRuntimeProcess: "keep"}, false},
		{"add", map[string]string{}, map[string]string{LabelRuntimeProcess: "restart"}, false},
		{"remove", map[string]string{LabelRuntimeProcess: "keep"}, map[string]string{}, false},
		{"unchanged", map[string]string{LabelRuntimeProcess: "keep"}, map[string]string{LabelRuntimeProcess: "keep", "other": "x"}, true},
		{"create carries any value", nil, map[string]string{LabelRuntimeProcess: "restart"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, err := DecideImmutableLabels(Request{OldLabels: c.old, NewLabels: c.new, UserInfoUsername: WarmPoolControllerSA})
			if err != nil {
				t.Fatalf("DecideImmutableLabels: %v", err)
			}
			if d.Allowed != c.allowed {
				t.Fatalf("allowed = %v (%s), want %v", d.Allowed, d.Reason, c.allowed)
			}
			if !c.allowed && d.Code != 403 {
				t.Errorf("code = %d, want 403", d.Code)
			}
		})
	}
	if LabelRuntimeProcess != "lenny.dev/runtime-process" {
		t.Errorf("LabelRuntimeProcess = %q", LabelRuntimeProcess)
	}
}
