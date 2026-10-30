// SPDX-License-Identifier: MIT

package line

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestApplyEditsRefusesTwoRewritesThatCoverTheSameBytes pins the splice as
// fail-closed. Every span is measured against the file as it stood, so two
// spans that overlap cannot both be spliced: the second indexes into a
// string the first already shortened and cuts bytes belonging to neither,
// which in a UTF-8 carrier cuts a character in half. The planner holds
// each widened span inside its neighbours, and this is the check that
// stops a plan that got it wrong from being written.
//
// spec: §28.1 (N8, the citation rule: a citation of the retired form is
// replaced by the anchor of the section it names, and the carrier is
// otherwise left as it stood)
func TestApplyEditsRefusesTwoRewritesThatCoverTheSameBytes(t *testing.T) {
	t.Parallel()
	const text = "the claim is a lease the gateway grants"
	got, err := applyEdits(text, []edit{{start: 4, end: 12}, {start: 10, end: 20}})
	if err == nil {
		t.Fatalf("the splice wrote two rewrites covering the same bytes, as %q", got)
	}
	if !strings.Contains(err.Error(), "cover the same bytes") {
		t.Errorf("the refusal does not state why the two rewrites cannot be written: %v", err)
	}
	if got != "" {
		t.Errorf("the refused splice returned text: %q", got)
	}
}

// TestApplyEditsSplicesEveryDisjointRewrite pins the splice over the plan
// the pass produces, which is a set of disjoint spans in source order, and
// holds it to leaving the carrier's remaining bytes whole.
//
// spec: §28.1 (N8, the citation rule: a citation of the retired form is
// replaced by the anchor of the section it names, and the carrier is
// otherwise left as it stood)
func TestApplyEditsSplicesEveryDisjointRewrite(t *testing.T) {
	t.Parallel()
	const text = "the claim — a lease — the gateway grants"
	got, err := applyEdits(text, []edit{{start: 0, end: 3, text: "each"}, {start: 16, end: 21, text: "renewal"}})
	if err != nil {
		t.Fatalf("the splice refused a disjoint plan: %v", err)
	}
	const want = "each claim — a renewal — the gateway grants"
	if got != want {
		t.Errorf("the splice produced %q, want %q", got, want)
	}
	if !utf8.ValidString(got) {
		t.Errorf("the splice left an incomplete character: %q", got)
	}
}
