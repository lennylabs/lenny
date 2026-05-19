// SPDX-License-Identifier: MIT

// Package redisstream is the §11.2.1 Tier 1 durable billing stream
// backed by a Redis stream. It is the production implementation of
// failover.StreamTier: when the primary Postgres billing write fails,
// the failover pipeline publishes the billing event here, and the
// background flusher in this package replays the queued events into
// Postgres once the primary recovers.
//
// The §11.2.1 design:
//
//   - Each tenant has its own stream, keyed `t:{tenant_id}:billing:stream`,
//     trimmed to MAXLEN ~billingRedisStreamMaxLen so the stream is
//     bounded.
//   - Publish is an XADD carrying the full billing event payload plus
//     the provisional stream_seq. Multiple gateway replicas XADD to the
//     same tenant stream, which provides durable ordering across
//     replicas.
//   - A consumer group `billing-flusher` is created with MKSTREAM. Each
//     gateway replica joins as a distinct consumer (its pod id), so the
//     group delivers each entry to exactly one replica — no duplicate
//     inserts across replicas.
//   - The flusher XREADGROUPs pending entries, re-attempts the Postgres
//     INSERT in stream_seq order, and on success XACKs and XDELs the
//     entry.
//   - A periodic XAUTOCLAIM reclaims entries assigned to a crashed
//     replica. Because a reclaimed entry may have already been inserted
//     by a consumer that crashed before XACK, the Postgres INSERT is
//     idempotent via ON CONFLICT (tenant_id, stream_entry_id) DO
//     NOTHING; the stream_entry_id column carries the Redis entry id.
package redisstream

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/billingstore/failover"
)

// consumerGroup is the §11.2.1 billing-flusher consumer group name. Each
// gateway replica joins this group under its own pod id.
const consumerGroup = "billing-flusher"

// Defaults for the §11.2.1 stream tuning knobs.
const (
	// DefaultStreamMaxLen is the billingRedisStreamMaxLen default at
	// Tier 1/2 (§17.8.2). Tier 3 raises it to 72,000.
	DefaultStreamMaxLen = 50_000
	// DefaultReclaimInterval is billingReclaimIntervalSeconds (§11.2.1).
	DefaultReclaimInterval = 15 * time.Second
	// DefaultReclaimMinIdle is billingReclaimMinIdleSeconds (§11.2.1):
	// an entry idle at least this long is eligible for XAUTOCLAIM.
	DefaultReclaimMinIdle = 30 * time.Second
)

// streamKey returns the §11.2.1 per-tenant stream key.
func streamKey(tenantID string) string {
	return "t:" + tenantID + ":billing:stream"
}

// Inserter commits a billing event reclaimed from the stream into the
// primary store. The implementation is the §11.2.1 idempotent
// INSERT ... ON CONFLICT (tenant_id, stream_entry_id) DO NOTHING — the
// streamEntryID dedupes a reclaimed entry that a crashed consumer had
// already inserted but not acknowledged.
type Inserter interface {
	// InsertFromStream commits e to the primary billing store, keyed by
	// the Redis stream entry id for idempotency. A duplicate
	// streamEntryID is a no-op and returns a nil error so the flusher
	// proceeds to acknowledge and delete the stream entry.
	InsertFromStream(ctx context.Context, e billingstore.Event, streamEntryID string) error
}

// Options configures a Tier.
type Options struct {
	// Client is the Redis client. Required.
	Client redis.UniversalClient

	// ConsumerName is this gateway replica's consumer name in the
	// billing-flusher group — its pod id. Required: a shared consumer
	// name across replicas would defeat the exactly-once delivery the
	// consumer group provides.
	ConsumerName string

	// Inserter commits reclaimed events to the primary store. Required
	// for the flusher; Publish alone does not need it.
	Inserter Inserter

	// StreamMaxLen is the per-tenant MAXLEN. Zero uses
	// DefaultStreamMaxLen.
	StreamMaxLen int64

	// ReclaimInterval and ReclaimMinIdle tune the XAUTOCLAIM reclaim.
	// Zero uses the defaults above.
	ReclaimInterval time.Duration
	ReclaimMinIdle  time.Duration
}

// Tier is the §11.2.1 Tier 1 Redis-stream billing buffer. It satisfies
// failover.StreamTier.
type Tier struct {
	client       redis.UniversalClient
	consumerName string
	inserter     Inserter
	maxLen       int64
	reclaimEvery time.Duration
	reclaimIdle  time.Duration

	// known tracks the tenant streams this replica has created the
	// consumer group on, so a group is created at most once per tenant.
	known map[string]bool
}

var _ failover.StreamTier = (*Tier)(nil)

// New returns a Tier. Client and ConsumerName are required.
func New(opts Options) (*Tier, error) {
	if opts.Client == nil {
		return nil, fmt.Errorf("redisstream: Options.Client is required")
	}
	if opts.ConsumerName == "" {
		return nil, fmt.Errorf("redisstream: Options.ConsumerName is required (use the replica pod id)")
	}
	maxLen := opts.StreamMaxLen
	if maxLen <= 0 {
		maxLen = DefaultStreamMaxLen
	}
	reclaimEvery := opts.ReclaimInterval
	if reclaimEvery <= 0 {
		reclaimEvery = DefaultReclaimInterval
	}
	reclaimIdle := opts.ReclaimMinIdle
	if reclaimIdle <= 0 {
		reclaimIdle = DefaultReclaimMinIdle
	}
	return &Tier{
		client:       opts.Client,
		consumerName: opts.ConsumerName,
		inserter:     opts.Inserter,
		maxLen:       maxLen,
		reclaimEvery: reclaimEvery,
		reclaimIdle:  reclaimIdle,
		known:        make(map[string]bool),
	}, nil
}

// streamPayload is the JSON-encoded billing event carried in a stream
// entry's `event` field. The provisional sequence travels in the
// separate `stream_seq` field so the flusher can replay in order
// without decoding the payload.
type streamPayload struct {
	Event billingstore.Event `json:"event"`
}

// Publish implements failover.StreamTier. It XADDs the billing event to
// the tenant's stream, ensuring the consumer group exists first. The
// stream is trimmed to the configured MAXLEN with the `~` approximate
// flag, which is the §11.2.1 `MAXLEN ~billingRedisStreamMaxLen`.
func (t *Tier) Publish(ctx context.Context, e billingstore.Event) error {
	if err := t.ensureGroup(ctx, e.TenantID); err != nil {
		return err
	}
	payload, err := json.Marshal(streamPayload{Event: e})
	if err != nil {
		return fmt.Errorf("redisstream: marshal billing event: %w", err)
	}
	return t.client.XAdd(ctx, &redis.XAddArgs{
		Stream: streamKey(e.TenantID),
		MaxLen: t.maxLen,
		Approx: true,
		Values: map[string]any{
			"event":      string(payload),
			"stream_seq": strconv.FormatUint(e.SequenceNumber, 10),
			"tenant_id":  e.TenantID,
		},
	}).Err()
}

// Pending implements failover.StreamTier: the §11.2.1
// BillingStreamBackpressure signal is derived from this. Pending is
// best-effort across tenants — it is not consulted in the write path.
func (t *Tier) Pending(ctx context.Context) (int, error) {
	total := 0
	for tenant := range t.known {
		n, err := t.client.XLen(ctx, streamKey(tenant)).Result()
		if err != nil {
			return total, err
		}
		total += int(n)
	}
	return total, nil
}

// ensureGroup creates the billing-flusher consumer group on the
// tenant's stream the first time the tenant is seen. XGroupCreateMkStream
// also creates the stream itself, so a Publish to a brand-new tenant
// works. A BUSYGROUP reply means another replica already created the
// group; that is treated as success.
func (t *Tier) ensureGroup(ctx context.Context, tenantID string) error {
	if t.known[tenantID] {
		return nil
	}
	err := t.client.XGroupCreateMkStream(ctx, streamKey(tenantID), consumerGroup, "0").Err()
	if err != nil && !isBusyGroup(err) {
		return err
	}
	t.known[tenantID] = true
	return nil
}

// isBusyGroup reports whether err is the Redis BUSYGROUP reply, which
// means the consumer group already exists.
func isBusyGroup(err error) bool {
	return err != nil && len(err.Error()) >= 9 && err.Error()[:9] == "BUSYGROUP"
}

// Flush drains a tenant's stream into the primary store. It XREADGROUPs
// pending entries for this replica's consumer, re-attempts the
// idempotent Postgres INSERT in stream order, and on success XACKs and
// XDELs each entry. It returns the number of entries flushed and the
// error that halted the drain (nil on a complete drain).
//
// The flusher is invoked on the §11.2.1 billingFlushIntervalMs schedule
// by RunFlusher; it is exported so an operator runbook or a test can
// trigger a drain directly.
func (t *Tier) Flush(ctx context.Context, tenantID string) (int, error) {
	if t.inserter == nil {
		return 0, fmt.Errorf("redisstream: Flush requires an Inserter")
	}
	if err := t.ensureGroup(ctx, tenantID); err != nil {
		return 0, err
	}
	flushed := 0
	for {
		// ">" delivers entries never handed to this consumer.
		res, err := t.client.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    consumerGroup,
			Consumer: t.consumerName,
			Streams:  []string{streamKey(tenantID), ">"},
			Count:    100,
			Block:    -1, // non-blocking: drain what is pending, then return.
		}).Result()
		if err == redis.Nil || (err == nil && len(res) == 0) {
			return flushed, nil
		}
		if err != nil {
			return flushed, err
		}
		n, ferr := t.flushEntries(ctx, tenantID, res[0].Messages)
		flushed += n
		if ferr != nil {
			return flushed, ferr
		}
		if len(res[0].Messages) < 100 {
			return flushed, nil
		}
	}
}

// Reclaim runs one §11.2.1 XAUTOCLAIM pass over a tenant's stream,
// transferring entries idle longer than the reclaim min-idle to this
// replica's consumer and flushing them. minIdle of zero is the
// §11.2.1 startup fast-recovery sweep (claim every entry assigned to a
// predecessor that held this pod name).
func (t *Tier) Reclaim(ctx context.Context, tenantID string, minIdle time.Duration) (int, error) {
	if t.inserter == nil {
		return 0, fmt.Errorf("redisstream: Reclaim requires an Inserter")
	}
	if err := t.ensureGroup(ctx, tenantID); err != nil {
		return 0, err
	}
	flushed := 0
	start := "0-0"
	for {
		messages, next, err := t.client.XAutoClaim(ctx, &redis.XAutoClaimArgs{
			Stream:   streamKey(tenantID),
			Group:    consumerGroup,
			Consumer: t.consumerName,
			MinIdle:  minIdle,
			Start:    start,
			Count:    100,
		}).Result()
		if err != nil {
			return flushed, err
		}
		n, ferr := t.flushEntries(ctx, tenantID, messages)
		flushed += n
		if ferr != nil {
			return flushed, ferr
		}
		if next == "0-0" || len(messages) == 0 {
			return flushed, nil
		}
		start = next
	}
}

// flushEntries commits a batch of stream entries to the primary store
// and, on success, acknowledges and deletes each. The §11.2.1
// idempotent INSERT means a reclaimed entry already committed by a
// crashed consumer is a no-op, after which this consumer still XACKs
// and XDELs it. flushEntries stops at the first entry the primary store
// rejects so the entry stays pending for the next cycle.
func (t *Tier) flushEntries(ctx context.Context, tenantID string, messages []redis.XMessage) (int, error) {
	flushed := 0
	for _, msg := range messages {
		e, err := decodeEntry(msg)
		if err != nil {
			// A corrupt entry cannot be replayed; acknowledge and delete
			// it so it does not block the stream, but report the error.
			t.ackAndDel(ctx, tenantID, msg.ID)
			return flushed, err
		}
		if err := t.inserter.InsertFromStream(ctx, e, msg.ID); err != nil {
			return flushed, err
		}
		if err := t.ackAndDel(ctx, tenantID, msg.ID); err != nil {
			return flushed, err
		}
		flushed++
	}
	return flushed, nil
}

// ackAndDel XACKs then XDELs a flushed stream entry.
func (t *Tier) ackAndDel(ctx context.Context, tenantID, entryID string) error {
	key := streamKey(tenantID)
	if err := t.client.XAck(ctx, key, consumerGroup, entryID).Err(); err != nil {
		return err
	}
	return t.client.XDel(ctx, key, entryID).Err()
}

// decodeEntry reconstructs a billing event from a stream entry.
func decodeEntry(msg redis.XMessage) (billingstore.Event, error) {
	raw, ok := msg.Values["event"].(string)
	if !ok {
		return billingstore.Event{}, fmt.Errorf("redisstream: stream entry %s has no event payload", msg.ID)
	}
	var p streamPayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return billingstore.Event{}, fmt.Errorf("redisstream: decode stream entry %s: %w", msg.ID, err)
	}
	return p.Event, nil
}

// RunFlusher runs the §11.2.1 background flush-and-reclaim loop for a
// tenant until ctx is cancelled. On startup it performs the
// fast-recovery XAUTOCLAIM with min-idle 0 (claiming entries left by a
// predecessor replica that held this pod name), then alternates a
// XREADGROUP flush with a periodic XAUTOCLAIM reclaim on the configured
// schedule.
func (t *Tier) RunFlusher(ctx context.Context, tenantID string, flushInterval time.Duration) {
	// §11.2.1 startup fast-recovery: claim entries a predecessor held.
	_, _ = t.Reclaim(ctx, tenantID, 0)

	flush := time.NewTicker(flushInterval)
	defer flush.Stop()
	reclaim := time.NewTicker(t.reclaimEvery)
	defer reclaim.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-flush.C:
			_, _ = t.Flush(ctx, tenantID)
		case <-reclaim.C:
			_, _ = t.Reclaim(ctx, tenantID, t.reclaimIdle)
		}
	}
}
