// SPDX-License-Identifier: MIT

// Tier-11 reconciliation between the operator guide's admission-policy list
// and the container identity rule the lenny-pod-security webhook enforces.
//
// A deployer whose mutating webhook injects a sidecar into agent pods meets
// this rule as an admission rejection. The operator guide is where that
// deployer looks for the cause and the fix, so its policy item must state the
// whole rule (post-mutation scope, explicit nonzero container-level identity,
// and the two reserved UIDs) and must quote the reserved UIDs' chart keys with
// the defaults the chart actually ships. A default that drifts from the chart
// tells the deployer to avoid the wrong UID.
//
// spec: 13.1 (Pod Security)

package tier11_docs_test

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// namespaceIsolationGuide is the operator-guide page that lists the chart's
// admission policies.
var namespaceIsolationGuide = []string{"docs", "operator-guide", "namespace-and-isolation.md"}

// chartValues is the chart's default values file, which owns the reserved
// UID defaults.
var chartValues = []string{"charts", "lenny", "values.yaml"}

// policyManifestItemRE matches one numbered item of the Policy Manifests list
// and captures its number and its full single-line body.
var policyManifestItemRE = regexp.MustCompile(`(?m)^(\d+)\. (\*\*.*)$`)

// policyManifestItems returns the numbered items of the Policy Manifests list,
// keyed by item number, and fails when the numbering is not consecutive from 1.
func policyManifestItems(t *testing.T, guide string) map[int]string {
	t.Helper()
	start := strings.Index(guide, "### Policy Manifests")
	if start < 0 {
		t.Fatalf("namespace-and-isolation.md: no Policy Manifests heading")
	}
	section := guide[start:]
	if end := strings.Index(section[len("### Policy Manifests"):], "\n### "); end >= 0 {
		section = section[:len("### Policy Manifests")+end]
	}
	items := map[int]string{}
	for i, m := range policyManifestItemRE.FindAllStringSubmatch(section, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("item number %q: %v", m[1], err)
		}
		if n != i+1 {
			t.Errorf("Policy Manifests item %d is numbered %d; the list must number consecutively from 1", i+1, n)
		}
		items[n] = m[2]
	}
	return items
}

// podUIDDefault returns the chart default for security.podUIDs.<key>.
func podUIDDefault(t *testing.T, values, key string) string {
	t.Helper()
	start := strings.Index(values, "\n  podUIDs:\n")
	if start < 0 {
		t.Fatalf("values.yaml: no security.podUIDs block")
	}
	m := regexp.MustCompile(`(?m)^    ` + key + `: (\d+)\s*$`).FindStringSubmatch(values[start:])
	if m == nil {
		t.Fatalf("values.yaml: no security.podUIDs.%s default", key)
	}
	return m[1]
}

// findItem returns the number and body of the Policy Manifests item whose
// title contains marker.
func findItem(items map[int]string, marker string) (int, string) {
	for n, body := range items {
		title, _, _ := strings.Cut(body, " -- ")
		if strings.Contains(title, marker) {
			return n, body
		}
	}
	return 0, ""
}

// spec: 13.1 (Pod Security)
// diagnosis: the operator guide's admission-policy list no longer states the
//
//	lenny-pod-security container identity check completely, or it quotes a
//	reserved-UID default that differs from the chart's values.yaml. A deployer
//	whose injected sidecar is rejected then cannot find the cause, or is told
//	to avoid the wrong UID. Restate the item from the 13.1 container identity
//	paragraph and the chart's security.podUIDs defaults.
func TestOperatorGuideStatesTheContainerIdentityCheckWithChartDefaults(t *testing.T) {
	root := repoRoot(t)
	guide := readRepoFile(t, root, namespaceIsolationGuide...)
	values := readRepoFile(t, root, chartValues...)
	items := policyManifestItems(t, guide)

	n, item := findItem(items, "`lenny-pod-security` webhook container identity check")
	if item == "" {
		t.Fatalf("namespace-and-isolation.md: Policy Manifests has no lenny-pod-security container identity item")
	}
	adapterUID := podUIDDefault(t, values, "adapter")
	agentUID := podUIDDefault(t, values, "agent")

	requireAllContain(t, "container identity item", item, []string{
		"`CREATE` and `UPDATE`",
		"after mutating admission",
		"every RuntimeClass",
		"init, regular, and ephemeral container",
		"`securityContext.runAsUser`",
		"`securityContext.runAsGroup`",
		"at container level",
		"pod-level",
		"`security.podUIDs.adapter` (default " + adapterUID + ")",
		"`security.podUIDs.agent` (default " + agentUID + ")",
		"container named `adapter`",
		"container named `runtime`",
		"outside both reserved UIDs",
		"`nonroot`",
		"override `security.podUIDs.adapter`",
	})
	if strings.Contains(item, "§") {
		t.Errorf("container identity item cites a specification section number; published docs must not")
	}

	// The recommended fix precedes the alternative of overriding the
	// adapter UID.
	if strings.Index(item, "recommended fix") > strings.Index(item, "override `security.podUIDs.adapter`") {
		t.Errorf("container identity item states the adapter-UID override before the recommended injector fix")
	}

	_, credGuard := findItem(items, "`lenny-ephemeral-container-cred-guard` webhook")
	if credGuard == "" {
		t.Fatalf("namespace-and-isolation.md: Policy Manifests has no lenny-ephemeral-container-cred-guard item")
	}
	if want := "described in item " + strconv.Itoa(n); !strings.Contains(credGuard, want) {
		t.Errorf("ephemeral-container cred-guard item does not point ephemeral containers at the container identity check (%q)", want)
	}
}
