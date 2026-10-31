// SPDX-License-Identifier: MIT

package slotcounter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/redis/go-redis/v9"
)

// This file carries the recovery of the slot sets when a Redis outage ends.
// §12.4 requires the gateway to rebuild the slot sets before fast-path
// enforcement resumes. The rebuild is add-only: it adds the members this
// Counter recorded in its outage ledger (fallback admissions and leaked
// marks) and deletes the rehydrated flag of every pod a non-terminal
// concurrent-workspace session names, so each such pod's next reservation or
// release rehydrates its set from the session rows. It removes no member, so
// a member whose release failed during the outage stays counted, which fails
// closed; the leader's stale-slot release returns it later.
//
// spec: §12.4 (Redis failure behavior), §5.2 (Post-recovery rehydration
// atomicity), §6.2 (leaked slot semantics).

// DefaultRecoveryProbeInterval is the cadence at which RunRecovery probes
// Redis reachability. It matches storagequota.DefaultRecoveryProbeInterval.
const DefaultRecoveryProbeInterval = 2 * time.Second

// recoveryProbeTimeout bounds one reachability probe.
const recoveryProbeTimeout = time.Second

// entryKind names the two kinds of outage-ledger entry.
type entryKind uint8

const (
	// entryAdmission records a session the §12.4 fallback admitted; the
	// rebuild adds the session's own member.
	entryAdmission entryKind = iota + 1
	// entryLeaked records a leaked release that failed; the rebuild adds
	// the leaked:<sessionId> member.
	entryLeaked
)

// ledgerEntry is one outage-ledger entry.
type ledgerEntry struct {
	kind      entryKind
	podID     string
	sessionID string
}

// member returns the set member the rebuild adds for the entry.
func (e ledgerEntry) member() string {
	if e.kind == entryLeaked {
		return LeakedMember(e.sessionID)
	}
	return e.sessionID
}

// rebuildScript adds the ledger's members for one pod and, when the listing
// names the pod, deletes its rehydrated flag so the pod's next reservation or
// release rehydrates the set from the session rows. It removes no member.
// KEYS[1] is the active-slots set, KEYS[2] the rehydrated flag. ARGV[1] is
// '1' when the listing names the pod and '0' otherwise; ARGV[2..] are the
// members to add, possibly none.
// spec: §12.4 (Redis failure behavior).
var rebuildScript = redis.NewScript(`
for i = 2, #ARGV do redis.call('SADD', KEYS[1], ARGV[i]) end
if ARGV[1] == '1' then redis.call('DEL', KEYS[2]) end
return 1
`)

// WithRecoveryProbe overrides the reachability probe RunRecovery uses. The
// gateway wires redisconn.PingWithTimeout against the Counter's client; a
// test drives the probe deterministically. Nil keeps the default.
func WithRecoveryProbe(probe func(ctx context.Context) error) Option {
	return func(c *Counter) {
		if probe != nil {
			c.probe = probe
		}
	}
}

// WithRecoveryProbeInterval overrides DefaultRecoveryProbeInterval. A
// non-positive value keeps the default.
func WithRecoveryProbeInterval(d time.Duration) Option {
	return func(c *Counter) {
		if d > 0 {
			c.probeInterval = d
		}
	}
}

// record adds an outage-ledger entry, or re-stamps an existing one with a
// fresh sequence number so a rebuild that read it before keeps it.
func (c *Counter) record(kind entryKind, podID, sessionID string) {
	c.outageMu.Lock()
	c.ledgerSeq++
	c.ledger[ledgerEntry{kind: kind, podID: podID, sessionID: sessionID}] = c.ledgerSeq
	c.outageMu.Unlock()
}

// needsRebuild reports whether a rebuild is due: the outage clock is set or
// the ledger holds an entry.
func (c *Counter) needsRebuild() bool {
	c.outageMu.Lock()
	defer c.outageMu.Unlock()
	return !c.outageSince.IsZero() || len(c.ledger) > 0
}

// outageKnown reports whether this Counter has observed a Redis outage that
// it has not finished recovering: the outage clock is set.
func (c *Counter) outageKnown() bool {
	c.outageMu.Lock()
	defer c.outageMu.Unlock()
	return !c.outageSince.IsZero()
}

// rebuildSnapshot is what one rebuild read under outageMu.
type rebuildSnapshot struct {
	since   time.Time
	entries map[ledgerEntry]uint64
}

// snapshot copies the outage clock and the ledger.
func (c *Counter) snapshot() rebuildSnapshot {
	c.outageMu.Lock()
	defer c.outageMu.Unlock()
	entries := make(map[ledgerEntry]uint64, len(c.ledger))
	for e, seq := range c.ledger {
		entries[e] = seq
	}
	return rebuildSnapshot{since: c.outageSince, entries: entries}
}

// rebuildAfterOutage runs the add-only recovery rebuild. It is
// single-flight per Counter: a caller that waited for a peer's rebuild
// finds nothing left to do and returns. With the outage clock set it lists
// the pods a non-terminal concurrent-workspace session names; for each pod
// the listing or the ledger names, one rebuildScript call adds the ledger's
// members for the pod and, for a listed pod, deletes its rehydrated flag.
// An entry is removed once its pod's script succeeds, unless it was
// recorded again after the snapshot. Only a rebuild that leaves the ledger
// empty clears the clock, and only when no later outage restamped it.
//
// spec: §12.4 (Redis failure behavior), §5.2 (Post-recovery rehydration
// atomicity).
func (c *Counter) rebuildAfterOutage(ctx context.Context) error {
	c.rebuildMu.Lock()
	defer c.rebuildMu.Unlock()
	snap := c.snapshot()
	if snap.since.IsZero() && len(snap.entries) == 0 {
		return nil
	}
	listed, err := c.listedPods(ctx, !snap.since.IsZero())
	if err != nil {
		return err
	}
	for _, pod := range rebuildPods(listed, snap.entries) {
		if err := c.rebuildPod(ctx, pod, listed[pod], snap.entries); err != nil {
			return err
		}
		c.dropEntries(pod, snap.entries)
	}
	c.clearOutage(snap.since)
	return nil
}

// listedPods returns the pods the slot-holder listing names, as a set. It
// lists only while the outage clock is set and a SlotSource is wired.
func (c *Counter) listedPods(ctx context.Context, clockSet bool) (map[string]bool, error) {
	listed := map[string]bool{}
	if !clockSet || c.source == nil {
		return listed, nil
	}
	pods, err := c.source.ListPodsWithActiveSlots(ctx)
	if err != nil {
		return nil, fmt.Errorf("slotcounter: recovery rebuild: list pods with active slots: %w: %w", errSlotSourceRead, err)
	}
	for _, pod := range pods {
		if pod != "" {
			listed[pod] = true
		}
	}
	return listed, nil
}

// rebuildPods returns, in a stable order, every pod the listing or the
// ledger names.
func rebuildPods(listed map[string]bool, entries map[ledgerEntry]uint64) []string {
	set := make(map[string]bool, len(listed))
	for pod := range listed {
		set[pod] = true
	}
	for e := range entries {
		set[e.podID] = true
	}
	pods := make([]string, 0, len(set))
	for pod := range set {
		pods = append(pods, pod)
	}
	sort.Strings(pods)
	return pods
}

// rebuildPod runs rebuildScript for one pod.
func (c *Counter) rebuildPod(ctx context.Context, pod string, listed bool, entries map[ledgerEntry]uint64) error {
	flag := "0"
	if listed {
		flag = "1"
	}
	args := []any{flag}
	for e := range entries {
		if e.podID == pod {
			args = append(args, e.member())
		}
	}
	if err := rebuildScript.Run(ctx, c.client,
		[]string{activeSlotsKey(pod), rehydratedKey(pod)}, args...).Err(); err != nil {
		return fmt.Errorf("slotcounter: recovery rebuild %s: %w", pod, err)
	}
	return nil
}

// dropEntries removes pod's entries that the snapshot read, keeping any
// entry recorded again after the snapshot.
func (c *Counter) dropEntries(pod string, read map[ledgerEntry]uint64) {
	c.outageMu.Lock()
	defer c.outageMu.Unlock()
	for e, seq := range read {
		if e.podID == pod && c.ledger[e] == seq {
			delete(c.ledger, e)
		}
	}
}

// clearOutage clears the outage clock after a successful rebuild when the
// ledger is empty and the clock still holds the value the rebuild read, so
// an outage stamped during the rebuild keeps its own window.
// spec: §12.4 (on Redis recovery the counter resumes fast-path enforcement).
func (c *Counter) clearOutage(since time.Time) {
	c.outageMu.Lock()
	defer c.outageMu.Unlock()
	if len(c.ledger) == 0 && c.outageSince.Equal(since) {
		c.outageSince = time.Time{}
	}
}

// RunRecovery probes Redis on the recovery probe interval until ctx ends, so
// a replica with no slot traffic still rebuilds the slot sets when an outage
// ends. An unreachable probe stamps the outage clock; a reachable probe runs
// the rebuild when one is due. It blocks; callers start it in a goroutine.
// spec: §12.4 (Redis failure behavior).
func (c *Counter) RunRecovery(ctx context.Context) {
	ticker := time.NewTicker(c.probeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.probeTick(ctx)
		}
	}
}

// probeTick runs one recovery probe round.
func (c *Counter) probeTick(ctx context.Context) {
	if err := c.ping(ctx); err != nil {
		c.stampOutage()
		return
	}
	if !c.needsRebuild() {
		return
	}
	if err := c.rebuildAfterOutage(ctx); err != nil && !errors.Is(err, context.Canceled) {
		slog.WarnContext(ctx, "slotcounter: recovery rebuild failed; retrying on the next probe", "error", err)
	}
}

// ping runs the configured probe, or a bounded Ping on the Counter's client.
func (c *Counter) ping(ctx context.Context) error {
	if c.probe != nil {
		return c.probe(ctx)
	}
	pctx, cancel := context.WithTimeout(ctx, recoveryProbeTimeout)
	defer cancel()
	return c.client.Ping(pctx).Err()
}
