// SPDX-License-Identifier: MIT

// Tier-11 reconciliation between the two halves of the
// lenny-ephemeral-container-cred-guard webhook.
//
// The guard exists to keep an attached ephemeral container away from the
// credential material on an agent pod. That material is written per session at
// /run/lenny/slots/{sessionId}/credentials.json, and no pod carries a
// pod-global credential file directly under /run/lenny/. The chart template that declares the
// webhook and the Go package that carries its decision logic both state, in
// prose, which file the guard protects. When one half names a location the
// platform no longer writes, an operator reading it concludes the guard covers
// a file that does not exist and cannot tell what the deployed conditions
// actually protect.
//
// The prose in the Go package sits under pkg/, which the credential-literal
// sweep does not walk, so this check is what holds it.
//
// spec: 13.1 (credential-file delivery and the ephemeral-container guard),
// 4.7 (adapter manifest credentialsPath)

package tier11_docs_test

import (
	"strings"
	"testing"
)

// credGuardChartTemplate is the chart half of the webhook: the template whose
// leading comment states what the deployed guard protects.
var credGuardChartTemplate = []string{
	"charts", "lenny", "templates", "admission-policies",
	"ephemeral-container-cred-guard-webhook.yaml",
}

// credGuardDecisionSource is the Go half of the webhook: the package whose doc
// comment states the threat and whose credPathPrefix comment states the reach
// of the mount condition.
var credGuardDecisionSource = []string{
	"pkg", "admission", "ephemeral_container_cred_guard", "guard.go",
}

// spec: 13.1, 4.7
// diagnosis: one half of the ephemeral-container credential guard still names
//
//	the retired pod-global credential file directly under /run/lenny/. The guard protects the
//	per-session credential files under /run/lenny/slots/, so the surviving
//	statement describes a file no pod carries and misreports what the deployed
//	webhook covers. A failure names the half to restate onto the per-session
//	path; the decision logic itself does not change, because credPathPrefix
//	stays "/run/lenny" and covers the whole tree.
func TestBothHalvesOfTheEphemeralContainerCredGuardNameThePerSessionCredentialPath(t *testing.T) {
	root := repoRoot(t)

	for _, half := range []struct {
		label string
		parts []string
	}{
		{"charts ephemeral-container-cred-guard-webhook.yaml", credGuardChartTemplate},
		{"pkg/admission/ephemeral_container_cred_guard/guard.go", credGuardDecisionSource},
	} {
		body := readRepoFile(t, root, half.parts...)
		requireNoneContain(t, half.label, body, []string{retiredPodGlobalCredentialPath})
		if !strings.Contains(body, "/run/lenny/slots/") {
			t.Errorf("%s: never names the per-session credential tree /run/lenny/slots/; the guard protects %s",
				half.label, credentialSlotPath)
		}
	}
}
