// SPDX-License-Identifier: MIT

package executor

import "testing"

// TestResolveFromBlockDefault_spec_15_4_1 — a message with no From
// attribution (a top-level client turn) resolves to the gateway-client
// identity used before F-13.5.11. spec: §15.4.
func TestResolveFromBlockDefault_spec_15_4_1(t *testing.T) {
	got := resolveFromBlock(Message{Role: "user", Content: "hi"})
	if got.Kind != "client" || got.ID != "client_gateway" {
		t.Errorf("unattributed message must default to the gateway-client from-object; got %+v", got)
	}
}

// TestResolveFromBlockAgent_spec_15_4_1 — an inter-session message
// carrying an authenticated sender is stamped verbatim as kind `agent`,
// so the runtime sees the §15.4 sender attribution rather than a forged
// or default client origin. spec: §15.4; §13.5
// mitigation 6. F-13.5.11.
func TestResolveFromBlockAgent_spec_15_4_1(t *testing.T) {
	got := resolveFromBlock(Message{
		Role:    "user",
		Content: "hi",
		From:    MessageFrom{Kind: "agent", ID: "sess_b"},
	})
	if got.Kind != "agent" || got.ID != "sess_b" {
		t.Errorf("attributed message must carry the sending session under kind agent; got %+v", got)
	}
}

// TestSenderFromNamesTheSendingAgent_spec_15_4_1 asserts that SenderFrom
// attributes a message to the sending session as an agent, and that an empty
// sender leaves the attribution to the executor's default.
//
// spec: §15.4 (MessageEnvelope from), §7.2 (Message delivery routing — seven
// paths, path 6)
func TestSenderFromNamesTheSendingAgent_spec_15_4_1(t *testing.T) {
	if got := SenderFrom("sess-a"); got != (MessageFrom{Kind: "agent", ID: "sess-a"}) {
		t.Errorf("SenderFrom(sess-a) = %+v, want agent sess-a", got)
	}
	if got := SenderFrom(""); got != (MessageFrom{}) {
		t.Errorf("SenderFrom(\"\") = %+v, want the zero value", got)
	}
}
