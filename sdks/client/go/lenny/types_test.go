// SPDX-License-Identifier: MIT

package lenny

import (
	"encoding/json"
	"testing"
)

// spec: §7.1 sessionIsolationLevel — the create response carries a
// conversationContinuity field whose value-set is platform|none keyed
// off the executionMode session|service value-set. The SDK decodes both
// fields verbatim from the wire envelope.
func TestIsolationLevelDecodesConversationContinuity(t *testing.T) {
	cases := []struct {
		name         string
		wire         string
		wantMode     string
		wantContinue string
	}{
		{
			name: "session mode preserves platform continuity",
			wire: `{
				"id": "sess_1",
				"sessionIsolationLevel": {
					"executionMode": "session",
					"isolationProfile": "gvisor",
					"podReuse": false,
					"residualStateWarning": false,
					"conversationContinuity": "platform"
				}
			}`,
			wantMode:     "session",
			wantContinue: "platform",
		},
		{
			name: "service mode declares no continuity",
			wire: `{
				"id": "sess_2",
				"sessionIsolationLevel": {
					"executionMode": "service",
					"isolationProfile": "runc",
					"podReuse": true,
					"scrubPolicy": "none",
					"residualStateWarning": true,
					"conversationContinuity": "none"
				}
			}`,
			wantMode:     "service",
			wantContinue: "none",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got CreateSessionResult
			if err := json.Unmarshal([]byte(tc.wire), &got); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if got.IsolationLevel.ExecutionMode != tc.wantMode {
				t.Errorf("ExecutionMode = %q, want %q", got.IsolationLevel.ExecutionMode, tc.wantMode)
			}
			if got.IsolationLevel.ConversationContinuity != tc.wantContinue {
				t.Errorf("ConversationContinuity = %q, want %q", got.IsolationLevel.ConversationContinuity, tc.wantContinue)
			}
		})
	}
}

// spec: §7.1 sessionIsolationLevel — a wire envelope that omits
// conversationContinuity decodes to the empty string rather than
// failing, matching the optional decode of every other isolation field.
func TestIsolationLevelOmittedConversationContinuity(t *testing.T) {
	var got CreateSessionResult
	if err := json.Unmarshal([]byte(`{"sessionIsolationLevel":{"executionMode":"session"}}`), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.IsolationLevel.ConversationContinuity != "" {
		t.Errorf("ConversationContinuity = %q, want empty", got.IsolationLevel.ConversationContinuity)
	}
}
