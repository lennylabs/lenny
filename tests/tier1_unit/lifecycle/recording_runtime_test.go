// SPDX-License-Identifier: MIT

package lifecycle_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

// writeFrame hands the runtime one inbound frame.
func writeFrame(t *testing.T, r *lifecycleharness.RecordingRuntime, frame map[string]any) {
	t.Helper()
	b, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("marshal frame: %v", err)
	}
	if err := r.WriteEnvelope("", b); err != nil {
		t.Fatalf("write frame: %v", err)
	}
}

// message returns a message frame for the session.
func message(sessionID, text string) map[string]any {
	return map[string]any{"type": "message", "sessionId": sessionID, "input": []map[string]any{{"inline": text}}}
}

// awaitResponded waits until the runtime wrote n responses for the session.
func awaitResponded(t *testing.T, r *lifecycleharness.RecordingRuntime, sessionID string, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for r.Responded(sessionID) < n {
		if time.Now().After(deadline) {
			t.Fatalf("the runtime wrote %d responses for %s, want %d", r.Responded(sessionID), sessionID, n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// texts returns the text of each message.
func texts(ms []lifecycleharness.Received) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Text)
	}
	return out
}

// TestRecordingRuntimeReportsTurnsTheReleaseEnded_spec_6_2 pins the harness
// runtime the idle-suspension scenarios assert with: it reports a turn whose
// response it wrote only after the release of the turn's binding reached
// it, so a scenario asserting that no release ends a turn can fail. Received and responded counts alone match
// for such a turn, because the runtime still writes the late response.
//
// spec: §6.2 (maxClientIdleSeconds clock behavior across states), §28.5.3
// (CH-MSGSOCK)
func TestRecordingRuntimeReportsTurnsTheReleaseEnded_spec_6_2(t *testing.T) {
	const id = "sess-a"

	t.Run("a turn answered before the release is not reported", func(t *testing.T) {
		r := lifecycleharness.NewRecordingRuntime()
		writeFrame(t, r, map[string]any{"type": "session_start", "sessionId": id})
		writeFrame(t, r, message(id, "early"))
		awaitResponded(t, r, id, 1)
		writeFrame(t, r, map[string]any{"type": "session_end", "sessionId": id})
		if got := r.TurnsNotAnsweredBeforeRelease(id); len(got) != 0 {
			t.Errorf("turns reported = %v, want none", texts(got))
		}
	})

	t.Run("a turn in hand when session_end arrives is reported", func(t *testing.T) {
		r := lifecycleharness.NewRecordingRuntime()
		writeFrame(t, r, map[string]any{"type": "session_start", "sessionId": id})
		r.SetResponseDelay(100 * time.Millisecond)
		writeFrame(t, r, message(id, "cut"))
		writeFrame(t, r, map[string]any{"type": "session_end", "sessionId": id})
		awaitResponded(t, r, id, 1)
		if got, done := len(r.Received(id)), r.Responded(id); got != done {
			t.Fatalf("received %d and responded %d; the counts are expected to match for a late response", got, done)
		}
		got := r.TurnsNotAnsweredBeforeRelease(id)
		if len(got) != 1 || got[0].Text != "cut" {
			t.Errorf("turns reported = %v, want [cut]", texts(got))
		}
	})

	t.Run("a turn not yet answered is reported", func(t *testing.T) {
		r := lifecycleharness.NewRecordingRuntime()
		writeFrame(t, r, map[string]any{"type": "session_start", "sessionId": id})
		r.Hold()
		defer r.Release()
		writeFrame(t, r, message(id, "pending"))
		if got := r.TurnsNotAnsweredBeforeRelease(id); len(got) != 1 {
			t.Errorf("turns reported = %v, want [pending]", texts(got))
		}
	})

	t.Run("an earlier binding's release does not mark a later binding's turn", func(t *testing.T) {
		r := lifecycleharness.NewRecordingRuntime()
		writeFrame(t, r, map[string]any{"type": "session_start", "sessionId": id})
		writeFrame(t, r, map[string]any{"type": "session_end", "sessionId": id})
		writeFrame(t, r, map[string]any{"type": "session_start", "sessionId": id})
		writeFrame(t, r, message(id, "rebound"))
		awaitResponded(t, r, id, 1)
		if got := r.TurnsNotAnsweredBeforeRelease(id); len(got) != 0 {
			t.Errorf("turns reported = %v, want none", texts(got))
		}
	})

	t.Run("another session's release does not mark the session's turn", func(t *testing.T) {
		r := lifecycleharness.NewRecordingRuntime()
		writeFrame(t, r, map[string]any{"type": "session_start", "sessionId": id})
		writeFrame(t, r, map[string]any{"type": "session_start", "sessionId": "sess-b"})
		r.SetResponseDelay(50 * time.Millisecond)
		writeFrame(t, r, message(id, "mine"))
		writeFrame(t, r, map[string]any{"type": "session_end", "sessionId": "sess-b"})
		awaitResponded(t, r, id, 1)
		if got := r.TurnsNotAnsweredBeforeRelease(id); len(got) != 0 {
			t.Errorf("turns reported = %v, want none", texts(got))
		}
	})
}
