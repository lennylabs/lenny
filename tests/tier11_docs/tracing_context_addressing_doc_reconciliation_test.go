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
// §28.5.3 also gives the frame an optional `slotId` and an addressing rule: the
// adapter resolves the frame against the stream that delivered it, and drops,
// counts, and logs a frame it cannot address to that stream's own live session.
// Both pages must carry that rule so a runtime author on a concurrent pod knows
// the frame has to name its slot.
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
//	addressing rule. The frame carries an optional `slotId` on a pod serving more
//	than one concurrent session, the adapter resolves it against the stream that
//	delivered it, and it drops, counts, and logs a frame it cannot address to
//	that stream's own live session. A failure here means a runtime author on a
//	concurrent pod would emit an untagged frame and read the silent drop as a
//	platform defect, or would expect an untagged frame to reach every slot as it
//	did before the frame was addressed.
func TestTracingContextAddressingRuleDocumented(t *testing.T) {
	root := repoRoot(t)

	contract := adapterContractDoc(t, root)
	entry := section(contract, "`set_tracing_context` ---")
	if entry == "" {
		t.Fatal("docs/reference/adapter-contract.md: `set_tracing_context` entry not found (renamed or removed?)")
	}
	requireAllContain(t, "adapter-contract.md set_tracing_context entry", entry, []string{
		// The documented frame carries the optional slot address in the
		// wire form the published JSONL schema accepts: a string.
		`"slotId": "slot_01"`,
		"| `slotId` | string, optional |",
		"`sessionPolicy.maxConcurrentSessions > 1`",
		// The addressing rule and its outcome.
		"resolves the frame against the stream that delivered it",
		"the frame's `slotId` matches the stream's slot",
		// The untagged side of the address comparison is the empty string,
		// the form the schema and the adapter's decoder both produce.
		"an absent or empty `slotId` matching a stream bound to no slot",
		"still binds that address to the stream's session",
		// The fail-closed second term: an untagged frame is rejected on a pod
		// that holds registered slots, where the address names no one session.
		"the address is unambiguous",
		"untagged frame is applied only on a pod that holds no registered slot",
		"dropped, counted in `lenny_adapter_set_tracing_context_dropped_total`",
		"logged as a protocol error",
		// The drop outcome is stated on the page rather than deferred to a
		// behavior the page never describes.
		"the runtime receives no error for a dropped frame",
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
