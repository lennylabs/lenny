// SPDX-License-Identifier: MIT

// Tier-11 doc/spec-consistency check for the reader-facing statements of the
// intra-pod MCP nonce handshake and of the adapter manifest's currency.
//
// The adapter starts the platform MCP server and each connector MCP server at
// most once per pod, and it writes one pod-global manifest before each session's
// runtime start. Two consequences reach a runtime author. A server validates a
// presented nonce against the value the manifest carried at the start that bound
// that server, so a later session's manifest write does not re-arm a running
// server, and the server resolves the calling session at call time and refuses
// the call unless the pod's shared runtime process has been given exactly one
// session and that session is the caller. The manifest itself is authoritative
// for the session whose start last wrote it, and on a pod holding more than one
// bound session a later start replaces its `sessionId`, `mcpNonce`, and
// `credentialsPath` while an earlier session's runtime is still processing.
//
// The retired claims are the per-session and per-connection scoping of the
// nonce, validation against the manifest's current value, and the manifest being
// stable for a single session or current for the session whose runtime is
// reading it. A runtime author who implements the retired handshake writes a
// connection the already-armed server rejects, and one who trusts the retired
// currency claim never re-reads a file a co-tenant start has replaced.
//
// This test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 4.7 (adapter manifest, MCP server security invariant), 15.4.3 (nonce
// handshake), 28.5.3 (intra-pod MCP channels)

package tier11_docs_test

import (
	"path/filepath"
	"testing"
)

// integrationLevelsDoc reads docs/runtime-author-guide/integration-levels.md
// into a string.
func integrationLevelsDoc(t *testing.T, root string) string {
	t.Helper()
	return readDocPage(t, filepath.Join(root, "docs", "runtime-author-guide", "integration-levels.md"))
}

// spec: 4.7, 15.4.3, 28.5.3
// diagnosis: a runtime-author page still describes the intra-pod MCP nonce as
//
//	scoping a connection to a session, or tells the author to validate against
//	the manifest's current `mcpNonce`. The intra-pod MCP servers are pod-wide
//	and started at most once per pod, so on a pod holding a second bound
//	session the manifest's current nonce is exactly the value the already-armed
//	server rejects. An author following the retired text implements a handshake
//	that fails, and never learns that the server resolves the calling session at
//	call time and refuses the call unless the pod's shared runtime process holds
//	exactly one session and that session is the caller.
func TestIntraPodMCPNonceScopingDocumentedOnThePodWideSurface(t *testing.T) {
	root := repoRoot(t)

	tools := platformToolsDoc(t, root)
	setup := section(tools, "Connection Setup")
	if setup == "" {
		t.Fatal("docs/runtime-author-guide/platform-tools.md: `Connection Setup` section not found (renamed or removed?)")
	}
	requireAllContain(t, "platform-tools.md Connection Setup section", setup, []string{
		"pod-wide and started at most once per pod",
		"the nonce the manifest carried at the start that bound the server",
		"A later session's manifest write does not re-arm a running server.",
		"resolves the calling session at call time",
		"refuses the call unless the pod's shared runtime process has been given exactly one session and that session is the caller",
	})
	requireNoneContain(t, "platform-tools.md Connection Setup section", setup, []string{
		"regenerated for each task execution",
		"regenerated per session",
		"validates `_lennyNonce` against `mcpNonce`",
	})

	levels := integrationLevelsDoc(t, root)
	standard := section(levels, "Standard")
	if standard == "" {
		t.Fatal("docs/runtime-author-guide/integration-levels.md: `Standard` section not found (renamed or removed?)")
	}
	requireAllContain(t, "integration-levels.md Standard section", standard, []string{
		"pod-wide and started at most once per pod",
		"the nonce a server validates against is the one the manifest carried at the start that bound it",
		"a later session's manifest write does not re-arm a running server",
		"resolves the calling session at call time",
		"refuses the call unless the pod's shared runtime process has been given exactly one session and that session is the caller",
	})
	requireNoneContain(t, "integration-levels.md Standard section", standard, []string{
		"regenerated per session",
		"scoped to the session",
	})
}

// spec: 4.7
// diagnosis: docs/reference/adapter-contract.md still tells a runtime author
//
//	that the adapter manifest is regenerated per session and current for the
//	session whose runtime is reading it. The manifest is one pod-global file the
//	adapter writes before each session's runtime start, so on a pod holding more
//	than one bound session a later start replaces its `sessionId`, `mcpNonce`,
//	and `credentialsPath` while an earlier session's runtime is still
//	processing. An author who trusts the retired currency claim reads the file
//	once at startup and treats stale values as its own for the whole session,
//	which is the collision the pod-global manifest produces.
func TestAdapterManifestCurrencyDocumentedOnThePodGlobalSurface(t *testing.T) {
	root := repoRoot(t)

	manifest := section(adapterContractDoc(t, root), "Adapter Manifest")
	if manifest == "" {
		t.Fatal("docs/reference/adapter-contract.md: `Adapter Manifest` section not found (renamed or removed?)")
	}
	requireAllContain(t, "adapter-contract.md Adapter Manifest section", manifest, []string{
		"one pod-global file",
		"before each session's runtime start",
		"authoritative for the session whose start last wrote it",
		"On a pod holding more than one bound session, a later start replaces the manifest's `sessionId`, `mcpNonce`, and `credentialsPath` while an earlier session's runtime is still processing.",
	})
	requireNoneContain(t, "adapter-contract.md Adapter Manifest section", manifest, []string{
		"regenerated per session",
		"stable for the duration of a single session",
		"always current for its session",
	})
}
