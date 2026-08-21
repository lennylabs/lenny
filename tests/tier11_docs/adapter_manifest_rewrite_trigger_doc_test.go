// SPDX-License-Identifier: MIT

// Tier-11 documentation check for the event that triggers a manifest rewrite.
//
// The adapter writes the one pod-global manifest before each session's runtime
// start. On a pod holding more than one bound session the shared runtime
// process is started at most once, so a co-tenant session's start rewrites the
// file without spawning anything. A page that keys the rewrite to a per-session
// binary spawn states the co-tenant collision with a trigger that is false in
// exactly the case the collision arises, and a runtime author concludes that
// the file an already-running process reads is stable for its own session.
//
// This test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 4.7 (adapter manifest currency), 6.4 (concurrent session slots)

package tier11_docs_test

import (
	"testing"
)

// spec: 4.7, 6.4
// diagnosis: the reader-facing manifest lead in
//
//	docs/reference/adapter-contract.md keys the per-session rewrite to a binary
//	spawn. One runtime process serves every slot on the pod, so a co-tenant
//	session's start rewrites the manifest with no spawn, and the page tells a
//	co-tenanted runtime author that the file its running process read is still
//	its own session's.
func TestAdapterManifestLeadKeysTheRewriteToTheRuntimeStart(t *testing.T) {
	root := repoRoot(t)

	contract := adapterContractDoc(t, root)
	manifest := section(contract, "Adapter Manifest")
	if manifest == "" {
		t.Fatal("docs/reference/adapter-contract.md: `Adapter Manifest` section not found (renamed or removed?)")
	}

	requireAllContain(t, "adapter-contract.md Adapter Manifest lead", manifest, []string{
		"the adapter rewrites it before each session's runtime start, including each session on a recycling pod",
		"a later session's start replaces the `sessionId`, `mcpNonce`, and `credentialsPath` members while an earlier session's runtime is still processing",
	})
	requireNoneContain(t, "adapter-contract.md Adapter Manifest lead", manifest, []string{
		"before each session's binary is spawned",
		"while an earlier session's binary is still running",
	})
}
