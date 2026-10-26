// SPDX-License-Identifier: MIT

package rules

import "testing"

// TestRunbookSlugDropsTheSectionFragment pins the alert_fired runbook
// slug for a rule that names only a runbook URL: a section fragment is
// dropped, so a section link yields the page slug, and a rule whose URL
// is empty or carries a fragment alone yields no slug. A URL with no
// path separator is its own slug.
//
// spec: 25.7 (Operational Runbooks), 16.5 (Alerting Rules and SLOs)
func TestRunbookSlugDropsTheSectionFragment(t *testing.T) {
	cases := map[string]Rule{
		"jwt-key-rotation": {RunbookURL: runbookSection("jwt-key-rotation", "rotate-the-signing-key")},
		"kms-signing":      {RunbookURL: runbook("kms-signing")},
		"explicit":         {RunbookShortName: "explicit", RunbookURL: runbook("other")},
		"bare-slug":        {RunbookURL: "bare-slug#section"},
		"":                 {RunbookURL: "#orphan-fragment"},
	}
	for want, r := range cases {
		if got := r.RunbookSlug(); got != want {
			t.Errorf("RunbookSlug(%q) = %q, want %q", r.RunbookURL, got, want)
		}
	}
	if got := (Rule{}).RunbookSlug(); got != "" {
		t.Errorf("RunbookSlug of a rule with no runbook = %q, want empty", got)
	}
}
