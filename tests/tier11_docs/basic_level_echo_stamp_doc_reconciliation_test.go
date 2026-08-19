// SPDX-License-Identifier: MIT

// Tier-11 doc/spec-consistency checks for the Basic-level echo obligation and
// for the two surfaces the same specification change corrects: the
// gateway-to-adapter shutdown RPC name, and the scope of the per-slot cleanup.
//
// §4.6.1's population rule has the adapter address every session-scoped frame
// by the session it belongs to, on every pod, and §15.4.3 excepts that
// identifier from the envelope fields a Basic-level runtime may ignore: such a
// runtime echoes it on the frames it emits in response. Several reader-facing
// pages carry a complete hand-written Basic-level runtime that speaks the raw
// JSON Lines protocol with no SDK. A sample that emits an unaddressed
// `response` or `tool_call` teaches a loop whose frames the adapter rejects on
// a pod holding more than one slot, so each sample is held to the obligation
// here.
//
// §4.7 states one session-scoped `Shutdown` request under one name, and the
// per-slot cleanup runs at each session release on a pod of any concurrency and
// any recycle setting. The reader-facing mirrors of both statements are pinned
// below so a page cannot drift back to the retired RPC alias or re-scope the
// cleanup to concurrent pods.
//
// The tests read the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 4.1 (request message scope), 4.7 (runtime adapter RPCs), 5.2 (per-slot
// cleanup and whole-pod scrub), 15.1 (integration levels), 28.5.3 (intra-pod
// frame addressing)

package tier11_docs_test

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// basicLevelRuntimeSamplePages are the reader-facing pages that carry a
// complete hand-written runtime speaking the raw JSON Lines protocol. Each one
// is a starting point an author copies, so each one carries the echo
// obligation.
var basicLevelRuntimeSamplePages = []string{
	filepath.Join("docs", "runtime-author-guide", "echo-runtime.md"),
	filepath.Join("docs", "runtime-author-guide", "sdk-examples", "go.md"),
	filepath.Join("docs", "runtime-author-guide", "sdk-examples", "python.md"),
	filepath.Join("docs", "runtime-author-guide", "sdk-examples", "typescript.md"),
	filepath.Join("docs", "tutorials", "build-a-runtime.md"),
	filepath.Join("docs", "tutorials", "recursive-delegation.md"),
}

// sessionScopedEmission matches the construction of a `response` or a
// `tool_call` frame in any of the three sample languages: a Go struct literal
// (`Type: "response"`), a JSON object literal, or a TypeScript object literal.
// Both frame types are session-scoped, so both carry the per-session
// identifier.
var sessionScopedEmission = regexp.MustCompile(`(?i)"?type"?\s*[:=]\s*"(response|tool_call)"`)

// echoedIdentifier matches the per-session identifier in the wire spelling and
// in the member spelling each sample language uses for it.
var echoedIdentifier = regexp.MustCompile(`sessionId|SessionID|session_id`)

// spec: 4.6.1, 15.1, 28.5.3
// diagnosis: a reader-facing page presents a hand-written Basic-level runtime
//
//	that emits a `response` or a `tool_call` carrying no per-session
//	identifier. The adapter addresses every session-scoped frame by the session
//	it belongs to on every pod, and a runtime echoes that identifier on the
//	frames it emits in response. A runtime copied from an unaddressed sample
//	has its frames rejected on any pod holding more than one slot, and the
//	author has no way to learn that from the page they copied.
func TestBasicLevelRuntimeSamplesEchoTheSessionIdentifier(t *testing.T) {
	root := repoRoot(t)

	for _, rel := range basicLevelRuntimeSamplePages {
		page := filepath.Join(root, rel)
		blocks, err := extractFencedBlocks(page)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		emitting := 0
		for _, b := range blocks {
			if !sessionScopedEmission.MatchString(b.Body) {
				continue
			}
			emitting++
			if !echoedIdentifier.MatchString(b.Body) {
				t.Errorf("%s:%d: the code block emits a session-scoped frame (`response` or `tool_call`) and names no per-session identifier; a Basic-level runtime echoes the identifier the adapter handed it", rel, b.StartLine)
			}
		}
		if emitting == 0 {
			t.Errorf("%s: no code block emits a `response` or a `tool_call` (page restructured?); the echo obligation is no longer held on this page", rel)
		}
	}
}

// spec: 4.1, 4.7
// diagnosis: docs/reference/adapter-contract.md still names the
//
//	gateway-to-adapter teardown RPC by the retired `Terminate` alias, or its
//	`Shutdown` row no longer states the per-session teardown and the recycle
//	disposition it carries beside it. The protocol declares one session-scoped
//	request under one name, and the recycle disposition rides beside the
//	teardown rather than selecting a scope. A reader-facing page that names an
//	RPC the protocol does not declare sends a runtime author looking for a
//	method that does not exist.
func TestAdapterContractNamesTheShutdownRPCUnderItsWireName(t *testing.T) {
	root := repoRoot(t)
	page := adapterContractDoc(t, root)

	row := lineContaining(page, "| `Shutdown` |")
	if row == "" {
		t.Fatal("docs/reference/adapter-contract.md: no Gateway-to-Adapter row names the `Shutdown` RPC (renamed or removed?)")
	}
	requireAllContain(t, "adapter-contract.md Shutdown row", row, []string{
		"end-of-session teardown",
		"recycle disposition",
		"ReportSessionScrub",
		"ReportPodScrub",
	})
	if strings.Contains(page, "| `Terminate` |") {
		t.Error("docs/reference/adapter-contract.md still carries a `Terminate` RPC row; the gateway-to-adapter teardown request is declared under the single name `Shutdown`")
	}
}

// perSlotCleanupTables are the residual-state tables that answer what a pod
// carries between sessions. Each states the scrub that runs at a session
// release for every configuration it lists.
var perSlotCleanupTables = []struct {
	page string
	rows []string
}{
	{
		page: filepath.Join("docs", "reference", "execution-modes.md"),
		rows: []string{"| One session per pod |", "| Pod reuse |", "| Concurrent (`maxConcurrentSessions > 1`) |"},
	},
	{
		page: filepath.Join("docs", "operator-guide", "multi-tenancy.md"),
		rows: []string{"| One session per pod (", "| Pod reuse (", "| Concurrent (`maxConcurrentSessions > 1`) |"},
	},
}

// spec: 4.7, 5.2
// diagnosis: a residual-state table scopes the per-slot cleanup to the
//
//	concurrent configuration alone. The per-slot cleanup runs at each session
//	release in session mode, on a pod of any concurrency and any recycle
//	setting, and the adapter reports its outcome to the gateway. A table that
//	states it on the concurrent row alone tells an operator that a
//	single-session pod releases without one, which is the question the table
//	exists to answer.
func TestPerSlotCleanupStatedOnEverySessionModeRow(t *testing.T) {
	root := repoRoot(t)

	for _, table := range perSlotCleanupTables {
		body := residualStateTable(t, table.page, readDocPage(t, filepath.Join(root, table.page)))
		for _, key := range table.rows {
			row := lineContaining(body, key)
			if row == "" {
				t.Fatalf("%s: no residual-state row matches %q (renamed or removed?)", table.page, key)
			}
			if !strings.Contains(row, "Per-slot cleanup") {
				t.Errorf("%s: the residual-state row %q does not state the per-slot cleanup; it runs at each session release on a pod of any concurrency and any recycle setting", table.page, key)
			}
		}
	}
}

// residualStateTable returns the residual-state table of a page, from its
// header row to the blank line that ends it. Both pages carry a presets table
// whose row keys repeat the configuration names, so an assertion scoped to the
// whole page reads the wrong row.
func residualStateTable(t *testing.T, label, page string) string {
	t.Helper()
	const header = "| Configuration | Scrub at session release"
	start := strings.Index(page, header)
	if start < 0 {
		t.Fatalf("%s: no residual-state table header found (renamed or removed?)", label)
	}
	body := page[start:]
	if end := strings.Index(body, "\n\n"); end > 0 {
		body = body[:end]
	}
	return body
}
