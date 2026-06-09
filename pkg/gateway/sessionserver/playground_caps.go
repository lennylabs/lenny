// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"net/http"

	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
)

// originPlayground is the §27.3 line 63 origin claim value stamped on every
// session-capability JWT minted for a /playground/* request, in all three
// playground auth modes. The session server keys the §27.6 idle/duration caps
// and the audit label on this claim rather than on the playground auth mode.
// It mirrors playground.PlaygroundOrigin as a single string literal so the
// session server carries no dependency on the playground package.
// spec: §27.3 line 63.
const originPlayground = "playground"

// PlaygroundCapResolver computes the §27.6 effective idle-timeout and
// session-duration caps for a playground-origin session from the runtime's
// own caps. *playground.Config satisfies it via its EffectiveIdleSeconds and
// EffectiveSessionMinutes methods; the gateway wires it post-construction
// through SetPlaygroundCaps. spec: §27.6 lines 200-201. F-27.6.1 / F-27.6.2.
type PlaygroundCapResolver interface {
	// EffectiveIdleSeconds returns min(runtimeIdleSeconds, playground idle
	// cap) in seconds, treating a zero runtimeIdleSeconds as "no runtime
	// limit declared" (the playground cap then applies unchanged).
	EffectiveIdleSeconds(runtimeIdleSeconds int) int
	// EffectiveSessionMinutes returns min(runtimeMinutes, playground
	// duration cap) in minutes, treating a zero runtimeMinutes as "no
	// runtime limit declared".
	EffectiveSessionMinutes(runtimeMinutes int) int
	// RuntimeVisible reports whether the runtime named name is exposed to a
	// playground caller under §27.2 playground.allowedRuntimes. The session
	// server consults it only for origin=playground requests, on the shared
	// §9.1 GET /v1/runtimes discovery surface and at session create, so the
	// playground value never narrows a non-playground caller. *playground.Config
	// satisfies it via its glob-matching RuntimeVisible method. spec: §27.5
	// line 190; §27.9 line 250. F-27.4.1.
	RuntimeVisible(name string) bool
}

// isPlaygroundOrigin reports whether the request carries a session bearer with
// the §27.3 origin=playground claim. It is the producer side of the
// mode-agnostic claim the §27.3 mint stamps on every /playground/* token. The
// §27.4 allowedRuntimes filter and the session-create admission key on it so a
// non-playground caller on the shared §9.1 discovery surface is never affected.
// spec: §27.3 line 63. F-27.4.1.
func (s *Server) isPlaygroundOrigin(r *http.Request) bool {
	principal, ok := getPrincipal(r)
	return ok && principal.Origin == originPlayground
}

// filterPlaygroundAllowedRuntimes drops every runtime the §27.2
// playground.allowedRuntimes glob list excludes. It is applied on the §9.1 GET
// /v1/runtimes discovery surface (after the §10.6 environment filter) only when
// the request is origin=playground and a cap resolver is wired, so the §27.5
// line 190 "filtered by playground.allowedRuntimes" rule holds for the picker
// while non-playground discovery is untouched. A nil resolver leaves the list
// unchanged. spec: §27.4 line 176; §27.5 line 190. F-27.4.1.
func (s *Server) filterPlaygroundAllowedRuntimes(r *http.Request, rows []runtimestore.Runtime) []runtimestore.Runtime {
	if s.playgroundCaps == nil || !s.isPlaygroundOrigin(r) {
		return rows
	}
	out := make([]runtimestore.Runtime, 0, len(rows))
	for _, rt := range rows {
		if s.playgroundCaps.RuntimeVisible(rt.Name) {
			out = append(out, rt)
		}
	}
	return out
}

// requirePlaygroundRuntimeVisible enforces the §27.4 allowedRuntimes boundary
// at session create. When the caller is origin=playground and the requested
// runtime is excluded by playground.allowedRuntimes, the create is rejected
// with 403 FORBIDDEN rather than admitting a runtime the playground picker
// would never surface — closing the §27.5 "see and select" gap that discovery
// filtering alone leaves open. It returns true (admit) for a non-playground
// caller, a nil resolver, or a visible runtime. spec: §27.5 line 190; §27.9
// line 250. F-27.4.1.
func (s *Server) requirePlaygroundRuntimeVisible(w http.ResponseWriter, r *http.Request, runtimeRef string) bool {
	if s.playgroundCaps == nil || runtimeRef == "" || !s.isPlaygroundOrigin(r) {
		return true
	}
	if s.playgroundCaps.RuntimeVisible(runtimeRef) {
		return true
	}
	s.writeError(w, http.StatusForbidden, "FORBIDDEN",
		"the requested runtime is not exposed to the playground by playground.allowedRuntimes (§27.5)",
		map[string]any{"reason": "runtime_not_playground_visible", "runtimeRef": runtimeRef})
	return false
}

// SetPlaygroundCaps wires the §27 playground enforcement onto a constructed
// server: the cap resolver that tightens a playground-origin session's idle
// and duration limits (§27.6), and the optional counter hook that records the
// §27.8 lenny_playground_sessions_created_total metric (F-27.6.11). The
// gateway calls it from the playground bootstrap block, which runs after the
// session server is built. Either argument may be nil.
func (s *Server) SetPlaygroundCaps(caps PlaygroundCapResolver, incSessionCreated func(runtime string)) {
	s.playgroundCaps = caps
	s.incPlaygroundSessionCreated = incSessionCreated
}

// applyPlaygroundCaps stamps the §27.6 playground session caps and the §27.3
// origin=playground label onto a session row being created, when the caller's
// session bearer carries the origin=playground claim. It is the consumer side
// of the mode-agnostic claim the §27.3 mint already produces (the producer
// side that this finding cluster found unread):
//
//   - origin=playground is recorded on the row for the §27.6 line 203 audit
//     label and the §27.8 dashboard slice (F-27.6.8);
//   - the §27.6 line 200 hard duration cap
//     min(runtime.maxSessionAge, playground.maxSessionMinutes) is stamped onto
//     Timeouts.MaxSessionAgeSeconds so the §11.3 watchdog (via the sessionage
//     resolver) expires the session at the playground bound (F-27.6.2);
//   - the §27.6 line 201 idle override
//     min(runtime.maxIdleTime, playground.maxIdleTimeSeconds) is stamped onto
//     Timeouts.MaxIdleSeconds (F-27.6.1). v1 models no per-runtime idle limit
//     in RuntimeDefinition.limits, so the runtime side is zero and the
//     playground cap applies unchanged.
//
// Both caps are stamped min-wins against any §14 client-requested timeout, so
// a tighter client value is never loosened. A non-playground caller, or a
// context with no resolved principal, is a no-op.
//
// spec: §27.3 line 63; §27.6 lines 200-203. F-27.3.3 / F-27.6.1 / F-27.6.2 /
// F-27.6.8.
func (s *Server) applyPlaygroundCaps(ctx context.Context, runtimeRef string, row *sessionstore.Session) {
	principal, ok := authmw.FromContext(ctx)
	if !ok || principal.Origin != originPlayground {
		return
	}
	row.Origin = originPlayground
	// Count the playground-originated session once the claim is read. This
	// fires even when no cap resolver is wired (the audit-label half still
	// applies), so the §27.8 counter tracks every admitted playground session.
	if s.incPlaygroundSessionCreated != nil {
		s.incPlaygroundSessionCreated(runtimeRef)
	}
	if s.playgroundCaps == nil {
		return
	}

	if row.Timeouts == nil {
		row.Timeouts = &sessionstore.SessionTimeouts{}
	}

	// §27.6 line 200 duration cap. The runtime cap is stored in seconds; the
	// playground cap is configured in minutes. Invoke the §27.6 helper in its
	// minute basis, then resolve the final value in seconds and re-clamp by
	// the exact runtime seconds so a sub-minute runtime cap (which rounds to
	// zero minutes) is never loosened to the playground default.
	runtimeSeconds := int64(s.runtimeMaxSessionAge(ctx, runtimeRef))
	effMinutes := s.playgroundCaps.EffectiveSessionMinutes(int(runtimeSeconds / 60))
	capSeconds := int64(effMinutes) * 60
	if runtimeSeconds > 0 {
		capSeconds = minPositiveInt64(capSeconds, runtimeSeconds)
	}
	row.Timeouts.MaxSessionAgeSeconds = minPositiveInt64(row.Timeouts.MaxSessionAgeSeconds, capSeconds)

	// §27.6 line 201 idle override. The §5.1 RuntimeDefinition
	// `limits.maxIdleTimeSeconds` (when declared) is resolved min-wins
	// against the playground idle cap, so a runtime that already declares a
	// tighter idle limit is never loosened to the playground default. The
	// resulting value lands on Timeouts.MaxIdleSeconds, which the §11.3
	// idle watchdog (via sessionidle.Resolver) enforces. F-11.3.7 / F-27.6.1.
	idleSeconds := int64(s.playgroundCaps.EffectiveIdleSeconds(s.runtimeMaxIdle(ctx, runtimeRef)))
	row.Timeouts.MaxIdleSeconds = minPositiveInt64(row.Timeouts.MaxIdleSeconds, idleSeconds)
}

// minPositiveInt64 returns the smaller of a and b, ignoring a non-positive
// (unset) value on either side: minPositiveInt64(0, n) == n and
// minPositiveInt64(n, 0) == n. A session that requested no override on an axis
// therefore inherits the playground cap unchanged.
func minPositiveInt64(a, b int64) int64 {
	switch {
	case a <= 0:
		return b
	case b <= 0:
		return a
	case a < b:
		return a
	default:
		return b
	}
}
