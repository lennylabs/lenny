// SPDX-License-Identifier: MIT

// Tier-11 doc/spec-consistency checks for the Basic-level echo obligation and
// for the two surfaces the same specification change corrects: the
// gateway-to-adapter shutdown RPC name, and the scope of the per-slot cleanup.
//
// §28.5.3's addressing rule has the adapter address every session-scoped frame
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
// cleanup and whole-pod scrub), 15.4.3 (runtime integration levels), 28.5.3
// (intra-pod frame addressing)

package tier11_docs_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
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

// frameAddressKeys is the set of spellings the per-session frame address key
// is published under across the tree. The JSON Lines schema and the
// reader-facing pages take the rename to the session spelling in separate
// changes, so a page carrying either spelling satisfies the assertions below.
// What they hold is that the address is present and carries a value the schema
// accepts, which is the obligation that stands whatever the key is called.
var frameAddressKeys = []string{"slotId", "sessionId"}

// wireSpelling returns the address key as it appears on the wire.
func wireSpelling(key string) string { return key }

// goFieldSpelling returns the exported Go member a sample names the address
// key with. `slotId` gives `SlotID`.
func goFieldSpelling(key string) string {
	stem := strings.TrimSuffix(key, "Id")
	return strings.ToUpper(stem[:1]) + stem[1:] + "ID"
}

// snakeSpelling returns the snake-case member a Python sample names the
// address key with. `slotId` gives `slot_id`.
func snakeSpelling(key string) string { return strings.TrimSuffix(key, "Id") + "_id" }

// stemSpelling returns the address key without its identifier suffix, which is
// the fragment a local variable or parameter name is built around.
func stemSpelling(key string) string { return strings.TrimSuffix(key, "Id") }

// addressSpellings applies a spelling function to every published address key.
func addressSpellings(spell func(string) string) []string {
	out := make([]string, 0, len(frameAddressKeys))
	for _, key := range frameAddressKeys {
		out = append(out, spell(key))
	}
	return out
}

// addressAlternation returns a regexp alternation over one spelling of every
// published address key, each quoted so it matches literally.
func addressAlternation(spell func(string) string) string {
	quoted := make([]string, 0, len(frameAddressKeys))
	for _, s := range addressSpellings(spell) {
		quoted = append(quoted, regexp.QuoteMeta(s))
	}
	return strings.Join(quoted, "|")
}

// frameAddressValue matches the per-session address key bound to a non-empty
// JSON string, which is the only form the published JSON Lines schema accepts.
// The gates below match the address by value rather than by key spelling
// because a literal that prints the key bound to `null` states the opposite of
// the rule it would otherwise satisfy: the adapter reads a null address as
// untagged.
var frameAddressValue = regexp.MustCompile(`"(` + addressAlternation(wireSpelling) + `)"\s*:\s*"[^"]+"`)

// frameAddressKeyBound matches the per-session address key bound to any value,
// so a literal that spells the key with a type the schema rejects is reported
// against the schema's string requirement rather than as a missing address.
var frameAddressKeyBound = regexp.MustCompile(`"?(` + addressAlternation(wireSpelling) + `)"?\s*[:=]`)

// echoedIdentifier matches the per-session identifier in its wire spelling and
// in the member spelling each sample language gives it.
var echoedIdentifier = regexp.MustCompile(
	addressAlternation(wireSpelling) + "|" +
		addressAlternation(goFieldSpelling) + "|" +
		addressAlternation(snakeSpelling),
)

// checkFrameAddress reports a session-scoped frame literal that does not carry
// the per-session address in the form the published JSON Lines schema accepts.
// The two dispositions are reported apart, because a key the literal omits and
// a key bound to a value the schema rejects are different edits to the page.
//
// spec: 4.1 (request message scope), 28.5.3
func checkFrameAddress(t *testing.T, at, literal string) {
	t.Helper()
	trimmed := strings.TrimSpace(literal)
	if frameAddressValue.MatchString(literal) {
		return
	}
	if frameAddressKeyBound.MatchString(literal) {
		t.Errorf("%s: this frame literal binds the per-session address to a value the published JSON Lines schema rejects; the schema accepts only a non-empty JSON string there and the adapter reads a null address as untagged\n%s", at, trimmed)
		return
	}
	t.Errorf("%s: this frame literal carries no per-session address; every session-scoped frame is addressed on every pod\n%s", at, trimmed)
}

// spec: 15.4.3, 28.5.3
// diagnosis: the frame-address matchers above resolve the per-session address
//
//	by one key spelling and reject the other. The schema, the SDKs, and the
//	reader-facing pages take the rename to the session spelling in separate
//	changes, so during that sequence the tree carries both spellings at once. A
//	matcher bound to a single spelling reports every page still carrying the
//	other as unaddressed, which turns the echo obligation into a rename tracker
//	and hides the defect it exists to catch. The obligation is that the address
//	is present and carries a value the published JSON Lines schema accepts.
func TestFrameAddressMatchersAcceptEitherPublishedKeySpelling(t *testing.T) {
	if len(frameAddressKeys) < 2 {
		t.Fatalf("frameAddressKeys names %d spelling(s); the matchers are written to span the rename", len(frameAddressKeys))
	}
	for _, key := range frameAddressKeys {
		addressed := fmt.Sprintf(`{"type": "response", %q: "sess_abc", "text": "hi"}`, key)
		if !frameAddressValue.MatchString(addressed) {
			t.Errorf("an addressed frame spelling the key %q is read as unaddressed: %s", key, addressed)
		}
		if !echoedIdentifier.MatchString(addressed) {
			t.Errorf("an emission naming the identifier under the key %q is read as naming none: %s", key, addressed)
		}
		for _, member := range []string{goFieldSpelling(key), snakeSpelling(key)} {
			if !echoedIdentifier.MatchString(member) {
				t.Errorf("the member spelling %q of the key %q is read as naming no identifier", member, key)
			}
		}
		null := fmt.Sprintf(`{"type": "response", %q: null}`, key)
		if frameAddressValue.MatchString(null) {
			t.Errorf("a null address spelling the key %q is read as addressed; the schema accepts only a non-empty string there: %s", key, null)
		}
		if !frameAddressKeyBound.MatchString(null) {
			t.Errorf("a null address spelling the key %q is read as omitting the key; the two dispositions are reported apart: %s", key, null)
		}
	}
	for _, sample := range addressOmittedWhenAbsent() {
		if len(sample.constructs) != len(frameAddressKeys) {
			t.Errorf("%s: the omit-when-absent obligation is stated for %d of the %d published key spellings", sample.page, len(sample.constructs), len(frameAddressKeys))
		}
	}
}

// spec: 15.4.3, 28.5.3
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
		// The annotated protocol traces are written as fences with no
		// language tag, and they emit the same session-scoped frames the
		// source blocks do, so they are read here too.
		blocks, err := extractFencedBlocksIncluding(page, true)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		emitting := 0
		for _, b := range blocks {
			for _, e := range sessionScopedEmissions(b.Body) {
				emitting++
				if echoedIdentifier.MatchString(e.Text) {
					continue
				}
				t.Errorf("%s:%d: this `response` or `tool_call` construction names no per-session identifier; a Basic-level runtime echoes the identifier the adapter handed it on every session-scoped frame it emits\n%s", rel, b.StartLine+e.Offset, e.Text)
			}
		}
		if emitting == 0 {
			t.Errorf("%s: no code block emits a `response` or a `tool_call` (page restructured?); the echo obligation is no longer held on this page", rel)
		}
	}
}

// emission is one `response` or `tool_call` construction inside a code block,
// with its offset in lines from the block's fence.
type emission struct {
	Offset int
	Text   string
}

// sessionScopedEmissions returns one entry per `response` or `tool_call`
// construction in a code block, each carrying the extent of that construction
// alone. Scanning the whole block instead would let one addressed frame
// satisfy the assertion for every unaddressed frame beside it.
func sessionScopedEmissions(body string) []emission {
	lines := strings.Split(body, "\n")
	var out []emission
	for i, line := range lines {
		if !sessionScopedEmission.MatchString(line) {
			continue
		}
		start := i
		if i > 0 && opensConstruction(lines[i-1]) {
			// The construction's own opener sits on the line above, which is
			// where a call that stamps the identifier is written.
			start = i - 1
		}
		end := constructionEnd(lines, start)
		out = append(out, emission{Offset: start + 1, Text: strings.Join(lines[start:end+1], "\n")})
	}
	return out
}

// opensConstruction reports whether a line ends by opening a composite value,
// which makes the line below it a continuation of the same construction.
func opensConstruction(line string) bool {
	trimmed := strings.TrimRight(line, " \t")
	return strings.HasSuffix(trimmed, "{") || strings.HasSuffix(trimmed, "(")
}

// constructionEnd returns the index of the line on which the construction
// opened at or after `start` closes, tracking brace and parenthesis depth. A
// construction written on one line ends on that line.
func constructionEnd(lines []string, start int) int {
	depth := 0
	opened := false
	for i := start; i < len(lines); i++ {
		for _, r := range lines[i] {
			switch r {
			case '{', '(', '[':
				depth++
				opened = true
			case '}', ')', ']':
				depth--
			}
		}
		if opened && depth <= 0 {
			return i
		}
	}
	return len(lines) - 1
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

// addressOmittedWhenAbsent records, per sample page, the constructs that keep
// the per-session address off a frame when the inbound envelope carried none.
// A frame carrying the key bound to a null value fails the published JSON Lines
// schema, which accepts a string there; a frame that omits the key resolves to
// the binding of the stream that delivered it on a pod holding at most one
// slot. Each page carries one construct per published spelling of the key, and
// any one of them discharges the obligation, so the assertion travels with the
// key rename.
func addressOmittedWhenAbsent() []struct {
	page       string
	constructs []string
} {
	goTag := func(key string) string { return fmt.Sprintf("`json:%q`", key+",omitempty") }
	pythonGuard := func(key string) string { return fmt.Sprintf("if %s is not None:", snakeSpelling(key)) }
	typescriptMember := func(key string) string { return fmt.Sprintf("%s?: string;", key) }
	return []struct {
		page       string
		constructs []string
	}{
		{filepath.Join("docs", "runtime-author-guide", "echo-runtime.md"), addressSpellings(goTag)},
		{filepath.Join("docs", "runtime-author-guide", "sdk-examples", "go.md"), addressSpellings(goTag)},
		{filepath.Join("docs", "runtime-author-guide", "sdk-examples", "python.md"), addressSpellings(pythonGuard)},
		{filepath.Join("docs", "runtime-author-guide", "sdk-examples", "typescript.md"), addressSpellings(typescriptMember)},
		{filepath.Join("docs", "tutorials", "build-a-runtime.md"), addressSpellings(goTag)},
		{filepath.Join("docs", "tutorials", "recursive-delegation.md"), addressSpellings(goTag)},
	}
}

// carriesAnyConstruct reports whether the page carries at least one of the
// constructs.
func carriesAnyConstruct(page string, constructs []string) bool {
	for _, c := range constructs {
		if strings.Contains(page, c) {
			return true
		}
	}
	return false
}

// spec: 15.4.3, 28.5.3
// diagnosis: a reader-facing runtime sample writes the per-session address
//
//	unconditionally, so a frame emitted before the runtime has read an inbound
//	envelope, or in response to one that carried no address, goes out with the
//	key bound to a null value. The published JSON Lines schema accepts a string
//	there, so such a frame is rejected outright, while a frame that omits the
//	key resolves against the stream's own binding on a pod holding at most one
//	slot. The sample teaches the rejected form.
func TestBasicLevelRuntimeSamplesOmitTheAddressWhenAbsent(t *testing.T) {
	root := repoRoot(t)

	for _, sample := range addressOmittedWhenAbsent() {
		page := readDocPage(t, filepath.Join(root, sample.page))
		if !carriesAnyConstruct(page, sample.constructs) {
			t.Errorf("%s: the sample does not keep the per-session address off a frame that has none (expected one of %q); a null address fails the published JSON Lines schema", sample.page, sample.constructs)
		}
	}
}

// spec: 15.4.3, 28.5.3
// diagnosis: docs/reference/adapter-contract.md grants a Basic-level runtime
//
//	an unqualified permission to ignore every envelope field outside `type`,
//	`id`, and `input`. The adapter addresses every session-scoped frame by the
//	session it belongs to on every pod, so the address is excepted from that
//	permission and the runtime echoes it on the frames it emits in response. An
//	unqualified permission tells the author to discard the field the same page's
//	field table says the adapter populates two lines above it.
func TestAdapterContractExceptsTheAddressFromTheBasicLevelIgnorePermission(t *testing.T) {
	root := repoRoot(t)
	page := adapterContractDoc(t, root)

	line := lineContaining(page, "**Basic-level runtimes:**")
	if line == "" {
		t.Fatal("docs/reference/adapter-contract.md: no `message` field table states what a Basic-level runtime reads (renamed or removed?)")
	}
	if !carriesAnyConstruct(line, addressSpellings(wireSpelling)) {
		t.Errorf("docs/reference/adapter-contract.md: the Basic-level permission names no per-session address field: %q", line)
	}
	requireAllContain(t, "adapter-contract.md Basic-level permission", line, []string{
		"echoes it",
	})
	if strings.Contains(line, "Ignore all other fields safely.") {
		t.Errorf("docs/reference/adapter-contract.md: the Basic-level permission is unqualified; the per-session address is excepted from it: %q", line)
	}
}

// singleLineFrameLiteral matches a session-scoped frame written as a complete
// one-line JSON object. The reference page spells its shorthand form, its
// empty-output form, and every line of its worked traces this way, inside a
// fence and in running prose alike. The multi-line canonical example of each
// frame is a separate family whose value correction lands with the rest of the
// example set.
var singleLineFrameLiteral = regexp.MustCompile(`\{[^{}]*"type"\s*:\s*"(message|tool_result|response|tool_call)"`)

// spec: 4.1 (request message scope), 15.4.3, 28.5.3
// diagnosis: a one-line `response`, `tool_call`, `message`, or `tool_result`
//
//	literal on docs/reference/adapter-contract.md carries no per-session
//	address. Every session-scoped frame is addressed on every pod, so the
//	page's shorthand form, its empty-output form, and its worked wire traces
//	spell the same field set its frame reference does. A literal an author can
//	copy in one line is the form most likely to be pasted straight into a
//	runtime, and an unaddressed one is rejected on any pod holding more than
//	one slot.
func TestAdapterContractSingleLineFrameLiteralsCarryTheAddress(t *testing.T) {
	root := repoRoot(t)
	rel := filepath.Join("docs", "reference", "adapter-contract.md")
	page := readDocPage(t, filepath.Join(root, rel))

	literals := 0
	for i, line := range strings.Split(page, "\n") {
		if !singleLineFrameLiteral.MatchString(line) {
			continue
		}
		literals++
		checkFrameAddress(t, fmt.Sprintf("%s:%d", rel, i+1), line)
	}
	if literals == 0 {
		t.Errorf("%s: no one-line session-scoped frame literal found (page restructured?); the addressing rule is no longer held on this page", rel)
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

// annotatedTracePages are the reader-facing pages that carry a worked
// step-by-step trace of the intra-pod protocol, written as a fenced block with
// no language tag. A trace states both halves of the conversation, so it is the
// one place a page can show the adapter handing a runtime an unaddressed frame
// while the same runtime emits an addressed one.
var annotatedTracePages = append(
	[]string{filepath.Join("docs", "reference", "adapter-contract.md")},
	basicLevelRuntimeSamplePages...,
)

// tracedSessionScopedFrame matches a JSON frame literal of any session-scoped
// type inside an annotated trace. The scan covers the adapter-written
// `message` and `tool_result` as well as the runtime-written `response` and
// `tool_call`, because the addressing rule binds the adapter's frames too.
var tracedSessionScopedFrame = regexp.MustCompile(`"type"\s*:\s*"(message|tool_result|response|tool_call)"`)

// spec: 15.4.3, 28.5.3
// diagnosis: an annotated protocol trace on a reader-facing page shows a
//
//	session-scoped frame with no per-session address. The adapter populates the
//	address on the frames it writes and the runtime echoes it on the frames it
//	writes, so every line of a trace spells it. A trace addressed on the runtime
//	half alone shows a runtime echoing an identifier the same trace never
//	delivered, which contradicts the sample source the page carries above it.
func TestAnnotatedTracesAddressBothHalvesOfTheConversation(t *testing.T) {
	root := repoRoot(t)

	traced := 0
	for _, rel := range annotatedTracePages {
		blocks, err := extractFencedBlocksIncluding(filepath.Join(root, rel), true)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		for _, b := range blocks {
			if b.Language != "" {
				continue
			}
			for i, line := range strings.Split(b.Body, "\n") {
				if !tracedSessionScopedFrame.MatchString(line) {
					continue
				}
				traced++
				checkFrameAddress(t, fmt.Sprintf("%s:%d", rel, b.StartLine+i+1), line)
			}
		}
	}
	if traced == 0 {
		t.Error("no annotated protocol trace carries a session-scoped frame (pages restructured?); the addressing rule is no longer held on the traces")
	}
}

// sampleSourceLanguages are the fence languages whose blocks carry a sample's
// executable source. The untagged fences on the same pages are annotated
// protocol traces, which have no functions and no variables, and the tagged
// non-source fences are shell transcripts.
var sampleSourceLanguages = map[string]bool{"go": true, "python": true, "typescript": true, "ts": true}

// sampleFunctionHeader matches a top-level function declaration in any of the
// three sample languages. The header is where a sample states which values the
// function is handed, so it is where the per-session address has to appear when
// the emission inside does not read it from an inbound envelope.
var sampleFunctionHeader = regexp.MustCompile(`^(func|def|function|async function) \w+\(`)

// inboundEnvelopeReference matches a read of the inbound frame the emission is
// answering, in the spelling all six samples use for it.
var inboundEnvelopeReference = regexp.MustCompile(`\bmsg\b`)

// frameTypeDeclaration matches the opener of a type declaration whose literal
// `type` member names a session-scoped frame. A declaration states the field
// set rather than emitting a frame, so it carries no address of its own.
var frameTypeDeclaration = regexp.MustCompile(`^\s*(interface|type|class)\s+\w+`)

// addressStems is a regexp alternation over the address key's stem under every
// published spelling, so both matchers below travel with the key rename.
var addressStems = "(?:" + addressAlternation(stemSpelling) + ")"

// addressHoldingVariable matches the declaration of a variable that holds the
// per-session address across frames, in the three sample languages.
var addressHoldingVariable = regexp.MustCompile(`(?i)^(var|let|const)\s+(\w*` + addressStems + `\w*id\w*)\b|^(\w*` + addressStems + `\w*id\w*)\s*=`)

// addressParameter matches the per-session address appearing in a function
// header's parameter list, under the local spelling any of the three languages
// gives it.
var addressParameter = regexp.MustCompile(`(?i)\(.*\b\w*` + addressStems + `_?id\b`)

// spec: 4.1 (request message scope), 15.4.3, 28.5.3
// diagnosis: a hand-written Basic-level sample stamps the per-session address
//
//	from a variable that outlives the frame it was read from, rather than from
//	the inbound envelope the emission answers. On a pod holding more than one
//	slot the adapter multiplexes every session's stream over the one channel, so
//	a second session's `message` overwrites such a variable while the first
//	session's tool-call round trip is still outstanding and the continuation
//	frames go out addressed to the wrong session. The adapter populates the
//	address on the `tool_result` too, so the value each emission needs is
//	already in scope on the frame being answered.
func TestBasicLevelRuntimeSamplesStampTheAddressFromTheFrameTheyAnswer(t *testing.T) {
	root := repoRoot(t)

	checked := 0
	for _, rel := range basicLevelRuntimeSamplePages {
		blocks, err := extractFencedBlocksIncluding(filepath.Join(root, rel), true)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		for _, b := range blocks {
			if !sampleSourceLanguages[strings.ToLower(b.Language)] {
				continue
			}
			lines := strings.Split(b.Body, "\n")
			for i, line := range lines {
				if addressHoldingVariable.MatchString(line) {
					t.Errorf("%s:%d: the sample holds the per-session address in a variable that outlives the frame it was read from; each session-scoped emission stamps the address carried by the inbound frame it answers\n%s", rel, b.StartLine+i+1, strings.TrimSpace(line))
				}
			}
			for _, e := range sessionScopedEmissions(b.Body) {
				if frameTypeDeclaration.MatchString(e.Text) {
					continue
				}
				checked++
				if inboundEnvelopeReference.MatchString(e.Text) {
					continue
				}
				header := enclosingFunctionHeader(lines, e.Offset-1)
				if header != "" && addressParameter.MatchString(header) {
					continue
				}
				t.Errorf("%s:%d: this emission takes the per-session address from neither the inbound frame it answers nor a parameter of %q; the address travels with the frame being answered\n%s", rel, b.StartLine+e.Offset, header, e.Text)
			}
		}
	}
	if checked == 0 {
		t.Error("no sample source block emits a `response` or a `tool_call` (pages restructured?); the addressing rule is no longer held on the samples")
	}
}

// enclosingFunctionHeader returns the top-level function declaration the line
// at `idx` sits inside, or the empty string when the block is a fragment with
// no declaration above it.
func enclosingFunctionHeader(lines []string, idx int) string {
	for i := idx; i >= 0 && i < len(lines); i-- {
		if sampleFunctionHeader.MatchString(lines[i]) {
			return strings.TrimSpace(lines[i])
		}
	}
	return ""
}

// gofmtFence returns the canonical formatting of a Go fence, and reports
// whether the fence could be parsed at all. A fence that opens inside a switch
// statement, which is how the sample pages present a single handler arm, is
// wrapped before formatting and unwrapped afterwards. A fence gofmt cannot
// parse is left to code_blocks_test.go, which reports the syntax error.
func gofmtFence(t *testing.T, body string) (string, bool) {
	t.Helper()
	body = strings.TrimRight(body, "\n")
	if out, err := runGofmt(body); err == nil {
		return out, true
	}
	if !strings.HasPrefix(strings.TrimLeft(body, " \t"), "case ") {
		return "", false
	}
	out, err := runGofmt("func _f() {\nswitch {\n" + body + "\n}\n}")
	if err != nil {
		return "", false
	}
	lines := strings.Split(out, "\n")
	if len(lines) < 4 {
		return "", false
	}
	inner := lines[2 : len(lines)-2]
	for i, l := range inner {
		inner[i] = strings.TrimPrefix(l, "\t")
	}
	return strings.Join(inner, "\n"), true
}

// runGofmt formats a Go source fragment, returning the error gofmt reported
// when the fragment does not parse.
func runGofmt(body string) (string, error) {
	cmd := exec.Command("gofmt")
	cmd.Stdin = strings.NewReader(body + "\n")
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gofmt: %s", strings.TrimSpace(stderr.String()))
	}
	return strings.TrimRight(out.String(), "\n"), nil
}

// spec: 15.4.3
// diagnosis: a Go fence on a page that presents a complete, runnable sample is
//
//	not in canonical gofmt form, so the field, type, struct-tag, and
//	composite-literal key columns no longer line up. An author copies these
//	pages as a starting point, and the first `gofmt` run on the copy rewrites
//	lines the page never asked them to touch. This most often follows an edit
//	that inserts a struct member or a literal key without re-aligning the
//	columns around it.
func TestBasicLevelRuntimeSampleGoFencesAreCanonicallyFormatted(t *testing.T) {
	root := repoRoot(t)

	formatted := 0
	for _, rel := range basicLevelRuntimeSamplePages {
		blocks, err := extractFencedBlocksIncluding(filepath.Join(root, rel), true)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		for _, b := range blocks {
			if strings.ToLower(b.Language) != "go" {
				continue
			}
			want, ok := gofmtFence(t, b.Body)
			if !ok {
				continue
			}
			formatted++
			if want == strings.TrimRight(b.Body, "\n") {
				continue
			}
			t.Errorf("%s:%d: this Go fence is not in canonical gofmt form; run the fence through gofmt and paste the result back\n--- got\n%s\n--- want\n%s", rel, b.StartLine, b.Body, want)
		}
	}
	if formatted == 0 {
		t.Error("no sample page carries a parseable Go fence (pages restructured?); the samples are no longer held to canonical formatting")
	}
}

// conformanceGuidePage is the reader-facing conformance page whose validator
// table states the Basic-level echo obligation and whose worked harness
// snippets show an author how to exercise a runtime by hand.
var conformanceGuidePage = filepath.Join("docs", "runtime-author-guide", "testing.md")

// inboundMessageLiteral matches an inbound `message` frame written as a
// complete one-line JSON object, which is the form a harness snippet pipes
// into the runtime under test.
var inboundMessageLiteral = regexp.MustCompile(`\{[^{}]*"type"\s*:\s*"message"`)

// addressReadBack matches a read of the per-session address off a parsed
// frame, under the map-index and member-access spellings the harness snippets
// use. A snippet that stamps the address on its inbound frame but never reads
// it back off the response certifies a runtime that drops it.
var addressReadBack = regexp.MustCompile(
	`\[\s*["'](` + addressAlternation(wireSpelling) + `)["']\s*\]|` +
		`\.(` + addressAlternation(wireSpelling) + "|" +
		addressAlternation(goFieldSpelling) + "|" +
		addressAlternation(snakeSpelling) + `)\b`,
)

// spec: 4.1 (request message scope), 15.4.3, 28.5.3
// diagnosis: the conformance page states the Basic-level echo obligation in
//
//	its round-trip validator row and then shows a hand-written harness that
//	pipes an unaddressed `message` into the runtime and checks only the frame
//	type of what comes back. An author who follows the page's own harness feeds
//	a frame the runtime has no address to echo from, so the harness passes a
//	runtime the validator row rejects, and the page contradicts itself. Every
//	inbound `message` the page shows is addressed, and the snippet that reads
//	the answer reads the address back off it.
func TestConformanceGuideHarnessFeedsAnAddressedMessageAndChecksTheEcho(t *testing.T) {
	root := repoRoot(t)
	rel := conformanceGuidePage
	page := filepath.Join(root, rel)

	row := lineContaining(mustReadPage(t, page), "| `message` / `response` round-trip |")
	if row == "" {
		t.Fatalf("%s: the round-trip validator row is gone (page restructured?); the echo obligation the harness below must agree with is no longer stated", rel)
	}
	if !frameAddressValue.MatchString(row) {
		t.Errorf("%s: the round-trip validator row states no per-session address on the accepted `response` form; the harness snippets below are held to the obligation this row states\n%s", rel, strings.TrimSpace(row))
	}

	blocks, err := extractFencedBlocksIncluding(page, true)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	feeding := 0
	for _, b := range blocks {
		lines := strings.Split(b.Body, "\n")
		carries := false
		for i, line := range lines {
			if !inboundMessageLiteral.MatchString(line) {
				continue
			}
			carries = true
			feeding++
			checkFrameAddress(t, fmt.Sprintf("%s:%d", rel, b.StartLine+i+1), line)
		}
		if carries && !addressReadBack.MatchString(b.Body) {
			t.Errorf("%s:%d: this harness snippet feeds an addressed `message` and never reads the per-session address back off the answer; the round-trip category asserts the echo, so the page's own harness asserts it too", rel, b.StartLine)
		}
	}
	if feeding == 0 {
		t.Errorf("%s: no snippet feeds an inbound `message` (page restructured?); the harness is no longer held to the echo obligation", rel)
	}
}

// mustReadPage reads a documentation page or fails the test.
func mustReadPage(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
