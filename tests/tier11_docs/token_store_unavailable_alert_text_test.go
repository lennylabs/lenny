// SPDX-License-Identifier: MIT

package tier11_docs_test

import (
	"strings"
	"testing"
)

// TestTokenStoreUnavailableRenderedDescriptionNamesNoDelegationMint pins
// the TokenStoreUnavailable description in every committed rendering of
// the alert catalog to the §16.5 row: an unreachable Postgres primary
// fails session creation and credential leasing. Internal delegation
// through lenny/delegate_task mints no token, so no rendering names a
// delegation mint among the failing paths. The pre-fix renderings listed
// "delegation minting" and fail this test.
//
// spec: §16.5 (TokenStoreUnavailable critical-alert row); §17.7; §8.2.
func TestTokenStoreUnavailableRenderedDescriptionNamesNoDelegationMint(t *testing.T) {
	const (
		want    = "session creation and credential leasing are both failing."
		retired = "delegation minting"
	)
	root := repoRoot(t)
	renderings := [][]string{
		{"charts", "lenny", "files", "alerting-rules.yaml"},
		{"docs", "alerting", "rules.yaml"},
		{"pkg", "embedded", "manifests", "manifests.yaml"},
	}
	for _, parts := range renderings {
		path := strings.Join(parts, "/")
		t.Run(path, func(t *testing.T) {
			line := tokenStoreUnavailableDescription(t, readRepoFile(t, root, parts...))
			if !strings.Contains(line, want) {
				t.Errorf("%s: TokenStoreUnavailable description %q does not contain %q", path, line, want)
			}
			if strings.Contains(line, retired) {
				t.Errorf("%s: TokenStoreUnavailable description %q still names %q", path, line, retired)
			}
		})
	}
}

// tokenStoreUnavailableDescription returns the description line of the
// TokenStoreUnavailable rule in a rendered rule file, failing the test
// when the rule or its description is absent.
func tokenStoreUnavailableDescription(t *testing.T, body string) string {
	t.Helper()
	_, rest, ok := strings.Cut(body, "alert: TokenStoreUnavailable")
	if !ok {
		t.Fatal("rendered rule file carries no TokenStoreUnavailable rule")
	}
	if next := strings.Index(rest, "- alert:"); next >= 0 {
		rest = rest[:next]
	}
	for _, line := range strings.Split(rest, "\n") {
		if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "description:") {
			return trimmed
		}
	}
	t.Fatal("TokenStoreUnavailable rule carries no description annotation")
	return ""
}
