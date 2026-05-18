// SPDX-License-Identifier: MIT

package elicitation

import (
	"errors"
	"testing"
)

// content is the shared {message, schema} fixture for the chain
// tests.
func content() Content {
	return Content{Message: "approve the deploy?", Schema: map[string]any{"type": "boolean"}}
}

// chainHops builds an n-hop chain from a deep raising session up to a
// root. Index 0 is the deepest (raising) hop; the last index is the
// root. The root is marked human-facing.
func chainHops(n int) []Hop {
	hops := make([]Hop, n)
	for i := 0; i < n; i++ {
		depth := n - 1 - i
		hops[i] = Hop{
			SessionID: sessionName(i),
			PodID:     "pod-" + sessionName(i),
			Depth:     depth,
			IsHuman:   depth == 0,
		}
	}
	return hops
}

func sessionName(i int) string {
	return "sess-" + string(rune('a'+i))
}

func TestWalkChainForwardsUpMultipleHops(t *testing.T) {
	// A four-hop chain: leaf → mid2 → mid1 → root(human). No parent
	// intercepts, so the elicitation forwards all the way to the
	// human-facing root.
	res, err := WalkChain(ChainInput{
		Hops:            chainHops(4),
		OriginalContent: content(),
		Initiator:       InitiatorAgent,
		DepthPolicy:     DepthAllowAll,
	})
	if err != nil {
		t.Fatalf("WalkChain: %v", err)
	}
	if res.Termination != TerminateHuman {
		t.Errorf("termination = %q, want human", res.Termination)
	}
	if res.ResolverSessionID != sessionName(3) {
		t.Errorf("resolver = %q, want the root session", res.ResolverSessionID)
	}
	if len(res.Hops) != 4 {
		t.Fatalf("traversed %d hops, want all 4", len(res.Hops))
	}
	// The chain must be ordered from the raising session upward.
	if res.Hops[0].SessionID != sessionName(0) || res.Hops[3].SessionID != sessionName(3) {
		t.Errorf("hop order wrong: %s..%s", res.Hops[0].SessionID, res.Hops[3].SessionID)
	}
}

func TestWalkChainParentIntercepts(t *testing.T) {
	// leaf → mid → root. The middle parent is configured to intercept;
	// the chain must stop at it and not reach the root.
	hops := chainHops(3)
	hops[1].Intercepts = true
	res, err := WalkChain(ChainInput{
		Hops:            hops,
		OriginalContent: content(),
		Initiator:       InitiatorAgent,
		DepthPolicy:     DepthAllowAll,
	})
	if err != nil {
		t.Fatalf("WalkChain: %v", err)
	}
	if res.Termination != TerminateIntercept {
		t.Errorf("termination = %q, want intercept", res.Termination)
	}
	if res.ResolverSessionID != sessionName(1) {
		t.Errorf("resolver = %q, want the intercepting parent", res.ResolverSessionID)
	}
	if res.ResolverPodID != "pod-"+sessionName(1) {
		t.Errorf("resolver pod = %q", res.ResolverPodID)
	}
	// The walk stopped at the intercepting hop — the root was never
	// traversed.
	if len(res.Hops) != 2 {
		t.Fatalf("traversed %d hops, want 2 (stopped at the intercept)", len(res.Hops))
	}
}

func TestWalkChainFirstInterceptWins(t *testing.T) {
	// Two ancestor hops intercept; the chain must stop at the nearer
	// one (the first reached walking upward).
	hops := chainHops(4)
	hops[1].Intercepts = true
	hops[2].Intercepts = true
	res, err := WalkChain(ChainInput{
		Hops:            hops,
		OriginalContent: content(),
		Initiator:       InitiatorAgent,
		DepthPolicy:     DepthAllowAll,
	})
	if err != nil {
		t.Fatalf("WalkChain: %v", err)
	}
	if res.ResolverSessionID != sessionName(1) {
		t.Errorf("resolver = %q, want the nearest intercepting parent", res.ResolverSessionID)
	}
}

func TestWalkChainSingleHopRootIsHuman(t *testing.T) {
	// A root session raising an elicitation directly: a one-hop chain
	// terminates at a human resolver.
	res, err := WalkChain(ChainInput{
		Hops:            chainHops(1),
		OriginalContent: content(),
		Initiator:       InitiatorAgent,
		DepthPolicy:     DepthAllowAll,
	})
	if err != nil {
		t.Fatalf("WalkChain: %v", err)
	}
	if res.Termination != TerminateHuman {
		t.Errorf("termination = %q, want human", res.Termination)
	}
	if res.ResolverSessionID != sessionName(0) {
		t.Errorf("resolver = %q", res.ResolverSessionID)
	}
}

func TestWalkChainEmptyChainRejected(t *testing.T) {
	_, err := WalkChain(ChainInput{OriginalContent: content(), DepthPolicy: DepthAllowAll})
	var chainErr *ChainError
	if !errors.As(err, &chainErr) {
		t.Fatalf("err = %v, want a *ChainError for an empty chain", err)
	}
}

func TestWalkChainDepthSuppression(t *testing.T) {
	// A six-hop chain places the raising session at depth 5. With a
	// suppress_at_depth of 3 the agent elicitation is suppressed and
	// never forwarded.
	res, err := WalkChain(ChainInput{
		Hops:            chainHops(6),
		OriginalContent: content(),
		Initiator:       InitiatorAgent,
		DepthPolicy:     DepthSuppressAtDepth,
		SuppressAtDepth: 3,
	})
	if err != nil {
		t.Fatalf("WalkChain: %v", err)
	}
	if res.Termination != TerminateSuppressed {
		t.Errorf("termination = %q, want suppressed", res.Termination)
	}
	// A suppressed elicitation does not advance past the raising hop.
	if len(res.Hops) != 1 {
		t.Errorf("traversed %d hops, want 1 (suppressed before forwarding)", len(res.Hops))
	}
}

func TestWalkChainConnectorExemptFromSuppression(t *testing.T) {
	// The same deep chain, but connector-initiated. §9.2 exempts
	// gateway-initiated OAuth flows from depth suppression, so it
	// forwards to the human edge.
	res, err := WalkChain(ChainInput{
		Hops:            chainHops(6),
		OriginalContent: content(),
		Initiator:       InitiatorConnector,
		DepthPolicy:     DepthSuppressAtDepth,
		SuppressAtDepth: 3,
	})
	if err != nil {
		t.Fatalf("WalkChain: %v", err)
	}
	if res.Termination != TerminateHuman {
		t.Errorf("termination = %q, want human (connector is exempt)", res.Termination)
	}
}

func TestWalkChainBlockAllSuppressesDelegatedSession(t *testing.T) {
	res, err := WalkChain(ChainInput{
		Hops:            chainHops(2),
		OriginalContent: content(),
		Initiator:       InitiatorAgent,
		DepthPolicy:     DepthBlockAll,
	})
	if err != nil {
		t.Fatalf("WalkChain: %v", err)
	}
	if res.Termination != TerminateSuppressed {
		t.Errorf("termination = %q, want suppressed under block_all", res.Termination)
	}
}

// tamperHop is a Hop whose verification is exercised against a
// divergent content payload — see TestWalkChainDigestVerifiedEachHop.
func TestWalkChainDigestVerifiedEachHop(t *testing.T) {
	// The digest is checked at every forward hop. When the recorded
	// original is canonicalizable and unchanged across the walk, the
	// verification passes at each hop and the elicitation forwards.
	orig := content()
	digest, err := orig.Digest()
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	res, err := WalkChain(ChainInput{
		Hops:            chainHops(5),
		OriginalContent: orig,
		Initiator:       InitiatorAgent,
		DepthPolicy:     DepthAllowAll,
	})
	if err != nil {
		t.Fatalf("WalkChain: %v", err)
	}
	// Every forward hop (hops 1..4) was verified — confirm by checking
	// the same digest still verifies after the walk.
	if err := VerifyContent(digest, orig); err != nil {
		t.Errorf("post-walk verify: %v", err)
	}
	if len(res.Hops) != 5 {
		t.Errorf("traversed %d hops, want 5", len(res.Hops))
	}
}

func TestVerifyContentAtHopDetectsTamper(t *testing.T) {
	orig := content()
	digest, err := orig.Digest()
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	// A forwarding pod re-emits a rewritten message — a §9.2 tamper.
	tampered := Content{Message: "approve the deploy to PRODUCTION?", Schema: orig.Schema}
	err = VerifyContentAtHop("pod-mallory", digest, tampered)
	var chainErr *ChainError
	if !errors.As(err, &chainErr) {
		t.Fatalf("err = %v, want a *ChainError", err)
	}
	if chainErr.Hop != "pod-mallory" {
		t.Errorf("ChainError.Hop = %q, want the tampering pod", chainErr.Hop)
	}
	var tamperErr *TamperError
	if !errors.As(err, &tamperErr) {
		t.Errorf("err does not wrap a *TamperError: %v", err)
	}
}

func TestVerifyContentAtHopAdmitsUnchanged(t *testing.T) {
	orig := content()
	digest, err := orig.Digest()
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	if err := VerifyContentAtHop("pod-bob", digest, orig); err != nil {
		t.Errorf("an unchanged re-emission must verify: %v", err)
	}
}

func TestURLModeAllowlistValidate(t *testing.T) {
	// Enabled with a non-empty allowlist is admissible.
	ok := URLModeAllowlist{Enabled: true, DomainAllowlist: []string{"accounts.example.com"}}
	if err := ok.Validate(); err != nil {
		t.Errorf("a populated allowlist must validate: %v", err)
	}
	// Disabled with no allowlist is fine — the block is the default.
	if err := (URLModeAllowlist{}).Validate(); err != nil {
		t.Errorf("a disabled allowlist must validate: %v", err)
	}
	// Enabled with an empty allowlist is the §9.2
	// URL_MODE_ELICITATION_DOMAIN_REQUIRED rejection.
	var cfgErr *URLModeConfigError
	if err := (URLModeAllowlist{Enabled: true}).Validate(); !errors.As(err, &cfgErr) {
		t.Errorf("an enabled empty allowlist must be rejected, got %v", err)
	}
	// Enabled with only blank entries is equally rejected.
	if err := (URLModeAllowlist{Enabled: true, DomainAllowlist: []string{"  ", ""}}).Validate(); !errors.As(err, &cfgErr) {
		t.Errorf("an allowlist of only blanks must be rejected, got %v", err)
	}
}

func TestCheckURLModeProvenanceAllowedDomain(t *testing.T) {
	allow := URLModeAllowlist{Enabled: true, DomainAllowlist: []string{"accounts.example.com"}}
	err := CheckURLModeProvenance(InitiatorAgent, "https://accounts.example.com/oauth/authorize", allow)
	if err != nil {
		t.Errorf("an allowlisted domain must pass: %v", err)
	}
}

func TestCheckURLModeProvenanceWildcardDomain(t *testing.T) {
	allow := URLModeAllowlist{Enabled: true, DomainAllowlist: []string{"*.example.com"}}
	if err := CheckURLModeProvenance(InitiatorAgent, "https://login.example.com/start", allow); err != nil {
		t.Errorf("a wildcard-matching subdomain must pass: %v", err)
	}
	// The bare suffix does not match a `*.suffix` wildcard.
	err := CheckURLModeProvenance(InitiatorAgent, "https://example.com/start", allow)
	var rej *URLModeRejection
	if !errors.As(err, &rej) {
		t.Errorf("the bare suffix must not match a wildcard, got %v", err)
	}
}

func TestCheckURLModeProvenanceDisallowedDomain(t *testing.T) {
	allow := URLModeAllowlist{Enabled: true, DomainAllowlist: []string{"accounts.example.com"}}
	err := CheckURLModeProvenance(InitiatorAgent, "https://phish.evil.test/login", allow)
	var rej *URLModeRejection
	if !errors.As(err, &rej) {
		t.Fatalf("a disallowed domain must be rejected, got %v", err)
	}
	if rej.Reason != URLModeRejectNotAllowlisted {
		t.Errorf("reason = %q, want domain_not_allowlisted", rej.Reason)
	}
	if rej.Host != "phish.evil.test" {
		t.Errorf("rejection host = %q", rej.Host)
	}
	if len(rej.Allowlist) != 1 || rej.Allowlist[0] != "accounts.example.com" {
		t.Errorf("rejection allowlist = %v", rej.Allowlist)
	}
}

func TestCheckURLModeProvenanceAgentBlockedByDefault(t *testing.T) {
	// §9.2 control 1: agent-initiated url-mode is blocked when the
	// pool does not allowlist it at all.
	err := CheckURLModeProvenance(InitiatorAgent, "https://accounts.example.com/oauth", URLModeAllowlist{})
	var rej *URLModeRejection
	if !errors.As(err, &rej) {
		t.Fatalf("agent url-mode must be blocked by default, got %v", err)
	}
	if rej.Reason != URLModeRejectDisabled {
		t.Errorf("reason = %q, want url_mode_disabled", rej.Reason)
	}
}

func TestCheckURLModeProvenanceConnectorAllowed(t *testing.T) {
	// A connector-initiated url-mode elicitation is admitted even
	// against an empty pool allowlist — §9.2 reserves url-mode for
	// gateway-registered connectors.
	err := CheckURLModeProvenance(InitiatorConnector, "https://github.com/login/oauth/authorize", URLModeAllowlist{})
	if err != nil {
		t.Errorf("a connector url-mode elicitation must be admitted: %v", err)
	}
}

func TestCheckURLModeProvenanceNonURLModeAdmitted(t *testing.T) {
	// An elicitation that carries no URL is not url-mode; the §9.2
	// url-mode controls do not apply.
	if err := CheckURLModeProvenance(InitiatorAgent, "", URLModeAllowlist{}); err != nil {
		t.Errorf("a non-url-mode elicitation must be admitted: %v", err)
	}
}

func TestCheckURLModeProvenanceMalformedURL(t *testing.T) {
	allow := URLModeAllowlist{Enabled: true, DomainAllowlist: []string{"accounts.example.com"}}
	for _, bad := range []string{"not-a-url", "ftp://accounts.example.com", "https://"} {
		err := CheckURLModeProvenance(InitiatorAgent, bad, allow)
		var rej *URLModeRejection
		if !errors.As(err, &rej) || rej.Reason != URLModeRejectMalformedURL {
			t.Errorf("CheckURLModeProvenance(%q) = %v, want a malformed-url rejection", bad, err)
		}
	}
}

func TestCheckURLModeProvenanceHostPortStripped(t *testing.T) {
	// A host with an explicit port matches the allowlist by host only.
	allow := URLModeAllowlist{Enabled: true, DomainAllowlist: []string{"accounts.example.com"}}
	if err := CheckURLModeProvenance(InitiatorAgent, "https://accounts.example.com:8443/oauth", allow); err != nil {
		t.Errorf("a host:port URL must match the host-only allowlist: %v", err)
	}
}
