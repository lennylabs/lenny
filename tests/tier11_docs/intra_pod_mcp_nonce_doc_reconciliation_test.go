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
// The predicate covers the runtime-author documentation and, in the second
// case below, every specification statement of the handshake together with the
// adapter manifest's currency statement and its reader-facing mirror.
//
// This test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 4.7 (adapter manifest, MCP server security invariant), 15.4.3 (nonce
// handshake), 28.5.3 (intra-pod MCP channels)

package tier11_docs_test

import (
	"path/filepath"
	"regexp"
	"strings"
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

// noncePodWideRule, nonceArmingRule, and nonceNoReArmRule are the three
// statements that make the intra-pod MCP nonce handshake implementable. A
// server is pod-wide, it validates against the value the manifest carried at
// the start that bound it, and a later start does not re-arm it.
const (
	noncePodWideRule = "started at most once per pod"
	nonceArmingRule  = "carried at the start that bound"
	nonceNoReArmRule = "does not re-arm"
)

// retiredNoncePhrasings are the readings the pod-wide rule replaces: a nonce
// regenerated for each session, a nonce that scopes a connection to a session,
// and a server that admits the manifest's current value. Each one describes a
// handshake that fails on a pod holding a co-tenant session.
var retiredNoncePhrasings = []string{
	"regenerated per session",
	"regenerated for each session",
	"regenerated each session",
	"scoped to the session",
	"scopes the connection to a session",
	"scopes a connection to a session",
	"manifest's current",
	"current manifest",
}

// nonceStatementSite names one statement of the handshake: the file it lives
// in, an anchor unique to it, and the rules it has to state.
type nonceStatementSite struct {
	label  string
	path   []string
	anchor string
	want   []string
}

// nonceStatementBlock returns the markdown block that carries anchor, with its
// wrapped lines joined into one string. A block is a table row, a list item, or
// a paragraph: the spec wraps a single sentence across several lines, so a
// line-scoped match would miss a rule split at a line break.
func nonceStatementBlock(t *testing.T, label, body, anchor string) string {
	t.Helper()
	lines := strings.Split(body, "\n")
	idx := -1
	for i, ln := range lines {
		if strings.Contains(ln, anchor) {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatalf("%s: no line carries %q (rewritten or removed?)", label, anchor)
	}
	start := idx
	for start > 0 && !nonceBlockStart(lines[start]) && strings.TrimSpace(lines[start-1]) != "" {
		start--
	}
	end := idx + 1
	for end < len(lines) && strings.TrimSpace(lines[end]) != "" && !nonceBlockStart(lines[end]) {
		end++
	}
	return strings.Join(strings.Fields(strings.Join(lines[start:end], " ")), " ")
}

// nonceBlockOpener matches the opening line of a numbered specification step,
// including the lettered form the §29.4 sequence uses.
var nonceBlockOpener = regexp.MustCompile(`^\d+[a-z]?\.\s`)

// nonceBlockStart reports whether a line opens a new block rather than
// continuing the one above it.
func nonceBlockStart(line string) bool {
	trimmed := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(trimmed, "- "), strings.HasPrefix(trimmed, "|"), strings.HasPrefix(trimmed, "#"):
		return true
	default:
		return nonceBlockOpener.MatchString(trimmed)
	}
}

// intraPodNonceSites returns every statement of the intra-pod MCP nonce
// handshake the pod-wide rule reaches, in the specification and in the
// reader-facing mirror of the adapter manifest. A site left behind states a
// handshake that contradicts the ones beside it.
func intraPodNonceSites() []nonceStatementSite {
	spec15 := []string{"spec", "15_external-api-surface.md"}
	spec28 := []string{"spec", "28_communication-channels.md"}
	spec04 := []string{"spec", "04_system-components.md"}
	spec29 := []string{"spec", "29_communication-scenarios.md"}
	return []nonceStatementSite{
		{
			label:  "spec/15 §15.4.3 Authentication lead",
			path:   spec15,
			anchor: "**Authentication.** Intra-pod MCP connections require a manifest-nonce handshake",
			want:   []string{noncePodWideRule, nonceArmingRule, nonceNoReArmRule},
		},
		{
			label:  "spec/15 §15.4.3 nonce validation sentence",
			path:   spec15,
			anchor: "The adapter validates the `_lennyNonce` value before processing any tool dispatch",
			want:   []string{nonceArmingRule, "rather than against that field's current value"},
		},
		{
			label:  "spec/28 CH-MCP-PLATFORM Endpoint bullet",
			path:   spec28,
			anchor: "**Endpoint.** An abstract Unix socket whose name the adapter manifest advertises under",
			want:   []string{nonceArmingRule, nonceNoReArmRule},
		},
		{
			label:  "spec/28 CH-MCP-PLATFORM Exclusivity bullet",
			path:   spec28,
			anchor: "The manifest nonce authenticates a connection to the pod's intra-pod MCP servers, which",
			want:   []string{noncePodWideRule, nonceArmingRule, nonceNoReArmRule},
		},
		{
			label:  "spec/28 CH-MCP-CONNECTOR Exclusivity bullet",
			path:   spec28,
			anchor: "The same manifest nonce that authenticates a connection to `CH-MCP-PLATFORM`\n  authenticates a connection here",
			want:   []string{noncePodWideRule, nonceArmingRule},
		},
		{
			label:  "spec/28 §28.6 MCP scoping clause",
			path:   spec28,
			anchor: "On `CH-MCP-PLATFORM` and `CH-MCP-CONNECTOR` the manifest nonce",
			want:   []string{noncePodWideRule, nonceArmingRule, nonceNoReArmRule},
		},
		{
			label:  "spec/28 §28.8 CH-MCP-PLATFORM row",
			path:   spec28,
			anchor: "| `CH-MCP-PLATFORM` | When the runtime is Basic-level",
			want:   []string{noncePodWideRule, nonceArmingRule, nonceNoReArmRule},
		},
		{
			label:  "spec/28 §28.8 CH-MCP-CONNECTOR row",
			path:   spec28,
			anchor: "| `CH-MCP-CONNECTOR` | When the runtime is Basic-level",
			want:   []string{noncePodWideRule},
		},
		{
			label:  "spec/04 §4.7.5 `mcpNonce` manifest field row",
			path:   spec04,
			anchor: "| `mcpNonce`",
			want:   []string{noncePodWideRule, nonceArmingRule, nonceNoReArmRule},
		},
		{
			label:  "spec/04 §4.7 MCP server security invariant",
			path:   spec04,
			anchor: "**MCP server security:**",
			want:   []string{noncePodWideRule, nonceArmingRule, nonceNoReArmRule},
		},
		{
			label:  "spec/29 §29.4 platform MCP connect step",
			path:   spec29,
			anchor: "The runtime reads the manifest and connects to the platform MCP",
			want:   []string{noncePodWideRule, nonceArmingRule, nonceNoReArmRule},
		},
		{
			label:  "spec/04 §4.7.5 adapter manifest lead",
			path:   spec04,
			anchor: "**Adapter manifest:** One pod-global file written to",
			want: []string{
				"One pod-global file",
				"authoritative for the session whose start last wrote it",
			},
		},
		{
			label:  "docs/reference/adapter-contract.md Adapter Manifest lead",
			path:   []string{"docs", "reference", "adapter-contract.md"},
			anchor: "The adapter writes `/run/lenny/adapter-manifest.json` before spawning your binary.",
			want: []string{
				"one pod-global file",
				"authoritative for the session whose start last wrote it",
			},
		},
	}
}

// spec: 4.7, 4.7.5, 15.4.3, 28.5.3, 28.6, 29.4
// diagnosis: one statement of the intra-pod MCP nonce handshake, or of the
//
//	adapter manifest's currency, disagrees with the others. The intra-pod MCP
//	servers are pod-wide and started at most once per pod, a server validates
//	against the nonce the manifest carried at the start that bound it, and a
//	later session's manifest write does not re-arm a running server. A site
//	that still describes a nonce regenerated per session, scoped to a
//	connection, or validated against the manifest's current value tells a
//	runtime author to implement a handshake that fails on a pod holding a
//	co-tenant session, and contradicts the sites that cite it.
func TestIntraPodMCPNonceStatementsAgreeAcrossSpecAndDocs(t *testing.T) {
	root := repoRoot(t)

	files := map[string]string{}
	for _, site := range intraPodNonceSites() {
		key := filepath.Join(site.path...)
		if _, ok := files[key]; !ok {
			files[key] = readRepoFile(t, root, site.path...)
		}
		anchor := strings.Join(strings.Fields(site.anchor), " ")
		block := nonceStatementBlock(t, site.label, files[key], strings.Split(site.anchor, "\n")[0])
		if !strings.Contains(block, anchor) {
			t.Fatalf("%s: the anchoring sentence was not found in one block (rewritten or removed?)", site.label)
		}
		requireAllContain(t, site.label, block, site.want)
		requireNoneContain(t, site.label, block, retiredNoncePhrasings)
	}
}
