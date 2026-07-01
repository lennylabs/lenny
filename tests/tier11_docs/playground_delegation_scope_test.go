// SPDX-License-Identifier: MIT

// Tier-11 documentation checks for the proposal-0015 reconciliation: the
// playground delegation-policy affordance is gated on the minted bearer's
// effective scope granting tools:sessions:write, the effective scope is
// surfaced through the POST /v1/playground/token mint response, and the
// create-payload wire-key is delegationLease.delegationPolicyRef. These
// tests pin the spec cross-surface agreement and the spec/doc/struct
// consistency the proposal staged.
//
// These tests are NOT under a build tag; they read the repository spec,
// docs, and the playground token.go directly and require no external
// infrastructure.

package tier11_docs_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readRepoFile reads a repo-relative file and fails the test if it is missing.
func readRepoFile(t *testing.T, root string, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{root}, parts...)...)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

// TestPlaygroundMintResponseEffectiveScopeSurfacesAgree pins the two spec
// surfaces that independently enumerate the POST /v1/playground/token success
// body and the tokenResponse struct to all carry effectiveScope. §27.3.1 is the
// authoritative mint-response body, §15.1 line 903 is the endpoint-table
// duplicate, and pkg/gateway/mcpfabric/playground/token.go is the code struct; the
// proposal added effectiveScope to all three so they cannot drift.
//
// diagnosis: a failure means one of the three effectiveScope carrier surfaces
// (the §27.3.1 mint body, the §15.1 endpoint-table enumeration, or the
// tokenResponse struct) dropped or renamed effectiveScope, so the SPA can no
// longer read the bearer's effective scope to gate the §27.4 delegation field.
//
// spec: §27.3.1 (mint response), §15.1 (REST endpoint table)
func TestPlaygroundMintResponseEffectiveScopeSurfacesAgree(t *testing.T) {
	root := repoRoot(t)

	spec27 := readRepoFile(t, root, "spec", "27_web-playground.md")
	spec15 := readRepoFile(t, root, "spec", "15_external-api-surface.md")
	tokenGo := readRepoFile(t, root, "pkg", "gateway", "mcpfabric", "playground", "token.go")

	// §27.3.1 mint-response body must carry the effectiveScope JSON field.
	if !strings.Contains(spec27, `"effectiveScope"`) {
		t.Error("spec/27_web-playground.md §27.3.1 mint-response body does not carry effectiveScope; the SPA reads it to gate the delegation field")
	}
	// §27.3.1 prose must document the field equals the JWT scope claim.
	if !strings.Contains(spec27, "effectiveScope` field carries the minted JWT's `scope` claim") {
		t.Error("spec/27_web-playground.md §27.3.1 prose does not document that effectiveScope carries the minted JWT's scope claim")
	}
	// §15.1 endpoint-table duplicate of the mint response must list it too.
	if !strings.Contains(spec15, `"effectiveScope"`) {
		t.Error("spec/15_external-api-surface.md §15.1 POST /v1/playground/token Response enumeration does not list effectiveScope; it must agree with §27.3.1 and the tokenResponse struct")
	}
	// The tokenResponse struct must carry the matching JSON tag.
	if !strings.Contains(tokenGo, "`json:\"effectiveScope\"`") {
		t.Error("pkg/gateway/mcpfabric/playground/token.go tokenResponse struct does not carry the effectiveScope JSON tag; the wire field must match the §27.3.1 and §15.1 spec surfaces")
	}
}

// TestPlaygroundDelegationGateWordingAgrees pins the §27.4 item 2 gate wording
// to the proposal's settled predicate (the minted bearer's effective scope
// granting tools:sessions:write, satisfied by tools:sessions:* or tools:*) and
// to the create-payload target field delegationLease.delegationPolicyRef. It
// also confirms the §25.1 playground-allowed scope set still pins
// tools:sessions:* (the ceiling capability that subsumes the probe) and that
// §14 lists delegationLease among the CreateSessionRequest outer fields.
//
// diagnosis: a failure means the §27.4 gate wording, the §25.1 ceiling, or the
// §14 CreateSessionRequest outer-field list drifted from the implemented
// client-side visibility gate; the spec would then describe a gate the code,
// helper, and tests do not enforce.
//
// spec: §27.4 (UI surface), §25.1 (playground-allowed scope set), §14 (CreateSessionRequest outer fields)
func TestPlaygroundDelegationGateWordingAgrees(t *testing.T) {
	root := repoRoot(t)

	spec27 := readRepoFile(t, root, "spec", "27_web-playground.md")
	spec25 := readRepoFile(t, root, "spec", "25_agent-operability.md")
	spec14 := readRepoFile(t, root, "spec", "14_workspace-plan-schema.md")

	// §27.4 must frame the delegation field as a client-side visibility
	// affordance keyed on the effective scope granting tools:sessions:write.
	if !strings.Contains(spec27, "client-side visibility affordance") {
		t.Error("spec/27_web-playground.md §27.4 item 2 no longer frames the delegation-policy field as a client-side visibility affordance")
	}
	if !strings.Contains(spec27, "effective scope grants `tools:sessions:write`") {
		t.Error("spec/27_web-playground.md §27.4 item 2 does not gate the delegation field on the effective scope granting tools:sessions:write")
	}
	// The wildcard subsumption (tools:sessions:* or tools:*) must be named so
	// the spec gate matches the SPA hasScope helper semantics.
	if !strings.Contains(spec27, "`tools:sessions:*` or `tools:*`") {
		t.Error("spec/27_web-playground.md §27.4 item 2 does not name the tools:sessions:* / tools:* wildcards that subsume the tools:sessions:write probe")
	}
	// The gate must name the create-payload target field.
	if !strings.Contains(spec27, "delegationLease.delegationPolicyRef") {
		t.Error("spec/27_web-playground.md §27.4 item 2 does not name delegationLease.delegationPolicyRef as the create-payload target field")
	}
	// The §25.1 ceiling must still pin tools:sessions:* (the capability the
	// gate keys on through the playground bearer).
	if !strings.Contains(spec25, "tools:sessions:*") {
		t.Error("spec/25_agent-operability.md §25.1 playground-allowed scope set no longer pins tools:sessions:*; the §27.4 gate depends on the bearer carrying it")
	}
	// §14 CreateSessionRequest outer-field list must include delegationLease.
	if !strings.Contains(spec14, "delegationLease") {
		t.Error("spec/14_workspace-plan-schema.md no longer lists delegationLease among the CreateSessionRequest outer fields; the §27.4 affordance sets delegationLease.delegationPolicyRef")
	}
}

// TestPlaygroundDocsDoNotDocumentDelegationGate confirms the reader-facing
// playground docs document neither the delegation-field visibility gate nor the
// effectiveScope mint field. The proposal's tier-11 step verified this so no
// doc-prose edit was required; if a later edit introduces either term to these
// pages without aligning them to the gate, this test flags the drift for review.
//
// diagnosis: a failure means a reader-facing playground page now mentions the
// delegation-field scope gate or effectiveScope; confirm the prose agrees with
// the §27.4/§27.3.1 spec wording before relaxing this guard, since the proposal
// reconciliation assumed these pages stay silent on the internal gate.
//
// spec: §27.4 (UI surface), §27.3.1 (mint response)
func TestPlaygroundDocsDoNotDocumentDelegationGate(t *testing.T) {
	root := repoRoot(t)

	docs := []string{
		filepath.Join("docs", "operator-guide", "web-playground.md"),
		filepath.Join("docs", "tutorials", "playground-tour.md"),
	}
	// Lower-cased substrings that would indicate the gate or the field is
	// documented in reader-facing prose.
	probes := []string{"effectivescope", "delegation-policy field", "if caller has the scope"}
	for _, rel := range docs {
		text := strings.ToLower(readRepoFile(t, root, rel))
		for _, probe := range probes {
			if strings.Contains(text, probe) {
				t.Errorf("%s now documents %q; the proposal-0015 reconciliation assumed reader-facing playground docs stay silent on the delegation visibility gate and effectiveScope. If the doc edit is intended, align it with the §27.4/§27.3.1 wording and update this guard.", rel, probe)
			}
		}
	}
}
