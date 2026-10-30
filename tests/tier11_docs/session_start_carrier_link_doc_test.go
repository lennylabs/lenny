// SPDX-License-Identifier: MIT

// Tier-11 documentation check that every page outside the runtime-author guide
// which names a session's `session_start` frame as the carrier of a per-session
// field links that mention to the frame's reference definition.
//
// A session's experiment context and its credential file path reach the runtime
// in the session's `session_start` frame on the message channel, and the
// adapter contract reference is the one page that defines that frame. A page
// that names the frame without linking to the reference leaves an operator or
// an API reader with no route to the frame's fields.
//
// This test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 28.5.3 (CH-MSGSOCK session_start frame), 4.7.6 (adapter manifest
// field reference), 10.7 (Experiment Primitives, variant context delivery)

package tier11_docs_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// sessionStartInboundAnchor is the fragment of the adapter contract heading
// that defines the inbound frames, `session_start` among them.
const sessionStartInboundAnchor = "adapter-contract.md#inbound-messages-adapter-writes-to-your-stdin"

// sessionStartCarrierSite is one docs/ sentence that names the
// `session_start` frame as the carrier of a per-session field.
type sessionStartCarrierSite struct {
	// page is the docs-relative path of the page.
	page string
	// marker is a phrase unique to the sentence on that page.
	marker string
}

// sessionStartCarrierSites lists the sentences outside the reference home and
// the runtime-author guide that name the frame as a per-session carrier.
var sessionStartCarrierSites = []sessionStartCarrierSite{
	{"about/why-lenny.md", "delivery of the chosen variant in each session's"},
	{"api/admin.md", "variant context in each session's"},
	{"api/admin.md", "Regardless of who decides the assignment, the gateway delivers `experimentContext`"},
	{"api/rest.md", "Replayed sessions receive experiment context in their"},
	{"getting-started/architecture.md", "and opens each session with a"},
	{"getting-started/architecture.md", "Reads the adapter manifest to discover available tools, and reads each session's"},
	{"operator-guide/configuration.md", "variant delivery in each session's"},
	{"getting-started/concepts.md", "**Variant context delivery.**"},
}

// spec: 28.5.3 (CH-MSGSOCK session_start frame), 10.7 (Experiment Primitives)
// diagnosis: a docs/ sentence names the session's `session_start` frame as the
//
//	carrier of experiment context or the credential path but does not link to
//	the adapter contract's inbound-frame definition, so a reader of that page
//	has no route to the frame's fields. A failure names the page and the
//	sentence whose link is missing or points at the wrong relative path.
func TestSessionStartCarrierSentencesLinkToAdapterContract(t *testing.T) {
	root := repoRoot(t)
	// Each site is a single-line paragraph or list item, so the check reads the
	// marker's own line and two sites in one list cannot vouch for each other.
	for _, site := range sessionStartCarrierSites {
		body := readRepoFile(t, root, "docs", filepath.FromSlash(site.page))
		line := lineContaining(body, site.marker)
		if line == "" {
			t.Errorf("docs/%s: sentence %q not found (renamed or removed?)", site.page, site.marker)
			continue
		}
		if !strings.Contains(line, "`session_start`") {
			t.Errorf("docs/%s: sentence %q no longer names the `session_start` frame", site.page, site.marker)
			continue
		}
		want := relativeReferenceLink(site.page) + sessionStartInboundAnchor
		if !strings.Contains(line, "("+want+")") {
			t.Errorf("docs/%s: sentence %q names the `session_start` frame without a link to %s", site.page, site.marker, want)
		}
	}
}

// relativeReferenceLink returns the relative prefix from a docs page to the
// docs/reference directory.
func relativeReferenceLink(page string) string {
	depth := strings.Count(page, "/")
	return strings.Repeat("../", depth) + "reference/"
}
