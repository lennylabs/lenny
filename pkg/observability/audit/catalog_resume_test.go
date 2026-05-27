// SPDX-License-Identifier: MIT

package audit

import "testing"

// TestSessionResumeRetryEventsCatalogued asserts the §7.3 retry/resume
// lifecycle audit events surfaced through F-7.3.25 are present in the
// catalog. spec: §7.3 lines 397-427; §11.7 hash-chained audit log.
func TestSessionResumeRetryEventsCatalogued(t *testing.T) {
	want := []EventType{
		EventSessionResumed,
		EventSessionRetryAttempted,
		EventSessionAwaitingActionEntered,
		EventSessionExpiredInAwaitingAction,
		EventSessionCascadeApplied,
	}
	for _, et := range want {
		if !IsKnownEventType(et) {
			t.Errorf("F-7.3.25: §7.3 retry/resume audit event %q must be in the catalog", et)
		}
	}
}

// TestSessionResumeRetryEventStrings pins the wire format of the §7.3
// audit event types. The strings match the §11.7 audit row event_type
// field. spec: §11.7 closed-enum contract.
func TestSessionResumeRetryEventStrings(t *testing.T) {
	cases := map[EventType]string{
		EventSessionResumed:                 "session.resumed",
		EventSessionRetryAttempted:          "session.retry_attempted",
		EventSessionAwaitingActionEntered:   "session.awaiting_action_entered",
		EventSessionExpiredInAwaitingAction: "session.expired_in_awaiting_action",
		EventSessionCascadeApplied:          "session.cascade_applied",
	}
	for et, want := range cases {
		if got := string(et); got != want {
			t.Errorf("F-7.3.25: event type wire string = %q, want %q", got, want)
		}
	}
}
