// SPDX-License-Identifier: MIT

package executor

import "testing"

// TestResolveFromBlockDefault_spec_15_4_1 — a message with no From
// attribution (a top-level client turn) resolves to the gateway-client
// identity used before F-13.5.11. spec: §15.4.1 lines 1696-1707.
func TestResolveFromBlockDefault_spec_15_4_1(t *testing.T) {
	got := resolveFromBlock(Message{Role: "user", Content: "hi"})
	if got.Kind != "client" || got.ID != "client_gateway" {
		t.Errorf("unattributed message must default to the gateway-client from-object; got %+v", got)
	}
}

// TestResolveFromBlockAgent_spec_15_4_1 — an inter-session message
// carrying an authenticated sender is stamped verbatim as kind `agent`,
// so the runtime sees the §15.4.1 sender attribution rather than a forged
// or default client origin. spec: §15.4.1 lines 1696-1707; §13.5
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
