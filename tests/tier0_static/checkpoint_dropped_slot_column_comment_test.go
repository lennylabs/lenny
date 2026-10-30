// SPDX-License-Identifier: MIT

package tier0_static

import (
	"regexp"
	"strings"
	"testing"
)

// Migration 0180 drops the slot column from both checkpoint tables, so neither
// session_checkpoints nor checkpoint_manifest carries one. A comment that
// justifies a rule by asserting, in the present tense, that one of those tables
// still holds the column sends a reader to a column the schema does not have
// and makes the rule's stated reason false. The claim survives most easily in
// the gates that reason about the drop itself, where the sibling table is named
// to explain why a lookup is scoped to a table. This gate reports that class of
// claim across the checkpoint comment set and the tier-11 pipeline gate that
// resolves the retirement to this migration.

// checkpointDroppedSlotColumnFiles are the checkpoint test files this gate
// covers beyond the production tree. It extends the set the scoping-key and
// slot-absence gates share with the tier-11 consistency gate, whose subject is
// the manifest column drop and which therefore names the sibling table more
// than any other file.
var checkpointDroppedSlotColumnFiles = append(
	append([]string(nil), checkpointScopingCommentFiles...),
	"tests/tier11_docs/checkpoint_pipeline_consistency_test.go",
)

// checkpointTableHoldsSlotColumn matches a present-tense assertion that one of
// the two checkpoint tables holds the dropped slot column. The table name, a
// possession verb, and the column must fall inside one sentence, so a sentence
// that names the drop ("migration 0180 dropped session_checkpoints.slot_id")
// reads clean and a past-tense statement about the pre-drop schema is not a
// claim about the current one.
var checkpointTableHoldsSlotColumn = regexp.MustCompile(
	`(?i)\b(session_checkpoints|checkpoint_manifest)\b[^.]{0,60}?\b(carries|carry|has|have|contains|contain|keeps|keep)\b[^.]{0,40}?\bslot_id\b`,
)

// diagnosis: a comment in the checkpoint pipeline, or in a checkpoint gate
// beside it, states that session_checkpoints or checkpoint_manifest carries the
// slot column. Migration 0180 dropped it from both tables, so the claim
// describes a schema the repository no longer has, and any rule it is offered
// as the reason for reads as unmotivated. Restate the comment on the drop:
// name the migration that performed it, and say that a drop of the same column
// name from a sibling table must not resolve the other table's record.
//
// spec: 10.1 (the manifest column enumeration and the session scoping key),
// 12.5 (the checkpoint retention catalog and the supersede rules the
// dropped columns keyed)
func TestCheckpointCommentsClaimNoDroppedSlotColumn_spec_10_1(t *testing.T) {
	offenses := checkpointCommentOffensesIn(t, checkpointTableHoldsSlotColumn, checkpointDroppedSlotColumnFiles)
	if len(offenses) > 0 {
		t.Errorf("comments assert that a checkpoint table carries the dropped slot column:\n%s", strings.Join(offenses, "\n"))
	}
}

// checkpointDroppedSlotColumnCases pin the matcher to a present-tense
// possession claim about a checkpoint table, so that prose naming the drop or
// the pre-drop schema in the past tense reads clean.
var checkpointDroppedSlotColumnCases = []struct {
	name    string
	comment string
	banned  bool
}{
	{"sibling table holds its own", "a drop of the same column name from a sibling table never satisfies the record: session_checkpoints carries a slot_id of its own.", true},
	{"sibling table holds it alongside", "session_checkpoints carries its own slot_id alongside checkpoint_manifest's.", true},
	{"manifest holds a column", "checkpoint_manifest has a slot_id column the reassembly predicate reads.", true},
	{"claim wrapped across comment lines", "the lookup is table-scoped because session_checkpoints\ncarries a slot_id of its own.", true},
	{"the drop named in the past tense", "migration 0180 dropped session_checkpoints.slot_id alongside checkpoint_manifest's.", false},
	{"the drop named in the present tense", "migration 0180 drops the slot column from session_checkpoints in the same change as checkpoint_manifest's.", false},
	{"table scoping stated without the claim", "a drop of slot_id from a sibling table must not resolve a checkpoint_manifest record.", false},
	{"the column named alone", "the supersede rule is keyed on session_id, so slot_id names no key the schema has.", false},
}

// diagnosis: the dropped-slot-column gate's matcher no longer matches the claim
// it enforces. A false negative lets a comment assert that a checkpoint table
// still carries the column migration 0180 dropped; a false positive bans
// correct prose that names the drop or the pre-drop schema.
//
// spec: 10.1 (the manifest column enumeration and the session scoping key),
// 12.5 (the checkpoint retention catalog and the supersede rules the
// dropped columns keyed)
func TestCheckpointDroppedSlotColumnGateMatchesThePossessionClaimOnly_spec_10_1(t *testing.T) {
	for _, tc := range checkpointDroppedSlotColumnCases {
		t.Run(tc.name, func(t *testing.T) {
			// Line breaks are folded to spaces the way the file walker folds
			// them, so a claim wrapped across two comment lines is one site.
			got := checkpointTableHoldsSlotColumn.FindString(strings.Join(strings.Fields(tc.comment), " "))
			if tc.banned && got == "" {
				t.Errorf("comment asserts a checkpoint table carries the dropped slot column but was not reported: %q", tc.comment)
			}
			if !tc.banned && got != "" {
				t.Errorf("comment names the drop but was reported as %q: %q", got, tc.comment)
			}
		})
	}
}
