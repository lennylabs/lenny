// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"log"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
)

// expiringSoonPayload is the §11.3 line 240 session_expiring_soon SSE event
// body. The field names match the documented wire schema
// (docs/client-guide/wire-format.md): the client receives the session's
// resolved maxSessionAge and the seconds remaining before expiry so it can
// extend or wrap up. F-11.3.5.
type expiringSoonPayload struct {
	MaxSessionAge    int `json:"maxSessionAge"`
	RemainingSeconds int `json:"remainingSeconds"`
}

// OnSessionExpiringSoon implements watchdog.ExpiryWarningNotifier. The
// watchdog calls it once per session, ~5 minutes before the session's
// effective maxSessionAge deadline. The gateway then performs both halves of
// the §11.3 line 240 contract: it emits the `session_expiring_soon` SSE event
// to the client and dispatches the `DEADLINE_APPROACHING` lifecycle-channel
// signal to the running pod.
//
// Both halves are best-effort and independent: a session with no live SSE
// subscriber still gets the adapter signal, and a session whose pod has no
// lifecycle channel (Basic/Standard integration level, §15 line 2141) still
// gets the SSE event.
//
// spec: §11.3 line 240. F-11.3.5.
func (s *Server) OnSessionExpiringSoon(ctx context.Context, sess sessionstore.Session, maxSessionAgeSeconds, remainingSeconds int) {
	s.publishEvent(sess.TenantID, sess.ID, "session_expiring_soon", expiringSoonPayload{
		MaxSessionAge:    maxSessionAgeSeconds,
		RemainingSeconds: remainingSeconds,
	})
	s.signalDeadlineApproaching(ctx, sess, remainingSeconds)
}

// signalDeadlineApproaching dispatches the §11.3 line 240 DEADLINE_APPROACHING
// signal to the session's running pod over the lifecycle channel, reusing the
// per-session adapter binding the gateway already holds (the same registry the
// interrupt path uses). A session with no live binding (no pod, or a
// coordinator handoff that has not re-bound) is skipped; a Basic/Standard
// runtime with no lifecycle channel returns delivered=false, which is the
// spec's expected posture (§15 line 2141) rather than an error. The call is
// bounded by the §4.7 interrupt deadline so a stuck runtime cannot stall the
// background sweep. F-11.3.5.
func (s *Server) signalDeadlineApproaching(ctx context.Context, sess sessionstore.Session, remainingSeconds int) {
	if s.podRegistry == nil {
		return
	}
	bind, ok := s.podRegistry.Get(sess.ID)
	if !ok || bind == nil || bind.Adapter == nil {
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, s.interruptDeadline())
	defer cancel()
	remainingMs := int32(time.Duration(remainingSeconds) * time.Second / time.Millisecond)
	// trigger is "session_age": the §11.3 line 240 warning is keyed off the
	// maxSessionAge cap. The adapter forwards it verbatim to the runtime.
	if _, err := bind.Adapter.SignalDeadline(callCtx, sess.ID, remainingMs, "session_age"); err != nil {
		log.Printf("sessionserver: deadline signal for session %s: %v", sess.ID, err)
	}
}
