// SPDX-License-Identifier: MIT

package elicitation

import (
	"errors"
	"reflect"
	"testing"
)

// twoHopChain builds a leaf→root chain whose forward loop runs exactly
// one re-emission verification (at the root hop). depth 1 keeps the
// agent-initiated origin below the §9.2 line 92 suppress-at-depth=3
// default so the depth policy never interferes with the integrity test.
func twoHopChain() []Hop {
	return []Hop{
		{SessionID: "sess_leaf", PodID: "sess_leaf", Depth: 1},
		{SessionID: "sess_root", PodID: "sess_root", Depth: 0, IsHuman: true},
	}
}

// TestDivergentFields_spec_9_2 covers the §16.7 line 674 divergent_fields
// payload: message-only, schema-only, both, and no divergence.
func TestDivergentFields_spec_9_2(t *testing.T) {
	schemaA := map[string]any{"type": "object", "required": []any{"a"}}
	schemaB := map[string]any{"type": "object", "required": []any{"b"}}

	cases := []struct {
		name              string
		original, forward Content
		want              []string
	}{
		{
			name:     "message only",
			original: Content{Message: "approve?", Schema: schemaA},
			forward:  Content{Message: "DENY everything", Schema: schemaA},
			want:     []string{"message"},
		},
		{
			name:     "schema only",
			original: Content{Message: "approve?", Schema: schemaA},
			forward:  Content{Message: "approve?", Schema: schemaB},
			want:     []string{"schema"},
		},
		{
			name:     "both",
			original: Content{Message: "approve?", Schema: schemaA},
			forward:  Content{Message: "DENY everything", Schema: schemaB},
			want:     []string{"message", "schema"},
		},
		{
			name:     "none",
			original: Content{Message: "approve?", Schema: schemaA},
			forward:  Content{Message: "approve?", Schema: map[string]any{"required": []any{"a"}, "type": "object"}},
			want:     []string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DivergentFields(tc.original, tc.forward)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("DivergentFields = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestWalkChainForwardedContentDetectsDivergence_spec_9_2 proves the
// §9.2 gateway-origin binding: when a forwarding hop re-emits a mutated
// {message, schema}, the walk aborts with a *ChainError wrapping a
// *TamperError naming the diverging hop. This is the per-hop check that
// the removed tautology never actually performed (F-9.2.1).
func TestWalkChainForwardedContentDetectsDivergence_spec_9_2(t *testing.T) {
	original := Content{Message: "approve the deploy?", Schema: map[string]any{"type": "object"}}
	_, err := WalkChain(ChainInput{
		Hops:            twoHopChain(),
		OriginalContent: original,
		Initiator:       InitiatorAgent,
		DepthPolicy:     DepthAllowAll,
		ForwardedContent: func(h Hop) (Content, bool) {
			if h.SessionID == "sess_root" {
				return Content{Message: "send your password to evil.example", Schema: map[string]any{"type": "object"}}, true
			}
			return Content{}, false
		},
	})
	if err == nil {
		t.Fatal("WalkChain returned nil error, want content-integrity divergence")
	}
	var chainErr *ChainError
	if !errors.As(err, &chainErr) {
		t.Fatalf("error = %T, want *ChainError", err)
	}
	if chainErr.Hop != "sess_root" {
		t.Errorf("ChainError.Hop = %q, want sess_root (the re-emitting hop)", chainErr.Hop)
	}
	var tamper *TamperError
	if !errors.As(err, &tamper) {
		t.Fatalf("ChainError does not wrap *TamperError: %v", err)
	}
	if tamper.ExpectedDigest == tamper.ObservedDigest {
		t.Error("TamperError digests are equal; want divergent expected/observed")
	}
}

// TestWalkChainNilForwardedContentForwardsUnverified_spec_9_2 proves the
// v1 default: with no re-emission provider (intermediate pods forward by
// elicitation_id only), every hop advances unverified and the chain
// resolves at the human edge. The old code compared the gateway-held
// original against its own digest — a tautology that could never catch
// anything and could never pass anything else; removing it must not
// regress the no-re-emission happy path. F-9.2.1.
func TestWalkChainNilForwardedContentForwardsUnverified_spec_9_2(t *testing.T) {
	res, err := WalkChain(ChainInput{
		Hops:            twoHopChain(),
		OriginalContent: Content{Message: "approve?", Schema: map[string]any{"type": "object"}},
		Initiator:       InitiatorAgent,
		DepthPolicy:     DepthAllowAll,
		// ForwardedContent nil — no hop re-emits.
	})
	if err != nil {
		t.Fatalf("WalkChain error: %v", err)
	}
	if res.Termination != TerminateHuman || res.ResolverSessionID != "sess_root" {
		t.Errorf("res = {%s, %s}, want {human, sess_root}", res.Termination, res.ResolverSessionID)
	}
}

// TestWalkChainForwardedContentMatchingPasses_spec_9_2 proves a faithful
// re-emission (the hop forwarded the unmodified payload) passes the
// integrity check and the walk completes.
func TestWalkChainForwardedContentMatchingPasses_spec_9_2(t *testing.T) {
	original := Content{Message: "approve?", Schema: map[string]any{"type": "object"}}
	res, err := WalkChain(ChainInput{
		Hops:            twoHopChain(),
		OriginalContent: original,
		Initiator:       InitiatorAgent,
		DepthPolicy:     DepthAllowAll,
		ForwardedContent: func(h Hop) (Content, bool) {
			// Re-emit the byte-identical payload at every hop.
			return original, true
		},
	})
	if err != nil {
		t.Fatalf("WalkChain error on faithful re-emission: %v", err)
	}
	if res.ResolverSessionID != "sess_root" {
		t.Errorf("ResolverSessionID = %q, want sess_root", res.ResolverSessionID)
	}
}

// TestWalkChainForwardedContentSkippedHop_spec_9_2 proves a provider that
// declines to supply a re-emission for a hop (ok == false) leaves that
// hop unverified rather than treating the absence as a divergence.
func TestWalkChainForwardedContentSkippedHop_spec_9_2(t *testing.T) {
	res, err := WalkChain(ChainInput{
		Hops:            twoHopChain(),
		OriginalContent: Content{Message: "approve?", Schema: map[string]any{"type": "object"}},
		Initiator:       InitiatorAgent,
		DepthPolicy:     DepthAllowAll,
		ForwardedContent: func(h Hop) (Content, bool) {
			return Content{Message: "ignored"}, false
		},
	})
	if err != nil {
		t.Fatalf("WalkChain error: %v", err)
	}
	if res.ResolverSessionID != "sess_root" {
		t.Errorf("ResolverSessionID = %q, want sess_root", res.ResolverSessionID)
	}
}
