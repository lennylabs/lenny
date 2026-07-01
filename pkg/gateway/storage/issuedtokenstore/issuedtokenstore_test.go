// SPDX-License-Identifier: MIT

package issuedtokenstore

import (
	"slices"
	"testing"
)

// TestAudienceListArgPrefersExplicitList exercises the §4.3 line 193
// audiences[] column writer. When the caller supplies an explicit
// multi-value list the function returns it verbatim with empty entries
// dropped.
// spec: §4.3 line 193, migration 0058.
func TestAudienceListArgPrefersExplicitList(t *testing.T) {
	t.Parallel()
	got := audienceListArg([]string{"lenny-gateway", "lenny-ops"}, "ignored")
	want := []string{"lenny-gateway", "lenny-ops"}
	if !slices.Equal(got, want) {
		t.Errorf("audienceListArg = %v, want %v", got, want)
	}
}

// TestAudienceListArgFallsBackToLegacySingle covers the pre-0058 caller
// path where only the legacy single-valued Audience string is set.
// "lenny-gateway" must arrive as ["lenny-gateway"].
// spec: §4.3 line 193, migration 0058.
func TestAudienceListArgFallsBackToLegacySingle(t *testing.T) {
	t.Parallel()
	got := audienceListArg(nil, "lenny-gateway")
	want := []string{"lenny-gateway"}
	if !slices.Equal(got, want) {
		t.Errorf("audienceListArg = %v, want %v", got, want)
	}
}

// TestAudienceListArgFallsBackToLegacyJoined covers the pre-0058
// space-joined form. "lenny-gateway lenny-ops" must arrive as the
// matching list. The tokenservice handler historically wrote
// strings.Join(audiences, " ") into the legacy column so the split
// must reverse that path.
// spec: §4.3 line 193, migration 0058.
func TestAudienceListArgFallsBackToLegacyJoined(t *testing.T) {
	t.Parallel()
	got := audienceListArg(nil, "lenny-gateway lenny-ops")
	want := []string{"lenny-gateway", "lenny-ops"}
	if !slices.Equal(got, want) {
		t.Errorf("audienceListArg = %v, want %v", got, want)
	}
}

// TestAudienceListArgEmptyArgsYieldEmptySlice covers the no-audience
// path. The audiences[] column has a NOT NULL DEFAULT '{}'::text[] so
// the writer must never pass nil.
// spec: §4.3 line 193, migration 0058.
func TestAudienceListArgEmptyArgsYieldEmptySlice(t *testing.T) {
	t.Parallel()
	got := audienceListArg(nil, "")
	if got == nil {
		t.Fatal("audienceListArg(nil, \"\") = nil, want []string{}")
	}
	if len(got) != 0 {
		t.Errorf("audienceListArg(nil, \"\") = %v, want []string{}", got)
	}
}

// TestAudienceListArgStripsEmptyEntries asserts the list-form filter
// drops empty strings. A poorly-constructed exchange might emit an
// empty token in the audience list; the audiences[] column must not
// carry it through to forensic queries.
// spec: §4.3 line 193, migration 0058.
func TestAudienceListArgStripsEmptyEntries(t *testing.T) {
	t.Parallel()
	got := audienceListArg([]string{"lenny-gateway", "", "lenny-ops", ""}, "")
	want := []string{"lenny-gateway", "lenny-ops"}
	if !slices.Equal(got, want) {
		t.Errorf("audienceListArg = %v, want %v", got, want)
	}
}

// TestSplitAudienceFallbackHandlesWhitespace covers the broader
// whitespace path used by the legacy reverse-fill: tabs and newlines
// also split. The §13.3 legacy writer used only spaces, but the
// reverse-fill should accept whatever a hand-edited migration backfill
// emitted.
// spec: §4.3 line 193, migration 0058.
func TestSplitAudienceFallbackHandlesWhitespace(t *testing.T) {
	t.Parallel()
	got := splitAudienceFallback("a\tb c\nd")
	want := []string{"a", "b", "c", "d"}
	if !slices.Equal(got, want) {
		t.Errorf("splitAudienceFallback = %v, want %v", got, want)
	}
}
