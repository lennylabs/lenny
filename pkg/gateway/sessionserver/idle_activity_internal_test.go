// SPDX-License-Identifier: MIT

package sessionserver

import "testing"

// spec: §6.2 lines 273-274 — only agent_output / tool_use events (published
// as response / response_degraded / tool_use*) reset the idle clock;
// lifecycle, inbound, and warning events do not. F-11.3.7.
func TestIsAgentActivityEvent_spec_6_2_273(t *testing.T) {
	activity := []string{"response", "response_degraded", "agent_output", "tool_use", "tool_use_requested", "tool_use_completed"}
	for _, et := range activity {
		if !isAgentActivityEvent(et) {
			t.Errorf("isAgentActivityEvent(%q) = false, want true", et)
		}
	}
	notActivity := []string{"status_change", "message_delivered", "session_complete", "workspace_plan_warning", "session.resumed", "checkpoint_boundary", ""}
	for _, et := range notActivity {
		if isAgentActivityEvent(et) {
			t.Errorf("isAgentActivityEvent(%q) = true, want false", et)
		}
	}
}

// recordingStamper captures Stamp calls for the publishEvent gating test.
type recordingStamper struct{ calls []string }

func (r *recordingStamper) Stamp(_, sessionID string) { r.calls = append(r.calls, sessionID) }

// publishEvent stamps idle activity only for qualifying event types, even
// when no event bus is wired. F-11.3.7.
func TestPublishEventStampsQualifyingActivity_spec_6_2_273(t *testing.T) {
	rec := &recordingStamper{}
	s := &Server{activityStamper: rec} // no events bus wired
	s.publishEvent("acme", "sess", "response", map[string]any{"type": "text"})
	s.publishEvent("acme", "sess", "status_change", map[string]any{"state": "running"})
	s.publishEvent("acme", "sess", "tool_use_requested", map[string]any{})
	if len(rec.calls) != 2 {
		t.Fatalf("Stamp call count = %d, want 2 (response + tool_use_requested)", len(rec.calls))
	}
	for _, id := range rec.calls {
		if id != "sess" {
			t.Errorf("stamped session id = %q, want sess", id)
		}
	}
}
