// SPDX-License-Identifier: MIT

// Tier-11 reconciliation for what a rotation-ceiling hit means.
//
// The §4.7 in-flight gate polls the adapter's pod-wide per-provider
// in-flight counter, and the 300-second ceiling is pod-scoped with it.
// Every session is bound to a slot on every pod, so on a pod holding
// more than one bound session a co-tenant's outstanding request for the
// same provider gates the rotating session and can drive the ceiling on
// its own. A surface that reads the counter or the alert as a compromise
// indicator without that qualification tells an operator to investigate
// a runtime that did nothing wrong.
//
// The single alert-authoring source, its three generated renders, and
// the two reader-facing mirrors are pinned here, so a catalog edit
// shipped without `make generate` fails at this tier rather than
// reaching a cluster through the embedded manifest bundle.
//
// These tests read the repository state directly and need no external
// infrastructure.
//
// spec: 4.7 (revocation-triggered rotation ceiling), 16.1 and 16.5
// (counter and alert), 6.1 (one bound slot per session on every pod)

package tier11_docs_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// coTenantGatePhrase is the qualification every surface carries: the
// counter the gate polls is pod-wide, so a co-tenant can drive it.
const coTenantGatePhrase = "co-tenant"

// staleCompromiseVerdict is the unqualified reading the restatement
// removes. It asserts a compromised or buggy runtime with no mention of
// the pod-wide counter, which is false for a firing the merged rotation
// path newly creates.
const staleCompromiseVerdict = "Indicates a compromised or buggy runtime."

// spec: 4.7, 16.5
// diagnosis: the single-source alert catalog still reads a
//
//	rotation-ceiling hit as a compromised or buggy runtime without
//	naming the pod-wide in-flight counter. Every session holds a slot on
//	every pod, so the gate a rotation waits on counts a co-tenant's
//	outstanding requests too, and an operator paged on this alert
//	investigates the wrong runtime.
func TestRotationCeilingAlertSourceNamesThePodWideGate(t *testing.T) {
	root := repoRoot(t)
	src := readRepoFile(t, root, "pkg", "alerting", "rules", "rules.go")
	if !strings.Contains(src, "pod-wide per-provider in-flight counter") {
		t.Error("pkg/alerting/rules/rules.go: the OutstandingInflightAtRotationCeiling description does not name the pod-wide in-flight counter")
	}
	if !strings.Contains(src, coTenantGatePhrase) {
		t.Error("pkg/alerting/rules/rules.go: the OutstandingInflightAtRotationCeiling description does not name the co-tenant cause")
	}
	if strings.Contains(src, staleCompromiseVerdict) {
		t.Errorf("pkg/alerting/rules/rules.go still carries the unqualified reading %q", staleCompromiseVerdict)
	}
}

// spec: 4.7, 16.5
// diagnosis: a generated alert render is stale. `make generate` renders
//
//	the chart fragment, the docs copy, and the embedded manifest bundle
//	from the catalog, and the bundle is what Embedded Mode applies to a
//	cluster, so a skipped re-render ships the retired reading.
func TestRotationCeilingRendersCarryThePodWideGate(t *testing.T) {
	root := repoRoot(t)
	for _, parts := range [][]string{
		{"charts", "lenny", "files", "alerting-rules.yaml"},
		{"docs", "alerting", "rules.yaml"},
		{"pkg", "embedded", "manifests", "manifests.yaml"},
	} {
		render := readRepoFile(t, root, parts...)
		rel := filepath.Join(parts...)
		if !strings.Contains(render, "pod-wide per-provider in-flight counter") {
			t.Errorf("%s does not carry the restated OutstandingInflightAtRotationCeiling description (stale render?)", rel)
		}
		if strings.Contains(render, staleCompromiseVerdict) {
			t.Errorf("%s still carries the unqualified rotation-ceiling reading (stale render?)", rel)
		}
	}
}

// spec: 4.7, 16.1, 16.5
// diagnosis: a reader-facing mirror of the rotation-ceiling counter or
//
//	alert still reads a hit as a compromised or buggy runtime alone. The
//	metrics reference and the observability guide are where an operator
//	looks the signal up, so both name the co-tenant cause the pod-wide
//	gate creates.
func TestRotationCeilingDocMirrorsNameTheCoTenantCause(t *testing.T) {
	root := repoRoot(t)
	for label, parts := range map[string][]string{
		"docs/reference/metrics.md":            {"docs", "reference", "metrics.md"},
		"docs/operator-guide/observability.md": {"docs", "operator-guide", "observability.md"},
	} {
		page := readRepoFile(t, root, parts...)
		for _, line := range strings.Split(page, "\n") {
			if !strings.Contains(line, "OutstandingInflightAtRotationCeiling") &&
				!strings.Contains(line, "lenny_credential_rotation_inflight_ceiling_hit_total") {
				continue
			}
			if strings.Contains(line, coTenantGatePhrase) {
				continue
			}
			// A row that only cross-references the alert by name carries
			// no reading to qualify.
			if !strings.Contains(line, "in-flight") {
				continue
			}
			t.Errorf("%s: a rotation-ceiling row states the reading without the co-tenant cause:\n%s",
				label, strings.TrimSpace(line))
		}
	}
}
