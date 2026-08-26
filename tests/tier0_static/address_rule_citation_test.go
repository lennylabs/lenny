// SPDX-License-Identifier: MIT

package tier0_static

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/citation"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// addressRuleSentence is the normative sentence that states the adapter
// boundary rule: a session-scoped request whose session identifier is empty
// is refused before any root is resolved. The section that carries this
// sentence is the section the cases pinning the rule cite, and it is derived
// from the specification here rather than written down, so a later move of
// the sentence to another heading moves the demanded citation with it.
const addressRuleSentence = "rejected at the adapter boundary with `InvalidArgument`"

// addressRuleCases is the inventory of cases that assert the adapter address
// guard: the empty and the malformed arm on each leg that carries a session
// address, plus the proto-descriptor case that pins the address onto every
// session-scoped message. Each one is credited to the section that states the
// rule, so the coverage view records the rule as tested where it is written.
var addressRuleCases = map[string][]string{
	"pkg/adapter/session_test.go": {
		"TestStartSessionRejectsAMalformedSessionAddress_spec_5_2",
	},
	"pkg/adapter/staging_test.go": {
		"TestPrepareWorkspaceStagesUnderTheSessionSlotTree",
		"TestPrepareWorkspaceRejectsAMalformedSessionAddress_spec_5_2",
		"TestPrepareWorkspaceRequiresASessionID",
	},
	"pkg/adapter/checkpoint_stream_test.go": {
		"TestCheckpointStreamRefusesAnUnaddressedStart_spec_5_2",
	},
	"tests/tier3_contract/checkpoint_stream/checkpoint_stream_wire_test.go": {
		"TestCheckpointStreamRejectsAnUnaddressedStart",
	},
	"tests/tier3_contract/adapter_session_address/session_address_wire_test.go": {
		"TestSessionScopedRequestsDeclareTheSessionAddress_spec_4_1",
	},
}

// sectionStatingTheAddressRule returns the dotted number of the specification
// section whose prose carries the address rule. It fails when the sentence is
// absent, and when more than one section carries it, because either state
// leaves the section a case must cite undetermined.
func sectionStatingTheAddressRule(t *testing.T) string {
	t.Helper()
	root := schematest.RepoRoot(t)
	entries, err := os.ReadDir(filepath.Join(root, "spec"))
	if err != nil {
		t.Fatalf("read spec directory: %v", err)
	}
	var found []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, "spec", e.Name()))
		if err != nil {
			t.Fatalf("read spec/%s: %v", e.Name(), err)
		}
		content := string(body)
		headings := citation.Headings(content)
		for _, line := range citation.ProseLines(content) {
			if !strings.Contains(line.Text, addressRuleSentence) {
				continue
			}
			number := enclosingSectionNumber(headings, line.Number)
			if number == "" {
				t.Fatalf("spec/%s:%d states the address rule under no numbered heading", e.Name(), line.Number)
			}
			found = append(found, number)
		}
	}
	sort.Strings(found)
	found = dedupeSections(found)
	if len(found) != 1 {
		t.Fatalf("the address rule is stated in section(s) %v; want exactly one section", found)
	}
	return found[0]
}

// enclosingSectionNumber returns the number of the numbered heading that most
// closely precedes the given 1-based line.
func enclosingSectionNumber(headings []citation.Heading, line int) string {
	number := ""
	for _, h := range headings {
		if h.Line > line {
			break
		}
		if h.Number != "" {
			number = h.Number
		}
	}
	return number
}

// dedupeSections collapses a sorted list of section numbers to its distinct
// members.
func dedupeSections(in []string) []string {
	out := in[:0:0]
	for i, id := range in {
		if i > 0 && in[i-1] == id {
			continue
		}
		out = append(out, id)
	}
	return out
}

// spec: §5.2 (a session-scoped request with no usable session identifier is
// rejected at the adapter boundary)
//
// Every case that asserts the adapter address guard cites the section whose
// prose states the rule. A case that cites a neighbouring section instead
// credits a section that says nothing about request addressing, and the
// section that does state the rule gains no coverage from the case that pins
// it; both errors are invisible to the spec-map credit gate, which checks
// that a citation resolves rather than that it names the right section.
func TestAddressGuardCasesCiteTheSectionStatingTheRule(t *testing.T) {
	want := sectionStatingTheAddressRule(t)
	files := make([]string, 0, len(addressRuleCases))
	for file := range addressRuleCases {
		files = append(files, file)
	}
	sort.Strings(files)
	for _, file := range files {
		perCase := annotatedSectionsPerCase(t, file)
		for _, fn := range addressRuleCases[file] {
			sections, ok := perCase[fn]
			if !ok {
				t.Errorf("%s declares no case %s; the address-guard inventory names one", file, fn)
				continue
			}
			if !sections[want] {
				t.Errorf("%s::%s cites section(s) %v; the address rule is stated in §%s and the case must cite it",
					file, fn, sortedSectionIDs(sections), want)
			}
		}
	}
}
