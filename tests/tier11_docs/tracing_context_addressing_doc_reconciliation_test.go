// SPDX-License-Identifier: MIT

// Tier-11 doc/spec-consistency checks for the `set_tracing_context` frame's
// registration mechanism and its addressing rule.
//
// Two reader-facing pages describe the same frame: the JSONL entry in
// docs/reference/adapter-contract.md and the platform-tool entry in
// docs/runtime-author-guide/platform-tools.md. Both previously stated that the
// adapter stores the tracing context and attaches it to every subsequent
// `lenny/delegate_task` request, and the reference page also stated that the
// validation rules are enforced when the delegation request arrives. Neither is
// how the platform behaves: the adapter relays the frame by calling the platform
// tool with the addressed session's id injected, the gateway merges the
// submitted context into the session's recorded context and validates the merged
// result at registration, and the registered context is attached to the child's
// delegation lease.
//
// §28.5.3 also addresses the frame to a session and states the addressing rule:
// the adapter resolves the frame against the stream that delivered it, and
// drops, counts, and logs a frame it cannot address to that stream's own live
// session. Both pages must carry that rule so a runtime author knows the frame
// names the session it belongs to.
//
// This test pins the corrected statements on both pages together, so a later
// edit to one page cannot reintroduce the contradiction on its own. It reads the
// repository state directly (no build tag, no infrastructure), the same posture
// as the other tier-11 doc checks.
//
// spec: 28.5.3 (set_tracing_context schema, addressing, and drop outcome), 8.3
// (registration-time validation and lease attachment)

package tier11_docs_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// adapterContractDoc reads docs/reference/adapter-contract.md into a string.
func adapterContractDoc(t *testing.T, root string) string {
	t.Helper()
	return readDocPage(t, filepath.Join(root, "docs", "reference", "adapter-contract.md"))
}

// platformToolsDoc reads docs/runtime-author-guide/platform-tools.md into a
// string.
func platformToolsDoc(t *testing.T, root string) string {
	t.Helper()
	return readDocPage(t, filepath.Join(root, "docs", "runtime-author-guide", "platform-tools.md"))
}

// spec: 28.5.3, 8.3
// diagnosis: docs/reference/adapter-contract.md or
//
//	docs/runtime-author-guide/platform-tools.md still describes
//	`set_tracing_context` as an adapter-side store that the adapter attaches to
//	later `lenny/delegate_task` requests, or still places the validation at the
//	delegation call. The adapter stores and attaches nothing: it relays the
//	frame by calling the platform tool with the addressed session's id injected,
//	the gateway merges the submitted context into the session's recorded context
//	and validates the merged result when the identifiers are registered, and the
//	registered context is attached to the child's delegation lease. A failure
//	here means a runtime author reading either page would expect the adapter to
//	hold state it does not hold, or would expect a rejection at a delegation
//	call that in fact happens at registration.
func TestTracingContextRegistrationMechanismAgreesAcrossDocs(t *testing.T) {
	root := repoRoot(t)

	contract := adapterContractDoc(t, root)
	entry := section(contract, "`set_tracing_context` ---")
	if entry == "" {
		t.Fatal("docs/reference/adapter-contract.md: `set_tracing_context` entry not found (renamed or removed?)")
	}
	requireAllContain(t, "adapter-contract.md set_tracing_context entry", entry, []string{
		"merges the submitted context into that session's recorded context",
		"validates the merged result against the tracing-context rules at registration time",
		"attaches the registered context to each child's delegation lease",
		"The adapter itself stores no context and attaches none to later requests.",
	})
	requireNoneContain(t, "adapter-contract.md set_tracing_context entry", entry, []string{
		"the adapter attaches to all subsequent",
		"Validation rules are enforced by the gateway when the delegation request arrives",
	})

	tools := platformToolsDoc(t, root)
	toolEntry := section(tools, "`lenny/set_tracing_context`")
	if toolEntry == "" {
		t.Fatal("docs/runtime-author-guide/platform-tools.md: `lenny/set_tracing_context` entry not found (renamed or removed?)")
	}
	requireAllContain(t, "platform-tools.md lenny/set_tracing_context entry", toolEntry, []string{
		"merges the submitted context into your session's recorded context",
		"validates the merged result at registration time",
		"attaches the registered context to each child's delegation lease",
		"The adapter itself stores no context and attaches none to later calls.",
		// The corrected note keeps the statement that the tracing context is
		// runtime plumbing the model never touches.
		"The LLM never sees or sets the tracing context",
	})
	requireNoneContain(t, "platform-tools.md lenny/set_tracing_context entry", toolEntry, []string{
		"The adapter stores the context and attaches it to every subsequent",
	})
}

// spec: 28.5.3
// diagnosis: docs/reference/adapter-contract.md or
//
//	docs/runtime-author-guide/platform-tools.md has lost the `set_tracing_context`
//	addressing rule. The frame carries the per-session identifier on every pod,
//	the adapter resolves it against the stream that delivered it, an identifier
//	the frame omits resolves to that stream's own binding on a pod holding at
//	most one slot and is rejected on a pod holding more, and the adapter drops,
//	counts, and logs a frame it cannot address to that stream's own live
//	session. A failure here means a runtime author would read the silent drop as
//	a platform defect, or would expect an unaddressed frame to reach every slot
//	as it did before the frame was addressed.
func TestTracingContextAddressingRuleDocumented(t *testing.T) {
	root := repoRoot(t)

	contract := adapterContractDoc(t, root)
	entry := section(contract, "`set_tracing_context` ---")
	if entry == "" {
		t.Fatal("docs/reference/adapter-contract.md: `set_tracing_context` entry not found (renamed or removed?)")
	}
	requireAllContain(t, "adapter-contract.md set_tracing_context entry", entry, []string{
		// The documented frame carries the session address in the
		// wire form the published JSONL schema accepts: a string.
		"| `slotId` | string, optional |",
		// The addressing rule and its outcome.
		"resolves the frame against the stream that delivered it",
		"matches the stream's session",
		"still holds that address with a bound session",
		// Absence resolves rather than selects a scope: it is the receiving
		// stream's own binding on a pod holding at most one slot, and an
		// error on a pod holding more.
		"resolves to the receiving stream's own binding on a pod holding at most one slot",
		"on a pod holding more than one slot it is rejected and relayed to no stream",
		"dropped, counted in `lenny_adapter_set_tracing_context_dropped_total`",
		"logged as a protocol error",
		// The drop outcome is stated on the page rather than deferred to a
		// behavior the page never describes.
		"the runtime receives no error for a dropped frame",
	})
	// The presence condition the value rule replaced: the identifier was
	// documented as carried only on a pod whose pool set
	// `sessionPolicy.maxConcurrentSessions > 1`, and an unaddressed frame as
	// applied on a pod holding no registered slot.
	requireNoneContain(t, "adapter-contract.md set_tracing_context entry", entry, []string{
		"`sessionPolicy.maxConcurrentSessions > 1`",
		"Omit the field on a pod that serves one session at a time",
		"untagged frame is applied only on a pod that holds no registered slot",
	})

	tools := platformToolsDoc(t, root)
	toolEntry := section(tools, "`lenny/set_tracing_context`")
	if toolEntry == "" {
		t.Fatal("docs/runtime-author-guide/platform-tools.md: `lenny/set_tracing_context` entry not found (renamed or removed?)")
	}
	requireAllContain(t, "platform-tools.md lenny/set_tracing_context entry", toolEntry, []string{
		"the JSONL frame must carry the emitting slot's `slotId`",
		"dropped and logged rather than applied",
	})

	// The availability paragraph under the level table repeats that the JSONL
	// frame is reachable from every level; it must repeat the addressing rule
	// with it, because a Basic-level runtime reaches the frame only there.
	availability := lineContaining(tools, "available at every level")
	if availability == "" {
		t.Fatal("docs/runtime-author-guide/platform-tools.md: no paragraph states the JSONL set_tracing_context frame is available at every level (renamed or removed?)")
	}
	requireAllContain(t, "platform-tools.md tool-availability paragraph", availability, []string{
		"the frame must carry the emitting slot's `slotId`",
		"dropped and logged rather than applied",
	})
}

// spec: 28.5.3 (set_tracing_context schema)
// diagnosis: the `set_tracing_context` frame printed on
//
//	docs/reference/adapter-contract.md does not validate against the published
//	JSONL schema at schemas/lenny-adapter-jsonl.schema.json. A runtime author
//	copies the documented frame verbatim, so a documented form the schema
//	rejects (a null `slotId`, for instance, where the schema declares a string)
//	produces a frame the adapter reads as untagged and, on a pod holding
//	registered slots, drops.
func TestDocumentedTracingContextFrameValidatesAgainstJSONLSchema(t *testing.T) {
	root := repoRoot(t)
	page := filepath.Join(root, "docs", "reference", "adapter-contract.md")

	blocks, err := extractFencedBlocks(page)
	if err != nil {
		t.Fatalf("read %s: %v", page, err)
	}

	compiler := schematest.NewCompiler(t)
	schematest.MustAddLocalSchema(t, compiler, "https://schemas.lenny.dev/messagepart/v1.json", "schemas/messagepart.schema.json")
	schema := schematest.MustCompile(t, compiler, "schemas/lenny-adapter-jsonl.schema.json")

	found := 0
	for _, b := range blocks {
		if normalize(b.Language) != "json" || !strings.Contains(b.Body, `"set_tracing_context"`) {
			continue
		}
		found++
		var doc any
		if err := json.Unmarshal([]byte(b.Body), &doc); err != nil {
			t.Errorf("%s:%d: documented set_tracing_context frame is not JSON: %v", page, b.StartLine, err)
			continue
		}
		if err := schema.Validate(doc); err != nil {
			t.Errorf("%s:%d: documented set_tracing_context frame fails the published JSONL schema: %v\n  payload: %s", page, b.StartLine, err, b.Body)
		}
	}
	if found == 0 {
		t.Fatalf("%s: no documented set_tracing_context frame found (renamed or removed?)", page)
	}
}

// spec: 28.5.3
// diagnosis: the reader-facing pages describing `set_tracing_context` cite a
//
//	spec section number. Reader-facing documentation states the behavior and
//	links to another documentation page; spec section numbers are internal and
//	shift, and a reader without the spec cannot resolve them.
func TestTracingContextDocsCiteNoSpecSections(t *testing.T) {
	root := repoRoot(t)

	for label, entry := range map[string]string{
		"adapter-contract.md set_tracing_context entry":         section(adapterContractDoc(t, root), "`set_tracing_context` ---"),
		"platform-tools.md lenny/set_tracing_context entry":     section(platformToolsDoc(t, root), "`lenny/set_tracing_context`"),
		"integration-levels.md lenny/set_tracing_context row":   lineContaining(readDocPage(t, filepath.Join(root, "docs", "runtime-author-guide", "integration-levels.md")), "| `lenny/set_tracing_context` |"),
		"platform-tools.md tool-availability paragraph (JSONL)": lineContaining(platformToolsDoc(t, root), "available at every level"),
	} {
		if entry == "" {
			t.Fatalf("%s: not found (renamed or removed?)", label)
		}
		requireNoneContain(t, label, entry, []string{"§", "spec/"})
	}
}

// tracingAddressingSpecBlock returns the Addressing block of the
// set_tracing_context card in spec/28_communication-channels.md, from its
// "**Addressing.**" lead to the next heading. The block is the normative
// statement of the addressing rule the reference page restates.
func tracingAddressingSpecBlock(t *testing.T, root string) string {
	t.Helper()
	content := readRepoFile(t, root, "spec", "28_communication-channels.md")
	const startMark = "**Addressing.** The adapter resolves the frame against the Attach stream"
	start := strings.Index(content, startMark)
	if start < 0 {
		t.Fatalf("spec/28: set_tracing_context Addressing block not found (rewritten or removed?)")
	}
	rest := content[start:]
	if end := strings.Index(rest, "\n#"); end > 0 {
		rest = rest[:end]
	}
	return rest
}

// spec: 28.5.3 (set_tracing_context addressing)
// diagnosis: docs/reference/adapter-contract.md publishes an addressing
//
//	outcome for `set_tracing_context` that §28.5.3 does not define. The
//	addressing rule resolves the frame through two conditions over an
//	address compared as exact string equality, with an absent or empty
//	`slotId` counting as the empty string on both sides. A page that also
//	tells a runtime author what happens to a `slotId` of some other JSON
//	type states behavior no implementor can derive from the contract, and
//	an adapter written from the spec alone would not match it. Either the
//	statement comes out of the page or the outcome goes into §28.5.3
//	first.
func TestTracingContextAddressingDocStatesNoOutcomeBeyondTheSpec(t *testing.T) {
	root := repoRoot(t)

	entry := section(adapterContractDoc(t, root), "`set_tracing_context` ---")
	if entry == "" {
		t.Fatal("docs/reference/adapter-contract.md: `set_tracing_context` entry not found (renamed or removed?)")
	}
	requireAllContain(t, "adapter-contract.md set_tracing_context entry", entry, []string{
		"The comparison is exact string equality.",
	})

	// The page may describe an outcome for a `slotId` of a non-string JSON
	// type only once §28.5.3 defines one, so the check reads the spec block
	// first and applies only while the spec is silent.
	specBlock := tracingAddressingSpecBlock(t, root)
	claims := []string{"non-string", "is not a string", "not a JSON string"}
	var stated []string
	for _, c := range claims {
		if strings.Contains(specBlock, c) {
			stated = append(stated, c)
		}
	}
	if len(stated) > 0 {
		t.Logf("spec/28 §28.5.3 Addressing now states a non-string `slotId` outcome (%v); the reference page may restate it", stated)
		return
	}
	requireNoneContain(t, "adapter-contract.md set_tracing_context entry", entry, claims)
}
