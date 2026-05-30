// SPDX-License-Identifier: MIT

package sessioninbox

import (
	"encoding/json"
	"testing"
	"time"
)

// spec: §15.4.1 lines 1760-1782 — the message_expired envelope carries
// schemaVersion, type, messageId, targetSessionId, reason, expiredAt.
func TestNewMessageExpiredEvent_Schema_spec_15_4_1(t *testing.T) {
	now := time.Date(2026, 5, 30, 12, 34, 56, 0, time.UTC)
	ev := NewMessageExpiredEvent("msg_abc", "sess_t", ReasonTargetTerminated, now)
	b, _ := json.Marshal(ev)
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]any{
		"schemaVersion":   float64(1),
		"type":            "message_expired",
		"messageId":       "msg_abc",
		"targetSessionId": "sess_t",
		"reason":          "target_terminated",
		"expiredAt":       "2026-05-30T12:34:56Z",
	} {
		if got[k] != want {
			t.Errorf("%s = %v, want %v", k, got[k], want)
		}
	}
}

// spec: §7.2 line 284 — the inbox_cleared event carries type, reason,
// clearedAt, sessionId, messagesPreservedInDLQ.
func TestNewInboxClearedEvent_Schema_spec_7_2_284(t *testing.T) {
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	ev := NewInboxClearedEvent("sess_t", 3, now)
	if ev.Type != EventInboxCleared || ev.Reason != "coordinator_failover" {
		t.Fatalf("type/reason = %q/%q", ev.Type, ev.Reason)
	}
	if ev.SessionID != "sess_t" || ev.MessagesPreservedInDLQ != 3 {
		t.Fatalf("session/preserved = %q/%d", ev.SessionID, ev.MessagesPreservedInDLQ)
	}
	if ev.ClearedAt != "2026-05-30T12:00:00Z" {
		t.Fatalf("clearedAt = %q", ev.ClearedAt)
	}
}
