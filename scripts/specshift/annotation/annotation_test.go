// SPDX-License-Identifier: MIT

package annotation

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The annotation rule is a project rule in .claude/rules/spec-annotations.md
// rather than a behavior the specification states, so the tests of its
// tooling carry no specification annotation.

// readFixture reads a fixture file under testdata/.
func readFixture(t *testing.T, parts ...string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(append([]string{"testdata"}, parts...)...))
	if err != nil {
		t.Fatalf("read fixture %v: %v", parts, err)
	}
	return body
}

func TestItemsSplitAtTopLevelSeparatorsOnly(t *testing.T) {
	t.Parallel()
	got := Items("§4.7 (Pod claims, release), POOL-R5; 4.8 (a (nested, list)), ")
	want := []string{"§4.7 (Pod claims, release)", "POOL-R5", "4.8 (a (nested, list))"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Items = %q, want %q", got, want)
	}
}

func TestSectionCitationsCountsEachCitedSection(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		payload string
		want    int
	}{
		{"§4.7 (Pod claims)", 1},
		{"4.7.11 (Separate UIDs), 28.5.3", 2},
		{"§4.7 / §5.1", 2},
		{"§4.7 (see §5.1 for the hold)", 1},
		{"POOL-R5, KPCLAIM-create", 0},
		{"POOL-R5, §4.7", 1},
		{"N8, F-4.7.22", 0},
		{"", 0},
	} {
		if got := SectionCitations(tc.payload); got != tc.want {
			t.Errorf("SectionCitations(%q) = %d, want %d", tc.payload, got, tc.want)
		}
	}
}

func TestClassifyReadsEachIdentifierForm(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		token string
		want  IdentifierKind
		ok    bool
	}{
		{"POOL-R5", Requirement, true},
		{"CLAIM-I4", Issue, true},
		{"KPCLAIM-create", Operation, true},
		{"ADAPTERRPC-ReportPodScrub", Operation, true},
		{"setting-maxidleseconds", DefinedValue, true},
		{"admission-refusal-pool-immutable", DefinedValue, true},
		{"GWCLAIM", PageCode, true},
		{"LNK-GWCONTROL", "", false},
		{"CH-SUPERVISE", "", false},
		{"REG-PODSTATE", "", false},
		{"state-machine", DefinedValue, true},
		{"error wrapping", "", false},
		{"N8", "", false},
		{"§4.7", "", false},
	} {
		got, ok := Classify(tc.token)
		if got != tc.want || ok != tc.ok {
			t.Errorf("Classify(%q) = %q, %v; want %q, %v", tc.token, got, ok, tc.want, tc.ok)
		}
	}
}

func TestIdentifiersReadsQualifiersAndSkipsSectionHeadings(t *testing.T) {
	t.Parallel()
	got := Identifiers("CLAIM-R4 (partial: CLAIM-I4), removal pending CLAIM-I2, " +
		"§4.7 (Pod claims, error-model), KPCLAIM-create, the hold, LNK-GWCONTROL")
	want := []string{"CLAIM-R4", "CLAIM-I4", "CLAIM-I2", "KPCLAIM-create"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Identifiers = %q, want %q", got, want)
	}
}

func TestPayloadReadsTheLabelAfterTheCommentMarker(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		raw     string
		payload string
		ok      bool
	}{
		{"// spec: POOL-R5", "POOL-R5", true},
		{"//spec:POOL-R5", "POOL-R5", true},
		{"/* spec: POOL-R5 */", "POOL-R5", true},
		{"// the spec: is elsewhere", "", false},
		{"// specification: POOL-R5", "", false},
	} {
		payload, ok := Payload(CommentText(tc.raw))
		if payload != tc.payload || ok != tc.ok {
			t.Errorf("Payload(%q) = %q, %v; want %q, %v", tc.raw, payload, ok, tc.payload, tc.ok)
		}
	}
}

func TestAnnotationsJoinWrappedLinesAndIgnoreStringLiterals(t *testing.T) {
	t.Parallel()
	src := readFixture(t, "ratchet", "two.go.txt")
	got := Annotations(Comments("two.go", src))
	if len(got) != 2 {
		t.Fatalf("read %d annotation(s), want the two comments and not the string literal: %+v", len(got), got)
	}
	first := got[0]
	if first.Line != 7 || first.EndLine != 8 {
		t.Errorf("the wrapped annotation spans lines %d to %d, want 7 to 8", first.Line, first.EndLine)
	}
	if want := "§4.7 (Claim hold and release rules, expiry), 4.8 (Release)"; first.Payload != want {
		t.Errorf("the wrapped payload reads %q, want %q", first.Payload, want)
	}
	if got[1].Payload != "CLAIM-R4" || got[1].EndLine != got[1].Line {
		t.Errorf("the one-line annotation reads %+v, want CLAIM-R4 on one line", got[1])
	}
}

func TestAnnotationsStopAtALabelledLine(t *testing.T) {
	t.Parallel()
	lines := []CommentLine{
		{Line: 1, Text: " spec: CLAIM-R4,"},
		{Line: 2, Text: " diagnosis: the claim was not refused."},
	}
	got := Annotations(lines)
	if len(got) != 1 || got[0].EndLine != 1 {
		t.Errorf("the annotation continued onto a labelled line: %+v", got)
	}
}
