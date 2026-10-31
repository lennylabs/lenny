// SPDX-License-Identifier: MIT

package slotcounter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
)

// This file carries the §12.4 Redis-outage Postgres fallback for the per-pod
// slot counter, separate from the §5.2 fast-path counter and rehydration in
// slotcounter.go. §12.4 requires every Redis-backed role to have a durable
// fallback; for the intra-pod capacity gate that fallback is the Postgres
// SessionStore, gated under a per-pod advisory lock and bounded by a
// fail-closed window so a Redis-only outage degrades slot admission to
// Postgres latency rather than rejecting all session dispatch.
// spec: §12.4 (durable fallback for every Redis-backed role), §12.4 (Redis HA
// and failure modes, bounded fail-closed window).

// ErrFailClosed reports that the §12.4 Redis-outage fallback could not
// admit a slot and the gate is failing closed: the Redis-outage window
// exceeded slotCounterPostgresFallbackMaxSeconds, no Postgres fallback was
// wired, or Postgres was also unavailable. The caller rejects new session
// dispatch requiring a slot on the pod (mapped to WARM_POOL_EXHAUSTED at
// the gateway). spec: §12.4 (fail closed after a bounded outage window or on
// dual-store unavailability).
var ErrFailClosed = errors.New("slotcounter: redis outage fallback failed closed")

// DefaultPostgresFallbackMaxWindow bounds the §12.4 Redis-outage
// Postgres-fallback window (`slotCounterPostgresFallbackMaxSeconds`,
// default 60s). After Redis has been continuously unreachable for longer
// than this, slot admission fails closed: a sustained Redis outage cannot
// keep the gateway gating capacity on the higher-latency Postgres path
// indefinitely.
// spec: §12.4 ("after slotCounterPostgresFallbackMaxSeconds (default: 60s)
// with Redis still unavailable ... slot admission fails closed").
const DefaultPostgresFallbackMaxWindow = 60 * time.Second

// FallbackSource is the §12.4 Redis-outage capacity gate and the durable
// record of slot reservations it counts. When Redis is unreachable the
// Counter routes intra-pod slot admission to ReserveSlotUnderLock, which
// counts the pod's holders and reservations, decides, and records the
// admitted session's reservation under a per-pod Postgres advisory lock, so
// two concurrent admissions cannot both observe the same free slot. While
// Redis is reachable the Counter records each reservation with
// RecordSlotReservation before its script runs, and every release deletes
// the record with DeleteSlotReservation, so the gate counts a reservation
// whichever path admitted it. The production implementation is the
// Postgres-backed SessionStore. A nil fallback disables the gate and the
// record: a Redis outage then fails closed immediately rather than
// degrading to Postgres latency.
// spec: §12.4 (durable fallback for every Redis-backed role); §12.4; §5.2.
type FallbackSource interface {
	ReserveSlotUnderLock(ctx context.Context, podID, sessionID string, maxConcurrent int32) (count int32, admitted bool, err error)
	RecordSlotReservation(ctx context.Context, podID, sessionID string) (inserted bool, err error)
	DeleteSlotReservation(ctx context.Context, podID, sessionID string) error
}

// reservationCleanupTimeout bounds the deletion of a reservation record
// after a failed reservation or a release. The deletion runs on a context
// detached from the caller's cancellation, so a cancelled create still
// removes the record it wrote; a deletion that does not finish leaves a
// record that over-counts until the stale-slot reaper reclaims it.
const reservationCleanupTimeout = 5 * time.Second

// reservationReleaseTimeout bounds the deletion of a reservation record
// inside a slot release. It is shorter than reservationCleanupTimeout because
// the release runs synchronously ahead of the occupancy-zero disposition, on
// the caller's budget of about the same length: a stalled Postgres must not
// spend that budget, and a record left behind only over-counts until the
// stale-slot reaper reclaims it.
const reservationReleaseTimeout = time.Second

// recordReservation writes sessionID's reservation record on podID before
// the reservation runs, and reports whether it inserted one. With no
// fallback wired it records nothing. A failed write fails the reservation
// closed: the gate could not count a reservation whose record is missing.
// spec: §12.4 (Redis failure behavior), §5.2 (Slot assignment atomicity).
func (c *Counter) recordReservation(ctx context.Context, podID, sessionID string) (bool, error) {
	if c.fallback == nil {
		return false, nil
	}
	inserted, err := c.fallback.RecordSlotReservation(ctx, podID, sessionID)
	if err != nil {
		return false, fmt.Errorf("slotcounter: reserve %s: record reservation: %w: %w", podID, ErrFailClosed, err)
	}
	return inserted, nil
}

// dropReservation deletes sessionID's reservation record on podID, on a
// context detached from the caller's cancellation and bounded by timeout. A
// failure is logged and left to the stale-slot reaper: the record over-counts the pod until then.
// spec: §12.4 (Redis failure behavior), §5.2 (Slot assignment atomicity).
func (c *Counter) dropReservation(ctx context.Context, podID, sessionID string, timeout time.Duration) {
	if c.fallback == nil {
		return
	}
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	if err := c.fallback.DeleteSlotReservation(dctx, podID, sessionID); err != nil {
		slog.WarnContext(ctx, "slotcounter: delete slot reservation record failed; the stale-slot reaper reclaims it",
			"pod", podID, "session_id", sessionID, "error", err)
	}
}

// WithFallbackSource wires the §12.4 Redis-outage capacity gate (the
// Postgres-backed SessionStore in production). Without it a Redis outage
// fails closed immediately rather than degrading to Postgres latency.
func WithFallbackSource(f FallbackSource) Option {
	return func(c *Counter) { c.fallback = f }
}

// WithFallbackMaxWindow overrides the default §12.4
// slotCounterPostgresFallbackMaxSeconds. A non-positive value keeps the
// default.
func WithFallbackMaxWindow(d time.Duration) Option {
	return func(c *Counter) {
		if d > 0 {
			c.fallbackMaxWindow = d
		}
	}
}

// WithClockForTest overrides the wall clock for the §12.4 outage-window
// measurement. It exists for tests that need to advance the outage window
// deterministically; production uses time.Now.
func WithClockForTest(now func() time.Time) Option {
	return func(c *Counter) {
		if now != nil {
			c.now = now
		}
	}
}

// fallbackReserve is the §12.4 Redis-outage capacity gate. It gates intra-pod
// slot admission on the Postgres FallbackSource under a per-pod advisory lock
// so two concurrent admissions during the outage cannot both observe the same
// free slot. The fallback window is bounded: the gate fails closed when Redis
// has been continuously unreachable longer than fallbackMaxWindow, when no
// Postgres fallback is wired, or when Postgres is also unavailable (dual-store
// outage). A Redis-only outage therefore degrades slot admission to Postgres
// latency rather than rejecting all session dispatch.
//
// The call stamps the outage clock when it is zero, and an admission records
// (pod, session) in the outage ledger, so the recovery rebuild adds the
// admitted session's member when Redis returns (recovery.go).
//
// spec: §12.4 ("Postgres fallback under a per-pod advisory lock, then fail
// closed"); §5.2 (intra-pod capacity gate during a Redis outage).
func (c *Counter) fallbackReserve(ctx context.Context, podID, sessionID string, maxConcurrent int32, rehydrated bool) (int32, bool, error) {
	// Stamp (or read) the start of the current outage window and fail closed
	// once it exceeds the bound. The window is measured from the first call
	// that observed Redis unreachable.
	exceeded := c.outageExceeded()
	if c.fallback == nil {
		// No Postgres fallback configured: a Redis outage fails closed.
		return 0, rehydrated, ErrFailClosed
	}
	if exceeded {
		return 0, rehydrated, fmt.Errorf("slotcounter: reserve %s: outage window exceeded: %w", podID, ErrFailClosed)
	}
	count, admitted, err := c.fallback.ReserveSlotUnderLock(ctx, podID, sessionID, maxConcurrent)
	if err != nil {
		// Postgres is also unavailable during the Redis outage: dual-store
		// outage fails closed immediately.
		return 0, rehydrated, fmt.Errorf("slotcounter: reserve %s: postgres fallback unavailable: %w", podID, ErrFailClosed)
	}
	if !admitted {
		return 0, rehydrated, ErrSlotsExhausted
	}
	c.record(entryAdmission, podID, sessionID)
	return count, rehydrated, nil
}

// outageExceeded reports whether the current Redis outage window has
// exceeded fallbackMaxWindow. The first call during an outage stamps the
// window start; subsequent calls measure against it. spec: §12.4.
func (c *Counter) outageExceeded() bool {
	c.outageMu.Lock()
	defer c.outageMu.Unlock()
	now := c.now()
	if c.outageSince.IsZero() {
		c.outageSince = now
		return false
	}
	return now.Sub(c.outageSince) > c.fallbackMaxWindow
}

// stampOutage stamps the outage clock when it is zero. spec: §12.4.
func (c *Counter) stampOutage() {
	c.outageMu.Lock()
	if c.outageSince.IsZero() {
		c.outageSince = c.now()
	}
	c.outageMu.Unlock()
}

// isRedisUnavailable reports whether err is a Redis-connectivity failure
// (a network or pool error) rather than a Redis-Nil miss or a Lua script
// error. A Redis outage routes slot admission to the §12.4 Postgres
// fallback; any other error is a genuine failure that propagates.
func isRedisUnavailable(err error) bool {
	if err == nil || errors.Is(err, redis.Nil) {
		return false
	}
	// A context cancellation or deadline is the caller's signal, not a Redis
	// outage; propagate it unchanged.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	if errors.Is(err, redis.ErrClosed) || errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	// A server that stops while a command is in flight closes the
	// connection, and go-redis returns the read's io.EOF unwrapped. A script
	// that ran before the connection closed leaves its member in the set,
	// which over-counts the pod after the outage and never under-counts it.
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	// go-redis wraps dial and pool exhaustion failures in messages the
	// typed checks above do not cover; match the stable substrings the
	// client emits so a connection outage routes to the fallback.
	msg := err.Error()
	for _, frag := range redisUnavailableFragments {
		if strings.Contains(msg, frag) {
			return true
		}
	}
	return false
}

// redisUnavailableFragments are the stable error-message substrings
// go-redis emits on a connectivity failure (dial, pool, or closed client)
// that the typed net.Error / redis.ErrClosed checks do not catch.
var redisUnavailableFragments = []string{
	"connection refused",
	"connect: connection refused",
	"no such host",
	"i/o timeout",
	"connection reset by peer",
	"redis: client is closed",
	"connection pool timeout",
	"broken pipe",
}
