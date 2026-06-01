// SPDX-License-Identifier: MIT

// Package events is the gateway's session event bus. It backs the
// §15.1 `GET /v1/sessions/{id}/events` SSE stream: session activity
// (message delivered, response produced, state transition) is
// published to the bus and relayed to every subscribed client. Each
// event carries a monotonic per-session sequence so a reconnecting
// client can resume with a cursor (the §15.1 streaming-reconnect
// contract).
//
// In single-replica deployments the Bus uses its in-memory state
// alone. In multi-replica deployments the Bus pairs with the §4.4
// line 225 / §12.3.7 RedisRelay (see redisrelay.go): every Publish
// fans out to Redis Streams via `XADD`, and every Subscribe pulls
// the cross-replica history via `XRANGE` so a client that reconnects
// to a different replica sees the prior events.
//
// spec: §4.4 line 225 — durable event cursors / stream offsets across
// replicas.
package sessionevents

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Event is one session event.
type Event struct {
	// Seq is the monotonic per-session sequence, starting at 1.
	Seq uint64 `json:"seq"`

	// SessionID scopes the event.
	SessionID string `json:"sessionId"`

	// Type is the event type (e.g., `message_delivered`,
	// `response`, `state_changed`).
	Type string `json:"type"`

	// Data is the event payload, an already-marshalled JSON object.
	Data string `json:"data"`

	// Timestamp is the UTC instant the event was published.
	Timestamp time.Time `json:"timestamp"`
}

// Bus is the session event bus. The in-memory state (per-session
// sequence counter, replay history, live subscriber list) is the
// authoritative source on the publishing replica. When the optional
// RedisRelay is wired the Bus also fans out every publish to Redis
// Streams so reading replicas can serve a reconnect-with-cursor
// against the cross-replica stream.
//
// Tenant binding: each session id is registered to its owning tenant
// on the first Publish, so Subscribe rejects a tenant id that does not
// match the registration (§7.2 tenant isolation defense-in-depth).
// Sessions IDs are random UUIDv8 prefixes (§15.1) so collisions are
// implausible, but the Bus enforces the binding at the interface
// boundary rather than relying on every call site to pre-check tenant
// ownership.
type Bus struct {
	mu sync.Mutex
	// seq tracks the next sequence per session.
	seq map[string]uint64
	// history retains recent events per session for cursor replay.
	history map[string][]Event
	// subs maps a session to its active subscriber channels.
	subs map[string][]*subscription
	// tenant records the tenant id a session was first published under.
	// Subscribe rejects mismatched tenant ids (§7.2 tenant isolation).
	tenant map[string]string
	// maxHistory bounds the per-session retained event count.
	maxHistory int
	// relay is the §4.4 / §12.3.7 cross-replica Redis fan-out. A nil
	// relay reduces the Bus to the single-replica path.
	relay *RedisRelay
	// lastSeqPersister, when set, durably advances the session row's
	// last_seq counter on every Publish. The persister is best-effort
	// and asynchronous so a Postgres outage does not stall in-memory
	// subscribers. F-7.3.3.
	lastSeqPersister LastSeqPersister
	// seedLoader, when set, is consulted on the first Publish for a
	// session to seed the in-memory counter from the persisted
	// last_seq — the §7.3 line 397 "coordinator-local copy is an
	// advisory cache primed from Postgres at handoff step 0" contract.
	// A nil loader leaves the counter at 0 (single-replica or no
	// prior coordinator). F-7.3.3.
	seedLoader LastSeqLoader
	// seeded records the sessions whose counter has been primed from
	// Postgres so the seed lookup runs at most once per (replica,
	// session). F-7.3.3.
	seeded map[string]struct{}
}

// LastSeqPersister durably advances the §7.3 line 397 sessions.last_seq
// counter for a session. Implementations MUST treat the value as a
// monotonic floor — a write that would rewind an existing higher value
// is a no-op (the Postgres pgstore enforces this via GREATEST). The
// publish path invokes the persister asynchronously so a Postgres
// slowdown cannot stall in-memory subscribers. F-7.3.3.
type LastSeqPersister interface {
	AdvanceLastSeq(ctx context.Context, tenantID, sessionID string, seq int64) error
}

// LastSeqLoader returns the persisted §7.3 line 397 sessions.last_seq
// counter for a session, used to seed the Bus's in-memory counter on
// the first publish for that session (the §7.3 / §10.4 "primed from
// Postgres at handoff step 0" contract). A miss returns (0, nil) so the
// Bus starts at 1 for a freshly-created session. F-7.3.3.
type LastSeqLoader interface {
	LoadLastSeq(ctx context.Context, tenantID, sessionID string) (int64, error)
}

// subscription is one active SSE client.
type subscription struct {
	ch     chan Event
	closed bool
}

// NewBus returns an empty Bus. maxHistory bounds the per-session
// in-memory replay buffer; pass 0 for the default of 256. The Bus
// runs single-replica until WithRedisRelay attaches the cross-replica
// fan-out.
func NewBus(maxHistory int) *Bus {
	if maxHistory <= 0 {
		maxHistory = 256
	}
	return &Bus{
		seq:        map[string]uint64{},
		history:    map[string][]Event{},
		subs:       map[string][]*subscription{},
		tenant:     map[string]string{},
		seeded:     map[string]struct{}{},
		maxHistory: maxHistory,
	}
}

// WithLastSeqPersister wires the §7.3 line 397 durable counter writer.
// Every Publish schedules an asynchronous AdvanceLastSeq so the
// persisted sessions.last_seq advances atomically with each event
// without blocking subscribers. A nil persister keeps the Bus's local
// counter advisory-only (the single-replica posture). F-7.3.3.
func (b *Bus) WithLastSeqPersister(p LastSeqPersister) *Bus {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lastSeqPersister = p
	return b
}

// WithLastSeqLoader wires the §7.3 line 397 / §10.4 handoff seed
// loader. On the first Publish for a session the Bus consults the
// loader to prime its in-memory counter from the persisted last_seq,
// so a fresh replica picking up a session never rewinds. A nil loader
// keeps the Bus on the single-replica path (counter starts at 0).
// F-7.3.3.
func (b *Bus) WithLastSeqLoader(l LastSeqLoader) *Bus {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seedLoader = l
	return b
}

// ErrTenantMismatch reports that a Subscribe call presented a tenant id
// that does not match the session's registered owner. Surfacing this
// rather than silently delivering events to a foreign-tenant subscriber
// is the §7.2 tenant-isolation defense-in-depth.
var ErrTenantMismatch = errors.New("sessionevents: session belongs to a different tenant")

// WithRedisRelay attaches the §4.4 / §12.3.7 cross-replica relay so
// every Publish fans out to Redis Streams and every Subscribe
// consults the cross-replica history. A nil relay disables the
// cross-replica path (the default single-replica behaviour).
//
// The relay attaches at construction time; later changes require a
// fresh Bus so the publish path and history path stay consistent.
//
// spec: §4.4 line 225.
func (b *Bus) WithRedisRelay(relay *RedisRelay) *Bus {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.relay = relay
	return b
}

// Publish appends an event to a session's stream, assigns its Seq,
// retains it for cursor replay, and fans it out to every live
// subscriber. A slow subscriber whose channel is full is skipped
// (the event remains in history for a reconnect-with-cursor).
//
// PublishForTenant is the tenant-aware variant; Publish preserves the
// legacy (untenanted) signature for callers in non-tenant-scoped
// contexts (e.g. internal bus self-tests). New code SHOULD call
// PublishForTenant so the §7.2 isolation invariant is upheld.
func (b *Bus) Publish(sessionID, eventType, data string, now time.Time) Event {
	return b.publish("", sessionID, eventType, data, now)
}

// PublishForTenant publishes a session event under tenantID. The
// session's tenant is registered on the first Publish and frozen; a
// later PublishForTenant with a different tenantID surfaces a noop
// (the event is dropped). The frozen tenant id is the predicate
// Subscribe enforces. spec: §7.2 tenant isolation.
func (b *Bus) PublishForTenant(tenantID, sessionID, eventType, data string, now time.Time) Event {
	return b.publish(tenantID, sessionID, eventType, data, now)
}

func (b *Bus) publish(tenantID, sessionID, eventType, data string, now time.Time) Event {
	b.mu.Lock()
	// First write wins for the tenant binding; a later publish under a
	// different tenant is rejected so the session id cannot be hijacked.
	if tenantID != "" {
		if existing, ok := b.tenant[sessionID]; !ok {
			b.tenant[sessionID] = tenantID
		} else if existing != tenantID {
			b.mu.Unlock()
			return Event{}
		}
	}
	// spec: §7.3 line 397 / §10.4 — prime the local counter from the
	// persisted last_seq on the first publish for the session so a
	// fresh replica picking up the session after a coordinator handoff
	// never rewinds. The seed is best-effort: a loader miss / error
	// leaves the counter at 0 (the single-replica or new-session
	// posture); the persister's GREATEST floor still prevents a
	// downstream rewind. F-7.3.3.
	if _, primed := b.seeded[sessionID]; !primed {
		b.seeded[sessionID] = struct{}{}
		if loader := b.seedLoader; loader != nil && tenantID != "" {
			// Drop the lock during the load so a slow Postgres lookup
			// does not stall sibling sessions' publishes.
			b.mu.Unlock()
			persisted, err := loader.LoadLastSeq(context.Background(), tenantID, sessionID)
			b.mu.Lock()
			if err == nil && persisted > 0 {
				if cur := b.seq[sessionID]; uint64(persisted) > cur {
					b.seq[sessionID] = uint64(persisted)
				}
			}
		}
	}
	b.seq[sessionID]++
	ev := Event{
		Seq:       b.seq[sessionID],
		SessionID: sessionID,
		Type:      eventType,
		Data:      data,
		Timestamp: now.UTC(),
	}
	hist := append(b.history[sessionID], ev)
	if len(hist) > b.maxHistory {
		hist = hist[len(hist)-b.maxHistory:]
	}
	b.history[sessionID] = hist
	for _, sub := range b.subs[sessionID] {
		if sub.closed {
			continue
		}
		select {
		case sub.ch <- ev:
		default:
			// Slow consumer — drop the live delivery; the event is
			// in history for a reconnect-with-cursor.
		}
	}
	relay := b.relay
	persister := b.lastSeqPersister
	b.mu.Unlock()
	if relay != nil {
		// Fan-out outside the lock so a slow Redis client does not
		// stall in-memory subscribers on the same replica.
		relay.PublishEvent(context.Background(), ev)
	}
	// spec: §7.3 line 397 — durably advance the per-session
	// monotonic counter. Best-effort and asynchronous so a Postgres
	// outage does not stall subscribers; the persister's GREATEST
	// floor keeps the persisted value monotonic across replicas.
	// F-7.3.3.
	if persister != nil && tenantID != "" {
		go func(seq int64) {
			_ = persister.AdvanceLastSeq(context.Background(), tenantID, sessionID, seq)
		}(int64(ev.Seq))
	}
	return ev
}

// Broadcast publishes a platform-level event to every session that
// currently has at least one live SSE subscriber. Each delivery goes
// through the normal publish path, so the event is assigned the
// session's next monotonic Seq, retained in history for a
// reconnect-with-cursor, and fanned out to that session's live
// subscribers (and the cross-replica relay when wired). It returns the
// number of sessions the event reached.
//
// The §10.1 dual-store degraded mode uses Broadcast to push a
// PLATFORM_DEGRADED event to all active client streams within 1 second
// of declaring both stores unreachable. The event publishes under each
// session's frozen tenant so the §7.2 isolation invariant holds.
//
// spec: §10.1 line 45 — "all active client SSE streams receive a
// PLATFORM_DEGRADED server-sent event ... within 1 second".
func (b *Bus) Broadcast(eventType, data string, now time.Time) int {
	b.mu.Lock()
	// Collect (session, tenant) pairs for sessions with a live
	// subscriber. Snapshot under the lock, then publish outside it so a
	// slow relay on one session does not stall the rest.
	type target struct{ sessionID, tenant string }
	targets := make([]target, 0, len(b.subs))
	for sid, subs := range b.subs {
		live := false
		for _, sub := range subs {
			if !sub.closed {
				live = true
				break
			}
		}
		if live {
			targets = append(targets, target{sessionID: sid, tenant: b.tenant[sid]})
		}
	}
	b.mu.Unlock()
	for _, t := range targets {
		b.publish(t.tenant, t.sessionID, eventType, data, now)
	}
	return len(targets)
}

// Subscription is the caller-facing handle returned by Subscribe.
type Subscription struct {
	bus       *Bus
	sessionID string
	sub       *subscription
	// Backlog holds events with Seq > afterSeq that were already in
	// history at subscribe time; the caller drains it before reading
	// Events.
	Backlog []Event
}

// Events returns the channel of live events. The channel is closed
// when Close is called.
func (s *Subscription) Events() <-chan Event { return s.sub.ch }

// Close detaches the subscription from the bus and closes the event
// channel. Safe to call multiple times.
func (s *Subscription) Close() {
	s.bus.mu.Lock()
	defer s.bus.mu.Unlock()
	if s.sub.closed {
		return
	}
	s.sub.closed = true
	close(s.sub.ch)
	subs := s.bus.subs[s.sessionID]
	kept := subs[:0]
	for _, sub := range subs {
		if sub != s.sub {
			kept = append(kept, sub)
		}
	}
	s.bus.subs[s.sessionID] = kept
}

// Subscribe registers a new subscriber for sessionID. Events with
// Seq > afterSeq already in history are returned in Backlog so a
// reconnecting client resumes exactly where it left off; live
// events arrive on Events(). bufferSize bounds the live channel.
//
// Subscribe is the untenanted legacy entry point and is kept only for
// tests; production code MUST call SubscribeForTenant so the §7.2
// tenant-isolation predicate is enforced at the bus interface.
func (b *Bus) Subscribe(sessionID string, afterSeq uint64, bufferSize int) *Subscription {
	sub, _ := b.subscribe("", sessionID, afterSeq, bufferSize)
	return sub
}

// SubscribeForTenant registers a tenant-bound subscriber for sessionID.
// If the session has a frozen tenant (set by the first PublishForTenant)
// and tenantID does not match it, SubscribeForTenant returns
// ErrTenantMismatch and creates no subscription. This is the §7.2
// defense-in-depth: even if a future caller skips the store.Get tenant
// precheck the bus refuses to deliver foreign-tenant events.
//
// When the §4.4 / §12.3.7 RedisRelay is wired the Backlog merges the
// local in-memory history with the Redis stream history so a client
// reconnecting to this replica also sees events originally published
// on a sibling replica.
func (b *Bus) SubscribeForTenant(tenantID, sessionID string, afterSeq uint64, bufferSize int) (*Subscription, error) {
	return b.subscribe(tenantID, sessionID, afterSeq, bufferSize)
}

func (b *Bus) subscribe(tenantID, sessionID string, afterSeq uint64, bufferSize int) (*Subscription, error) {
	if bufferSize <= 0 {
		bufferSize = 64
	}
	b.mu.Lock()
	if tenantID != "" {
		if owner, ok := b.tenant[sessionID]; ok && owner != tenantID {
			b.mu.Unlock()
			return nil, ErrTenantMismatch
		}
	}
	sub := &subscription{ch: make(chan Event, bufferSize)}
	b.subs[sessionID] = append(b.subs[sessionID], sub)

	var backlog []Event
	for _, ev := range b.history[sessionID] {
		if ev.Seq > afterSeq {
			backlog = append(backlog, ev)
		}
	}
	relay := b.relay
	b.mu.Unlock()

	// When the cross-replica relay is wired and the local history
	// either has not seen this session yet or starts past the
	// requested cursor, fetch the Redis stream history and merge it
	// in (deduping by Seq).
	if relay != nil {
		remote, err := relay.History(context.Background(), sessionID, afterSeq)
		if err == nil {
			backlog = mergeByCursor(backlog, remote, afterSeq)
		}
	}
	return &Subscription{bus: b, sessionID: sessionID, sub: sub, Backlog: backlog}, nil
}

// mergeByCursor combines a local history slice with a remote history
// slice, returning a single slice sorted by Seq with no duplicates
// (a Seq present in both lists keeps the local copy). The output
// excludes any Seq ≤ afterSeq.
func mergeByCursor(local, remote []Event, afterSeq uint64) []Event {
	// Deduplicate by Seq, preferring local entries (they carry the
	// authoritative payload from the publishing replica before the
	// relay re-encode).
	bySeq := make(map[uint64]Event, len(local)+len(remote))
	for _, ev := range local {
		if ev.Seq > afterSeq {
			bySeq[ev.Seq] = ev
		}
	}
	for _, ev := range remote {
		if ev.Seq <= afterSeq {
			continue
		}
		if _, has := bySeq[ev.Seq]; !has {
			bySeq[ev.Seq] = ev
		}
	}
	out := make([]Event, 0, len(bySeq))
	for _, ev := range bySeq {
		out = append(out, ev)
	}
	sortBySeq(out)
	return out
}

// sortBySeq sorts a slice of Event in ascending Seq order. Used by
// the cross-replica history-merge path so the backlog arrives in
// monotonic-cursor order.
func sortBySeq(out []Event) {
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].Seq > out[j].Seq; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
}

// History returns the retained events for a session with Seq >
// afterSeq. Used by the polling (non-SSE) read path.
func (b *Bus) History(sessionID string, afterSeq uint64) []Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []Event
	for _, ev := range b.history[sessionID] {
		if ev.Seq > afterSeq {
			out = append(out, ev)
		}
	}
	return out
}

// OldestRetainedSeq returns the smallest Seq currently held in the
// in-memory replay buffer for sessionID, and ok=true when the bus has
// any retained events. The caller uses this together with a client's
// reconnect cursor to detect the §7.2 line 143 buffer-eviction case:
// when the client's last-seen cursor sits below the oldest retained
// event, the gateway emits a `gap_detected` / `checkpoint_boundary`
// marker before replaying the backlog so the client knows events were
// dropped. The Bus does not currently track time-based replay-window
// boundaries (§7.2 line 349); buffer-count eviction is the only path,
// and the result here is authoritative for that case.
//
// When the bus has never seen sessionID (or every event for it has been
// removed) the function returns (0, false).
//
// spec: §7.2 line 143 — "If the requested sequence has been evicted
// from the gateway event replay buffer (§10.4), the adapter emits a
// single protocol-level gap_detected frame ({\"lastSeenSeq\": N,
// \"nextSeq\": M}) before the oldest retained event".
func (b *Bus) OldestRetainedSeq(sessionID string) (uint64, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	hist := b.history[sessionID]
	if len(hist) == 0 {
		return 0, false
	}
	return hist[0].Seq, true
}

// ActiveSubscribers reports the total number of live SSE subscribers
// across all sessions. The gateway uses this value as the source of
// the §4.1 lenny_gateway_active_streams gauge — the secondary HPA
// metric reflecting in-flight streaming connections on this replica.
// Closed subscriptions are excluded; the count drops as soon as a
// client disconnects and Close runs.
func (b *Bus) ActiveSubscribers() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	var n int
	for _, subs := range b.subs {
		for _, sub := range subs {
			if !sub.closed {
				n++
			}
		}
	}
	return n
}

// MaxReplayBufferUtilization returns the worst-case ratio of retained
// events to the configured maxHistory across every session whose
// replay buffer is currently non-empty. The result is in [0, 1]; 1.0
// means at least one session's replay buffer is full and oldest events
// are being evicted. Returns 0 when the bus has no retained events.
//
// The gateway samples this value periodically to feed the §16.1
// lenny_event_bus_replay_buffer_utilization gauge so the §10.4 line
// 389 buffer-depth knob has a feedback signal. F-10.4.11.
//
// spec: §16 observability catalog `lenny_event_bus_replay_buffer_utilization`.
func (b *Bus) MaxReplayBufferUtilization() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.maxHistory <= 0 {
		return 0
	}
	var worst int
	for _, hist := range b.history {
		if len(hist) > worst {
			worst = len(hist)
		}
	}
	return float64(worst) / float64(b.maxHistory)
}
