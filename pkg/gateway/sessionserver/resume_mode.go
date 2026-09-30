// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"

	"github.com/lennylabs/lenny/pkg/checkpoint"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// classifyResumeWithAdapter combines the gateway-side classification
// (eviction / partial-manifest lookups) with the §4.4 / §7.2 mode the
// adapter reported on its ResumeResponse. The gateway-side classifier
// remains authoritative for `conversation_only` (eviction record) and
// `partial_workspace` (partial-manifest record) because those signals
// are stored in Postgres and outlive the adapter's view of the resume.
// When the gateway classifier picked `full` and the adapter reported a
// distinct mode, the adapter's mode is preferred (e.g., the
// `coordinator_handoff` synthesis path). F-7.3.22.
func (s *Server) classifyResumeWithAdapter(
	ctx context.Context, row sessionstore.Session, adapterMode string,
) checkpoint.ResumeMode {
	gateway := s.classifyResume(ctx, row)
	if gateway != checkpoint.ResumeFull {
		return gateway
	}
	if adapterMode == "" {
		return gateway
	}
	m := checkpoint.ResumeMode(adapterMode)
	if m.IsValid() {
		return m
	}
	return gateway
}

// classifyResume picks the §4.4 / §7.2 ResumeMode for a resume of the
// given session by combining (a) the workspace snapshot source, (b)
// the eviction-state-store lookup (conversation-only fallback), and
// (c) the partial-manifest lookup (partial-workspace reassembly). The
// precedence is:
//
//   - eviction-state record present → ResumeConversationOnly (the
//     workspace bytes are gone; the §4.4 fallback writer recorded
//     conversation cursor + last-message context only).
//   - active partial manifest present → ResumePartialWorkspace (the
//     §10.1 reassembly path applies; recovery fraction is carried on
//     the event when the manifest had a baseline full checkpoint
//     size).
//   - workspace snapshot present, no eviction / partial state →
//     ResumeFull.
//
// A nil lookup (production without the store wired, or dev mode)
// degrades to ResumeFull. A lookup error degrades to ResumeFull as
// well — the resume itself succeeded, so a transient store outage
// must not block the session from coming back online; the operator
// observes the degraded classification only by inspecting the gauge
// rather than by the event.
//
// spec: §4.4; §7.2; §10.1 partial-manifest path.
func (s *Server) classifyResume(ctx context.Context, row sessionstore.Session) checkpoint.ResumeMode {
	if s.evictionStateLookup != nil {
		has, err := s.evictionStateLookup.HasEvictionState(ctx, row.TenantID, row.ID)
		if err == nil && has {
			return checkpoint.ResumeConversationOnly
		}
	}
	if s.partialManifestLookup != nil {
		has, err := s.partialManifestLookup.HasActivePartialManifest(ctx, row.TenantID, row.ID)
		if err == nil && has {
			return checkpoint.ResumePartialWorkspace
		}
	}
	return checkpoint.ResumeFull
}

// resumedEventPayload is the §7.2 event
// schema: `resumeMode`, `workspaceLost`, and an optional
// `workspaceRecoveryFraction` (populated by the §10.1 partial-manifest
// path; omitted on full and conversation-only resumes per the
// optional-fraction rule).
type resumedEventPayload struct {
	ResumeMode                string   `json:"resumeMode"`
	WorkspaceLost             bool     `json:"workspaceLost"`
	WorkspaceRecoveryFraction *float64 `json:"workspaceRecoveryFraction,omitempty"`
}

// emitResumedEvent publishes the §7.2 event
// onto the session's event stream. Best-effort: when the gateway is
// not wired with an event bus (dev mode / unit tests without one) the
// emission is a no-op.
//
// spec: §7.2, §4.4, §10.1 partial-manifest path.
func (s *Server) emitResumedEvent(_ context.Context, row sessionstore.Session, mode checkpoint.ResumeMode) {
	if s.events == nil {
		return
	}
	payload := resumedEventPayload{
		ResumeMode:    string(mode),
		WorkspaceLost: mode.WorkspaceLost(),
	}
	s.publishEvent(row.TenantID, row.ID, "session.resumed", payload)
}

// handoffResumedFrame builds the §10.4 synthesized
// `session.resumed` payload for a coordinator-handoff reattach. The
// resume mode is fixed to `coordinator_handoff`; the handoff re-attaches
// the live pod, so the workspace is intact (workspaceLost: false) and
// workspaceRecoveryFraction is 1.0. ok is false only on a marshal error.
// spec: §10.4; §7.2. F-7.2.13, F-10.4.2.
func (s *Server) handoffResumedFrame() ([]byte, bool) {
	full := 1.0
	payload := resumedEventPayload{
		ResumeMode:                string(checkpoint.ResumeCoordinatorHandoff),
		WorkspaceLost:             false,
		WorkspaceRecoveryFraction: &full,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, false
	}
	return data, true
}
