// SPDX-License-Identifier: MIT

package mcptools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/elicitation"
	"github.com/lennylabs/lenny/pkg/gateway/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
)

// ElicitationPerHopForwardingTimeout caps a single forwarding hop on
// the §9.2 elicitation chain. The §11.3 line 211 / §9.1 line 104 contract
// is hard-coded at 30s in v1 ("not configurable" per the §11.3 table):
// if a hop does not complete its forward within the deadline the gateway
// treats it as a failure and returns ELICITATION_PER_HOP_TIMEOUT to the
// originating pod so the agent can either give up or raise a fresh
// elicitation. v1's chain walk is server-internal, so the bound is
// applied to each ancestor session lookup; that is the only point where
// the walk can block waiting on an external dependency (the session
// store). spec: §11.3 line 211; §9.1 line 104.
const ElicitationPerHopForwardingTimeout = 30 * time.Second

// elicitationDispatcher drives the §9.2 hop-by-hop elicitation chain.
// It is the bridge between the pure pkg/elicitation chain walk and
// the gateway's session store: it builds the chain of hops by walking
// the §8 delegation tree from the raising session upward, runs the
// §9.2 url-mode provenance check, and reports where the chain
// terminates so the request_elicitation handler knows which session
// owns resolution.
//
// The dispatcher is per-Register and stateless w.r.t. tenant: the
// raising session's TenantID flows through dispatch() into every
// store lookup and metric emission so multi-tenant deployments scope
// elicitations correctly. spec: §9.2 / §16.1; F-9.2.13, F-15.2.15.
type elicitationDispatcher struct {
	store sessionstore.Store

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

	// dropMetrics, when non-nil, receives a §9.1 drop event for a
	// url-mode rejection (`reason="domain_not_allowlisted"`). The
	// §16.7 catalog does not declare a `elicitation.url_mode_domain_rejected`
	// audit event, so the rejection surfaces only through the metric
	// and the per-hop tool error envelope. spec: §9.2 line 86;
	// F-9.2.11.
	dropMetrics ElicitationDropRecorder

	// tamperMetrics, when non-nil, receives a notification every time
	// the §9.2 chain walk catches a tamper at a forwarding hop. The
	// observer is responsible for incrementing
	// lenny_elicitation_content_tamper_detected_total{tenant_id}.
	tamperMetrics ElicitationTamperRecorder

	// perHopTimeout overrides the §11.3 line 211 hard-coded 30s per-hop
	// forwarding deadline used by buildHops on each ancestor lookup.
	// Tests set this to a small duration so they can drive the timeout
	// branch deterministically; production binaries leave it zero,
	// which selects ElicitationPerHopForwardingTimeout.
	perHopTimeout time.Duration
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
// resolver. tenantID scopes the §16.1 tamper metric and the §8
// delegation-tree lookups to the raising session's tenant — multi-
// tenant deployments must not commingle these. originalContent is
// the {message, schema} pair recorded at origination; rawURL is the
// URL a url-mode elicitation carries (empty for a non-url-mode
// elicitation). spec: §9.2 / §16.1; F-9.2.13, F-15.2.15.
func (d *elicitationDispatcher) dispatch(
	ctx context.Context,
	tenantID string,
	raising sessionstore.Session,
	originalContent elicitation.Content,
	initiator elicitation.InitiatorType,
	rawURL string,
) (dispatchResult, error) {
	// §9.2 url-mode security controls. An agent-initiated url-mode
	// elicitation is dropped unless the pool allowlists the URL's
	// domain; the rejection increments the §9.1
	// lenny_elicitation_dropped_total{reason="domain_not_allowlisted"}
	// counter and returns the per-hop DOMAIN_NOT_ALLOWLISTED error.
	// The §16.7 audit catalog is closed and does not list
	// `elicitation.url_mode_domain_rejected`, so the rejection does
	// not write an audit row. F-9.2.11.
	if err := elicitation.CheckURLModeProvenance(initiator, rawURL, d.urlModeAllowlist); err != nil {
		var rej *elicitation.URLModeRejection
		if errors.As(err, &rej) {
			if d.dropMetrics != nil {
				d.dropMetrics.RecordElicitationDrop(elicitationDropDomainNotAllowlisted)
			}
			// §15.1 DOMAIN_NOT_ALLOWLISTED is the spec error code for the
			// disallowed-domain drop; the disabled / malformed cases share
			// the same drop semantics. spec: §15.2.1 — surface the
			// canonical lenny code via *mcp.ToolError so REST and MCP
			// envelopes share the same (category, retryable) pair.
			// F-8.5.10.
			return dispatchResult{}, mcp.NewToolError("DOMAIN_NOT_ALLOWLISTED",
				err.Error(), map[string]any{
					"url":          rawURL,
					"host":         rej.Host,
					"reason":       string(rej.Reason),
					"allowlist":    rej.Allowlist,
					"sessionId":    raising.ID,
					"originPod":    raising.ID,
					"initiatorType": string(initiator),
				})
		}
		return dispatchResult{}, err
	}

	hops, err := d.buildHops(ctx, tenantID, raising)
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
						tenantID, string(elicitation.ModeEnforce),
					)
				}
				// spec: §15.2.1 — surface the canonical lenny code via
				// *mcp.ToolError so REST and MCP envelopes share the
				// same (category, retryable) pair. F-8.5.10.
				return dispatchResult{}, mcp.NewToolError("ELICITATION_CONTENT_TAMPERED",
					err.Error(), nil)
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
//
// Each ancestor lookup is bounded by the §11.3 line 211 per-hop
// forwarding deadline (ElicitationPerHopForwardingTimeout, 30s by
// default). A hop that does not complete its lookup within the
// deadline aborts the walk and surfaces ELICITATION_PER_HOP_TIMEOUT
// to the originating pod so the agent can either give up or raise a
// fresh elicitation. spec: §11.3 line 211; §9.1 line 104.
func (d *elicitationDispatcher) buildHops(ctx context.Context, tenantID string, raising sessionstore.Session) ([]elicitation.Hop, error) {
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
		parent, err := d.lookupAncestor(ctx, tenantID, cur.ParentSessionID)
		if err != nil {
			if errors.Is(err, sessionstore.ErrNotFound) {
				break // ancestor GC'd — treat current as the root
			}
			if errors.Is(err, context.DeadlineExceeded) {
				// spec: §11.3 line 211 — per-hop forwarding deadline.
				// The forwarding pod (cur.ID) is the hop whose lookup
				// did not complete; the originating pod sees its
				// elicitation fail with ELICITATION_PER_HOP_TIMEOUT.
				return nil, mcp.NewToolError("ELICITATION_PER_HOP_TIMEOUT",
					fmt.Sprintf("forwarding hop %s did not complete within %s", cur.ID, d.effectivePerHopTimeout()),
					map[string]any{
						"hopSessionId":   cur.ID,
						"timeout":        d.effectivePerHopTimeout().String(),
						"timeoutSeconds": d.effectivePerHopTimeout().Seconds(),
					})
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

// effectivePerHopTimeout returns the §11.3 line 211 30s deadline the
// dispatcher applies per forwarding hop, defaulting to the package
// constant when the test override is unset.
func (d *elicitationDispatcher) effectivePerHopTimeout() time.Duration {
	if d.perHopTimeout > 0 {
		return d.perHopTimeout
	}
	return ElicitationPerHopForwardingTimeout
}

// lookupAncestor performs one §9.2 forwarding hop's ancestor lookup
// under the per-hop deadline. The deadline is layered onto the
// caller's context — if the caller's deadline is sooner, that one
// wins. spec: §11.3 line 211; §9.1 line 104.
func (d *elicitationDispatcher) lookupAncestor(ctx context.Context, tenantID, parentID string) (sessionstore.Session, error) {
	hopCtx, cancel := context.WithTimeout(ctx, d.effectivePerHopTimeout())
	defer cancel()
	return d.store.Get(hopCtx, tenantID, parentID)
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

// resolveElicitationTool is the shared body of the §9.2 MCP
// `lenny/respond_to_elicitation` and `lenny/dismiss_elicitation` tools.
// It looks up the calling session row (so the triple's user_id is the
// session's bound user identity — the same binding the §9.2 dispatcher
// used to record the pending row), enforces the (tenant, session,
// user, elicitation) triple via ResolveElicitation, and surfaces a
// well-typed lenny error envelope on the three failure paths:
// ELICITATION_NOT_FOUND on triple mismatch, INTERACTION_ALREADY_RESOLVED
// on a second resolution attempt, INTERNAL_ERROR otherwise. Symmetric
// with sessionserver.resolveInteraction. F-9.2.17.
func resolveElicitationTool(
	ctx context.Context,
	deps Deps,
	tenant, sessionID, elicitationID string,
	phase interactionstore.Phase,
	response any,
	reason string,
) (mcp.ToolResult, error) {
	row, err := deps.Store.Get(ctx, tenant, sessionID)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			return mcp.ToolResult{}, mcp.NewToolError("ELICITATION_NOT_FOUND",
				"elicitation not found",
				map[string]any{"elicitationId": elicitationID})
		}
		return mcp.ToolResult{}, mcp.NewToolError("INTERNAL_ERROR",
			fmt.Sprintf("session lookup: %v", err), nil)
	}
	out, err := ResolveElicitation(ctx, deps.Interactions, tenant, sessionID,
		row.UserID, elicitationID, phase, response, reason)
	if err != nil {
		switch {
		case errors.Is(err, ErrElicitationNotFound):
			return mcp.ToolResult{}, mcp.NewToolError("ELICITATION_NOT_FOUND",
				"elicitation not found",
				map[string]any{"elicitationId": elicitationID})
		case errors.Is(err, interactionstore.ErrAlreadyResolved):
			return mcp.ToolResult{}, mcp.NewToolError("INTERACTION_ALREADY_RESOLVED",
				"interaction has already been resolved",
				map[string]any{"elicitationId": elicitationID})
		default:
			return mcp.ToolResult{}, mcp.NewToolError("INTERNAL_ERROR", err.Error(), nil)
		}
	}
	body, _ := json.Marshal(map[string]any{
		"id":         out.ID,
		"phase":      string(out.Phase),
		"resolvedAt": out.ResolvedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
	})
	return textResult(string(body)), nil
}
