// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// SessionIsolationLevel mirrors the §7.1 sessionIsolationLevel object.
type SessionIsolationLevel struct {
	ExecutionMode        string `json:"executionMode"`
	IsolationProfile     string `json:"isolationProfile"`
	PodReuse             bool   `json:"podReuse"`
	ScrubPolicy          string `json:"scrubPolicy,omitempty"`
	ResidualStateWarning bool   `json:"residualStateWarning"`
	// ConversationContinuity is the §7.1 contract field:
	// "platform" for session mode (the platform binds the session to a pod
	// and preserves conversation context across messages for the session's
	// lifetime) or "none" for service mode (the gateway routes each message
	// to any ready replica and keeps no conversation context between
	// messages, so clients of multi_turn runtimes re-inject context into
	// each message's input). spec: §5.2 (service-mode session contract),
	// §7.1.
	ConversationContinuity string `json:"conversationContinuity"`
}

// conversationContinuityFor maps a §5.2 execution mode to its §7.1
// conversationContinuity contract value: "none" for service mode (no
// cross-message continuity, each message routes to any ready replica) and
// "platform" for session mode (the session is pinned to a pod that
// preserves context for its lifetime). An empty mode resolves to the
// session-mode default, mirroring the executionMode fallbacks elsewhere so
// the field never understates continuity. spec: §5.2, §7.1.
func conversationContinuityFor(mode string) string {
	if mode == string(runtimestore.ExecutionModeService) {
		return continuityNone
	}
	return continuityPlatform
}

// persistedContinuity returns the §7.1 conversationContinuity for a
// read off the persisted row. The stored value (the S25a
// conversation_continuity column) is authoritative when non-empty, so a
// GET / List after a coordinator handoff returns the exact value the create
// response carried. An empty column (a pre-migration row, or a row created
// before the gateway resolved a pool) falls back to the mode-derived value
// so the field never understates continuity. spec: §7.1.
func persistedContinuity(stored, mode string) string {
	if stored != "" {
		return stored
	}
	return conversationContinuityFor(mode)
}

const (
	// continuityPlatform is the §7.1 conversationContinuity value
	// for session mode.
	continuityPlatform = "platform"
	// continuityNone is the §7.1 conversationContinuity value for
	// service mode.
	continuityNone = "none"
)

// effectiveRequestedProfile is the §5.3 profile to resolve a session's
// pool against. spec: §7.1 — a client-pinned pool
// overrides the default pool selection and the resolved level is
// populated from the assigned pool's configuration, so when the client
// pins a pool and omits isolationProfile the pool's own profile governs.
// In that case the effective requested profile is empty, which lets
// ResolvePool's `isolationProfile != ""` short-circuit defer to the named
// pool rather than reject a pool whose profile differs from the deployment
// default. When the client states a profile explicitly (clientRequested is
// non-empty) that profile governs and an inconsistent pin is rejected;
// when no pool is pinned the defaulted profile is used so the level still
// resolves. clientRequested is the raw request field (empty when omitted);
// defaulted is the validated profile with the deployment default applied.
func effectiveRequestedProfile(clientRequested, defaulted isolation.Profile, pinnedPool string) isolation.Profile {
	if pinnedPool != "" && clientRequested == "" {
		return ""
	}
	return defaulted
}

// persistedRowProfile is the §5.3 profile to persist on the session row.
// spec: §7.1 — the row.IsolationProfile is the source of truth the
// same-call and later claim re-resolve the pool against, so it must reflect
// the assigned pool's profile. The resolved level carries the pool's
// profile when a pool was resolved (including a pinned pool whose profile
// differs from the deployment default); when no pool resolved cleanly the
// level falls back to the requested profile, which can be empty if the
// client deferred to a pinned pool, so fall back to the validated defaulted
// profile rather than persist an empty profile. F-CS2 (0018).
func persistedRowProfile(level SessionIsolationLevel, defaulted isolation.Profile) isolation.Profile {
	if level.IsolationProfile == "" {
		return defaulted
	}
	return isolation.Profile(level.IsolationProfile)
}

// resolveIsolationLevel computes the §7.1 sessionIsolationLevel for a
// session against its assigned pool. spec: §7.1 — the field is
// populated from the assigned pool's configuration at session creation
// time. When a pool resolver is wired, it resolves the pool from the
// runtime and §5.3 profile and derives the fields from the pool's §5.2
// execution mode and scrub policy. When no resolver is wired (the
// Postgres-only posture) or the pool does not resolve cleanly, it falls
// back to the session-mode level; a session-mode pod is the §5.2
// default and carries no pod reuse, so the fallback never understates
// the isolation posture a client would observe.
func (s *Server) resolveIsolationLevel(ctx context.Context, runtimeRef string, requested isolation.Profile, pinnedPool string) SessionIsolationLevel {
	if s.podBinder == nil || s.podBinder.Client == nil {
		return defaultIsolationLevel(requested)
	}
	// spec: §7.1 / §14.1 — when the client pinned a pool, derive the level
	// from that named pool so the persisted sessionIsolationLevel reflects
	// the pool the session will bind to. F-CS2 (0018).
	match, err := podsession.ResolvePool(ctx, s.podBinder.Client, s.poolPolicyReader(), s.agentNamespace, runtimeRef, string(requested), pinnedPool)
	if err != nil {
		return defaultIsolationLevel(requested)
	}
	return isolationLevelForPool(match, requested)
}

// defaultIsolationLevel returns the §7.1 sessionIsolationLevel for a
// session-mode pod: no pod reuse, no scrub, no residual-state warning.
// It is the fallback when the gateway runs without a pool resolver or
// the resolved pool reports the default `executionMode: session`.
func defaultIsolationLevel(p isolation.Profile) SessionIsolationLevel {
	return SessionIsolationLevel{
		ExecutionMode:        string(runtimestore.ExecutionModeSession),
		IsolationProfile:     string(p),
		PodReuse:             false,
		ScrubPolicy:          "",
		ResidualStateWarning: false,
		// spec: §7.1 — a session-mode pod binds the session to one
		// pod for its lifetime and preserves conversation context across
		// messages.
		ConversationContinuity: continuityPlatform,
	}
}

// persistedIsolationLevel returns the §7.1 sessionIsolationLevel
// derived from the persisted row. ExecutionMode + ScrubPolicy are
// resolved against the assigned pool at create time and frozen for the
// session lifetime; reading them off the row makes GET / List return
// the same envelope a client received from POST /v1/sessions even after
// a coordinator handoff. Rows whose ExecutionMode is empty (gateway
// never resolved a pool, or pre-migration-0084 rows) fall back to the
// session-mode default, mirroring resolveIsolationLevel's fallback
// posture so the field never understates the isolation level.
func persistedIsolationLevel(row sessionstore.Session) SessionIsolationLevel {
	mode := row.ExecutionMode
	if mode == "" {
		return defaultIsolationLevel(row.IsolationProfile)
	}
	level := SessionIsolationLevel{
		ExecutionMode:    mode,
		IsolationProfile: string(row.IsolationProfile),
		// spec: §7.1 — derive conversationContinuity from the frozen
		// execution mode so a GET / List after a coordinator handoff returns
		// "none" for a service-mode row and "platform" otherwise, matching
		// the create response. The stored ConversationContinuity column
		// (migration from S25a) is authoritative when present; an empty
		// column falls back to the mode-derived value so a pre-migration or
		// never-resolved row still reports the correct continuity.
		ConversationContinuity: persistedContinuity(row.ConversationContinuity, mode),
	}
	// spec: §5.2 / §7.1 — a service-mode pod serves successive requests with
	// no scrub, and a session-mode pod that recorded a non-empty scrubPolicy
	// at create time (recycle.enabled or maxConcurrentSessions > 1) reuses a
	// pod across more than one session. Both report podReuse and the
	// residual-state warning; the one-session-per-pod default does neither.
	if mode == string(runtimestore.ExecutionModeService) || row.ScrubPolicy != "" {
		level.PodReuse = true
		level.ResidualStateWarning = true
		level.ScrubPolicy = row.ScrubPolicy
	}
	return level
}

// isolationLevelForPool maps a resolved §5.2 pool to the §7.1
// sessionIsolationLevel fields. spec: §5.2 / §7.1 — a
// service-mode pool and a session-mode pool that reuses a pod
// (recycle.enabled or maxConcurrentSessions > 1) report podReuse and the
// residual-state warning; the one-session-per-pod default does neither.
func isolationLevelForPool(match podsession.PoolMatch, requested isolation.Profile) SessionIsolationLevel {
	profile := match.IsolationProfile
	if profile == "" {
		profile = string(requested)
	}
	mode := match.ExecutionMode
	if mode == "" {
		mode = string(runtimestore.ExecutionModeSession)
	}
	level := SessionIsolationLevel{
		ExecutionMode:    mode,
		IsolationProfile: profile,
		// spec: §7.1 — a service-mode pool provides no cross-message
		// continuity (each message routes to any ready replica); every other
		// mode binds the session to one pod that preserves context.
		ConversationContinuity: conversationContinuityFor(mode),
	}
	scrub := scrubPolicyForPool(match)
	if mode == string(runtimestore.ExecutionModeService) || scrub != "" {
		level.PodReuse = true
		level.ResidualStateWarning = true
		level.ScrubPolicy = scrub
	}
	return level
}

// scrubPolicyForPool returns the §7.1 scrubPolicy string for a
// reuse pool. spec: §5.2 — a service-mode pod serves successive requests
// with no scrub (`none`); a session-mode pod that recycles a pod across
// sessions scrubs best-effort, with the cross-tenant microvm variants
// selecting the VM-level scrub, and concurrent slots scrub per slot. A
// one-session-per-pod session pool returns the empty string (the field is
// omitted on the wire). The PoolMatch recycle/concurrency signals are
// derived from the §5.2 sessionPolicy mirror by ResolvePool.
func scrubPolicyForPool(match podsession.PoolMatch) string {
	if match.ExecutionMode == string(runtimestore.ExecutionModeService) {
		return "none"
	}
	if match.MaxConcurrentSessions > 1 {
		// Concurrent sessions scrub per slot on completion or failure.
		return "best-effort-per-slot"
	}
	if match.Recycle {
		// Cross-tenant microvm sequential reuse selects a VM-level scrub
		// variant; same-tenant and non-microvm reuse uses the standard
		// best-effort scrub. A cross-tenant-reuse pool is validated to carry
		// scrubProfile vm-restart or in-place (the standard in-guest scrub is
		// rejected for cross-tenant reuse, §5.2), so in-place maps to the
		// in-place scrub and any other value (vm-restart) maps to vm-restart.
		if match.IsolationProfile == string(isolation.ProfileMicrovm) && match.AllowCrossTenantReuse {
			if match.MicrovmScrubMode == string(runtimestore.MicrovmScrubInPlace) {
				return "best-effort-in-place"
			}
			return "vm-restart"
		}
		return "best-effort"
	}
	return ""
}
