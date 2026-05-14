// SPDX-License-Identifier: MIT

package elicitation

import (
	"testing"
)

// FuzzContentDigest exercises the §9.2 canonical-digest path on
// arbitrary message + schema strings. Invariants:
//
//   - Digest never panics.
//   - Equal Content values produce identical digests (determinism).
//   - The digest is a 64-character hex string when err == nil.
func FuzzContentDigest(f *testing.F) {
	f.Add("hello")
	f.Add("")
	f.Add(string(make([]byte, 1<<14))) // 16 KiB

	f.Fuzz(func(t *testing.T, msg string) {
		c := Content{Message: msg, Schema: map[string]any{"type": "string"}}
		first, err := c.Digest()
		if err != nil {
			return
		}
		second, err := c.Digest()
		if err != nil {
			t.Errorf("Digest not deterministic on second call: %v", err)
			return
		}
		if first != second {
			t.Errorf("digest changed across calls: %q vs %q", first, second)
		}
		if len(first) != 64 {
			t.Errorf("digest length: want 64 hex chars, got %d", len(first))
		}
	})
}

// FuzzProvenanceValidate exercises the §9.2 provenance validator on
// arbitrary inputs. Invariant: never panics.
func FuzzProvenanceValidate(f *testing.F) {
	f.Add("pod-1", 0, "claude-code", "tool-call", "connector-foo", "acme.com", "connector")
	f.Add("", 0, "", "", "", "", "")
	f.Add("pod-1", -1, "", "", "", "", "agent")
	f.Add("pod-1", 99, "", "", "", "", "connector")

	f.Fuzz(func(t *testing.T,
		origin string, depth int, runtime, purpose, connector, domain, initiatorType string,
	) {
		p := Provenance{
			OriginPod:       origin,
			DelegationDepth: depth,
			OriginRuntime:   runtime,
			Purpose:         purpose,
			ConnectorID:     connector,
			ExpectedDomain:  domain,
			InitiatorType:   InitiatorType(initiatorType),
		}
		_ = p.Validate()
	})
}
