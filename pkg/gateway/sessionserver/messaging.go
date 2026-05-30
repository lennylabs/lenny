// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
)

// busEmitter adapts the §15.1 SSE event bus to sessioninbox.Emitter so
// the §7.2 inbox/DLQ lifecycle can publish `message_expired` and
// `inbox_cleared` events onto session event streams. The events are
// persisted to the target stream and replayable within the §7.2 replay
// window like any other SessionEvent.
//
// spec: §7.2 line 284 (inbox_cleared); §15.4.1 (message_expired).
type busEmitter struct {
	bus *sessionevents.Bus
	now func() time.Time
}

// NewBusEmitter returns a sessioninbox.Emitter publishing onto bus. It
// returns nil when bus is nil so a gateway without an event bus wires a
// nil Emitter (event emission is best-effort). The Coordinator tolerates
// a nil Emitter.
func NewBusEmitter(bus *sessionevents.Bus, now func() time.Time) sessioninbox.Emitter {
	if bus == nil {
		return nil
	}
	if now == nil {
		now = time.Now
	}
	return &busEmitter{bus: bus, now: now}
}

// MessageExpired publishes the §15.4.1 message_expired event on the
// sender session's event stream.
func (e *busEmitter) MessageExpired(tenantID, senderSessionID string, ev sessioninbox.MessageExpiredEvent) {
	e.publish(tenantID, senderSessionID, sessioninbox.EventMessageExpired, ev)
}

// InboxCleared publishes the §7.2 inbox_cleared event on the target
// session's own event stream.
func (e *busEmitter) InboxCleared(tenantID, targetSessionID string, ev sessioninbox.InboxClearedEvent) {
	e.publish(tenantID, targetSessionID, sessioninbox.EventInboxCleared, ev)
}

func (e *busEmitter) publish(tenantID, sessionID, eventType string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	e.bus.PublishForTenant(tenantID, sessionID, eventType, string(data), e.now())
}

// drainMessagingOnTerminal runs the §7.2 line 343 / §7.3 line 425 inbox
// + DLQ drain when sess reaches a terminal state. The coordinator emits
// a `message_expired` event with `reason: "target_terminated"` on each
// registered sender's event stream and deletes the DLQ key, so a sender
// that previously received a `queued` receipt learns the message was
// never consumed. Best-effort: a drain error is logged and dropped
// rather than failing the terminal transition. No-op when messaging is
// not wired.
//
// spec: §7.2 line 343; §7.3 line 425; §15.4.1 reason target_terminated.
// F-7.3.12.
func (s *Server) drainMessagingOnTerminal(ctx context.Context, sess sessionstore.Session) {
	if s.messaging == nil {
		return
	}
	if _, err := s.messaging.DrainOnTerminal(ctx, sess.TenantID, sess.ID); err != nil {
		log.Printf("lenny-gateway: §7.2 DLQ drain on terminal session=%s: %v", sess.ID, err)
	}
}

// migrateInboxOnResumePending runs the §7.2 lines 305-311 atomic inbox
// drain to the DLQ when sess enters resume_pending. In the default
// in-memory mode the buffered inbox messages are written to the DLQ
// (scored by the resume-window TTL) so they survive a coordinator crash
// during recovery; in durable mode the Redis inbox is left in place with
// an EXPIRE. A drain failure still commits the resume_pending transition
// (pod failure is non-negotiable) and is logged. No-op when messaging is
// not wired.
//
// spec: §7.2 lines 305-311. F-7.2.4.
func (s *Server) migrateInboxOnResumePending(ctx context.Context, sess sessionstore.Session) {
	if s.messaging == nil {
		return
	}
	if _, err := s.messaging.MigrateInboxToDLQ(ctx, sess.TenantID, sess.ID, sess.PoolRef); err != nil {
		log.Printf("lenny-gateway: §7.2 inbox-to-DLQ drain on resume_pending session=%s: %v", sess.ID, err)
	}
}
