// SPDX-License-Identifier: MIT

// Tier-11 documentation check for what a manifest rewrite changes.
//
// The adapter writes the one pod-global manifest before each session's runtime
// start. On a pod holding more than one bound session the shared runtime
// process is started at most once, so a co-tenant session's start rewrites the
// file without spawning anything. The manifest carries only pod-scoped fields,
// and each session's own context reaches the runtime in that session's
// `session_start` frame, so the rewrite replaces only the pod-scoped
// `mcpNonce` while an earlier session's runtime is still processing.
//
// Two readings are retired. One keys the rewrite to a per-session binary
// spawn, a trigger that is false in exactly the case the collision arises. The
// other names the session's identifier and credential path among the members a
// later start replaces, which sends a runtime author to the manifest for
// context the manifest no longer carries.
//
// This test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 4.7.5 (adapter manifest), 28.5.3 (CH-MSGSOCK session_start frame),
// 6.4 (concurrent session slots)

package tier11_docs_test

import (
	"testing"
)

// spec: 4.7.5, 28.5.3, 6.4
// diagnosis: the reader-facing manifest lead in
//
//	docs/reference/adapter-contract.md keys the rewrite to a binary spawn, or
//	names per-session context among the members a later start replaces. One
//	runtime process serves every slot on the pod, so a co-tenant session's
//	start rewrites the manifest with no spawn, and a session's identifier and
//	credential path travel in its own `session_start` frame. A page that says
//	otherwise tells a co-tenanted runtime author to read its session's context
//	from a file that names no session.
func TestAdapterManifestLeadKeysTheRewriteToTheRuntimeStart(t *testing.T) {
	root := repoRoot(t)

	contract := adapterContractDoc(t, root)
	manifest := section(contract, "Adapter Manifest")
	if manifest == "" {
		t.Fatal("docs/reference/adapter-contract.md: `Adapter Manifest` section not found (renamed or removed?)")
	}

	requireAllContain(t, "adapter-contract.md Adapter Manifest lead", manifest, []string{
		"The adapter rewrites it before each session's runtime start, including each session on a recycling pod",
		"reach your runtime in that session's `session_start` frame, so a later start's rewrite changes none of them for an earlier session",
		"a later session's start replaces the `mcpNonce` member while an earlier session's runtime is still processing",
	})
	requireNoneContainFold(t, "adapter-contract.md Adapter Manifest lead", manifest, []string{
		"before each session's binary is spawned",
		"while an earlier session's binary is still running",
		"replaces the `sessionId`",
		"`credentialsPath` members",
		"authoritative for the session whose start last wrote it",
	})
}
