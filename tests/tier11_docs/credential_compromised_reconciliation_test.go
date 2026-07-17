// SPDX-License-Identifier: MIT

// Tier-11 documentation / spec-consistency checks for the retain-and-deny
// reconciliation of the credential-revocation surface. Under the shared
// lease store a proxy-mode revocation retains the lease and denies it in
// place via the credential deny list instead of deleting it, so:
//
//   - The `CredentialCompromised` alert and the two gauges that drive it
//     count only a revoked credential whose active lease is not shadowed
//     by a deny-list entry, and the alert clears once every active lease
//     against the credential is on the deny list (or was terminated in
//     direct mode). A lease correctly on the deny list is the normal
//     successful outcome and is not a compromise.
//   - The `active_leases_terminated` audit field counts affected leases:
//     terminated via RotateCredentials in direct mode, denied in place via
//     the deny list in proxy mode.
//
// These tests pin the reconciled wording across the spec (§4.9, §11.8,
// §16.5, §24), the single alert-authoring source
// (pkg/alerting/rules/rules.go), its three generated renders, the two
// revocation runbooks, and the metrics reference, and fail against the
// pre-reconciliation "still has active leases" / "clears once all active
// leases are terminated" wording.
//
// These tests are NOT under a build tag: they read the repository state
// directly and need no external infrastructure.

package tier11_docs_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// denyShadowPhrase is the deny-list-shadowing condition the reconciled
// CredentialCompromised gauge, alert, and doc mirrors all carry.
const denyShadowPhrase = "not shadowed by a deny-list entry"

// staleCompromiseSummary is the pre-reconciliation alert summary; its
// presence anywhere means a render or the authoring source is stale.
const staleCompromiseSummary = "Revoked credential still has active leases"

// staleCompromiseDescription is a fragment of the pre-reconciliation alert
// description that must be gone everywhere.
const staleCompromiseDescription = "still has active leases alive against it"

// TestCredentialCompromisedAlertSourceIsDenyListShadowing pins the single
// alert-authoring source (pkg/alerting/rules/rules.go) to the deny-list
// shadowing condition. `make generate` renders these strings into the
// three DO NOT EDIT catalog renders, so the authoring source is the
// upstream of the whole reconciliation.
//
// spec: §16.5 — CredentialCompromised alert row.
func TestCredentialCompromisedAlertSourceIsDenyListShadowing(t *testing.T) {
	root := repoRoot(t)
	src := readRepoFile(t, root, "pkg", "alerting", "rules", "rules.go")

	for _, want := range []string{
		"Revoked credential has an active lease not on the deny list",
		"still has an active lease that is not shadowed by a deny-list entry",
		"The alert clears once every active lease against the credential is on the deny list or has been terminated in direct mode.",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("pkg/alerting/rules/rules.go missing reconciled CredentialCompromised string %q", want)
		}
	}
	for _, stale := range []string{staleCompromiseSummary, staleCompromiseDescription} {
		if strings.Contains(src, stale) {
			t.Errorf("pkg/alerting/rules/rules.go still carries pre-reconciliation string %q", stale)
		}
	}
}

// TestCredentialCompromisedRendersMatchDenyListShadowing pins the three
// generated catalog renders to the reconciled wording so a stale render
// left behind by a skipped `make generate` fails here rather than only in
// the catalog cross-check.
//
// spec: §16.5 — CredentialCompromised alert row.
func TestCredentialCompromisedRendersMatchDenyListShadowing(t *testing.T) {
	root := repoRoot(t)
	for _, parts := range [][]string{
		{"charts", "lenny", "files", "alerting-rules.yaml"},
		{"docs", "alerting", "rules.yaml"},
		{"pkg", "embedded", "manifests", "manifests.yaml"},
	} {
		render := readRepoFile(t, root, parts...)
		rel := filepath.Join(parts...)
		if !strings.Contains(render, "is not shadowed by a deny-list entry") {
			t.Errorf("%s does not carry the reconciled CredentialCompromised description (stale render?)", rel)
		}
		if !strings.Contains(render, "Revoked credential has an active lease not on the deny list") {
			t.Errorf("%s does not carry the reconciled CredentialCompromised summary (stale render?)", rel)
		}
		if strings.Contains(render, staleCompromiseDescription) || strings.Contains(render, staleCompromiseSummary) {
			t.Errorf("%s still carries pre-reconciliation CredentialCompromised wording (stale render?)", rel)
		}
	}
}

// TestCredentialCompromisedSpecSurfacesAgree pins the spec surfaces that
// independently describe the CredentialCompromised signal to the single
// deny-list-shadowing meaning: the two §16.5 gauges, the §16.5 alert row,
// the §11.8 security-signals row and its alert-clear primitive, and the
// §4.9 runbook step 4. The pre-reconciliation "clears once all active
// leases are terminated" wording must be gone.
//
// spec: §16.5, §11.8, §4.9.
func TestCredentialCompromisedSpecSurfacesAgree(t *testing.T) {
	root := repoRoot(t)

	obs := readRepoFile(t, root, "spec", "16_observability.md")
	// Both gauges and the alert row carry the deny-list-shadowing clause.
	if n := strings.Count(obs, denyShadowPhrase); n < 3 {
		t.Errorf("spec/16 carries the deny-list-shadowing clause %d times, want >= 3 (two gauges + alert row)", n)
	}

	pol := readRepoFile(t, root, "spec", "11_policy-and-controls.md")
	if !strings.Contains(pol, "revoked credential has an active lease not on the deny list") {
		t.Errorf("spec/11 §11.8 security-signals row not reconciled to the deny-list-shadowing signal")
	}
	if !strings.Contains(pol, "clears once every active lease against the credential is on the deny list") {
		t.Errorf("spec/11 §11.8 alert-clear primitive not reconciled to the deny-list-shadowing condition")
	}
	if strings.Contains(pol, "clears once all active leases are terminated") {
		t.Errorf("spec/11 §11.8 still carries the pre-reconciliation alert-clear wording")
	}

	comp := readRepoFile(t, root, "spec", "04_system-components.md")
	if !strings.Contains(comp, "every active lease against the credential has reached a deny-list entry or been terminated in direct mode") {
		t.Errorf("spec/04 §4.9 runbook step 4 not reconciled to the deny-list-shadowing definition of propagation success")
	}
}

// TestCredentialCompromisedDocMirrorsAgree pins the two revocation runbooks
// and the metrics reference to the reconciled deny-list-shadowing wording,
// so a doc mirror cannot drift from the spec and the alert catalog.
//
// spec: §16.5 — CredentialCompromised alert row.
func TestCredentialCompromisedDocMirrorsAgree(t *testing.T) {
	root := repoRoot(t)

	emergency := readRepoFile(t, root, "docs", "runbooks", "emergency-credential-revocation.md")
	if !strings.Contains(emergency, "is not shadowed by a deny-list entry") {
		t.Errorf("docs/runbooks/emergency-credential-revocation.md trigger not reconciled to the deny-list-shadowing condition")
	}
	if strings.Contains(emergency, "still has active leases against it for more than 30s") {
		t.Errorf("docs/runbooks/emergency-credential-revocation.md still carries the pre-reconciliation trigger wording")
	}

	revocation := readRepoFile(t, root, "docs", "runbooks", "credential-revocation.md")
	if !strings.Contains(revocation, denyShadowPhrase) {
		t.Errorf("docs/runbooks/credential-revocation.md gauge description not reconciled to the deny-list-shadowing condition")
	}

	metrics := readRepoFile(t, root, "docs", "reference", "metrics.md")
	if !strings.Contains(metrics, denyShadowPhrase) {
		t.Errorf("docs/reference/metrics.md CredentialCompromised row not reconciled to the deny-list-shadowing condition")
	}
	if strings.Contains(metrics, "Revoked credential has active leases for > 30s") {
		t.Errorf("docs/reference/metrics.md CredentialCompromised row still carries the pre-reconciliation wording")
	}
}

// TestActiveLeasesTerminatedCountsAffectedLeases pins the audit-count
// reconciliation: with proxy-mode leases denied in place rather than
// removed, the active_leases_terminated audit field and its lenny-ctl and
// §11.2.1 mirrors describe leases affected (terminated in direct mode,
// denied in place via the deny list in proxy mode) across §4.9, §11.2.1,
// and §24.
//
// spec: §4.9, §11.2.1, §24.
func TestActiveLeasesTerminatedCountsAffectedLeases(t *testing.T) {
	root := repoRoot(t)

	comp := readRepoFile(t, root, "spec", "04_system-components.md")
	if !strings.Contains(comp, "denied in place via the credential deny list in proxy mode") {
		t.Errorf("spec/04 §4.9 active_leases_terminated semantics not reconciled to denied-in-place proxy-mode wording")
	}

	pol := readRepoFile(t, root, "spec", "11_policy-and-controls.md")
	if !strings.Contains(pol, "denied in place via the deny list in proxy mode") {
		t.Errorf("spec/11 §11.2.1 audit rows not reconciled to denied-in-place proxy-mode wording")
	}

	ctl := readRepoFile(t, root, "spec", "24_lenny-ctl-command-reference.md")
	if !strings.Contains(ctl, "terminated in direct mode, denied in place in proxy mode") {
		t.Errorf("spec/24 revoke-credential command description not reconciled to denied-in-place proxy-mode wording")
	}
}
