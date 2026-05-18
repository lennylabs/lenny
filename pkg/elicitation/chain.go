// SPDX-License-Identifier: MIT

package elicitation

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Hop is one node on the §9.2 hop-by-hop elicitation chain. The
// chain runs from the raising session upward through every parent
// session to the chain terminus. Each hop carries the session
// identity plus the §9.2 forwarding policy the gateway resolved for
// that session.
type Hop struct {
	// SessionID is the delegation-tree session this hop represents.
	SessionID string

	// PodID is the §9.2 pod identifier for the hop's session. It is
	// what the provenance origin_pod / tampering_pod fields and the
	// audit events record. Defaults to SessionID when blank.
	PodID string

	// Depth is the hop's §8 delegation depth (root = 0). The raising
	// session is the deepest hop; depth decreases toward the root.
	Depth int

	// Intercepts reports whether this parent session is configured to
	// intercept the elicitation rather than forward it onward. A §9.2
	// intercepting parent terminates the chain — the elicitation is
	// answered by the parent agent instead of a human. The raising
	// session itself never intercepts; Intercepts is consulted only
	// for ancestor hops.
	Intercepts bool

	// IsHuman reports whether this hop is the human-facing edge — the
	// client/human terminus of the §9.2 chain ("Gateway edge → Client
	// / Human"). When the walk reaches a human hop the chain
	// terminates at a human resolver. A delegation-tree root session
	// whose client is a human is the canonical human hop.
	IsHuman bool
}

// effectivePod returns the hop's pod identifier, falling back to the
// session id when PodID is unset.
func (h Hop) effectivePod() string {
	if h.PodID != "" {
		return h.PodID
	}
	return h.SessionID
}

// TerminationKind classifies why a §9.2 hop-by-hop walk stopped.
type TerminationKind string

const (
	// TerminateHuman: the chain reached the human-facing edge. The
	// elicitation is resolved by a human via the §15.1 respond/dismiss
	// endpoints.
	TerminateHuman TerminationKind = "human"
	// TerminateIntercept: a parent agent hop is configured to
	// intercept. The chain stops at that hop; the parent answers the
	// elicitation instead of a human.
	TerminateIntercept TerminationKind = "intercept"
	// TerminateSuppressed: the §9.2 depth policy suppressed the
	// elicitation before it could be forwarded. The originating pod
	// receives a SUPPRESSED response.
	TerminateSuppressed TerminationKind = "suppressed"
)

// ChainResult is the outcome of walking the §9.2 elicitation chain.
type ChainResult struct {
	// Termination records why the walk stopped.
	Termination TerminationKind

	// ResolverSessionID is the session that resolves the elicitation:
	// the intercepting parent under TerminateIntercept, or the
	// human-facing hop under TerminateHuman. Empty under
	// TerminateSuppressed.
	ResolverSessionID string

	// ResolverPodID is ResolverSessionID's pod identifier.
	ResolverPodID string

	// Hops lists every hop the elicitation traversed, ordered from the
	// raising session upward up to and including the terminating hop.
	// Each forwarding hop had its content-integrity digest verified
	// before the elicitation advanced past it.
	Hops []Hop
}

// ChainInput is the closed set of parameters a §9.2 chain walk needs.
type ChainInput struct {
	// Hops is the chain ordered from the raising session (index 0,
	// deepest) upward to the root. The first hop is the originating
	// pod; subsequent hops are its ancestors. A single-element chain
	// is a root session raising an elicitation directly.
	Hops []Hop

	// OriginalContent is the {message, schema} pair the gateway
	// recorded at origination. Its digest is the content-integrity
	// reference compared at every forward hop.
	OriginalContent Content

	// Initiator is the §9.2 initiator_type of the elicitation.
	// Connector-initiated elicitations are exempt from depth
	// suppression.
	Initiator InitiatorType

	// DepthPolicy is the §9.2 elicitationDepthPolicy in force.
	DepthPolicy DepthPolicy

	// SuppressAtDepth carries the threshold N for the
	// DepthSuppressAtDepth policy; ignored for the other policies.
	SuppressAtDepth int
}

// ChainError is a §9.2 hop-by-hop walk failure that is not a normal
// termination. The two non-terminal failures are an empty chain and a
// content-integrity divergence detected at a forward hop.
type ChainError struct {
	// Reason is a short machine-stable cause string.
	Reason string
	// Hop is the hop the failure was detected at, when applicable.
	Hop string
	// Cause is the underlying error (a *TamperError for an integrity
	// divergence). May be nil.
	Cause error
}

func (e *ChainError) Error() string {
	if e.Hop != "" {
		return fmt.Sprintf("elicitation: chain walk failed at hop %s: %s", e.Hop, e.Reason)
	}
	return "elicitation: chain walk failed: " + e.Reason
}

func (e *ChainError) Unwrap() error { return e.Cause }

// WalkChain runs the §9.2 hop-by-hop elicitation chain.
//
// The walk starts at the raising session (Hops[0]) and proceeds
// upward through each parent hop. At every hop the gateway-origin
// content-integrity digest is re-verified against OriginalContent —
// an intermediate hop forwards by elicitation_id and MUST NOT alter
// the rendered {message, schema}; a divergence is a §9.2 tamper and
// aborts the walk with a *ChainError wrapping a *TamperError.
//
// The walk terminates when it reaches either:
//
//   - an ancestor hop with Intercepts set — the parent agent answers
//     the elicitation (TerminateIntercept), or
//   - the human-facing edge hop (TerminateHuman).
//
// Before forwarding past the raising hop, the §9.2 depth policy is
// applied: a suppressed agent-initiated elicitation terminates the
// walk with TerminateSuppressed and is never forwarded.
//
// WalkChain performs only the in-memory policy walk. It does not
// resolve, record, or block; the gateway binary drives those around
// the result.
func WalkChain(in ChainInput) (ChainResult, error) {
	if len(in.Hops) == 0 {
		return ChainResult{}, &ChainError{Reason: "chain has no hops"}
	}
	origin := in.Hops[0]

	// §9.2 depth suppression. The raising hop's depth decides whether
	// the elicitation may be forwarded at all. A connector-initiated
	// elicitation is exempt (gateway-initiated OAuth flows are never
	// suppressed). A suppressed elicitation does not advance.
	policy := in.DepthPolicy
	if !policy.IsValid() {
		policy = DepthAllowAll
	}
	if policy.ShouldSuppress(in.Initiator, origin.Depth, in.SuppressAtDepth) {
		return ChainResult{
			Termination: TerminateSuppressed,
			Hops:        []Hop{origin},
		}, nil
	}

	// The origin's recorded content is the integrity reference. A
	// non-encodable schema is rejected up front rather than at the
	// first hop.
	originalDigest, err := in.OriginalContent.Digest()
	if err != nil {
		return ChainResult{}, &ChainError{
			Reason: "original content is not canonicalizable",
			Hop:    origin.effectivePod(),
			Cause:  err,
		}
	}

	traversed := make([]Hop, 0, len(in.Hops))
	traversed = append(traversed, origin)

	// A single-hop chain is a root session raising an elicitation
	// directly: there is no parent to forward to, so the chain
	// terminates at the human-facing edge.
	if len(in.Hops) == 1 {
		return ChainResult{
			Termination:       TerminateHuman,
			ResolverSessionID: origin.SessionID,
			ResolverPodID:     origin.effectivePod(),
			Hops:              traversed,
		}, nil
	}

	// Forward up the task tree hop by hop. Each parent hop re-emits the
	// upstream elicitation/create frame; the gateway re-verifies the
	// {message, schema} digest before the elicitation advances past
	// that hop (§9.2 gateway-origin binding).
	for i := 1; i < len(in.Hops); i++ {
		hop := in.Hops[i]
		if err := VerifyContent(originalDigest, in.OriginalContent); err != nil {
			return ChainResult{}, &ChainError{
				Reason: "content-integrity divergence",
				Hop:    hop.effectivePod(),
				Cause:  err,
			}
		}
		traversed = append(traversed, hop)

		// §9.2: a parent agent configured to intercept answers the
		// elicitation itself; the chain stops here.
		if hop.Intercepts {
			return ChainResult{
				Termination:       TerminateIntercept,
				ResolverSessionID: hop.SessionID,
				ResolverPodID:     hop.effectivePod(),
				Hops:              traversed,
			}, nil
		}
		// §9.2: the human-facing edge terminates the chain at a human
		// resolver.
		if hop.IsHuman {
			return ChainResult{
				Termination:       TerminateHuman,
				ResolverSessionID: hop.SessionID,
				ResolverPodID:     hop.effectivePod(),
				Hops:              traversed,
			}, nil
		}
	}

	// The walk reached the root without an intercepting parent and
	// without an explicit human hop. The root session is the
	// client-facing edge, so the chain terminates at a human resolver.
	last := traversed[len(traversed)-1]
	return ChainResult{
		Termination:       TerminateHuman,
		ResolverSessionID: last.SessionID,
		ResolverPodID:     last.effectivePod(),
		Hops:              traversed,
	}, nil
}

// VerifyContentAtHop is the §9.2 per-hop content-integrity check a
// forwarding pod's re-emitted frame is run through. It is a thin
// wrapper over VerifyContent that names the hop in the returned
// *ChainError so the gateway audit event can record which pod's
// re-emission diverged. forwarded is the {message, schema} pair the
// intermediate pod re-emitted; originalDigest is the gateway-recorded
// origination digest.
func VerifyContentAtHop(tamperingPod, originalDigest string, forwarded Content) error {
	if err := VerifyContent(originalDigest, forwarded); err != nil {
		return &ChainError{
			Reason: "content-integrity divergence",
			Hop:    tamperingPod,
			Cause:  err,
		}
	}
	return nil
}

// URLModeAllowlist is the §9.2 per-pool agent-initiated URL-mode
// elicitation allowlist: `{"urlModeElicitation": {"enabled": bool,
// "domainAllowlist": [...]}}`. A url-mode elicitation carries a URL
// for the user to visit (an OAuth flow, for example); §9.2 blocks
// agent-initiated url-mode by default to stop a compromised agent
// from phishing users with crafted URLs.
type URLModeAllowlist struct {
	// Enabled reports whether agent-initiated url-mode elicitations
	// are permitted for the pool at all. False (the default) blocks
	// every agent-initiated url-mode elicitation.
	Enabled bool

	// DomainAllowlist is the §9.2 set of permitted effective hosts.
	// Each entry is an exact host or a `*.suffix` wildcard. §9.2
	// requires this array be non-empty whenever Enabled is true.
	DomainAllowlist []string
}

// Validate runs the §9.2 url-mode allowlist cross-field rule: an
// allowlist with Enabled true MUST carry a non-empty DomainAllowlist.
// Pool registration that violates this is rejected by the gateway
// with 400 URL_MODE_ELICITATION_DOMAIN_REQUIRED. Returns
// *URLModeConfigError on violation.
func (a URLModeAllowlist) Validate() error {
	if a.Enabled && len(a.nonEmptyEntries()) == 0 {
		return &URLModeConfigError{}
	}
	return nil
}

// nonEmptyEntries returns the trimmed, lower-cased, non-blank
// allowlist entries.
func (a URLModeAllowlist) nonEmptyEntries() []string {
	out := make([]string, 0, len(a.DomainAllowlist))
	for _, e := range a.DomainAllowlist {
		e = strings.ToLower(strings.TrimSpace(e))
		if e != "" {
			out = append(out, e)
		}
	}
	return out
}

// URLModeConfigError is the §9.2 url-mode allowlist configuration
// failure surfaced by pool registration as 400
// URL_MODE_ELICITATION_DOMAIN_REQUIRED.
type URLModeConfigError struct{}

func (e *URLModeConfigError) Error() string {
	return "elicitation: urlModeElicitation.enabled requires a non-empty domainAllowlist (§9.2)"
}

// URLModeRejection is the §9.2 url-mode provenance failure. The
// gateway drops the elicitation and returns DOMAIN_NOT_ALLOWLISTED to
// the originator.
type URLModeRejection struct {
	// Reason classifies the rejection.
	Reason URLModeRejectReason
	// Host is the URL's effective host (empty when the URL did not
	// parse).
	Host string
	// Allowlist is the set of permitted hosts the host failed to
	// match. Echoed into the §15.1 error details and the audit event.
	Allowlist []string
}

func (e *URLModeRejection) Error() string {
	switch e.Reason {
	case URLModeRejectNotAllowlisted:
		return fmt.Sprintf("elicitation: url-mode host %q is not on the connector-domain allowlist (§9.2 DOMAIN_NOT_ALLOWLISTED)", e.Host)
	case URLModeRejectDisabled:
		return "elicitation: agent-initiated url-mode elicitation is disabled for this pool (§9.2)"
	case URLModeRejectMalformedURL:
		return "elicitation: url-mode elicitation carries a URL that does not parse (§9.2)"
	default:
		return "elicitation: url-mode elicitation rejected (§9.2)"
	}
}

// URLModeRejectReason classifies a §9.2 url-mode rejection.
type URLModeRejectReason string

const (
	// URLModeRejectDisabled: the pool does not allowlist agent-initiated
	// url-mode elicitations at all.
	URLModeRejectDisabled URLModeRejectReason = "url_mode_disabled"
	// URLModeRejectNotAllowlisted: the URL's host is not on the pool's
	// domainAllowlist.
	URLModeRejectNotAllowlisted URLModeRejectReason = "domain_not_allowlisted"
	// URLModeRejectMalformedURL: the elicitation's URL does not parse.
	URLModeRejectMalformedURL URLModeRejectReason = "malformed_url"
)

// CheckURLModeProvenance applies the §9.2 url-mode security controls
// to one elicitation.
//
//   - A connector-initiated elicitation is admitted: §9.2 control 1
//     scopes the agent-initiated block to agent binaries, and
//     gateway-registered connectors own url-mode by design. The
//     connector's own registered expected_domain is the boundary for
//     a connector elicitation (enforced at connector registration).
//   - An agent-initiated elicitation is admitted only when the pool
//     allowlist has Enabled true and the URL's effective host matches
//     a domainAllowlist entry (exact host or `*.suffix` wildcard).
//
// rawURL is the URL the elicitation carries. A non-url-mode
// elicitation passes an empty rawURL and is always admitted — the
// check applies only to elicitations that carry a URL. Returns
// *URLModeRejection when the elicitation must be dropped.
func CheckURLModeProvenance(initiator InitiatorType, rawURL string, allow URLModeAllowlist) error {
	if rawURL == "" {
		// Not a url-mode elicitation; §9.2 url-mode controls do not apply.
		return nil
	}
	host, err := effectiveHost(rawURL)
	if err != nil {
		return &URLModeRejection{Reason: URLModeRejectMalformedURL}
	}
	if initiator == InitiatorConnector {
		// §9.2 control 1: url-mode is reserved for gateway-registered
		// connectors; a connector elicitation is admitted here. The
		// connector's expected_domain match is a separate hard boundary
		// applied against the registered connector definition.
		return nil
	}
	// §9.2 control 1: agent-initiated url-mode is blocked unless the
	// pool explicitly allowlists it.
	if !allow.Enabled {
		return &URLModeRejection{Reason: URLModeRejectDisabled, Host: host}
	}
	entries := allow.nonEmptyEntries()
	if len(entries) == 0 {
		// Defensive: an Enabled allowlist with no entries should have
		// been rejected at pool registration. Treat it as a block.
		return &URLModeRejection{Reason: URLModeRejectDisabled, Host: host}
	}
	if !hostMatchesAllowlist(host, entries) {
		return &URLModeRejection{
			Reason:    URLModeRejectNotAllowlisted,
			Host:      host,
			Allowlist: entries,
		}
	}
	return nil
}

// effectiveHost returns the lower-cased host of rawURL, stripped of
// any port. An absolute http/https URL is required; anything else is
// reported as malformed so a non-URL string cannot slip past the
// url-mode check.
func effectiveHost(rawURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", err
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", errors.New("url-mode URL must be absolute http or https")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", errors.New("url-mode URL has no host")
	}
	return host, nil
}

// hostMatchesAllowlist reports whether host matches one of the §9.2
// allowlist entries. An entry is either an exact host or a `*.suffix`
// wildcard that matches any proper subdomain of suffix (`*.example.com`
// matches `a.example.com` and `a.b.example.com` but not `example.com`
// itself). Entries are already trimmed and lower-cased.
func hostMatchesAllowlist(host string, entries []string) bool {
	for _, pat := range entries {
		if rest, ok := strings.CutPrefix(pat, "*."); ok {
			suffix := "." + rest
			if strings.HasSuffix(host, suffix) && len(host) > len(suffix) {
				return true
			}
			continue
		}
		if host == pat {
			return true
		}
	}
	return false
}
