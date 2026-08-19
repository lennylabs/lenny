// SPDX-License-Identifier: MIT

// Tier-11 doc/spec-consistency checks for the two senses of "slot".
//
// §5.2 defines the term twice, once per execution mode. A session-mode slot
// is the unit of per-pod session capacity: it owns a workspace subtree, a
// credential lease, and a lifecycle, it is identified by the identifier of
// the session bound to it, and it is a pod-side resource that survives its
// session when it leaks or fails. A service-mode slot is unnamed per-pod
// request capacity with none of those properties. The reader-facing
// glossary carried no entry for either sense, so a reader who met the word
// on a page had no definition to resolve it against and no way to tell
// which of the two mechanisms a page meant.
//
// These cases pin the glossary entry to §5.2's statement of both senses,
// and pin the pages that use the term to the anchor that defines it, so a
// later edit to §5.2 cannot leave the glossary stating the retired reading
// and a page cannot use the word with no route to its definition.
//
// spec: 5.2 (session-mode slot definition, service-mode slot definition)

package tier11_docs_test

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// inlineMarkdownLinkExpr reads an inline markdown link so a caller can
// recover the prose the page reads as. A phrase assertion over a page that
// links a glossary term inside the sentence it pins would otherwise fail on
// the link syntax rather than on the statement.
var inlineMarkdownLinkExpr = regexp.MustCompile(`\[([^\]]+)\]\([^()\s]*\)`)

// stripInlineMarkdownLinks replaces every inline markdown link in body with
// its label, leaving the sentence as the reader reads it.
func stripInlineMarkdownLinks(body string) string {
	return inlineMarkdownLinkExpr.ReplaceAllString(body, "$1")
}

// glossaryPath is the reader-facing definition page under test.
func glossaryPath(root string) string {
	return filepath.Join(root, "docs", "reference", "glossary.md")
}

// glossarySlotEntry returns the body of the glossary's Slot entry, from its
// heading to the next entry heading. It fails the test when the entry or
// its anchor attribute is absent, because both the definition and the
// address other pages link to are part of what this case pins.
func glossarySlotEntry(t *testing.T, glossary string) string {
	t.Helper()
	const heading = "### Slot\n"
	start := strings.Index(glossary, heading)
	if start < 0 {
		t.Fatalf("docs/reference/glossary.md: no %q entry; the term is used across the docs with no definition to resolve it against", strings.TrimSpace(heading))
	}
	body := glossary[start+len(heading):]
	if next := strings.Index(body, "\n### "); next >= 0 {
		body = body[:next]
	}
	if !strings.Contains(body, "{: #slot }") {
		t.Fatalf("docs/reference/glossary.md: the Slot entry declares no `{: #slot }` anchor, so the pages that use the term cannot link to it")
	}
	return body
}

// spec: 5.2
// diagnosis: docs/reference/glossary.md and spec §5.2 disagree on what a
//
//	session-mode slot is. §5.2 defines it as the unit of per-pod session
//	capacity, owning a workspace subtree, a credential lease, and a
//	lifecycle, identified by the identifier of the session bound to it, held
//	between zero and `sessionPolicy.maxConcurrentSessions` times per pod, and
//	retained when leaked or failed until the pod terminates. A failure here
//	means the glossary carries no entry for the term or states a reading §5.2
//	does not, so a reader resolves the word against a definition the platform
//	no longer holds.
func TestGlossarySlotEntryStatesSessionModeSense(t *testing.T) {
	root := repoRoot(t)

	s52 := specSection(t, filepath.Join(root, "spec", "05_runtime-registry-and-pool-model.md"), "### 5.2 ")
	specLine := requireLine(t, s52, "**Slot (session mode).**")
	requireAllContain(t, "§5.2 session-mode slot definition", specLine, []string{
		"unit of per-pod session capacity",
		"workspace subtree, a credential lease, and a lifecycle",
		"identified by the identifier of the session bound to it",
		"between zero and `sessionPolicy.maxConcurrentSessions` slots",
		"warm pod serving no session holds none",
		"leaked and failed slots are retained until the pod terminates",
	})

	entry := glossarySlotEntry(t, readDocPage(t, glossaryPath(root)))
	requireAllContain(t, "glossary Slot entry (session mode)", entry, []string{
		"unit of per-pod session capacity",
		"workspace subtree, a credential lease, and a lifecycle",
		"identified by the identifier of the session bound to it",
		"between zero and `sessionPolicy.maxConcurrentSessions` slots",
		"warm pod serving no session holds none",
		"leaked and failed slots are retained until the pod terminates",
		"still count against pod occupancy",
	})

	// Every session is bound to a slot on every pod, so the entry must not
	// restate the retired reading under which a slot exists only on a pool
	// configured for concurrency.
	if strings.Contains(entry, "maxConcurrentSessions > 1") {
		t.Errorf("docs/reference/glossary.md: the Slot entry conditions the term on `maxConcurrentSessions > 1`; every session is bound to a slot on every pod, whatever the pool's concurrency")
	}
}

// spec: 5.2
// diagnosis: docs/reference/glossary.md and spec §5.2 disagree on what a
//
//	service-mode slot is. §5.2 states it is a different thing from a
//	session-mode slot: unnamed per-pod request capacity with no identifier,
//	no session binding, no workspace subtree, no credential lease, and no
//	tracked lifecycle. A failure here means the glossary carries only the
//	session-mode sense or attributes session-mode properties to service-mode
//	capacity, so a reader of a service-mode page expects a workspace and a
//	lease that no such pod materializes.
func TestGlossarySlotEntryStatesServiceModeSense(t *testing.T) {
	root := repoRoot(t)

	s52 := specSection(t, filepath.Join(root, "spec", "05_runtime-registry-and-pool-model.md"), "### 5.2 ")
	specLine := requireLine(t, s52, "A service-mode slot is a different thing")
	requireAllContain(t, "§5.2 service-mode slot definition", specLine, []string{
		"unnamed per-pod request capacity",
		"no identifier",
		"no session binding",
		"no workspace subtree",
		"no credential lease",
		"no tracked lifecycle",
	})

	entry := glossarySlotEntry(t, readDocPage(t, glossaryPath(root)))
	serviceLine := requireLine(t, entry, "`service` mode")
	requireAllContain(t, "glossary Slot entry (service mode)", serviceLine, []string{
		"unnamed per-pod request capacity",
		"`maxConcurrent`",
		"no identifier",
		"no session binding",
		"no workspace subtree",
		"no credential lease",
		"no tracked lifecycle",
	})
}

// slotGlossaryLinkPages are the reader-facing pages that use "slot" as a
// standalone term in prose, with the link target each writes to reach the
// glossary anchor from its own directory.
var slotGlossaryLinkPages = map[string]string{
	filepath.Join("docs", "getting-started", "concepts.md"):       "(../reference/glossary#slot)",
	filepath.Join("docs", "reference", "execution-modes.md"):      "(glossary#slot)",
	filepath.Join("docs", "reference", "state-machines.md"):       "(glossary#slot)",
	filepath.Join("docs", "reference", "configuration.md"):        "(glossary#slot)",
	filepath.Join("docs", "runtime-author-guide", "lifecycle.md"): "(../reference/glossary#slot)",
	filepath.Join("docs", "operator-guide", "configuration.md"):   "(../reference/glossary#slot)",
}

// spec: 5.2
// diagnosis: a page that uses "slot" in prose no longer links the glossary
//
//	entry that defines it. §5.2 gives the word two senses, one per execution
//	mode, so a reader who meets it on a mode page needs the route to the
//	definition to tell which mechanism the page means. A failure here means
//	the link was dropped from a page or the glossary anchor moved without the
//	citing pages following it.
func TestPagesUsingSlotLinkTheGlossaryEntry(t *testing.T) {
	root := repoRoot(t)

	for rel, target := range slotGlossaryLinkPages {
		page := readDocPage(t, filepath.Join(root, rel))
		if !strings.Contains(page, target) {
			t.Errorf("%s: uses \"slot\" in prose but writes no link to the glossary entry (expected a link target %s)", rel, target)
		}
	}
}
