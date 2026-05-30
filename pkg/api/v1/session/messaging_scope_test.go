// SPDX-License-Identifier: MIT

package session_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
)

// TestResolveEffectiveMessagingScope_spec_7_2_250 verifies the §7.2
// "Effective scope" resolution: the narrowest of the deployment default,
// the deployment maxScope ceiling, and the optional tenant/runtime
// overrides; the restrictiveness order is `direct` < `siblings`; an
// empty default resolves to `direct`; empty tenant/runtime values are
// skipped rather than read as `direct`; an empty ceiling imposes no cap
// while an explicit `direct` ceiling forbids siblings tree-wide.
// spec: §7.2 lines 250-266. F-7.2.6.
func TestResolveEffectiveMessagingScope_spec_7_2_250(t *testing.T) {
	const (
		direct   = session.MessagingScopeDirect
		siblings = session.MessagingScopeSiblings
		unset    = session.MessagingScope("")
	)
	cases := []struct {
		name                             string
		dflt, max, tenant, runtime, want session.MessagingScope
	}{
		{"all unset defaults to direct", unset, unset, unset, unset, direct},
		{"default direct, ceiling siblings, no overrides", direct, siblings, unset, unset, direct},
		{"default siblings under siblings ceiling", siblings, siblings, unset, unset, siblings},
		{"empty ceiling honours a siblings default", siblings, unset, unset, unset, siblings},
		{"direct ceiling caps a siblings default", siblings, direct, unset, unset, direct},
		{"tenant narrows siblings to direct", siblings, siblings, direct, unset, direct},
		{"runtime narrows siblings to direct", siblings, siblings, unset, direct, direct},
		{"tenant cannot widen a direct default", direct, siblings, siblings, unset, direct},
		{"runtime cannot widen a direct default", direct, siblings, unset, siblings, direct},
		{"garbage values collapse to direct", "garbage", "garbage", unset, unset, direct},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := session.ResolveEffectiveMessagingScope(tc.dflt, tc.max, tc.tenant, tc.runtime)
			if got != tc.want {
				t.Errorf("ResolveEffectiveMessagingScope(%q,%q,%q,%q) = %q, want %q",
					tc.dflt, tc.max, tc.tenant, tc.runtime, got, tc.want)
			}
		})
	}
}
