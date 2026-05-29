// SPDX-License-Identifier: MIT

package eventbus

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

func mustEvent(t *testing.T, tenant, short string) Event {
	t.Helper()
	ev, err := NewEvent(NewEventInput{
		TenantID: tenant, PublisherID: "gw-1", ShortName: short, Subject: "s/1",
		Data: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	return ev
}

// spec: 12.3.7
// diagnosis: §12.3.7 says Publish requires a mandatory tenantID and
// validates the topic and the envelope. A missing tenantID, an unknown
// topic, or a malformed envelope must be rejected before any send.
func TestPublishRejectsInvalidInput(t *testing.T) {
	bus := NewRedisEventBus(nil, nil) // nil substrate: in-process no-op
	ctx := context.Background()

	if err := bus.Publish(ctx, "", TopicSessionLifecycle, mustEvent(t, "acme", "x")); err == nil {
		t.Error("Publish accepted an empty tenantID")
	}
	if err := bus.Publish(ctx, "acme", EventTopic("nope"), mustEvent(t, "acme", "x")); err == nil {
		t.Error("Publish accepted an unknown topic")
	}
	if err := bus.Publish(ctx, "acme", TopicSessionLifecycle, Event{}); err == nil {
		t.Error("Publish accepted a malformed (empty) envelope")
	}
}

// spec: 12.3.7
// diagnosis: §12.3.7 says EventBus emits lenny_event_bus_publish_total
// per topic. A successful Publish over the no-op substrate must still
// record the publish-attempt metric.
func TestPublishRecordsMetrics(t *testing.T) {
	metrics := NewCountingBusMetrics()
	bus := NewRedisEventBus(nil, metrics)
	if err := bus.Publish(context.Background(), "acme", TopicSessionLifecycle, mustEvent(t, "acme", "x")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if metrics.Published(TopicSessionLifecycle) != 1 {
		t.Errorf("publish_total = %d, want 1", metrics.Published(TopicSessionLifecycle))
	}
}

// spec: 12.3.7
// diagnosis: §12.3.7 publish-state enum is
// pending → retry_pending → published | failed. IsValid must accept
// exactly those four and reject anything else; AllPublishStates is the
// closed enumeration.
func TestPublishStateEnumIsClosed(t *testing.T) {
	for _, s := range AllPublishStates() {
		if !s.IsValid() {
			t.Errorf("%q is in AllPublishStates but IsValid rejects it", s)
		}
	}
	if PublishState("succeeded").IsValid() {
		t.Error("succeeded is the OCSF enum, not a publish state — IsValid must reject it")
	}
	if len(AllPublishStates()) != 4 {
		t.Errorf("publish-state enum has %d values, want 4", len(AllPublishStates()))
	}
}

// fakeRepublishStore is an in-memory eventbus.RetranscribeStore.
type fakeRepublishStore struct {
	mu   sync.Mutex
	rows []storedRow
}

type storedRow struct {
	row   RetranscribeRow
	state PublishState
}

func (f *fakeRepublishStore) PendingRepublish(_ context.Context, maxRetry, limit int) ([]RetranscribeRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []RetranscribeRow
	for _, r := range f.rows {
		eligible := (r.state == PublishFailed || r.state == PublishRetryPending) && r.row.RetryCount < maxRetry
		if eligible {
			out = append(out, r.row)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (f *fakeRepublishStore) SetPublishState(_ context.Context, tenantID string, seq uint64,
	state PublishState, retryCount int,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.rows {
		if f.rows[i].row.TenantID == tenantID && f.rows[i].row.Seq == seq {
			f.rows[i].state = state
			f.rows[i].row.RetryCount = retryCount
			return nil
		}
	}
	return errors.New("fakeRepublishStore: row not found")
}

func (f *fakeRepublishStore) lookup(seq uint64) (PublishState, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rows {
		if r.row.Seq == seq {
			return r.state, r.row.RetryCount
		}
	}
	return "", 0
}

// flakyPublisher fails the first failUntil publishes, then succeeds.
type flakyPublisher struct {
	mu       sync.Mutex
	failNext int
}

func (p *flakyPublisher) Publish(_ context.Context, _ TenantID, _ EventTopic, _ Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failNext > 0 {
		p.failNext--
		return &PublishError{Type: ErrBackendUnavailable, Err: errors.New("redis down")}
	}
	return nil
}

// spec: 12.3.7
// diagnosis: §12.3.7 retranscribe worker: on a successful re-publish
// the row transitions to published; the retry_count is retained for
// forensics. Sweep must drive that transition.
func TestRetranscribeSweepRepublishesFailedRow(t *testing.T) {
	store := &fakeRepublishStore{rows: []storedRow{{
		row:   RetranscribeRow{TenantID: "acme", Seq: 1, Topic: TopicSessionLifecycle, Event: mustEvent(t, "acme", "audit"), RetryCount: 2},
		state: PublishFailed,
	}}}
	pub := &flakyPublisher{failNext: 0} // succeeds immediately
	rt := NewRetranscriber(store, pub, DefaultRetranscribeConfig(), nil)

	res, err := rt.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if res.Republished != 1 {
		t.Errorf("Republished = %d, want 1", res.Republished)
	}
	st, rc := store.lookup(1)
	if st != PublishPublished {
		t.Errorf("row state = %q, want published", st)
	}
	if rc != 2 {
		t.Errorf("retry_count = %d, want 2 retained for forensics", rc)
	}
}

// spec: 12.3.7
// diagnosis: §12.3.7 retranscribe worker: a failed re-publish writes
// retry_pending with retry_count+1; when retry_count would reach
// maxRetryAttempts the row transitions to terminal failed and stops
// being swept. Sweep driven to exhaustion must walk that path.
func TestRetranscribeSweepReachesTerminalFailure(t *testing.T) {
	cfg := DefaultRetranscribeConfig()
	cfg.MaxRetryAttempts = 3
	store := &fakeRepublishStore{rows: []storedRow{{
		row:   RetranscribeRow{TenantID: "acme", Seq: 1, Topic: TopicSessionLifecycle, Event: mustEvent(t, "acme", "audit"), RetryCount: 0},
		state: PublishFailed,
	}}}
	pub := &flakyPublisher{failNext: 100} // always fails
	metrics := newCountingRetranscribeMetrics()
	rt := NewRetranscriber(store, pub, cfg, metrics)
	ctx := context.Background()

	// Sweeps 1 and 2 fail → retry_pending, count rising.
	for attempt := 1; attempt < cfg.MaxRetryAttempts; attempt++ {
		if _, err := rt.Sweep(ctx); err != nil {
			t.Fatalf("Sweep %d: %v", attempt, err)
		}
		st, rc := store.lookup(1)
		if st != PublishRetryPending {
			t.Errorf("after sweep %d state = %q, want retry_pending", attempt, st)
		}
		if rc != attempt {
			t.Errorf("after sweep %d retry_count = %d, want %d", attempt, rc, attempt)
		}
	}
	// The next sweep reaches terminal failure.
	res, err := rt.Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep terminal: %v", err)
	}
	if res.TerminalFailures != 1 {
		t.Errorf("TerminalFailures = %d, want 1", res.TerminalFailures)
	}
	st, rc := store.lookup(1)
	if st != PublishFailed {
		t.Errorf("terminal row state = %q, want failed", st)
	}
	if rc != cfg.MaxRetryAttempts {
		t.Errorf("terminal retry_count = %d, want %d", rc, cfg.MaxRetryAttempts)
	}
	// A row at retry_count == maxRetryAttempts is no longer swept.
	res, err = rt.Sweep(ctx)
	if err != nil {
		t.Fatalf("post-terminal Sweep: %v", err)
	}
	if res.Republished+res.RetryScheduled+res.TerminalFailures != 0 {
		t.Errorf("a terminal row was still swept: %+v", res)
	}
	if metrics.finalFailures != 1 {
		t.Errorf("EventBusPublishFinalFailure metric = %d, want 1", metrics.finalFailures)
	}
}

// spec: 12.3.7
// diagnosis: §12.3.7 retranscribe worker: after a transient backend
// outage a subsequent sweep re-publishes the row successfully — the
// correctness layer that guarantees every failed row is eventually
// re-published.
func TestRetranscribeSweepRecoversAfterTransientFailure(t *testing.T) {
	store := &fakeRepublishStore{rows: []storedRow{{
		row:   RetranscribeRow{TenantID: "acme", Seq: 1, Topic: TopicSessionLifecycle, Event: mustEvent(t, "acme", "audit")},
		state: PublishRetryPending,
	}}}
	pub := &flakyPublisher{failNext: 1} // fails once, then recovers
	rt := NewRetranscriber(store, pub, DefaultRetranscribeConfig(), nil)
	ctx := context.Background()

	if _, err := rt.Sweep(ctx); err != nil {
		t.Fatalf("first Sweep: %v", err)
	}
	if st, _ := store.lookup(1); st != PublishRetryPending {
		t.Errorf("after the failing sweep state = %q, want retry_pending", st)
	}
	if _, err := rt.Sweep(ctx); err != nil {
		t.Fatalf("second Sweep: %v", err)
	}
	if st, _ := store.lookup(1); st != PublishPublished {
		t.Errorf("after recovery state = %q, want published", st)
	}
}

// countingRetranscribeMetrics is an in-memory RetranscribeMetrics.
type countingRetranscribeMetrics struct {
	mu            sync.Mutex
	successes     int
	failures      int
	finalFailures int
}

func newCountingRetranscribeMetrics() *countingRetranscribeMetrics {
	return &countingRetranscribeMetrics{}
}

func (m *countingRetranscribeMetrics) Attempt(outcome string, _ PublishErrorType) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if outcome == "success" {
		m.successes++
	} else {
		m.failures++
	}
}

func (m *countingRetranscribeMetrics) FinalFailure(EventTopic) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.finalFailures++
}

// quiet the unused-import check when only some helpers are exercised.
var _ = time.Second
