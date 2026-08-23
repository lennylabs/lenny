// SPDX-License-Identifier: MIT

package tier0_static

import (
	"regexp"
	"strings"
	"testing"
)

// Every session is bound to a slot on every pod, whatever the pool's
// concurrency, and a session-mode slot's identifier is its session's
// identifier. A binding's slot identifier is populated exactly when the slot
// reservation returned a slot result, so an empty one records that the pod
// keeps no counted slot ledger and is the key the two release paths dispatch
// on. A comment that explains an empty slot identifier by asserting that the
// session names or has no slot restates the retired conditional rule, and it
// files the test that carries it against a spec section stating the opposite.
// This gate reports that class of claim in the same comment set the scoping-key
// gate covers.

// slotAbsenceClaim matches an assertion that a session, a bind, or a pod names
// or has no slot at all. The negative lookahead keeps a statement about an
// empty identifier or an absent wire field out of the match, because those name
// a field's value rather than deny that the slot exists.
var slotAbsenceClaim = regexp.MustCompile(`(?i)\b(names?|naming|has|have|had|is bound to|are bound to)\s+no\s+slot\b(?:\s*[-_ ]?(?:id|ids|identifier|identifiers)\b)?`)

// slotAbsenceFieldSuffix matches the trailing field noun that makes a "no slot"
// phrase a statement about an identifier's value. A match that ends in one is
// not an absence claim.
var slotAbsenceFieldSuffix = regexp.MustCompile(`(?i)(id|ids|identifier|identifiers)$`)

// slotAbsenceClaimIn returns the absence claim a comment makes, or the empty
// string when it makes none.
func slotAbsenceClaimIn(comment string) string {
	text := strings.Join(strings.Fields(comment), " ")
	for _, m := range slotAbsenceClaim.FindAllString(text, -1) {
		if slotAbsenceFieldSuffix.MatchString(m) {
			continue
		}
		return m
	}
	return ""
}

// diagnosis: a comment in the checkpoint pipeline, or in a checkpoint test
// beside it, explains an empty binding slot identifier by asserting that the
// session names or has no slot. Every session is bound to a slot on every pod,
// so the claim contradicts the section it sits under and misfiles the test that
// carries it. Restate the comment on what the member records: it is set exactly
// when a slot reservation returned a slot result, and the release paths key on
// it.
//
// spec: 5.2 (a session-mode slot's identifier is its session's identifier, and
// maxConcurrentSessions: 1 is the smallest value of the slot ceiling rather
// than a special case of it)
func TestCheckpointCommentsClaimNoSlotAbsence_spec_5_2(t *testing.T) {
	offenses := checkpointCommentOffenses(t, slotAbsenceClaim)
	var kept []string
	for _, o := range offenses {
		if slotAbsenceFieldSuffix.MatchString(o) {
			continue
		}
		kept = append(kept, o)
	}
	if len(kept) > 0 {
		t.Errorf("comments assert that a bound session names no slot, which every session having a slot contradicts:\n%s", strings.Join(kept, "\n"))
	}
}

// slotAbsenceClaimCases pin the matcher to the denial of a slot's existence
// rather than to any sentence containing "no slot", so that a statement about
// an empty identifier or an absent wire field reads clean.
var slotAbsenceClaimCases = []struct {
	name    string
	comment string
	banned  bool
}{
	{"exclusive bind names no slot", "an exclusive maxConcurrentSessions=1 bind claims the whole pod for the session and names no slot.", true},
	{"pod has no slot", "the exclusive pod has no slot, so the release takes the other path", true},
	{"session is bound to no slot", "on an exclusive pool the session is bound to no slot.", true},
	{"empty identifier", "the base-mode harness registers a binding with no slot identifier", false},
	{"absent wire field", "the recycle request carries no slot_id and names the last-released session", false},
	{"no reservation", "an exclusive-pool bind reserves no slot and keeps an empty BindResult.SlotID", false},
	{"every session has one", "every session is bound to a slot whose identifier is the session's own", false},
}

// diagnosis: the slot-absence gate's matcher no longer matches the claim it
// enforces. A false negative lets a comment deny that a bound session has a
// slot; a false positive bans correct prose about an empty identifier or an
// absent wire field.
//
// spec: 5.2 (a session-mode slot's identifier is its session's identifier)
func TestSlotAbsenceGateMatchesTheDenialOnly_spec_5_2(t *testing.T) {
	for _, tc := range slotAbsenceClaimCases {
		t.Run(tc.name, func(t *testing.T) {
			got := slotAbsenceClaimIn(tc.comment)
			if tc.banned && got == "" {
				t.Errorf("comment denies that a bound session has a slot but was not reported: %q", tc.comment)
			}
			if !tc.banned && got != "" {
				t.Errorf("comment names an empty identifier or an absent field but was reported as %q: %q", got, tc.comment)
			}
		})
	}
}
