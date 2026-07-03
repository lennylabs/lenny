//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §12.2.1 EventStore — the Postgres-backed
// audit_log ledger exercised against a real Postgres container with
// the production migrations applied — and the §12.3.7 EventBus over a
// real Redis container. It covers the §11.7 audit hash chain and
// per-tenant sequence monotonicity, the OCSF translation state machine
// (pending → retry_pending → succeeded | dead_lettered), the §12.3.7
// EventBus publish-state machine, the startup chain-continuity check,
// RLS tenant isolation, erasure, and the Redis pub/sub CloudEvents
// envelope on tenant-prefixed channels.
//
// This file converts the TestEventStoreContract and TestEventBusContract
// scaffolds (formerly skipped in scaffolds_test.go) into real tests.
package stores_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/audit/integrity"
	"github.com/lennylabs/lenny/pkg/audit/ocsf"
	"github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/eventbus"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pubsub"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// spec: 12.2.1
// diagnosis: the §12.2.1 EventStore — the Postgres-backed audit_log
// ledger — must build a verifiable §11.7 hash chain with a monotonic
// per-tenant sequence_number, drive the OCSF translation state machine
// to a terminal state, drive the §12.3.7 EventBus publish-state
// machine, isolate each tenant's rows under RLS, survive the startup
// chain-continuity check, and permit erasure under the erasure-mode
// guard. A failure here is a real defect in pkg/gateway/auditstore.
func TestEventStoreContract(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := auditstore.New(pg.Router(t))
	ctx := context.Background()

	t.Run("append builds a verifiable hash chain with monotonic sequence", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		var rows []audit.Row
		for i, et := range []string{"session.created", "credential.leased", "session.completed"} {
			payload := json.RawMessage(`{"i":` + string(rune('0'+i)) + `}`)
			row, err := store.Append(ctx, tenant, et, payload, time.Now())
			if err != nil {
				t.Fatalf("Append %s: %v", et, err)
			}
			if row.Seq != uint64(i+1) {
				t.Errorf("Append %s seq = %d, want %d (monotonic per-tenant)", et, row.Seq, i+1)
			}
			rows = append(rows, row)
		}
		if rows[0].PrevHash != audit.GenesisPrevHash {
			t.Errorf("genesis prev_hash = %q, want the sentinel", rows[0].PrevHash)
		}
		if rows[1].PrevHash != audit.LinkHash(rows[0]) {
			t.Error("row 2 prev_hash does not link to row 1")
		}
		res, err := store.Verify(ctx, tenant)
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if res.Integrity != audit.ChainVerified {
			t.Errorf("Verify = %q (%s), want verified", res.Integrity, res.Detail)
		}
	})

	t.Run("OCSF translation state machine reaches succeeded", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		row, err := store.Append(ctx, tenant, "session.created", json.RawMessage(`{"user_id":"alice@acme.com"}`), time.Now())
		if err != nil {
			t.Fatalf("Append: %v", err)
		}
		// A freshly appended row starts in the pending OCSF state.
		st, _, err := store.TranslationState(ctx, tenant, row.Seq)
		if err != nil {
			t.Fatalf("TranslationState: %v", err)
		}
		if st != audit.OCSFPending {
			t.Errorf("fresh row OCSF state = %q, want pending", st)
		}
		// Drive the translator over the real store; the session.created
		// row translates cleanly and transitions to succeeded.
		sink := &capturingSink{}
		tr := ocsf.NewTranslator(store, sink, ocsf.DefaultTranslationConfig(), nil)
		if _, err := tr.RunCycle(ctx); err != nil {
			t.Fatalf("translator RunCycle: %v", err)
		}
		st, _, err = store.TranslationState(ctx, tenant, row.Seq)
		if err != nil {
			t.Fatalf("TranslationState after translate: %v", err)
		}
		if st != audit.OCSFSucceeded {
			t.Errorf("OCSF state after translate = %q, want succeeded", st)
		}
		if len(sink.records) == 0 {
			t.Error("translator did not multicast the translated record to the sink")
		}
	})

	t.Run("OCSF translation state machine dead-letters an unmapped event", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		// An event type with no §11.7 OCSF class mapping fails every
		// translation attempt and must dead-letter.
		row, err := store.Append(ctx, tenant, "totally.unmapped.type", json.RawMessage(`{}`), time.Now())
		if err != nil {
			t.Fatalf("Append: %v", err)
		}
		cfg := ocsf.DefaultTranslationConfig()
		cfg.MaxAttempts = 2
		sink := &capturingSink{}
		tr := ocsf.NewTranslator(store, sink, cfg, nil)
		// Cycle MaxAttempts times: attempt 1 → retry_pending, attempt 2
		// → dead_lettered.
		for i := 0; i < cfg.MaxAttempts; i++ {
			if _, err := tr.RunCycle(ctx); err != nil {
				t.Fatalf("RunCycle %d: %v", i, err)
			}
		}
		st, rc, err := store.TranslationState(ctx, tenant, row.Seq)
		if err != nil {
			t.Fatalf("TranslationState: %v", err)
		}
		if st != audit.OCSFDeadLettered {
			t.Errorf("OCSF state = %q, want dead_lettered", st)
		}
		if rc != cfg.MaxAttempts {
			t.Errorf("retry_count = %d, want %d", rc, cfg.MaxAttempts)
		}
		if !st.IsTerminal() {
			t.Error("dead_lettered must be a terminal OCSF state")
		}
	})

	t.Run("EventBus publish-state machine transitions failed to published", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		row, err := store.Append(ctx, tenant, "session.completed", json.RawMessage(`{}`), time.Now())
		if err != nil {
			t.Fatalf("Append: %v", err)
		}
		// Simulate a publish that failed after the durable commit.
		if err := store.SetPublishState(ctx, tenant, row.Seq, eventbus.PublishFailed, 0); err != nil {
			t.Fatalf("SetPublishState failed: %v", err)
		}
		ps, _, err := store.PublishState(ctx, tenant, row.Seq)
		if err != nil {
			t.Fatalf("PublishState: %v", err)
		}
		if ps != eventbus.PublishFailed {
			t.Errorf("publish state = %q, want failed", ps)
		}
		// The retranscribe worker rebuilds the envelope and re-publishes.
		rows, err := store.PendingRepublish(ctx, 5, 256)
		if err != nil {
			t.Fatalf("PendingRepublish: %v", err)
		}
		var found bool
		var rebuiltID string
		for _, r := range rows {
			if r.TenantID == tenant && r.Seq == row.Seq {
				found = true
				rebuiltID = r.Event.ID
				// The rebuilt envelope is a valid §12.3.7 CloudEvents
				// envelope carrying an OCSF audit record.
				if err := r.Event.Validate(); err != nil {
					t.Errorf("rebuilt CloudEvents envelope is invalid: %v", err)
				}
				if !r.Event.IsAuditBearing() {
					t.Error("rebuilt envelope must be audit-bearing (application/ocsf+json)")
				}
			}
		}
		if !found {
			t.Fatal("the failed row was not returned by PendingRepublish")
		}
		// §12.3.7: the CloudEvents id is byte-identical across
		// retranscribes (it is derived from the immutable canonical
		// tuple) so downstream de-duplication by id keeps working.
		rows2, err := store.PendingRepublish(ctx, 5, 256)
		if err != nil {
			t.Fatalf("PendingRepublish second call: %v", err)
		}
		for _, r := range rows2 {
			if r.TenantID == tenant && r.Seq == row.Seq && r.Event.ID != rebuiltID {
				t.Errorf("retranscribe id is not stable: %q then %q", rebuiltID, r.Event.ID)
			}
		}
		rt := eventbus.NewRetranscriber(store, eventbus.NewRedisEventBus(nil, nil),
			eventbus.DefaultRetranscribeConfig(), nil)
		if _, err := rt.Sweep(ctx); err != nil {
			t.Fatalf("retranscribe Sweep: %v", err)
		}
		ps, _, err = store.PublishState(ctx, tenant, row.Seq)
		if err != nil {
			t.Fatalf("PublishState after sweep: %v", err)
		}
		if ps != eventbus.PublishPublished {
			t.Errorf("publish state after retranscribe = %q, want published", ps)
		}
	})

	t.Run("startup chain-continuity check verifies every tenant chain", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		for _, et := range []string{"session.created", "session.completed"} {
			if _, err := store.Append(ctx, tenant, et, json.RawMessage(`{}`), time.Now()); err != nil {
				t.Fatalf("Append: %v", err)
			}
		}
		results, err := integrity.CheckChainContinuity(ctx, pg.Pool)
		if err != nil {
			t.Fatalf("CheckChainContinuity: %v", err)
		}
		if broken := integrity.FirstBroken(results); broken != nil {
			t.Errorf("startup continuity check found a broken chain: tenant %q, %s",
				broken.TenantID, broken.Result.Detail)
		}
		var sawTenant bool
		for _, r := range results {
			if r.TenantID == tenant {
				sawTenant = true
				if r.Result.Integrity != audit.ChainVerified {
					t.Errorf("tenant %q chain = %q, want verified", tenant, r.Result.Integrity)
				}
			}
		}
		if !sawTenant {
			t.Errorf("continuity check did not walk tenant %q", tenant)
		}
	})

	t.Run("startup chain-continuity check detects a tampered chain", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		for _, et := range []string{"e1", "e2", "e3"} {
			if _, err := store.Append(ctx, tenant, et, json.RawMessage(`{"v":1}`), time.Now()); err != nil {
				t.Fatalf("Append: %v", err)
			}
		}
		// Tamper row 2 in place under the erasure-mode guard (the only
		// way the immutability trigger permits an UPDATE). The startup
		// continuity check must report the chain broken.
		tx, err := pg.Pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin tamper tx: %v", err)
		}
		_, _ = tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenant)
		_, _ = tx.Exec(ctx, "SELECT set_config('lenny.erasure_mode', 'true', true)")
		if _, err := tx.Exec(ctx,
			`UPDATE audit_log SET payload = '{"v":999}'::jsonb
			 WHERE tenant_id = $1 AND sequence_number = 2`, tenant); err != nil {
			t.Fatalf("tamper UPDATE: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit tamper tx: %v", err)
		}
		results, err := integrity.CheckChainContinuity(ctx, pg.Pool)
		if err != nil {
			t.Fatalf("CheckChainContinuity: %v", err)
		}
		var found bool
		for _, r := range results {
			if r.TenantID == tenant {
				found = true
				if !r.Broken() {
					t.Errorf("tampered tenant %q chain = %q, want broken", tenant, r.Result.Integrity)
				}
			}
		}
		if !found {
			t.Errorf("continuity check did not walk tampered tenant %q", tenant)
		}

		// The windowed startup check (the §12.3 line 101 path the reworked
		// runStartupChainContinuityCheck WARN string reads) must report the
		// same real-Postgres committed-row tamper as broken, so a genuine
		// tamper reaches the boundary-populated ChainBroken branch that
		// emits the committed-row-tamper-or-removal WARN. The audit
		// sequence_number is allocated by nextval (§11.7 Path A), so this
		// pins that the reconciled verifyChainWindow still breaks on a
		// non-linking prev_hash against a live chain. spec: 12.3 (startup
		// chain-continuity check line 101), 11.7 (prev_hash is the tamper
		// authority). F-11.2.10.
		recent, err := integrity.CheckChainContinuityRecent(ctx, pg.Pool, 1000)
		if err != nil {
			t.Fatalf("CheckChainContinuityRecent: %v", err)
		}
		var foundRecent bool
		for _, r := range recent {
			if r.TenantID == tenant {
				foundRecent = true
				if !r.Broken() {
					t.Errorf("windowed check: tampered tenant %q chain = %q, want broken", tenant, r.Result.Integrity)
				}
			}
		}
		if !foundRecent {
			t.Errorf("windowed continuity check did not walk tampered tenant %q", tenant)
		}
	})

	t.Run("RLS isolates each tenant's audit rows", func(t *testing.T) {
		a := freshTenant(t, ctx, pg)
		b := freshTenant(t, ctx, pg)
		if _, err := store.Append(ctx, a, "a.event", json.RawMessage(`{}`), time.Now()); err != nil {
			t.Fatalf("Append a: %v", err)
		}
		if _, err := store.Append(ctx, b, "b.event", json.RawMessage(`{}`), time.Now()); err != nil {
			t.Fatalf("Append b: %v", err)
		}
		rowsA, err := store.Rows(ctx, a)
		if err != nil {
			t.Fatalf("Rows a: %v", err)
		}
		if len(rowsA) != 1 || rowsA[0].EventType != "a.event" {
			t.Errorf("tenant a sees %d rows %v, want exactly its own", len(rowsA), rowsA)
		}
		// A SELECT under tenant a's RLS context, run as the
		// non-superuser lenny_app role (the gateway's production
		// role — a superuser bypasses RLS), must not see tenant b.
		// The lenny_tenant_isolation policy filters the read.
		leaked := countAuditRowsUnderScopeAsApp(t, ctx, pg, a, b)
		if leaked != 0 {
			t.Errorf("RLS leak: tenant a's context saw %d of tenant b's audit rows", leaked)
		}
		// And tenant a still sees its own row under the same role.
		own := countAuditRowsUnderScopeAsApp(t, ctx, pg, a, a)
		if own != 1 {
			t.Errorf("RLS over-filter: tenant a saw %d of its own audit rows, want 1", own)
		}
	})

	t.Run("erasure deletes a tenant's audit rows under the erasure-mode guard", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		if _, err := store.Append(ctx, tenant, "gdpr.subject.event", json.RawMessage(`{}`), time.Now()); err != nil {
			t.Fatalf("Append: %v", err)
		}
		// Erasure runs under SET LOCAL lenny.erasure_mode = 'true',
		// which the immutability trigger honors to permit the DELETE.
		tx, err := pg.Pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin erasure tx: %v", err)
		}
		_, _ = tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenant)
		_, _ = tx.Exec(ctx, "SELECT set_config('lenny.erasure_mode', 'true', true)")
		if _, err := tx.Exec(ctx, `DELETE FROM audit_log WHERE tenant_id = $1`, tenant); err != nil {
			t.Fatalf("erasure DELETE: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit erasure tx: %v", err)
		}
		rows, err := store.Rows(ctx, tenant)
		if err != nil {
			t.Fatalf("Rows after erasure: %v", err)
		}
		if len(rows) != 0 {
			t.Errorf("erasure left %d audit rows, want 0", len(rows))
		}
	})
}

// spec: 12.2.1
// diagnosis: the §12.2.1 EventBus — the §12.3.7 Redis pub/sub event
// bus — must publish CloudEvents v1.0.2 envelopes on tenant-prefixed
// channels with at-most-once delivery, and a subscriber on one tenant
// must never receive another tenant's events. This exercises the
// RedisEventBus over a real Redis container.
func TestEventBusContract(t *testing.T) {
	t.Parallel()
	rd := containers.StartRedis(t, containers.RedisOptions{})
	metrics := eventbus.NewCountingBusMetrics()
	bus := eventbus.NewRedisEventBus(pubsub.New(rd.Client), metrics)
	ctx := context.Background()

	t.Run("publishes a CloudEvents envelope a subscriber receives", func(t *testing.T) {
		received := make(chan eventbus.Event, 4)
		sub, err := bus.Subscribe(ctx, "acme", eventbus.TopicSessionLifecycle,
			func(_ context.Context, ev eventbus.Event) error {
				received <- ev
				return nil
			})
		if err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		defer func() { _ = sub.Unsubscribe() }()

		ev := mustBusEvent(t, "acme", "session_state_changed")
		// Redis pub/sub is at-most-once: retry the publish until the
		// subscriber's consume loop has attached.
		got, ok := publishUntilReceived(t, ctx, bus, "acme", ev, received)
		if !ok {
			t.Fatal("subscriber never received the published event")
		}
		if got.ID != ev.ID {
			t.Errorf("received event id = %q, want %q", got.ID, ev.ID)
		}
		if got.Type != ev.Type {
			t.Errorf("received event type = %q, want %q", got.Type, ev.Type)
		}
		if err := got.Validate(); err != nil {
			t.Errorf("received envelope is not a valid CloudEvents v1.0.2 envelope: %v", err)
		}
		if got.Extensions[eventbus.ExtTenantID] != "acme" {
			t.Errorf("received envelope lennytenantid = %q, want acme", got.Extensions[eventbus.ExtTenantID])
		}
		if metrics.Published(eventbus.TopicSessionLifecycle) == 0 {
			t.Error("publish-attempt metric was not recorded")
		}
		if metrics.PublishDurationCount(eventbus.TopicSessionLifecycle) == 0 {
			t.Error("spec: §12.6 line 709 — publish_duration_seconds histogram was not recorded")
		}
		// spec: §12.6 line 709 — handler_duration_seconds is recorded after
		// the handler returns, which races the channel read; poll briefly.
		deadline := time.After(2 * time.Second)
		for metrics.HandlerDurationCount(eventbus.TopicSessionLifecycle) == 0 {
			select {
			case <-deadline:
				t.Fatal("spec: §12.6 line 709 — handler_duration_seconds histogram was never recorded")
			case <-time.After(20 * time.Millisecond):
			}
		}
	})

	t.Run("tenant-prefixed channels isolate subscribers", func(t *testing.T) {
		// A subscriber on tenant globex must never see a tenant acme
		// event — the channel name is tenant-prefixed.
		globexEvents := make(chan eventbus.Event, 4)
		subG, err := bus.Subscribe(ctx, "globex", eventbus.TopicSessionLifecycle,
			func(_ context.Context, ev eventbus.Event) error {
				globexEvents <- ev
				return nil
			})
		if err != nil {
			t.Fatalf("Subscribe globex: %v", err)
		}
		defer func() { _ = subG.Unsubscribe() }()

		// Subscribe acme too so the test knows the publish landed (the
		// consume loops are attached).
		acmeEvents := make(chan eventbus.Event, 4)
		subA, err := bus.Subscribe(ctx, "acme", eventbus.TopicSessionLifecycle,
			func(_ context.Context, ev eventbus.Event) error {
				acmeEvents <- ev
				return nil
			})
		if err != nil {
			t.Fatalf("Subscribe acme: %v", err)
		}
		defer func() { _ = subA.Unsubscribe() }()

		acmeEvent := mustBusEvent(t, "acme", "session_state_changed")
		if _, ok := publishUntilReceived(t, ctx, bus, "acme", acmeEvent, acmeEvents); !ok {
			t.Fatal("acme subscriber never received its own event")
		}
		// The globex subscriber must not have received the acme event.
		select {
		case leaked := <-globexEvents:
			t.Errorf("channel-isolation leak: globex subscriber received acme event %q", leaked.ID)
		default:
		}
	})
}

// spec: §4.4 line 232 — audit-bearing CloudEvents on the EventBus.
// diagnosis: the §4.4 line 232 contract names "the CloudEvents-wrapped
// audit events published on the EventBus" as one of the OCSF-egress
// targets. The PublishingAppender wraps Store.Append with a
// first-publish that emits the OCSF record on
// TopicSessionLifecycle and transitions the row's
// eventbus_publish_state to `published`. This test exercises the
// end-to-end path against real Postgres + Redis.
func TestPublishingAppenderAuditBearingPublish(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	rd := containers.StartRedis(t, containers.RedisOptions{})
	store := auditstore.New(pg.Router(t))
	bus := eventbus.NewRedisEventBus(pubsub.New(rd.Client), eventbus.NewCountingBusMetrics())
	app := auditstore.NewPublishingAppender(store, bus, "gw-test")
	ctx := context.Background()
	tenant := freshTenant(t, ctx, pg)

	// Subscribe before publishing so the at-most-once Redis path
	// reliably delivers.
	received := make(chan eventbus.Event, 4)
	sub, err := bus.Subscribe(ctx, eventbus.TenantID(tenant), eventbus.TopicSessionLifecycle,
		func(_ context.Context, ev eventbus.Event) error {
			received <- ev
			return nil
		})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	// publishUntilReceived loop: at-most-once delivery means we may
	// need to call Append more than once to win the subscriber attach
	// race.
	var lastSeq uint64
	for attempt := 0; attempt < 20; attempt++ {
		row, aerr := app.Append(ctx, tenant, "session.created",
			json.RawMessage(`{"user_id":"alice@acme.com","session_id":"sess-1"}`),
			time.Now())
		if aerr != nil {
			t.Fatalf("Append attempt %d: %v", attempt, aerr)
		}
		lastSeq = row.Seq
		select {
		case ev := <-received:
			// Subscriber attached; assert envelope shape.
			if ev.DataContentType != eventbus.ContentTypeOCSF {
				t.Errorf("envelope datacontenttype = %q, want %q",
					ev.DataContentType, eventbus.ContentTypeOCSF)
			}
			if ev.Extensions[eventbus.ExtTenantID] != tenant {
				t.Errorf("envelope tenant ext = %q, want %q",
					ev.Extensions[eventbus.ExtTenantID], tenant)
			}
			if err := ev.Validate(); err != nil {
				t.Errorf("envelope not valid CloudEvents v1.0.2: %v", err)
			}
			// The published state must reflect the successful publish.
			st, _, perr := store.PublishState(ctx, tenant, row.Seq)
			if perr != nil {
				t.Errorf("PublishState lookup: %v", perr)
			}
			if st != eventbus.PublishPublished {
				t.Errorf("publish state = %q, want %q (published)", st, eventbus.PublishPublished)
			}
			return
		case <-time.After(200 * time.Millisecond):
			// Try again — at-most-once delivery, subscriber may not
			// have attached yet.
		}
	}
	t.Fatalf("subscriber never received audit-bearing envelope after 20 publish attempts (lastSeq=%d)", lastSeq)
}

// capturingSink is an in-memory ocsf.Sink for the EventStore test.
type capturingSink struct {
	records []ocsf.Record
}

func (s *capturingSink) Deliver(_ context.Context, _, _ string, rec ocsf.Record) error {
	s.records = append(s.records, rec)
	return nil
}

// countAuditRowsUnderScopeAsApp opens a transaction, sets `scope`'s
// RLS context, drops to the non-superuser lenny_app role with SET
// LOCAL ROLE (a superuser bypasses RLS, so the count must run as the
// gateway's production role), and counts the audit rows visible for
// `other`. A correct lenny_tenant_isolation policy yields rows only
// for the tenant that equals `scope`.
func countAuditRowsUnderScopeAsApp(t *testing.T, ctx context.Context, pg *containers.Postgres, scope, other string) int {
	t.Helper()
	tx, err := pg.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin scoped tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", scope); err != nil {
		t.Fatalf("set scope: %v", err)
	}
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE lenny_app"); err != nil {
		t.Fatalf("set role lenny_app: %v", err)
	}
	var n int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE tenant_id = $1`, other).Scan(&n); err != nil {
		t.Fatalf("scoped count: %v", err)
	}
	return n
}

// mustBusEvent builds a CloudEvents envelope for the EventBus test.
func mustBusEvent(t *testing.T, tenant, short string) eventbus.Event {
	t.Helper()
	ev, err := eventbus.NewEvent(eventbus.NewEventInput{
		TenantID: tenant, PublisherID: "gw-test", ShortName: short,
		Subject: "session/s-1", Data: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	return ev
}

// publishUntilReceived re-publishes ev (Redis pub/sub is at-most-once,
// so the subscriber's consume loop may not have attached on the first
// try) until the subscriber delivers it or the deadline elapses.
func publishUntilReceived(t *testing.T, ctx context.Context, bus *eventbus.RedisEventBus,
	tenant string, ev eventbus.Event, received <-chan eventbus.Event,
) (eventbus.Event, bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-deadline:
			return eventbus.Event{}, false
		default:
		}
		if err := bus.Publish(ctx, eventbus.TenantID(tenant), eventbus.TopicSessionLifecycle, ev); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		select {
		case got := <-received:
			return got, true
		case <-time.After(100 * time.Millisecond):
		}
	}
}
