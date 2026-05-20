// SPDX-License-Identifier: MIT

package mcptools

import (
	"context"
	"errors"
	"fmt"

	"github.com/lennylabs/lenny/pkg/elicitation"
	"github.com/lennylabs/lenny/pkg/gateway/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
)

// elicitationDispatcher drives the §9.2 hop-by-hop elicitation chain.
// It is the bridge between the pure pkg/elicitation chain walk and
// the gateway's session store: it builds the chain of hops by walking
// the §8 delegation tree from the raising session upward, runs the
// §9.2 url-mode provenance check, and reports where the chain
// terminates so the request_elicitation handler knows which session
// owns resolution.
type elicitationDispatcher struct {
	store    sessionstore.Store
	tenantID string

	// depthPolicy + suppressAtDepth are the §9.2 elicitationDepthPolicy
	// in force for agent-initiated elicitations.
	depthPolicy     elicitation.DepthPolicy
	suppressAtDepth int

	// urlModeAllowlist is the §9.2 per-pool agent-initiated url-mode
	// allowlist. The zero value blocks every agent-initiated url-mode
	// elicitation.
	urlModeAllowlist elicitation.URLModeAllowlist

	// intercepts, when non-nil, reports whether an ancestor session is
	// configured to intercept the elicitation chain rather than
	// forward it onward. A nil predicate means no parent intercepts —
	// every elicitation forwards to the human-facing edge.
	intercepts func(sess sessionstore.Session) bool

	// audit records the §11.7 url-mode rejection audit event. Optional.
	audit DelegationAuditor

	// tamperMetrics, when non-nil, receives a notification every time
	// the §9.2 chain walk catches a tamper at a forwarding hop. The
	// observer is responsible for incrementing
	// lenny_elicitation_content_tamper_detected_total{tenant_id}.
	tamperMetrics ElicitationTamperRecorder
}

// ElicitationTamperRecorder is the metric-emission hook the gateway
// wires into the elicitation dispatcher so the §9.2 chain walker
// reports tamper detections without depending on the gateway's
// metrics package directly. *gatewaymetrics.Metrics satisfies it.
// enforcementMode is the §9.2 mode that was in force at the time
// the tamper was caught (off | detect-only | enforce); the §16.5
// alert scopes to enforce-mode catches.
type ElicitationTamperRecorder interface {
	RecordElicitationContentTamperDetected(tenantID, enforcementMode string)
}

// dispatchResult is the outcome of dispatching one elicitation up the
// §9.2 chain.
type dispatchResult struct {
	// Chain is the resolved hop-by-hop walk result.
	Chain elicitation.ChainResult

	// ResolverSessionID is the session that resolves the elicitation —
	// the human-facing edge or the intercepting parent. The
	// request_elicitation handler records the pending interaction
	// against this session so the §9.2/§15.1 respond/dismiss triple
	// targets the resolver.
	ResolverSessionID string

	// Suppressed reports that the §9.2 depth policy suppressed the
	// elicitation. The handler returns a SUPPRESSED response.
	Suppressed bool
}

// dispatch walks the §9.2 elicitation chain for one elicitation
// raised by raising. It builds the chain of hops from the §8
// delegation tree, runs the url-mode provenance check, verifies the
// content-integrity digest at each forward hop, and reports the
// resolver. originalContent is the {message, schema} pair recorded at
// origination; rawURL is the URL a url-mode elicitation carries
// (empty for a non-url-mode elicitation).
func (d *elicitationDispatcher) dispatch(
	ctx context.Context,
	raising sessionstore.Session,
	originalContent elicitation.Content,
	initiator elicitation.InitiatorType,
	rawURL string,
) (dispatchResult, error) {
	// §9.2 url-mode security controls. An agent-initiated url-mode
	// elicitation is dropped unless the pool allowlists the URL's
	// domain; the rejection emits the §11.7 audit event.
	if err := elicitation.CheckURLModeProvenance(initiator, rawURL, d.urlModeAllowlist); err != nil {
		var rej *elicitation.URLModeRejection
		if errors.As(err, &rej) {
			d.emitURLModeRejection(ctx, raising, initiator, rawURL, rej)
			// §15.1 DOMAIN_NOT_ALLOWLISTED is the spec error code for the
			// disallowed-domain drop; the disabled / malformed cases share
			// the same drop semantics.
			return dispatchResult{}, fmt.Errorf("DOMAIN_NOT_ALLOWLISTED: %w", err)
		}
		return dispatchResult{}, err
	}

	hops, err := d.buildHops(ctx, raising)
	if err != nil {
		return dispatchResult{}, err
	}

	res, err := elicitation.WalkChain(elicitation.ChainInput{
		Hops:            hops,
		OriginalContent: originalContent,
		Initiator:       initiator,
		DepthPolicy:     d.depthPolicy,
		SuppressAtDepth: d.suppressAtDepth,
	})
	if err != nil {
		// A chain walk error is a §9.2 content-integrity divergence or a
		// malformed chain. Surface it to the originating pod and, on a
		// tamper, increment the §16.5 content-tamper-detected counter.
		var chainErr *elicitation.ChainError
		if errors.As(err, &chainErr) {
			var tamper *elicitation.TamperError
			if errors.As(err, &tamper) {
				if d.tamperMetrics != nil {
					d.tamperMetrics.RecordElicitationContentTamperDetected(
						d.tenantID, string(elicitation.ModeEnforce))
				}
				return dispatchResult{}, fmt.Errorf("ELICITATION_CONTENT_TAMPERED: %w", err)
			}
		}
		return dispatchResult{}, err
	}

	if res.Termination == elicitation.TerminateSuppressed {
		return dispatchResult{Chain: res, Suppressed: true}, nil
	}
	return dispatchResult{
		Chain:             res,
		ResolverSessionID: res.ResolverSessionID,
	}, nil
}

// buildHops walks the §8 delegation tree from raising up to the root,
// returning the §9.2 chain ordered from the raising session (index 0,
// deepest) to the root. Each ancestor hop's Intercepts flag is
// resolved from the dispatcher's interception predicate. The root hop
// is marked human-facing — a delegation-tree root's client is the
// human edge. A malformed cyclic stored chain is defended against
// with a visited set.
func (d *elicitationDispatcher) buildHops(ctx context.Context, raising sessionstore.Session) ([]elicitation.Hop, error) {
	var chain []sessionstore.Session
	visited := map[string]bool{}
	cur := raising
	for {
		if visited[cur.ID] {
			break // defensive: corrupt stored chain
		}
		visited[cur.ID] = true
		chain = append(chain, cur)
		if cur.ParentSessionID == "" {
			break
		}
		parent, err := d.store.Get(ctx, d.tenantID, cur.ParentSessionID)
		if err != nil {
			if errors.Is(err, sessionstore.ErrNotFound) {
				break // ancestor GC'd — treat current as the root
			}
			return nil, fmt.Errorf("elicitation chain: ancestor lookup: %w", err)
		}
		cur = parent
	}
	// chain is raising-first (deepest first); depth decreases toward
	// the root. The deepest session sits at depth len(chain)-1.
	hops := make([]elicitation.Hop, 0, len(chain))
	for i, sess := range chain {
		depth := len(chain) - 1 - i
		intercepts := false
		// The raising session itself never intercepts; only ancestors.
		if i > 0 && d.intercepts != nil {
			intercepts = d.intercepts(sess)
		}
		hops = append(hops, elicitation.Hop{
			SessionID:  sess.ID,
			PodID:      sess.ID,
			Depth:      depth,
			Intercepts: intercepts,
			// The delegation-tree root is the client-facing edge.
			IsHuman: depth == 0,
		})
	}
	return hops, nil
}

// emitURLModeRejection records the §11.7 audit event for a §9.2
// url-mode elicitation that was dropped. The event names the
// originating pod, the rejection reason, and the URL host so an
// operator can trace an agent attempting to phish via a crafted URL.
func (d *elicitationDispatcher) emitURLModeRejection(
	ctx context.Context,
	raising sessionstore.Session,
	initiator elicitation.InitiatorType,
	rawURL string,
	rej *elicitation.URLModeRejection,
) {
	if d.audit == nil {
		return
	}
	d.audit.EmitDelegationEvent(ctx, "elicitation.url_mode_domain_rejected", map[string]any{
		"sessionId":     raising.ID,
		"originPod":     raising.ID,
		"tenantId":      raising.TenantID,
		"userId":        raising.UserID,
		"initiatorType": string(initiator),
		"reason":        string(rej.Reason),
		"host":          rej.Host,
		"allowlist":     rej.Allowlist,
		"url":           rawURL,
	})
}

// ErrElicitationNotFound is the §9.2 / §15.1 ELICITATION_NOT_FOUND
// condition: the respond/dismiss authorization triple did not match a
// pending elicitation. Returned uniformly for an unknown id, a wrong
// session, a wrong user, or a non-resolver session so the existence
// of another session's elicitations is never leaked.
var ErrElicitationNotFound = errors.New("mcptools: elicitation not found")

// ResolveElicitation applies a §9.2 respond / dismiss resolution to a
// pending elicitation after enforcing the §9.2 / §15.1
// authorization triple. It is the chain-aware resolution path: a
// human at the chain's human-facing edge, or an intercepting parent
// agent that the chain terminated at, both resolve through this
// function.
//
// The authorization triple is (tenantID, resolverSessionID, userID,
// elicitationID). Because the §9.2 dispatcher records every
// elicitation against the session the chain terminates at,
// resolverSessionID is exactly the session a legitimate resolver
// holds — the triple therefore enforces "the elicitation_id must
// have been issued to the exact session making the call and must
// belong to the authenticated user". Any mismatch — an unknown id,
// an id issued against a different (resolver) session, or an id
// belonging to a different user — returns ErrElicitationNotFound so
// the existence of another session's or user's elicitation never
// leaks (§9.2 mandates 404, not 403).
//
// respond carries the human / parent answer when phase is
// PhaseResponded; reason carries the dismiss reason when phase is
// PhaseDismissed. A resolution that targets an interaction that is
// not a §9.2 elicitation (a tool-use interaction on the elicitation
// path) is rejected as not found, symmetric with the §15.1 REST
// handler.
func ResolveElicitation(
	ctx context.Context,
	store interactionstore.Store,
	tenantID, resolverSessionID, userID, elicitationID string,
	phase interactionstore.Phase,
	respond any,
	reason string,
) (interactionstore.Interaction, error) {
	if store == nil {
		return interactionstore.Interaction{}, errors.New("mcptools: no interaction store configured")
	}
	out, err := store.Resolve(ctx, tenantID, resolverSessionID, userID, elicitationID,
		func(in *interactionstore.Interaction) error {
			if in.Kind != interactionstore.KindElicitation {
				// A tool_call_id used on the elicitation path is treated as
				// not found.
				return interactionstore.ErrNotFound
			}
			in.Phase = phase
			in.Response = respond
			in.Reason = reason
			return nil
		})
	if err != nil {
		if errors.Is(err, interactionstore.ErrNotFound) {
			// Unknown id, wrong session, wrong user, or wrong kind — every
			// §9.2 triple mismatch collapses to ELICITATION_NOT_FOUND.
			return interactionstore.Interaction{}, ErrElicitationNotFound
		}
		return interactionstore.Interaction{}, err
	}
	return out, nil
}
