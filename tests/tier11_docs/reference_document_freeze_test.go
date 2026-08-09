// SPDX-License-Identifier: MIT

// Tier-11 documentation check holding the reference document
// gateway-runtime-comms.md to the freeze the proposal applied to it: a
// point-in-time header naming the working-tree commit the reading was
// taken at and the specification sections that supersede it, over a body
// that no longer moves. The document is the source §28 and §29 were
// derived from, so an unfrozen copy of it is a second, unmarked
// description of the same contract, which is the failure this
// specification section exists to end. These tests are NOT under a build
// tag because they exercise the repository state directly — no external
// infrastructure required.

package tier11_docs_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// referenceDocumentPath is the frozen reference, at the repository root.
const referenceDocumentPath = "gateway-runtime-comms.md"

// referenceDocumentCommit is the working-tree commit the reference
// document is a reading of. The header names this commit; a header
// naming any other commit describes a reading the body is not.
const referenceDocumentCommit = "fcda83e3"

// referenceDocumentBodyDigest is the SHA-256 of the frozen body, which
// is the document below its header with the blank lines separating the
// two removed. It was recorded when the freeze landed. The digest is
// pinned here rather than read from git so that an edit committed
// alongside a change to this constant is visible as what it is: a
// deliberate reopening of a document the specification says is closed.
//
// A failure of this constant is not an invitation to re-record it.
// Correcting the reference makes it a maintained second source rather
// than a historical record, which is the state the freeze exists to
// prevent; the correction belongs in §28 or §29.
const referenceDocumentBodyDigest = "f7ebd7d4fb0a15980043e2106b9012c5fb029b4e20653eb3b88b24a0badfabb0"

// referenceDocumentSupersedingSections are the specification sections
// the header must name as superseding the document for all current
// behavior. Each entry is a set of spellings, any one of which satisfies
// that section, so the header may write "Sections 28 and 29" or "§28".
var referenceDocumentSupersedingSections = [][]string{
	{"section 28", "sections 28", "§28"},
	{"section 29", "sections 29", "and 29", "§29"},
}

// splitFrozenReference splits a reference document into its header block
// and its body. The header block is the run of blockquote lines the
// document opens with, which is the form the freeze header is written
// in; the body is everything below it, with the blank lines separating
// the two removed so that the digest does not depend on that spacing.
//
// A document that opens with anything other than a blockquote line has
// no header block, and the whole document is its body.
//
// spec: §28.1
func splitFrozenReference(text string) (header, body string) {
	lines := strings.Split(text, "\n")
	i := 0
	for i < len(lines) && strings.HasPrefix(lines[i], ">") {
		i++
	}
	header = strings.Join(lines[:i], "\n")
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	return header, strings.Join(lines[i:], "\n")
}

// frozenReferenceViolations returns one entry for every way the given
// document text fails the freeze, in a stable order. It is the predicate
// the gate asserts: the header is present, it names the commit the
// reading was taken at, it names the sections that supersede the
// document, and the body below it matches the committed text.
//
// The predicate reads the header and the body separately, so that a
// commit identifier standing in the body — the document's own opening
// paragraph names it — cannot satisfy the header requirement.
//
// spec: §28.1
func frozenReferenceViolations(text string) []string {
	header, body := splitFrozenReference(text)

	var out []string
	if strings.TrimSpace(header) == "" {
		out = append(out, "no point-in-time header: the document does not open with a blockquote header block")
	} else {
		lowered := strings.ToLower(header)
		if !strings.Contains(lowered, "point-in-time") {
			out = append(out, "header does not state that the document is a point-in-time reading")
		}
		if !strings.Contains(header, referenceDocumentCommit) {
			out = append(out, fmt.Sprintf("header does not name the working-tree commit %s the reading was taken at", referenceDocumentCommit))
		}
		for _, spellings := range referenceDocumentSupersedingSections {
			named := false
			for _, spelling := range spellings {
				if strings.Contains(lowered, strings.ToLower(spelling)) {
					named = true
					break
				}
			}
			if !named {
				out = append(out, fmt.Sprintf("header does not name %q as superseding the document", spellings[0]))
			}
		}
		if !strings.Contains(lowered, "supersede") {
			out = append(out, "header does not state that the named sections supersede the document for current behavior")
		}
	}

	sum := sha256.Sum256([]byte(body))
	if got := hex.EncodeToString(sum[:]); got != referenceDocumentBodyDigest {
		out = append(out, fmt.Sprintf("body below the header does not match the committed text: digest %s, frozen digest %s", got, referenceDocumentBodyDigest))
	}

	sort.Strings(out)
	return out
}

// readFrozenReference reads the reference document from the working
// tree.
func readFrozenReference(t testing.TB) string {
	t.Helper()
	path := filepath.Join(repoRoot(t), referenceDocumentPath)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", referenceDocumentPath, err)
	}
	return string(b)
}

// diagnosis: a failure of the landed case means gateway-runtime-comms.md
// is no longer frozen — either its point-in-time header is gone or no
// longer names the commit and the superseding sections, or its body has
// been edited since the freeze. Either way the repository now carries a
// second, unmarked description of the communication channels that a
// reader can find before §28 and §29 and read as current. The fix is to
// restore the header or revert the body edit and carry the correction
// into §28 or §29; re-recording the pinned digest reopens the document
// and defeats the check. A failure of a rejection case means the
// predicate has been widened past what it must catch, so a document that
// is not frozen would pass as frozen.
//
// spec: §28.1
func TestReferenceDocumentIsFrozen_spec_28_1(t *testing.T) {
	landed := readFrozenReference(t)

	t.Run("landed document", func(t *testing.T) {
		if v := frozenReferenceViolations(landed); len(v) > 0 {
			t.Errorf("%s is not frozen (%d finding(s)):", referenceDocumentPath, len(v))
			for _, f := range v {
				t.Errorf("  - %s", f)
			}
		}
	})

	// The three non-happy cases the predicate must catch. Each is the
	// landed document with one property of the freeze removed, so a
	// case that passes proves the predicate reads that property and
	// nothing weaker.
	header, body := splitFrozenReference(landed)

	t.Run("missing header", func(t *testing.T) {
		assertFreezeRejects(t, body, "point-in-time header")
	})

	t.Run("header naming a different commit", func(t *testing.T) {
		other := strings.ReplaceAll(header, referenceDocumentCommit, "0000000")
		assertFreezeRejects(t, other+"\n\n"+body, referenceDocumentCommit)
	})

	t.Run("header naming no superseding sections", func(t *testing.T) {
		var kept []string
		for _, line := range strings.Split(header, "\n") {
			lowered := strings.ToLower(line)
			if strings.Contains(lowered, "supersede") {
				continue
			}
			kept = append(kept, line)
		}
		assertFreezeRejects(t, strings.Join(kept, "\n")+"\n\n"+body, "supersed")
	})

	t.Run("body edit", func(t *testing.T) {
		edited := header + "\n\n" + body + "\n\nA later correction to the reference.\n"
		assertFreezeRejects(t, edited, "does not match the committed text")
	})
}

// assertFreezeRejects fails when the predicate accepts a specimen that
// is not frozen, or when it rejects it for a reason other than the one
// the specimen removes.
func assertFreezeRejects(t *testing.T, specimen, want string) {
	t.Helper()
	v := frozenReferenceViolations(specimen)
	if len(v) == 0 {
		t.Fatalf("predicate accepted a specimen that is not frozen; expected a finding mentioning %q", want)
	}
	for _, f := range v {
		if strings.Contains(f, want) {
			return
		}
	}
	t.Fatalf("predicate rejected the specimen, but no finding mentions %q: %v", want, v)
}
