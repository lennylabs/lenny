// SPDX-License-Identifier: MIT

package gatewaylayout

import "testing"

// TestClassifierGivesTheRegisterPartOrTheDirectoryPart checks the
// classifier the wiring check reads parts through.
// spec: GW-R1
func TestClassifierGivesTheRegisterPartOrTheDirectoryPart(t *testing.T) {
	reg := Register{Version: 1, Packages: []PackageRow{
		{Package: "pkg/gateway/sessionserver", Part: "core", Step: "0117.S11"},
		{Package: "pkg/common/semver", Part: PartDomain, Step: "0117.S4"},
	}}
	cl := NewClassifier(Layout, reg)
	cases := []struct {
		rel    string
		part   string
		kind   Kind
		isPart bool
	}{
		{"pkg/gateway/sessionserver", "core", KindCore, true},
		{"pkg/common/semver", PartDomain, KindDomain, false},
		{"pkg/gateway/streamproxy/bus", "stream-proxy", KindSeam, true},
		{"pkg/gateway/unknown/x", "", KindUnplaced, false},
		{"cmd/lenny-gateway", "", KindCompositionRoot, false},
		{"pkg/quota", "", KindOutside, false},
	}
	for _, tc := range cases {
		part, kind := cl.Part(tc.rel)
		if part != tc.part || kind != tc.kind || IsGatewayPart(kind) != tc.isPart {
			t.Errorf("Part(%s) = %q, %v (part %v), want %q, %v (part %v)", tc.rel, part, kind, IsGatewayPart(kind), tc.part, tc.kind, tc.isPart)
		}
	}
	moves := []Move{{Old: "pkg/gateway/a", New: "pkg/gateway/core/a", Step: "0117.S11"}}
	if got := MapBack("pkg/gateway/core/a/sub", moves); got != "pkg/gateway/a/sub" {
		t.Errorf("MapBack = %q", got)
	}
}
