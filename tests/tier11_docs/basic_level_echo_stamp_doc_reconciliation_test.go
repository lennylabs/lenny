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
	"encoding/json"
	"fmt"
	"os"
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
// has ever been published under. The published schema names exactly one of
// them, and every reader-facing page carries that one.
var frameAddressKeys = []string{"slotId", "sessionId"}

// publishedFrameAddressKey returns the wire spelling of the per-session address
// key that schemas/lenny-adapter-jsonl.schema.json currently publishes on the
// `response` frame. Deriving the spelling from the schema is what makes the
// assertions below travel with the key rename: the documentation set is held to
// the key the tree emits at this commit rather than to a literal that has to be
// edited in step with it.
func publishedFrameAddressKey(t *testing.T, root string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "schemas", "lenny-adapter-jsonl.schema.json"))
	if err != nil {
		t.Fatalf("read the published JSON Lines schema: %v", err)
	}
	var doc struct {
		Defs map[string]struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse the published JSON Lines schema: %v", err)
	}
	frame, ok := doc.Defs["response"]
	if !ok {
		t.Fatal("schemas/lenny-adapter-jsonl.schema.json: no `response` frame definition (renamed or removed?)")
	}
	var found string
	for _, key := range frameAddressKeys {
		if _, ok := frame.Properties[key]; !ok {
			continue
		}
		if found != "" {
			t.Fatalf("schemas/lenny-adapter-jsonl.schema.json: the `response` frame declares both %q and %q; the per-session address has one key", found, key)
		}
		found = key
	}
	if found == "" {
		t.Fatalf("schemas/lenny-adapter-jsonl.schema.json: the `response` frame declares none of %v as its per-session address", frameAddressKeys)
	}
	return found
}

// retiredFrameAddressKey returns the spelling the published key replaced. A
// reader-facing page carrying it names a key the tree does not emit.
func retiredFrameAddressKey(published string) string {
	for _, key := range frameAddressKeys {
		if key != published {
			return key
		}
	}
	return ""
}

// addressSpellings returns the wire spelling of the per-session address key
// together with the member spellings the sample languages give it: an exported
// Go field, and a snake-case Python name. `slotId` gives `SlotID` and
// `slot_id`.
func addressSpellings(key string) (goField, snake string) {
	stem := strings.TrimSuffix(key, "Id")
	return strings.ToUpper(stem[:1]) + stem[1:] + "ID", stem + "_id"
}

// echoedIdentifier matches the per-session identifier in the published wire
// spelling and in the member spelling each sample language uses for it.
func echoedIdentifier(key string) *regexp.Regexp {
	goField, snake := addressSpellings(key)
	return regexp.MustCompile(regexp.QuoteMeta(key) + "|" + regexp.QuoteMeta(goField) + "|" + regexp.QuoteMeta(snake))
}

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
	key := publishedFrameAddressKey(t, root)
	echoed := echoedIdentifier(key)
	stale := echoedIdentifier(retiredFrameAddressKey(key))

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
				if stale.MatchString(e.Text) {
					t.Errorf("%s:%d: this `response` or `tool_call` construction spells the per-session address key %q, which the published JSON Lines schema does not declare; the samples and the schema name one key\n%s", rel, b.StartLine+e.Offset, retiredFrameAddressKey(key), e.Text)
					continue
				}
				if echoed.MatchString(e.Text) {
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

// addressOmittedWhenAbsent records, per sample page, the construct that keeps
// the per-session address off a frame when the inbound envelope carried none.
// A frame carrying the key bound to a null value fails the published JSON Lines
// schema, which accepts a string there; a frame that omits the key resolves to
// the binding of the stream that delivered it on a pod holding at most one
// slot. Each construct is written against the published key, so it also pins
// the declaration the emissions echo from.
func addressOmittedWhenAbsent(key string) []struct {
	page      string
	construct string
} {
	goField, snake := addressSpellings(key)
	goTag := fmt.Sprintf("`json:%q`", key+",omitempty")
	camel := "current" + goField[:len(goField)-2] + "Id"
	return []struct {
		page      string
		construct string
	}{
		{filepath.Join("docs", "runtime-author-guide", "echo-runtime.md"), goTag},
		{filepath.Join("docs", "runtime-author-guide", "sdk-examples", "go.md"), goTag},
		{filepath.Join("docs", "runtime-author-guide", "sdk-examples", "python.md"), fmt.Sprintf("if current_%s is not None:", snake)},
		{filepath.Join("docs", "runtime-author-guide", "sdk-examples", "typescript.md"), fmt.Sprintf("let %s: string | undefined;", camel)},
		{filepath.Join("docs", "tutorials", "build-a-runtime.md"), goTag},
		{filepath.Join("docs", "tutorials", "recursive-delegation.md"), goTag},
	}
}

// spec: 4.6.1, 15.1, 28.5.3
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

	for _, sample := range addressOmittedWhenAbsent(publishedFrameAddressKey(t, root)) {
		page := readDocPage(t, filepath.Join(root, sample.page))
		if !strings.Contains(page, sample.construct) {
			t.Errorf("%s: the sample does not keep the per-session address off a frame that has none (expected %q); a null address fails the published JSON Lines schema", sample.page, sample.construct)
		}
	}
}

// spec: 4.6.1, 15.1, 28.5.3
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
	key := publishedFrameAddressKey(t, root)
	page := adapterContractDoc(t, root)

	line := lineContaining(page, "**Basic-level runtimes:**")
	if line == "" {
		t.Fatal("docs/reference/adapter-contract.md: no `message` field table states what a Basic-level runtime reads (renamed or removed?)")
	}
	if !strings.Contains(line, key) {
		t.Errorf("docs/reference/adapter-contract.md: the Basic-level permission names no per-session address field: %q", line)
	}
	requireAllContain(t, "adapter-contract.md Basic-level permission", line, []string{
		"echoes it",
	})
	if strings.Contains(line, "Ignore all other fields safely.") {
		t.Errorf("docs/reference/adapter-contract.md: the Basic-level permission is unqualified; the per-session address is excepted from it: %q", line)
	}
}

// spec: 4.6.1, 28.5.3
// diagnosis: a `response` or `tool_call` literal on
//
//	docs/reference/adapter-contract.md carries no per-session address. Every
//	session-scoped frame is addressed on every pod, so the page's shorthand
//	examples and its annotated wire traces spell the same field set its frame
//	reference does. A worked trace showing an unaddressed frame demonstrates the
//	page's own field reference being violated.
func TestAdapterContractFrameLiteralsCarryTheAddress(t *testing.T) {
	root := repoRoot(t)
	key := publishedFrameAddressKey(t, root)
	stale := retiredFrameAddressKey(key)
	rel := filepath.Join("docs", "reference", "adapter-contract.md")

	blocks, err := extractFencedBlocksIncluding(filepath.Join(root, rel), true)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	emitting := 0
	for _, b := range blocks {
		for _, e := range sessionScopedEmissions(b.Body) {
			emitting++
			if strings.Contains(e.Text, stale) {
				t.Errorf("%s:%d: this frame literal spells the per-session address key %q, which the published JSON Lines schema does not declare; the reference page and the schema name one key\n%s", rel, b.StartLine+e.Offset, stale, e.Text)
				continue
			}
			if strings.Contains(e.Text, key) {
				continue
			}
			t.Errorf("%s:%d: this frame literal carries no per-session address; every session-scoped frame is addressed on every pod\n%s", rel, b.StartLine+e.Offset, e.Text)
		}
	}
	if emitting == 0 {
		t.Errorf("%s: no code block carries a `response` or `tool_call` literal (page restructured?); the addressing rule is no longer held on this page", rel)
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
