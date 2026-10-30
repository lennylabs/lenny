// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
)

// ErrHeldDeliveryPreempted is the error deliverBuffered returns when a
// readable REG-COORDLEASE shows that this replica no longer coordinates the
// session: the lease is absent or names another holder. The entries stay in
// place for the coordinating replica. Nothing of the caller's path-2 message
// reached the runtime, so a REST or MCP path-2 caller applies the
// not-delivered outcome of §29.3 Forwarding rule 6 itself, as rule 5 states
// for a replica that does not hold the lease: a message with no inReplyTo is
// held in the session DLQ and answered with its receipt, and any other
// message is refused with TARGET_NOT_READY.
// spec: §7.2 (Message delivery routing — seven paths, path 6), §29.3
// (Interactive message send) Forwarding rules 5 and 6
var ErrHeldDeliveryPreempted = errors.New("sessionserver: another replica coordinates the session; held messages are left to it")

// heldEntry is one held message the delivery of held messages takes: the
// decoded inbox or DLQ entry and the handle that removes exactly it.
type heldEntry struct {
	msg     sessioninbox.Message
	handle  string
	fromDLQ bool
}

// deliverBuffered delivers a running session's held messages, the session's
// inbox and DLQ entries, on the coordinating replica, with the session's
// delivery lock held by the caller. It delivers one entry at a time, the
// earliest EnqueuedAt across the inbox head and the DLQ head, and the inbox
// head on a tie. For each entry it sends the message, records the delivered
// response, and removes the entry; those three steps run on the
// server-lifetime context whatever ctx is, so a caller cancelled mid-turn
// lets the turn complete and removes only that entry. ctx is read only before
// the loop takes the next entry, and an ended ctx returns its error with the
// untaken entries in place.
//
// Before it takes each entry the loop reads the row from the read pool; when
// that row is terminal it returns nil and leaves the entry in place, and a
// read error is logged and the loop continues. It then reads the session's
// REG-COORDLEASE; when the lease is absent or names another holder it returns
// ErrHeldDeliveryPreempted and leaves the entry in place, because only the
// coordinating replica delivers held messages, and a replica whose lease was
// handed off mid-delivery would otherwise deliver an entry the new
// coordinator's takeover delivery also takes. A lease read error is not
// evidence of a new holder, and the loop continues. A Send, audit-record, or
// removal error stops the loop and returns the error with that entry at the
// head. A Peek or PeekOldest error is logged, stops the loop, and returns
// nil, so a following path-2 message proceeds. An entry whose payload does
// not decode is logged at ERROR and removed, the only removal of an
// undelivered entry. Every held message runs the PostAgentOutput chain; a
// REJECT records its audit row, transcribes and publishes nothing for the
// entry, removes it, and continues. The loop expires nothing and writes
// nothing to the inbox or the DLQ. With no messaging coordinator it returns
// nil at once.
//
// spec: §7.2 (Message delivery routing — seven paths, path 6)
func (s *Server) deliverBuffered(ctx context.Context, row sessionstore.Session) error {
	if s.messaging == nil || s.executor == nil {
		return nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if s.heldDeliveryStopped(ctx, row) {
			return nil
		}
		if s.heldDeliveryPreempted(ctx, row) {
			return ErrHeldDeliveryPreempted
		}
		entry, ok, err := s.oldestHeld(ctx, row)
		if err != nil {
			slog.Warn("sessionserver: read held messages", "session_id", row.ID, "error", err)
			return nil
		}
		if !ok {
			return nil
		}
		if err := s.deliverHeldEntry(row, entry); err != nil {
			return err
		}
	}
}

// heldDeliveryStopped is the delivery's terminal stop: it reads the row from
// the read pool and reports true when that row is terminal. A terminal state
// is absorbing, so a lagging row can only delay the stop.
func (s *Server) heldDeliveryStopped(ctx context.Context, row sessionstore.Session) bool {
	cur, err := s.store.Get(ctx, row.TenantID, row.ID)
	if err != nil {
		slog.Warn("sessionserver: held-message delivery could not read the session row; continuing",
			"session_id", row.ID, "error", err)
		return false
	}
	return session.IsTerminal(cur.State)
}

// heldDeliveryPreempted reports whether a readable REG-COORDLEASE shows
// that this replica no longer coordinates row's session: the lease is absent
// or names another holder value. The single-gateway posture, which reads no
// lease, is never preempted, and a lease read error reports false, so a
// lease-store fault does not stop the coordinator's delivery.
//
// spec: §7.2 (Message delivery routing — seven paths, path 6) Delivery of
// held messages, §10.1.1 (Stateless Replicas and Per-Session Coordination)
func (s *Server) heldDeliveryPreempted(ctx context.Context, row sessionstore.Session) bool {
	if s.coordinatesEverySession() {
		return false
	}
	lease, err := s.leaseStore.Get(ctx, row.TenantID, row.ID)
	switch {
	case errors.Is(err, leasestore.ErrNotFound):
		return true
	case err != nil:
		slog.Warn("sessionserver: held-message delivery could not read the coordination lease; continuing",
			"session_id", row.ID, "error", err)
		return false
	}
	return lease.Holder != s.replicaID
}

// oldestHeld returns the held entry with the earliest EnqueuedAt across the
// inbox head and the DLQ head, the inbox head on a tie.
func (s *Server) oldestHeld(ctx context.Context, row sessionstore.Session) (heldEntry, bool, error) {
	inMsg, inHandle, inOK, err := s.messaging.Peek(ctx, row.TenantID, row.ID)
	if err != nil {
		return heldEntry{}, false, fmt.Errorf("peek inbox: %w", err)
	}
	// spec: §7.2 (path 6) — a PeekOldest error, including a DLQ head whose
	// envelope does not decode, stops the delivery like a Peek error. Only a
	// decoded entry whose payload does not decode is removed undelivered.
	dlqMsg, dlqMember, dlqOK, err := s.messaging.PeekOldest(ctx, row.TenantID, row.ID)
	if err != nil {
		return heldEntry{}, false, fmt.Errorf("peek DLQ: %w", err)
	}
	switch {
	case inOK && (!dlqOK || !dlqMsg.EnqueuedAt.Before(inMsg.EnqueuedAt)):
		return heldEntry{msg: inMsg, handle: inHandle}, true, nil
	case dlqOK:
		return heldEntry{msg: dlqMsg, handle: dlqMember, fromDLQ: true}, true, nil
	default:
		return heldEntry{}, false, nil
	}
}

// deliverHeldEntry sends one held entry to the runtime, records the
// response, and removes the entry, on the server-lifetime context.
func (s *Server) deliverHeldEntry(row sessionstore.Session, entry heldEntry) error {
	ctx := s.lifetime()
	var payload MessagePayload
	if err := json.Unmarshal(entry.msg.Payload, &payload); err != nil || len(entry.msg.Payload) == 0 {
		slog.Error("sessionserver: held message payload does not decode; removing it",
			"session_id", row.ID, "message_id", entry.msg.MessageID, "error", err)
		_, rerr := s.removeHeld(ctx, row, entry)
		return rerr
	}
	msg := projectMessage(payload)
	if msg.ID == "" {
		msg.ID = entry.msg.MessageID
	}
	msg.From = executor.SenderFrom(entry.msg.SenderSessionID)
	msgs := []executor.Message{msg}
	resp, err := s.executor.Send(ctx, row.ID, msgs)
	if err != nil {
		return fmt.Errorf("deliver held message %s: %w", entry.msg.MessageID, err)
	}
	if _, rejected, auditErr := s.recordDeliveredResponse(ctx, row.TenantID, row, msgs, resp); rejected != nil && auditErr != nil {
		return fmt.Errorf("record the rejection of held message %s: %w", entry.msg.MessageID, auditErr)
	}
	_, err = s.removeHeld(ctx, row, entry)
	return err
}

// removeHeld removes exactly the held entry. A removal that finds nothing is
// not an error.
func (s *Server) removeHeld(ctx context.Context, row sessionstore.Session, entry heldEntry) (bool, error) {
	var (
		found bool
		err   error
	)
	if entry.fromDLQ {
		found, err = s.messaging.Remove(ctx, row.TenantID, row.ID, entry.handle)
	} else {
		found, err = s.messaging.Ack(ctx, row.TenantID, row.ID, entry.handle)
	}
	if err != nil {
		return found, fmt.Errorf("remove held message %s: %w", entry.msg.MessageID, err)
	}
	return found, nil
}

// HoldOutcome is what holdMessage answers for a held batch.
type HoldOutcome int

const (
	// HoldQueued is a batch held in the DLQ.
	HoldQueued HoldOutcome = iota
	// HoldDropped is a batch held in the DLQ whose enqueue evicted an older
	// entry under the DLQ cap.
	HoldDropped
	// HoldTerminal is a batch the session ended before: its entries were
	// removed and the caller answers TARGET_TERMINAL.
	HoldTerminal
)

// HoldMessage holds one inter-session message in the session's DLQ through
// holdMessage, for the MCP message path. It answers as holdMessage does.
// spec: §7.2 (Message delivery routing — seven paths, path 6)
func (s *Server) HoldMessage(ctx context.Context, tenantID, sessionID string, msg sessioninbox.Message) (HoldOutcome, session.State, error) {
	if s.messaging == nil {
		return HoldQueued, "", sessioninbox.ErrMessagingUnavailable
	}
	return s.holdMessage(ctx, tenantID, sessionID, []sessioninbox.Message{msg}, 0)
}

// HoldUndeliveredForward holds one inter-session message whose forward to
// the session's coordinating replica was not delivered, for the MCP message
// path. The entry takes the TTL of the §7.2 dead-letter table's
// recovering-state row, and the hold answers as holdMessage does.
// spec: §29.3 (Interactive message send) Forwarding rule 6, §7.2 (Message
// delivery routing — seven paths, path 6)
func (s *Server) HoldUndeliveredForward(ctx context.Context, row sessionstore.Session, msg sessioninbox.Message) (HoldOutcome, session.State, error) {
	if s.messaging == nil {
		return HoldQueued, "", sessioninbox.ErrMessagingUnavailable
	}
	return s.holdMessage(ctx, row.TenantID, row.ID, []sessioninbox.Message{msg}, s.recoveringDLQTTL(row))
}

// LockDelivery takes the session's delivery lock, honoring ctx, for the MCP
// message path and the lenny/delegate_task held delivery, and returns its
// release. The lock is not reentrant. spec: §7.2 (Message delivery routing
// — seven paths, path 6).
func (s *Server) LockDelivery(ctx context.Context, sessionID string) (func(), error) {
	return s.deliveryLocks.Lock(ctx, sessionID)
}

// DeliverHeld delivers the held messages of row, a session its caller found
// `running` with the delivery lock held. It reads no row and returns
// deliverBuffered's error. spec: §7.2 (Message delivery routing — seven
// paths, path 6).
func (s *Server) DeliverHeld(ctx context.Context, row sessionstore.Session) error {
	return s.deliverBuffered(ctx, row)
}

// holdMessage is the one DLQ enqueue site of the REST message path: it holds
// msgs in the session's DLQ, then reads the row from the primary, so a hold
// that races a terminal transition does not leave entries in the DLQ of an
// ended session.
//
//   - With no messaging coordinator the enqueue returns
//     sessioninbox.ErrMessagingUnavailable, which the caller answers as error
//     with inbox_unavailable.
//   - A re-read error removes each entry it enqueued and returns the error;
//     the caller answers error with inbox_unavailable.
//   - A terminal row removes each entry it enqueued and returns HoldTerminal
//     when any removal found its entry (a removal error counts as found),
//     and HoldQueued otherwise, when the terminal drain already reported
//     every entry; it then drains the session once with the re-read row, so
//     an entry a failed removal left is removed and reported message_expired.
//   - Any other row returns HoldQueued, or HoldDropped on an eviction.
//
// It returns the re-read row's state for the caller's TARGET_TERMINAL answer.
//
// spec: §7.2 (Message delivery routing — seven paths, path 6)
func (s *Server) holdMessage(ctx context.Context, tenantID, sessionID string, msgs []sessioninbox.Message, ttl time.Duration) (HoldOutcome, session.State, error) {
	evicted := false
	members := make([]string, 0, len(msgs))
	for _, m := range msgs {
		if m.EnqueuedAt.IsZero() {
			m.EnqueuedAt = s.clock()
		}
		dropped, err := s.messaging.EnqueueDLQ(ctx, tenantID, sessionID, m, ttl)
		if err != nil {
			s.removeHeldMembers(ctx, tenantID, sessionID, members)
			return HoldQueued, "", err
		}
		if member, merr := sessioninbox.DLQMember(m); merr == nil {
			members = append(members, member)
		}
		evicted = evicted || dropped != nil
	}
	row, err := s.readPrimary(ctx, tenantID, sessionID)
	if err != nil {
		s.removeHeldMembers(ctx, tenantID, sessionID, members)
		return HoldQueued, "", fmt.Errorf("re-read session %s after holding its messages: %w", sessionID, err)
	}
	if session.IsTerminal(row.State) {
		found := s.removeHeldMembers(ctx, tenantID, sessionID, members)
		s.drainMessagingOnTerminal(ctx, row)
		if found {
			return HoldTerminal, row.State, nil
		}
		return HoldQueued, row.State, nil
	}
	if evicted {
		return HoldDropped, row.State, nil
	}
	return HoldQueued, row.State, nil
}

// removeHeldMembers removes the DLQ entries holdMessage enqueued and reports
// whether any removal found its entry; a removal error counts as found.
func (s *Server) removeHeldMembers(ctx context.Context, tenantID, sessionID string, members []string) bool {
	found := false
	for _, member := range members {
		ok, err := s.messaging.Remove(ctx, tenantID, sessionID, member)
		if err != nil && !errors.Is(err, context.Canceled) {
			slog.Warn("sessionserver: remove a held message", "session_id", sessionID, "error", err)
		}
		found = found || ok || err != nil
	}
	return found
}
