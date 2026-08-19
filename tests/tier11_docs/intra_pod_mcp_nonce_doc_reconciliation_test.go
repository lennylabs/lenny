// SPDX-License-Identifier: MIT

// Tier-11 documentation check for the runtime-author statements of the
// intra-pod MCP nonce handshake.
//
// The adapter starts the platform MCP server and each connector MCP server at
// most once per pod. A server validates a presented nonce against the value the
// manifest carried at the start that bound that server, so a later session's
// manifest write does not re-arm a running server, and the server resolves the
// calling session at call time and refuses the call unless the pod's shared
// runtime process has been given exactly one session and that session is the
// caller.
//
// The retired claim is that the nonce scopes a connection to a session and is
// regenerated for each session, so that re-reading the manifest always yields
// the value a server admits. On a pod holding a second bound session the
// manifest's current nonce is exactly the value the already-armed server
// rejects, so a runtime author who implements the retired handshake writes a
// connection that fails and a troubleshooting reader is told to repeat the step
// that failed.
//
// The predicate is the runtime-author documentation alone. The specification
// sites that state the same handshake are reconciled against their reader-facing
// mirrors by the case that lands with the manifest's own restatement.
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

// runtimeAuthorNoncePages returns every runtime-author-guide page that states
// the intra-pod MCP nonce handshake, keyed by the label a failure reports. The
// retired scoping claim is swept across the whole of each page rather than a
// named section, because the claim survived twice in a troubleshooting table
// while the sections stating the handshake carried the current rule.
func runtimeAuthorNoncePages(t *testing.T, root string) map[string]string {
	t.Helper()
	pages := map[string]string{}
	for _, name := range []string{
		"platform-tools.md",
		"integration-levels.md",
		"testing.md",
		"local-development.md",
	} {
		pages["runtime-author-guide/"+name] = readDocPage(t, filepath.Join(root, "docs", "runtime-author-guide", name))
	}
	return pages
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
func TestRuntimeAuthorGuideStatesTheIntraPodMCPNonceAsPodWide(t *testing.T) {
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
		"scoped to the session",
	})

	for label, page := range runtimeAuthorNoncePages(t, root) {
		requireNoneContain(t, label, page, []string{
			"regenerated per session",
			"regenerated for each task execution",
		})
	}
}

// spec: 4.7, 15.4.3
// diagnosis: a runtime-author troubleshooting table still explains a rejected
//
//	MCP nonce as a stale manifest read and tells the author to re-read the
//	manifest. On a pod holding a co-tenant session the manifest's current nonce
//	is the value the already-armed server rejects, so the advice names the step
//	that produced the failure. A failure here means the troubleshooting tables
//	contradict the handshake the same guide states.
func TestRuntimeAuthorTroubleshootingExplainsARejectedNonceOnTheArmingValue(t *testing.T) {
	root := repoRoot(t)

	for label, page := range map[string]string{
		"testing.md":           readDocPage(t, filepath.Join(root, "docs", "runtime-author-guide", "testing.md")),
		"local-development.md": readDocPage(t, filepath.Join(root, "docs", "runtime-author-guide", "local-development.md")),
	} {
		row := lineContaining(page, "| MCP nonce rejected |")
		if row == "" {
			t.Fatalf("docs/runtime-author-guide/%s: the `MCP nonce rejected` troubleshooting row was not found (renamed or removed?)", label)
		}
		requireAllContain(t, label+" MCP nonce rejected row", row, []string{
			"pod-wide and started at most once per pod",
			"does not re-arm a running server",
		})
		requireNoneContain(t, label+" MCP nonce rejected row", row, []string{
			"stale manifest",
			"cached the manifest too early",
		})
	}
}
