// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos test for the §25.4 escalation tiered-persistence and
// reconciliation guarantees exercised against real Postgres and real
// Redis stores plus a real Redis event stream.
//
// The tier-4 webhook-under-outage test proves the durable-postgres ->
// durable-redis transition and that escalation_created is emitted
// independently of the accepting tier, but it stops at the Redis tier
// and never reaches the in-memory Tier-3 buffer, the requireDurable
// fail-fast, or the reconciliation flush. The tier-8 emission-retry test
// models the emitter as an in-process up/down flag over the in-memory
// buffer alone. Neither exercises the full create-path tier progression
// (durable-postgres -> durable-redis -> buffered-memory), the
// ESCALATION_NO_DURABLE_STORE fail-fast under dual outage, or the
// reconciliation flush that promotes a buffered record upward into real
// Postgres while preserving the authoring timestamp and the
// exactly-once emission flag. This test composes all of those against
// the real escalation.Service wired the way cmd/lenny-ops wires it: the
// Postgres pgstore (Tier 1) and Redis redisstore (Tier 2) over the
// always-present in-memory buffer (Tier 3), with escalation_created
// emitted onto the real ops:events:stream so the exactly-once invariant
// is asserted by counting entries on that stream.
//
// Store availability is toggled at the Store boundary rather than by
// terminating the container, because the reconciliation flush requires a
// durable tier to RECOVER after an outage (a terminated testcontainer
// cannot come back), and because it keeps the shared Redis event stream
// reachable while the escalation Tier-2 store is unavailable so the
// exactly-once event count stays observable. The toggle emits the exact
// escalation.ErrStoreUnavailable signal the real pgstore and redisstore
// raise on a genuine connection failure (their outage-classification
// path is covered by pkg unit tests and the tier-4 genuine
// Postgres-termination test); when a tier is up every operation
// round-trips through the real database, so durability is genuinely
// verified on every reachable tier.
package tier8_chaos_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
	"github.com/lennylabs/lenny/pkg/ops/escalation"
	escpgstore "github.com/lennylabs/lenny/pkg/ops/escalation/pgstore"
	escredisstore "github.com/lennylabs/lenny/pkg/ops/escalation/redisstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// toggleStore wraps a real §25.4 escalation Store with an atomic
// availability switch. While down every operation returns
// escalation.ErrStoreUnavailable — the exact signal the pgstore and
// redisstore raise on a genuine connection failure — so the tiered
// Service falls through to the next tier; while up every operation
// delegates to the real store, which round-trips through the real
// Postgres or Redis backend. Tier() is a static label and is always
// answerable, matching the real stores.
type toggleStore struct {
	inner escalation.Store
	up    atomic.Bool
}

func newToggleStore(inner escalation.Store, up bool) *toggleStore {
	t := &toggleStore{inner: inner}
	t.up.Store(up)
	return t
}

func (t *toggleStore) setUp(up bool) { t.up.Store(up) }

func (t *toggleStore) Tier() string { return t.inner.Tier() }

func (t *toggleStore) Put(ctx context.Context, esc escalation.Escalation) error {
	if !t.up.Load() {
		return escalation.ErrStoreUnavailable
	}
	return t.inner.Put(ctx, esc)
}

func (t *toggleStore) Get(ctx context.Context, id string) (*escalation.Escalation, error) {
	if !t.up.Load() {
		return nil, escalation.ErrStoreUnavailable
	}
	return t.inner.Get(ctx, id)
}

func (t *toggleStore) List(ctx context.Context, f escalation.Filter, cursor string, limit int) (escalation.ListPage, error) {
	if !t.up.Load() {
		return escalation.ListPage{}, escalation.ErrStoreUnavailable
	}
	return t.inner.List(ctx, f, cursor, limit)
}

func (t *toggleStore) SetStatus(ctx context.Context, id, status string, now time.Time) (*escalation.Escalation, error) {
	if !t.up.Load() {
		return nil, escalation.ErrStoreUnavailable
	}
	return t.inner.SetStatus(ctx, id, status, now)
}

func (t *toggleStore) SetEmitted(ctx context.Context, id string) error {
	if !t.up.Load() {
		return escalation.ErrStoreUnavailable
	}
	return t.inner.SetEmitted(ctx, id)
}

func (t *toggleStore) PendingEmission(ctx context.Context) ([]escalation.Escalation, error) {
	if !t.up.Load() {
		return nil, escalation.ErrStoreUnavailable
	}
	return t.inner.PendingEmission(ctx)
}

// streamEscalationEmitter publishes escalation_created onto the real
// §25.5 ops:events:stream through the production eventbuffer.StreamEmitter,
// mirroring the cmd/lenny-ops streamEscalationEmitter. It is the real
// event path the exactly-once invariant is asserted against.
type streamEscalationEmitter struct {
	em *eventbuffer.StreamEmitter
}

func (e streamEscalationEmitter) EmitEscalationCreated(esc escalation.Escalation) bool {
	payload, err := json.Marshal(esc)
	if err != nil {
		return false
	}
	err = e.em.Emit(context.Background(), events.OperationalEvent{
		Type:            events.EventEscalationCreated.CloudEventsType(),
		Source:          "//lenny.dev/ops/tier8-chaos",
		Subject:         "escalation/" + esc.ID,
		Severity:        esc.Severity,
		DataContentType: "application/json",
		Data:            payload,
	})
	return err == nil
}

// newStreamEmitter builds a real StreamEmitter over client writing to the
// default ops:events:stream.
func newStreamEmitter(client redis.UniversalClient) streamEscalationEmitter {
	return streamEscalationEmitter{
		em: eventbuffer.NewStreamEmitter(eventbuffer.StreamEmitterOptions{
			Client:    client,
			Buffer:    eventbuffer.NewEventBuffer(0),
			Source:    "//lenny.dev/ops/tier8-chaos",
			ReplicaID: "tier8-chaos",
		}),
	}
}

// countEscalationCreated returns the number of escalation_created events
// currently on the real ops:events:stream, decoding each entry's marshalled
// CloudEvents record and matching its type. It is the observable the §25.4
// exactly-once emission invariant is asserted against.
func countEscalationCreated(t *testing.T, client redis.UniversalClient) int {
	t.Helper()
	msgs, err := client.XRange(context.Background(), eventbuffer.DefaultStreamKey, "-", "+").Result()
	if err != nil {
		t.Fatalf("XRange %s: %v", eventbuffer.DefaultStreamKey, err)
	}
	want := events.EventEscalationCreated.CloudEventsType()
	n := 0
	for _, m := range msgs {
		raw, _ := m.Values["event"].(string)
		if raw == "" {
			continue
		}
		var ce struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(raw), &ce); err != nil {
			continue
		}
		if ce.Type == want {
			n++
		}
	}
	return n
}

// spec: 25.4 (escalation tiered create path, emission exactly-once, and
// requireDurable fail-fast)
// diagnosis: a failure means the §25.4 tiered escalation create path is
// broken against real stores. §25.4 Storage Tiers (Create Path): the
// create attempts Postgres (Tier 1, durable-postgres), then Redis (Tier
// 2, durable-redis), then the in-memory buffer (Tier 3, buffered-memory),
// stamping the accepting tier onto the record's persistence. §25.4
// Emission Exactly-Once: "The event stream receives exactly one
// escalation_created event per escalation." §25.4 line 2402: with
// requireDurable set, a create with both Postgres and Redis unavailable
// fails with ESCALATION_NO_DURABLE_STORE instead of a memory-only record.
// Any deviation leaves an operator either without the durability tier the
// deployment expects, without the exactly-once notification, or without
// the explicit fail-fast a requireDurable deployer chose over a silent
// durability gap.
func TestEscalationCreatePathTierProgressionAgainstRealStores(t *testing.T) {
	ctx := context.Background()

	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	rd := containers.StartRedis(t, containers.RedisOptions{})

	pgTier := newToggleStore(escpgstore.New(pg.Pool), true)
	rdTier := newToggleStore(escredisstore.New(rd.Client), true)
	emitter := newStreamEmitter(rd.Client)

	svc := escalation.NewWithStores(escalation.Options{
		Durable: []escalation.Store{pgTier, rdTier},
		Emitter: emitter,
	})

	create := func(summary string) *escalation.Escalation {
		t.Helper()
		esc, err := svc.Create(ctx, escalation.CreateRequest{
			Severity: escalation.SeverityCritical,
			Summary:  summary,
			Source:   "prod-watchdog",
		})
		if err != nil {
			t.Fatalf("create escalation %q: %v", summary, err)
		}
		if !esc.Emitted {
			t.Errorf("escalation %q emitted=false; the ops:events:stream is reachable so it must emit", summary)
		}
		return esc
	}

	// pgRow reads persistence straight from the Tier-1 table so a
	// durable-postgres assertion confirms the record round-tripped through
	// real Postgres.
	pgRow := func(id string) (persistence string, present bool) {
		t.Helper()
		err := pg.Pool.QueryRow(ctx,
			`SELECT persistence FROM ops_escalations WHERE id = $1`, id).Scan(&persistence)
		if err != nil {
			return "", false
		}
		return persistence, true
	}

	// redisHas reports whether the Tier-2 Redis hash holds the record.
	redisHas := func(id string) bool {
		t.Helper()
		n, err := rd.Client.Exists(ctx, "ops:escalations:"+id).Result()
		if err != nil {
			t.Fatalf("redis EXISTS ops:escalations:%s: %v", id, err)
		}
		return n == 1
	}

	// ---- Tier 1: both stores up, the record lands in Postgres ----
	escPG := create("both stores up: durable Postgres tier")
	if escPG.Persistence != escalation.PersistenceDurablePostgres {
		t.Errorf("Tier-1 persistence = %q, want durable-postgres", escPG.Persistence)
	}
	if got, ok := pgRow(escPG.ID); !ok || got != escalation.PersistenceDurablePostgres {
		t.Errorf("Tier-1 escalation not persisted to real Postgres as durable-postgres: row=%q present=%v", got, ok)
	}

	// ---- Tier 2: Postgres unavailable, the record lands in Redis ----
	pgTier.setUp(false)
	escRD := create("Postgres down: durable Redis tier")
	if escRD.Persistence != escalation.PersistenceDurableRedis {
		t.Errorf("Tier-2 persistence = %q, want durable-redis (Postgres unavailable)", escRD.Persistence)
	}
	if !redisHas(escRD.ID) {
		t.Errorf("Tier-2 escalation %s not written to the real Redis ops:escalations hash", escRD.ID)
	}
	if _, ok := pgRow(escRD.ID); ok {
		t.Errorf("Tier-2 escalation %s reached Postgres while it was unavailable", escRD.ID)
	}

	// ---- Tier 3: both stores unavailable, the record buffers in memory ----
	rdTier.setUp(false)
	escMem := create("both stores down: in-memory Tier-3 buffer")
	if escMem.Persistence != escalation.PersistenceBufferedMemory {
		t.Errorf("Tier-3 persistence = %q, want buffered-memory (both stores unavailable)", escMem.Persistence)
	}

	// ---- Exactly-once: one escalation_created per create, no duplicates ----
	// Three creates, each emitting to the real stream exactly once.
	if got := countEscalationCreated(t, rd.Client); got != 3 {
		t.Errorf("ops:events:stream carried %d escalation_created events after 3 creates, want exactly 3 (§25.4 exactly-once)", got)
	}

	// ---- requireDurable: dual outage fails fast rather than buffering ----
	// A separate Service configured with requireDurable rejects a create
	// while both durable tiers are unavailable, with the §25.4 error code.
	svcRequireDurable := escalation.NewWithStores(escalation.Options{
		Durable:        []escalation.Store{pgTier, rdTier},
		Emitter:        emitter,
		RequireDurable: true,
	})
	_, err := svcRequireDurable.Create(ctx, escalation.CreateRequest{
		Severity: escalation.SeverityCritical,
		Summary:  "requireDurable: both stores down, must fail fast",
		Source:   "prod-watchdog",
	})
	if err == nil {
		t.Fatalf("requireDurable create with both stores down returned no error, want ESCALATION_NO_DURABLE_STORE")
	}
	if code := escalation.CodeOf(err); code != escalation.ErrCodeNoDurableStore {
		t.Errorf("requireDurable create error code = %q, want ESCALATION_NO_DURABLE_STORE", code)
	}
}

// spec: 25.4 (escalation reconciliation flush: timestamp preserved,
// emission flag preserved, flush never re-emits, promoted to Postgres)
// diagnosis: a failure means the §25.4 reconciliation flush is broken
// against real stores. §25.4 Reconciliation: buffered escalations are
// flushed upward (in-memory -> Redis -> Postgres) when a higher-priority
// store recovers; the original CreatedAt is preserved across the move
// ("Agents querying the escalation after reconciliation see the real
// authoring time, not the flush time"); the emitted flag is preserved
// ("emitted is not reset during flush"); and the flush "never re-emits —
// it only promotes the record to a higher-durability tier." A failure
// leaves a flushed escalation with a rewritten authoring time, a reset
// emission flag, or a duplicate escalation_created event that re-pages an
// operator, and means a buffered record was never promoted into durable
// Postgres once it recovered.
func TestEscalationReconciliationFlushPreservesRecordAgainstRealStores(t *testing.T) {
	ctx := context.Background()

	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	rd := containers.StartRedis(t, containers.RedisOptions{})

	pgTier := newToggleStore(escpgstore.New(pg.Pool), true)
	rdTier := newToggleStore(escredisstore.New(rd.Client), true)
	emitter := newStreamEmitter(rd.Client)

	svc := escalation.NewWithStores(escalation.Options{
		Durable: []escalation.Store{pgTier, rdTier},
		Emitter: emitter,
	})

	// Pin a distinctly-in-the-past authoring time so a preserved CreatedAt
	// is unambiguously distinguishable from a flush-time rewrite. Truncate
	// to microseconds to match Postgres TIMESTAMPTZ precision.
	authored := time.Now().UTC().Add(-72 * time.Hour).Truncate(time.Microsecond)
	svc.SetClock(func() time.Time { return authored })

	// ---- both durable tiers down: the record buffers in memory ----
	pgTier.setUp(false)
	rdTier.setUp(false)
	esc, err := svc.Create(ctx, escalation.CreateRequest{
		Severity: escalation.SeverityCritical,
		Summary:  "warm pool exhausted while both stores are down",
		Source:   "prod-watchdog",
	})
	if err != nil {
		t.Fatalf("create during dual outage: %v", err)
	}
	if esc.Persistence != escalation.PersistenceBufferedMemory {
		t.Fatalf("persistence = %q, want buffered-memory during dual outage", esc.Persistence)
	}
	if !esc.Emitted {
		t.Fatalf("emitted=false during dual outage; the ops:events:stream is reachable so it must emit")
	}
	if !esc.CreatedAt.Equal(authored) {
		t.Fatalf("createdAt = %s, want the pinned authoring time %s", esc.CreatedAt, authored)
	}
	if got := countEscalationCreated(t, rd.Client); got != 1 {
		t.Fatalf("stream carried %d escalation_created events after create, want exactly 1", got)
	}

	// ---- Postgres recovers: the reconciliation flush promotes the record ----
	pgTier.setUp(true)
	flushed, err := svc.Flush(ctx)
	if err != nil {
		t.Fatalf("reconciliation flush: %v", err)
	}
	if flushed != 1 {
		t.Fatalf("flush promoted %d records, want exactly 1", flushed)
	}

	// The record now lives in real Postgres as durable-postgres, with the
	// original authoring time and the emitted flag both preserved.
	var (
		persistence string
		emitted     bool
		createdAt   time.Time
	)
	err = pg.Pool.QueryRow(ctx,
		`SELECT persistence, emitted, created_at FROM ops_escalations WHERE id = $1`, esc.ID).
		Scan(&persistence, &emitted, &createdAt)
	if err != nil {
		t.Fatalf("read flushed escalation %s from Postgres: %v", esc.ID, err)
	}
	if persistence != escalation.PersistenceDurablePostgres {
		t.Errorf("flushed persistence = %q, want durable-postgres", persistence)
	}
	if !emitted {
		t.Errorf("flushed emitted = false, want true (the flag is preserved across flush, not reset)")
	}
	if !createdAt.UTC().Equal(authored) {
		t.Errorf("flushed created_at = %s, want the preserved authoring time %s (not the flush time)", createdAt.UTC(), authored)
	}

	// ---- flush never re-emits: the stream still carries exactly one event ----
	if got := countEscalationCreated(t, rd.Client); got != 1 {
		t.Errorf("stream carried %d escalation_created events after flush, want exactly 1 (the flush must not re-emit)", got)
	}

	// The promoted record now reads back as durable-postgres from the
	// highest reachable tier, and a second flush is a no-op.
	got, err := svc.Get(ctx, esc.ID)
	if err != nil {
		t.Fatalf("get flushed escalation: %v", err)
	}
	if got.Persistence != escalation.PersistenceDurablePostgres {
		t.Errorf("post-flush Get persistence = %q, want durable-postgres", got.Persistence)
	}
	if again, err := svc.Flush(ctx); err != nil || again != 0 {
		t.Errorf("second flush = (%d, %v), want (0, nil) once the buffer is drained", again, err)
	}
}
