// SPDX-License-Identifier: MIT

package sessioninbox

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrMessagingUnavailable is returned by the incoming-message buffering
// methods (EnqueueInbox / EnqueueDLQ) when the Coordinator is nil — the
// gateway has no inbox/DLQ wired (no Redis). The §7.2 caller maps it to
// a `DeliveryStatusError` receipt with `reason: inbox_unavailable`
// rather than silently dropping the message.
var ErrMessagingUnavailable = errors.New("sessioninbox: coordinator not wired")

// DrainFailureRecorder records a §7.2 inbox-to-DLQ drain
// failure for the `lenny_inbox_drain_failure_total{pool, session_state}`
// counter. *gatewaymetrics.Metrics satisfies it; a nil recorder
// disables the metric.
//
// spec: §7.2; §16.5 InboxDrainFailure alert.
type DrainFailureRecorder interface {
	RecordInboxDrainFailure(pool, sessionState string)
}

// Coordinator bundles a session's inbox, DLQ, and event Emitter and
// provides the §7.2 lifecycle operations the gateway drives on state
// transitions: the inbox-to-DLQ migration on `resume_pending`, the
// inbox+DLQ drain on a terminal transition, and the DLQ TTL sweep. A
// nil Coordinator is a no-op everywhere, so a gateway running without
// Redis (or with messaging disabled) leaves transitions unaffected.
//
// spec: §7.2.
type Coordinator struct {
	inbox    Inbox
	dlq      *DLQ
	emit     Emitter
	now      func() time.Time
	failures DrainFailureRecorder
	// durable mirrors the deployment `messaging.durableInbox` flag. In
	// durable mode the inbox is Redis-backed and survives coordinator
	// failover, so `resume_pending` leaves it in place (with an EXPIRE)
	// rather than draining it to the DLQ.
	durable bool
}

// Config configures NewCoordinator.
type Config struct {
	// Inbox is the per-session message buffer (MemoryInbox by default,
	// RedisInbox in durable mode). Required.
	Inbox Inbox
	// DLQ is the Redis-backed dead-letter queue. Required.
	DLQ *DLQ
	// Emitter publishes message_expired / inbox_cleared events. Optional;
	// nil disables event emission (the events are best-effort).
	Emitter Emitter
	// Now returns the current time. Nil selects time.Now.
	Now func() time.Time
	// Failures records the lenny_inbox_drain_failure_total counter.
	// Optional.
	Failures DrainFailureRecorder
	// Durable selects durable-inbox semantics for the migration path.
	Durable bool
}

// NewCoordinator returns a Coordinator from cfg. It returns nil when the
// inbox or DLQ is missing so callers can treat "messaging not wired" and
// "no-op coordinator" uniformly.
func NewCoordinator(cfg Config) *Coordinator {
	if cfg.Inbox == nil || cfg.DLQ == nil {
		return nil
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Coordinator{
		inbox:    cfg.Inbox,
		dlq:      cfg.DLQ,
		emit:     cfg.Emitter,
		now:      now,
		failures: cfg.Failures,
		durable:  cfg.Durable,
	}
}

// MigrateInboxToDLQ performs the §7.2 atomic inbox drain on a
// `resume_pending` transition and on a suspension release, which leaves the
// session `suspended` with no pod. sessionState is the state the session
// entered, the `session_state` label of a drain failure.
//
// In the default (in-memory) mode it reads every inbox message in FIFO
// order and writes each to the DLQ with the default TTL, keeping each
// message's original EnqueuedAt so the DLQ's score order stays acceptance
// order, then returns the count preserved. A DLQ write failure increments
// `lenny_inbox_drain_failure_total` and returns the error; the caller
// still commits the `resume_pending` transition (pod failure is
// non-negotiable) but logs the loss.
//
// In durable mode the inbox is already persisted in Redis and survives
// coordinator crashes, so it is left in place with no TTL and the call
// returns 0. A durable inbox entry stays until the delivery of held
// messages delivers it, the maxInboxSize overflow drops it, or the
// terminal drain removes it.
//
// spec: §7.2 (Interactive Session Model).
func (c *Coordinator) MigrateInboxToDLQ(ctx context.Context, tenantID, sessionID, pool, sessionState string) (preserved int, err error) {
	if c == nil || c.durable {
		return 0, nil
	}
	msgs, derr := c.inbox.Drain(ctx, tenantID, sessionID)
	if derr != nil {
		c.recordDrainFailure(pool, sessionState)
		return 0, derr
	}
	for _, m := range msgs {
		if _, eerr := c.dlq.Enqueue(ctx, tenantID, sessionID, m, DefaultDLQTTL); eerr != nil {
			// Partial drain: the messages already written survive; the
			// remainder is the documented loss window.
			c.recordDrainFailure(pool, sessionState)
			return preserved, eerr
		}
		preserved++
	}
	return preserved, nil
}

// Peek returns the session inbox's oldest entry and its handle without
// removing it. A nil Coordinator returns an empty result and nil.
// spec: §7.2 (Message delivery routing — seven paths, path 6).
func (c *Coordinator) Peek(ctx context.Context, tenantID, sessionID string) (Message, string, bool, error) {
	if c == nil {
		return Message{}, "", false, nil
	}
	return c.inbox.Peek(ctx, tenantID, sessionID)
}

// Ack removes the inbox entry handle names. A nil Coordinator returns false
// and nil. spec: §7.2 (Message delivery routing — seven paths, path 6).
func (c *Coordinator) Ack(ctx context.Context, tenantID, sessionID, handle string) (bool, error) {
	if c == nil {
		return false, nil
	}
	return c.inbox.Ack(ctx, tenantID, sessionID, handle)
}

// PeekOldest returns the session DLQ's lowest-scored entry and its raw
// member without removing it. A nil Coordinator returns an empty result and
// nil. spec: §7.2 (Message delivery routing — seven paths, path 6).
func (c *Coordinator) PeekOldest(ctx context.Context, tenantID, sessionID string) (Message, string, bool, error) {
	if c == nil {
		return Message{}, "", false, nil
	}
	return c.dlq.PeekOldest(ctx, tenantID, sessionID)
}

// Remove removes the DLQ entry member names. A nil Coordinator returns false
// and nil. spec: §7.2 (Message delivery routing — seven paths, path 6).
func (c *Coordinator) Remove(ctx context.Context, tenantID, sessionID, member string) (bool, error) {
	if c == nil {
		return false, nil
	}
	return c.dlq.Remove(ctx, tenantID, sessionID, member)
}

// EnqueueInbox buffers an incoming message in the session's inbox per
// the §7.2 paths 3, 5, and 6 (input_required / runtime-busy /
// suspended) routing. It returns the message the `maxInboxSize`
// overflow policy evicted (nil when nothing was dropped) so the caller
// can return a `dropped` delivery receipt with `reason: inbox_overflow`.
// A nil msg.EnqueuedAt is stamped with the coordinator clock so FIFO
// order and the durable-inbox per-message TTL are well defined.
//
// spec: §7.2.
func (c *Coordinator) EnqueueInbox(ctx context.Context, tenantID, sessionID string, msg Message) (dropped *Message, err error) {
	if c == nil {
		return nil, ErrMessagingUnavailable
	}
	if msg.EnqueuedAt.IsZero() {
		msg.EnqueuedAt = c.now()
	}
	return c.inbox.Enqueue(ctx, tenantID, sessionID, msg)
}

// EnqueueDLQ buffers an incoming message in the session's dead-letter
// queue per the §7.2 path 7 (recovering target) and the pre-running
// inter-session row of the dead-letter-handling table. ttl bounds the
// entry; a non-positive ttl selects DefaultDLQTTL. It returns the
// message the `maxDLQSize` overflow policy evicted (nil when nothing
// was dropped) so the caller can return a `dropped` delivery receipt
// with `reason: dlq_overflow`.
//
// spec: §7.2; the dead-letter table recovering and pre-running
// rows.
func (c *Coordinator) EnqueueDLQ(ctx context.Context, tenantID, sessionID string, msg Message, ttl time.Duration) (dropped *Message, err error) {
	if c == nil {
		return nil, ErrMessagingUnavailable
	}
	if msg.EnqueuedAt.IsZero() {
		msg.EnqueuedAt = c.now()
	}
	return c.dlq.Enqueue(ctx, tenantID, sessionID, msg, ttl)
}

// HeldLen counts a session's held messages for the backlog check of
// §7.2 delivery path 6. With a zero acceptedAfter it counts every DLQ
// entry; otherwise it counts the DLQ entries accepted after acceptedAfter.
// When withInbox is true it adds the inbox length. A nil Coordinator
// returns 0.
//
// spec: §7.2 (Message delivery routing — seven paths, path 6).
func (c *Coordinator) HeldLen(ctx context.Context, tenantID, sessionID string, withInbox bool, acceptedAfter time.Time) (int, error) {
	if c == nil {
		return 0, nil
	}
	var (
		n   int
		err error
	)
	if acceptedAfter.IsZero() {
		n, err = c.dlq.Len(ctx, tenantID, sessionID)
	} else {
		n, err = c.dlq.CountAcceptedAfter(ctx, tenantID, sessionID, acceptedAfter)
	}
	if err != nil {
		return 0, fmt.Errorf("count held DLQ entries of session %s: %w", sessionID, err)
	}
	if !withInbox {
		return n, nil
	}
	in, err := c.inbox.Len(ctx, tenantID, sessionID)
	if err != nil {
		return 0, fmt.Errorf("count inbox entries of session %s: %w", sessionID, err)
	}
	return n + in, nil
}

// EraseSession deletes the session's DLQ and inbox for the §12.8 user
// erasure and returns how many entries the two held. It emits no
// message_expired, because the erasure removes the sender's data rather
// than ending the session. A nil Coordinator erases nothing.
//
// spec: §12.8 (Compliance Interfaces).
func (c *Coordinator) EraseSession(ctx context.Context, tenantID, sessionID string) (int, error) {
	if c == nil {
		return 0, nil
	}
	dlqN, err := c.dlq.Delete(ctx, tenantID, sessionID)
	if err != nil {
		return 0, fmt.Errorf("erase DLQ of session %s: %w", sessionID, err)
	}
	inN, err := c.inbox.Delete(ctx, tenantID, sessionID)
	if err != nil {
		return dlqN, fmt.Errorf("erase inbox of session %s: %w", sessionID, err)
	}
	return dlqN + inN, nil
}

// InboxLen returns the current depth of the session inbox, backing the
// §15.4 delivery-receipt `queueDepth` field for a `queued` outcome. A
// nil Coordinator returns 0.
func (c *Coordinator) InboxLen(ctx context.Context, tenantID, sessionID string) (int, error) {
	if c == nil {
		return 0, nil
	}
	return c.inbox.Len(ctx, tenantID, sessionID)
}

// DrainOnTerminal performs the §7.2 / §7.3 inbox+DLQ
// drain on a terminal transition. It reads every buffered message from
// both the inbox and the DLQ, emits a `message_expired` event with
// `reason: "target_terminated"` on each registered sender's event
// stream, and deletes the DLQ key. Messages with no sender session
// (external-client sources) are drained without an event, since there is
// no sender stream to notify.
//
// spec: §7.2; §7.3; §15.4 reason target_terminated.
func (c *Coordinator) DrainOnTerminal(ctx context.Context, tenantID, sessionID string) (drained int, err error) {
	if c == nil {
		return 0, nil
	}
	now := c.now()
	inboxMsgs, ierr := c.inbox.Drain(ctx, tenantID, sessionID)
	if ierr != nil {
		return 0, ierr
	}
	dlqMsgs, derr := c.dlq.DrainAll(ctx, tenantID, sessionID)
	if derr != nil {
		// Still emit for the inbox messages already read.
		c.emitExpired(tenantID, sessionID, inboxMsgs, ReasonTargetTerminated, now)
		return len(inboxMsgs), derr
	}
	all := append(inboxMsgs, dlqMsgs...)
	c.emitExpired(tenantID, sessionID, all, ReasonTargetTerminated, now)
	return len(all), nil
}

// ClearInboxOnAcquire emits the §7.2 event on the
// target session's own event stream when a new coordinator acquires a
// session whose in-memory inbox did not survive the handoff. The gateway
// calls it on the resume path (`POST /resume`), the v1 point at which a
// coordinator re-establishes coordination of a recovering session that
// lost its pod.
//
// In the default in-memory mode the prior coordinator's inbox is gone, so
// the event signals the target client that buffered messages may have been
// lost; `messagesPreservedInDLQ` reports how many were drained to the DLQ
// on the `resume_pending` transition and will be redelivered on resume.
// In durable mode the Redis inbox survives failover and the new
// coordinator recovers it, so no `inbox_cleared` is emitted. A nil Emitter
// or nil Coordinator is a no-op.
//
// spec: §7.2.
func (c *Coordinator) ClearInboxOnAcquire(ctx context.Context, tenantID, sessionID string) (preservedInDLQ int, err error) {
	if c == nil || c.emit == nil {
		return 0, nil
	}
	if c.durable {
		// Durable inbox survives coordinator failover; nothing was cleared.
		return 0, nil
	}
	n, lerr := c.dlq.Len(ctx, tenantID, sessionID)
	if lerr != nil {
		return 0, lerr
	}
	c.emit.InboxCleared(tenantID, sessionID,
		NewInboxClearedEvent(sessionID, n, c.now()))
	return n, nil
}

// SweepExpired removes every DLQ entry whose TTL has elapsed and emits a
// `message_expired` event with `reason: "dlq_ttl_expired"` on each
// sender's stream. It is the periodic-sweeper hook a recovering session
// runs while it waits to resume.
//
// spec: §7.2; §15.4 reason dlq_ttl_expired.
func (c *Coordinator) SweepExpired(ctx context.Context, tenantID, sessionID string) (expired int, err error) {
	if c == nil {
		return 0, nil
	}
	now := c.now()
	msgs, serr := c.dlq.SweepExpired(ctx, tenantID, sessionID, now)
	if serr != nil {
		return 0, serr
	}
	c.emitExpired(tenantID, sessionID, msgs, ReasonDLQTTLExpired, now)
	return len(msgs), nil
}

// emitExpired publishes a message_expired event for each message that
// carries a sender session, scoped to tenantID (cross-tenant messaging
// is denied upstream, so sender and target share a tenant).
func (c *Coordinator) emitExpired(tenantID, targetSessionID string, msgs []Message, reason string, now time.Time) {
	if c.emit == nil {
		return
	}
	for _, m := range msgs {
		if m.SenderSessionID == "" {
			continue
		}
		c.emit.MessageExpired(tenantID, m.SenderSessionID,
			NewMessageExpiredEvent(m.MessageID, targetSessionID, reason, now))
	}
}

func (c *Coordinator) recordDrainFailure(pool, sessionState string) {
	if c.failures != nil {
		c.failures.RecordInboxDrainFailure(pool, sessionState)
	}
}
