// SPDX-License-Identifier: MIT

package evictionfallback_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/checkpoint"
	"github.com/lennylabs/lenny/pkg/gateway/evictionfallback"
	"github.com/lennylabs/lenny/pkg/gateway/evictionstatestore"
)

// noRetryBudget disables the §4.4 line 277 retry loop in unit tests
// that exercise the total-loss path with a permanently-failing store.
// The retry-budget behaviour itself is covered by TestWriteRetryBudget*
// below.
var noRetryBudget = checkpoint.RetryBudget{
	Initial:     1,
	Cap:         1,
	TotalBudget: 1, // 1 ns total — first failure terminates.
}

// spec: §4.4 lines 263–291 — eviction-fallback writer + total-loss path.

// fakeUploader records every upload and optionally fails.
type fakeUploader struct {
	mu          sync.Mutex
	tenantSeen  string
	sessionSeen string
	body        []byte
	err         error
}

func (f *fakeUploader) Upload(_ context.Context, tenantID, sessionID string, body io.Reader, _ int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tenantSeen = tenantID
	f.sessionSeen = sessionID
	if f.err != nil {
		return f.err
	}
	b, _ := io.ReadAll(body)
	f.body = b
	return nil
}

// fakeMetrics records every total-loss increment and partial-keys-
// logged increment.
type fakeMetrics struct {
	calls           []string
	partialKeyCalls []string
	fallbackCalls   []string
}

func (f *fakeMetrics) IncSessionEvictionTotalLoss(pool string, hadPriorCheckpoint bool) {
	tag := "no_prior"
	if hadPriorCheckpoint {
		tag = "with_prior"
	}
	f.calls = append(f.calls, pool+"|"+tag)
}

func (f *fakeMetrics) IncCheckpointEvictionPartialKeysLogged(pool, keysCommitted string) {
	f.partialKeyCalls = append(f.partialKeyCalls, pool+"|"+keysCommitted)
}

// spec: §4.4 line 263 — counts every entry to the eviction-fallback
// writer.
func (f *fakeMetrics) IncCheckpointEvictionFallback(pool string, hadPriorCheckpoint bool) {
	tag := "no_prior"
	if hadPriorCheckpoint {
		tag = "with_prior"
	}
	f.fallbackCalls = append(f.fallbackCalls, pool+"|"+tag)
}

// fakeEvents records every session.lost emission.
type fakeEvents struct {
	sessionID string
	reason    string
	fields    map[string]any
	calls     int
}

func (f *fakeEvents) EmitSessionLost(_ context.Context, sessionID, reason string, fields map[string]any) {
	f.calls++
	f.sessionID = sessionID
	f.reason = reason
	f.fields = fields
}

// failingStore unconditionally fails Put. Used to exercise the
// total-loss path.
type failingStore struct{}

func (failingStore) Put(context.Context, evictionstatestore.Record) error {
	return errors.New("postgres rejected the write")
}

func (failingStore) Get(context.Context, string, string) (evictionstatestore.Record, error) {
	return evictionstatestore.Record{}, evictionstatestore.ErrNotFound
}
func (failingStore) Delete(context.Context, string, string) error                  { return nil }
func (failingStore) DeleteByUser(context.Context, string, string, []string) error  { return nil }
func (failingStore) DeleteByTenant(context.Context, string) error                  { return nil }
func (failingStore) SweepDeletedBefore(context.Context, time.Time) (int, error)    { return 0, nil }

func newMemStore() *evictionstatestore.MemoryStore {
	return evictionstatestore.NewMemoryStore(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
}

func baseTemplate() evictionstatestore.Record {
	return evictionstatestore.Record{
		TenantID:           "acme",
		SessionID:          "sess-evict-1",
		RecoveryGeneration: 3,
		ConversationCursor: "cur-42",
		EvictedAt:          time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
	}
}

func TestWriteInlineForSmallContext(t *testing.T) {
	store := newMemStore()
	w := &evictionfallback.Writer{Store: store}

	res, err := w.Write(context.Background(), evictionfallback.WriteParams{
		Record:  baseTemplate(),
		Context: []byte("conversation up to here"),
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if res.Outcome != evictionfallback.OutcomeInline {
		t.Errorf("Outcome = %q, want inline", res.Outcome)
	}
	if res.PersistedRecord.IsMinIOKey {
		t.Error("IsMinIOKey = true, want false for inline path")
	}
	if !res.PersistedRecord.WorkspaceLost {
		t.Error("WorkspaceLost = false, want true by construction")
	}

	// The store actually received the record.
	got, err := store.Get(context.Background(), "acme", "sess-evict-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got.LastMessageContext) != "conversation up to here" {
		t.Errorf("stored context = %q, want %q", got.LastMessageContext, "conversation up to here")
	}
}

// spec: §4.4 line 263 — every entry to the eviction-fallback writer
// bumps lenny_checkpoint_eviction_fallback_total. The counter fires
// on success and failure alike so operators can count every fallback
// attempt regardless of the chooser outcome.
func TestWriteIncrementsFallbackEntryCounter(t *testing.T) {
	store := newMemStore()
	metrics := &fakeMetrics{}
	w := &evictionfallback.Writer{Store: store, Metrics: metrics}

	if _, err := w.Write(context.Background(), evictionfallback.WriteParams{
		Record:             baseTemplate(),
		Context:            []byte("small payload"),
		Pool:               "default-pool",
		HadPriorCheckpoint: true,
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(metrics.fallbackCalls) != 1 {
		t.Fatalf("eviction_fallback emissions = %d, want 1", len(metrics.fallbackCalls))
	}
	if metrics.fallbackCalls[0] != "default-pool|with_prior" {
		t.Fatalf("eviction_fallback label = %q, want default-pool|with_prior", metrics.fallbackCalls[0])
	}
}

func TestWriteMinIOKeyForLargeContext(t *testing.T) {
	store := newMemStore()
	uploader := &fakeUploader{}
	w := &evictionfallback.Writer{Store: store, ContextUploader: uploader}

	big := strings.Repeat("x", evictionfallback.MaxInlineContextBytes+10)
	res, err := w.Write(context.Background(), evictionfallback.WriteParams{
		Record:  baseTemplate(),
		Context: []byte(big),
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if res.Outcome != evictionfallback.OutcomeMinIOKey {
		t.Errorf("Outcome = %q, want minio_key", res.Outcome)
	}
	if !res.PersistedRecord.IsMinIOKey {
		t.Error("IsMinIOKey = false on the MinIO path")
	}
	wantKey := evictionfallback.EvictionContextObjectKey("acme", "sess-evict-1")
	if string(res.PersistedRecord.LastMessageContext) != wantKey {
		t.Errorf("LastMessageContext = %q, want %q", res.PersistedRecord.LastMessageContext, wantKey)
	}
	if uploader.tenantSeen != "acme" || uploader.sessionSeen != "sess-evict-1" {
		t.Errorf("uploader saw (%q, %q), want (acme, sess-evict-1)", uploader.tenantSeen, uploader.sessionSeen)
	}
	if len(uploader.body) != len(big) {
		t.Errorf("uploader body bytes = %d, want %d", len(uploader.body), len(big))
	}
}

func TestWriteTruncatesOnMinIOFailure(t *testing.T) {
	store := newMemStore()
	uploader := &fakeUploader{err: errors.New("minio unreachable")}
	w := &evictionfallback.Writer{Store: store, ContextUploader: uploader}

	big := strings.Repeat("y", evictionfallback.MaxInlineContextBytes+500)
	res, err := w.Write(context.Background(), evictionfallback.WriteParams{
		Record:  baseTemplate(),
		Context: []byte(big),
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if res.Outcome != evictionfallback.OutcomeTruncated {
		t.Errorf("Outcome = %q, want truncated", res.Outcome)
	}
	if !res.PersistedRecord.ContextTruncated {
		t.Error("ContextTruncated = false, want true on MinIO failure")
	}
	if res.PersistedRecord.IsMinIOKey {
		t.Error("IsMinIOKey = true on truncation path, want false")
	}
	if len(res.PersistedRecord.LastMessageContext) != evictionfallback.MaxTruncatedContextBytes {
		t.Errorf("truncated body len = %d, want %d",
			len(res.PersistedRecord.LastMessageContext), evictionfallback.MaxTruncatedContextBytes)
	}
}

func TestWriteTruncatesWhenNoUploaderWired(t *testing.T) {
	store := newMemStore()
	w := &evictionfallback.Writer{Store: store} // no uploader

	big := strings.Repeat("z", evictionfallback.MaxInlineContextBytes+1)
	res, err := w.Write(context.Background(), evictionfallback.WriteParams{
		Record:  baseTemplate(),
		Context: []byte(big),
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if res.Outcome != evictionfallback.OutcomeTruncated {
		t.Errorf("Outcome = %q, want truncated when uploader is nil", res.Outcome)
	}
	if !res.PersistedRecord.ContextTruncated {
		t.Error("ContextTruncated = false on no-uploader truncation, want true")
	}
}

func TestWriteCapsOriginalContextAt64KB(t *testing.T) {
	store := newMemStore()
	uploader := &fakeUploader{}
	w := &evictionfallback.Writer{Store: store, ContextUploader: uploader}

	// 80 KB input — writer must clamp at 64 KB before uploading.
	big := strings.Repeat("a", 80*1024)
	if _, err := w.Write(context.Background(), evictionfallback.WriteParams{
		Record:  baseTemplate(),
		Context: []byte(big),
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(uploader.body) != evictionfallback.MaxOriginalContextBytes {
		t.Errorf("uploader received %d bytes, want clamped to %d",
			len(uploader.body), evictionfallback.MaxOriginalContextBytes)
	}
}

func TestWriteDrivesTotalLossOnPostgresFailure(t *testing.T) {
	metrics := &fakeMetrics{}
	events := &fakeEvents{}
	w := &evictionfallback.Writer{
		Store:       failingStore{},
		Metrics:     metrics,
		Events:      events,
		RetryBudget: noRetryBudget,
		Sleep:       func(time.Duration) {},
	}

	_, err := w.Write(context.Background(), evictionfallback.WriteParams{
		Record:             baseTemplate(),
		Context:            []byte("conversation"),
		Pool:               "claude-code-pool",
		HadPriorCheckpoint: true,
		MinIOError:         errors.New("prior minio outage"),
	})
	if err == nil {
		t.Fatal("Write returned nil, want the underlying Postgres error")
	}
	if len(metrics.calls) != 1 || metrics.calls[0] != "claude-code-pool|with_prior" {
		t.Errorf("total-loss metric = %v, want [claude-code-pool|with_prior]", metrics.calls)
	}
	if events.calls != 1 || events.reason != "eviction_total_loss" {
		t.Errorf("session.lost event calls=%d reason=%q, want 1 / eviction_total_loss",
			events.calls, events.reason)
	}
	if events.sessionID != "sess-evict-1" {
		t.Errorf("session.lost sessionID = %q, want sess-evict-1", events.sessionID)
	}
	if events.fields["minio_error"] != "prior minio outage" {
		t.Errorf("session.lost fields[minio_error] = %v, want 'prior minio outage'", events.fields["minio_error"])
	}
	if events.fields["postgres_error"] == nil || events.fields["postgres_error"] == "" {
		t.Errorf("session.lost fields[postgres_error] empty, want a non-empty summary")
	}
}

func TestWriteTotalLossWithoutMetricsAndEventsStillCompletes(t *testing.T) {
	w := &evictionfallback.Writer{
		Store:       failingStore{},
		RetryBudget: noRetryBudget,
		Sleep:       func(time.Duration) {},
	}
	_, err := w.Write(context.Background(), evictionfallback.WriteParams{
		Record:  baseTemplate(),
		Context: []byte("c"),
	})
	if err == nil {
		t.Fatal("Write returned nil, want Postgres error")
	}
	// No panic — the path is silent when metrics/events are unwired.
}

func TestWriteRejectsMissingIDs(t *testing.T) {
	w := &evictionfallback.Writer{Store: newMemStore()}
	_, err := w.Write(context.Background(), evictionfallback.WriteParams{
		Record:  evictionstatestore.Record{}, // tenant + session empty
		Context: []byte("x"),
	})
	if err == nil {
		t.Error("Write accepted an empty Record; expected validation error")
	}
}

func TestWriteRejectsNilStore(t *testing.T) {
	w := &evictionfallback.Writer{}
	_, err := w.Write(context.Background(), evictionfallback.WriteParams{
		Record:  baseTemplate(),
		Context: []byte("x"),
	})
	if err == nil {
		t.Error("Write accepted a nil Store; expected validation error")
	}
}

func TestEvictionContextObjectKey(t *testing.T) {
	got := evictionfallback.EvictionContextObjectKey("acme", "sess-1")
	want := "/acme/eviction/sess-1/context"
	if got != want {
		t.Errorf("key = %q, want %q", got, want)
	}
}

// TestWriteHadPriorCheckpointFalseLabel covers the second label
// branch of the total-loss metric (no prior full checkpoint = total
// data loss).
func TestWriteHadPriorCheckpointFalseLabel(t *testing.T) {
	metrics := &fakeMetrics{}
	w := &evictionfallback.Writer{
		Store:       failingStore{},
		Metrics:     metrics,
		RetryBudget: noRetryBudget,
		Sleep:       func(time.Duration) {},
	}
	_, _ = w.Write(context.Background(), evictionfallback.WriteParams{
		Record:             baseTemplate(),
		Context:            []byte("x"),
		Pool:               "echo-pool",
		HadPriorCheckpoint: false,
	})
	if len(metrics.calls) != 1 || metrics.calls[0] != "echo-pool|no_prior" {
		t.Errorf("total-loss metric = %v, want [echo-pool|no_prior]", metrics.calls)
	}
}

// flakyStore fails on the first N attempts then succeeds. Used to
// exercise the §4.4 line 277 retry loop.
type flakyStore struct {
	mu        sync.Mutex
	failsLeft int
	calls     int
	got       *evictionstatestore.Record
}

func (f *flakyStore) Put(_ context.Context, r evictionstatestore.Record) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.failsLeft > 0 {
		f.failsLeft--
		return errors.New("postgres failover in progress")
	}
	cp := r
	f.got = &cp
	return nil
}

func (f *flakyStore) Get(context.Context, string, string) (evictionstatestore.Record, error) {
	return evictionstatestore.Record{}, evictionstatestore.ErrNotFound
}
func (f *flakyStore) Delete(context.Context, string, string) error                 { return nil }
func (f *flakyStore) DeleteByUser(context.Context, string, string, []string) error { return nil }
func (f *flakyStore) DeleteByTenant(context.Context, string) error                 { return nil }
func (f *flakyStore) SweepDeletedBefore(context.Context, time.Time) (int, error)   { return 0, nil }

// TestWriteRetriesPostgresFailover exercises the §4.4 line 277 retry
// loop. The flakyStore fails twice then succeeds; the writer must
// loop without driving the total-loss path.
//
// spec: §4.4 line 277.
func TestWriteRetriesPostgresFailover(t *testing.T) {
	store := &flakyStore{failsLeft: 2}
	metrics := &fakeMetrics{}
	sleeps := []time.Duration{}
	w := &evictionfallback.Writer{
		Store:       store,
		Metrics:     metrics,
		RetryBudget: checkpoint.RetryBudgetForFallback(),
		Sleep:       func(d time.Duration) { sleeps = append(sleeps, d) },
	}
	res, err := w.Write(context.Background(), evictionfallback.WriteParams{
		Record:  baseTemplate(),
		Context: []byte("conversation"),
		Pool:    "claude-code-pool",
	})
	if err != nil {
		t.Fatalf("Write: %v (succeeded on retry, should be nil)", err)
	}
	if res.Outcome != evictionfallback.OutcomeInline {
		t.Errorf("Outcome = %q, want inline after retry success", res.Outcome)
	}
	if store.calls != 3 {
		t.Errorf("Store.Put attempts = %d, want 3 (2 failures + 1 success)", store.calls)
	}
	// The retry path is the success branch — neither the total-loss
	// metric nor the partial-keys WARN counter should fire.
	if len(metrics.calls) != 0 {
		t.Errorf("total-loss metric fired during successful retry: %v", metrics.calls)
	}
	if len(metrics.partialKeyCalls) != 0 {
		t.Errorf("partial-keys metric fired during successful retry: %v", metrics.partialKeyCalls)
	}
	// First retry sleep should be 500ms; second 1s (2x).
	if len(sleeps) != 2 {
		t.Fatalf("retry sleeps = %v, want 2 backoffs", sleeps)
	}
	if sleeps[0] != 500*time.Millisecond {
		t.Errorf("first backoff = %v, want 500ms", sleeps[0])
	}
	if sleeps[1] != time.Second {
		t.Errorf("second backoff = %v, want 1s", sleeps[1])
	}
}

// TestWriteExhaustsRetryBudgetThenDrivesTotalLoss confirms the §4.4
// line 277 total budget terminates the loop and the §4.4 line 279 and
// 283–289 emissions fire on exhaustion.
//
// spec: §4.4 lines 277, 279, 283–289.
func TestWriteExhaustsRetryBudgetThenDrivesTotalLoss(t *testing.T) {
	metrics := &fakeMetrics{}
	events := &fakeEvents{}
	store := &flakyStore{failsLeft: 100} // never recovers within the budget
	w := &evictionfallback.Writer{
		Store:   store,
		Metrics: metrics,
		Events:  events,
		// Use a small budget that the test exhausts in a few attempts.
		RetryBudget: checkpoint.RetryBudget{
			Initial:     1 * time.Microsecond,
			Cap:         2 * time.Microsecond,
			TotalBudget: 10 * time.Microsecond,
		},
		Sleep: func(time.Duration) {}, // no real sleeps
	}
	_, err := w.Write(context.Background(), evictionfallback.WriteParams{
		Record:             baseTemplate(),
		Context:            []byte("c"),
		Pool:               "p",
		CommittedMinIOKeys: []string{"/acme/eviction/sess-evict-1/partial-1.tar"},
		ChunkEncoding:      "tar",
	})
	if err == nil {
		t.Fatal("Write returned nil after retry-budget exhaustion; want underlying Postgres error")
	}
	if len(metrics.calls) != 1 {
		t.Errorf("total-loss metric = %v, want 1 increment", metrics.calls)
	}
	// Partial-keys metric should fire with keys_committed=1+ (we passed
	// a non-empty CommittedMinIOKeys list).
	if len(metrics.partialKeyCalls) != 1 || metrics.partialKeyCalls[0] != "p|1+" {
		t.Errorf("partial-keys metric = %v, want [p|1+]", metrics.partialKeyCalls)
	}
	if events.calls != 1 {
		t.Errorf("session.lost events = %d, want 1", events.calls)
	}
	if store.calls < 2 {
		t.Errorf("Store.Put attempts = %d, want ≥ 2 (first + at least one retry)", store.calls)
	}
}

// TestWritePartialKeysCounterZeroLabel covers the keys_committed="0"
// label branch — total-MinIO-failure with no chunks committed.
//
// spec: §4.4 line 279.
func TestWritePartialKeysCounterZeroLabel(t *testing.T) {
	metrics := &fakeMetrics{}
	w := &evictionfallback.Writer{
		Store:       failingStore{},
		Metrics:     metrics,
		RetryBudget: noRetryBudget,
		Sleep:       func(time.Duration) {},
	}
	_, _ = w.Write(context.Background(), evictionfallback.WriteParams{
		Record:             baseTemplate(),
		Context:            []byte("x"),
		Pool:               "echo-pool",
		CommittedMinIOKeys: nil, // no chunks committed
	})
	if len(metrics.partialKeyCalls) != 1 || metrics.partialKeyCalls[0] != "echo-pool|0" {
		t.Errorf("partial-keys metric = %v, want [echo-pool|0]", metrics.partialKeyCalls)
	}
}

// fakeCatalog records every artifact_store row written.
type fakeCatalog struct {
	calls []string
	err   error
}

func (c *fakeCatalog) RecordEvictionContext(_ context.Context, tenantID, sessionID, uri string, size int64) error {
	if c.err != nil {
		return c.err
	}
	c.calls = append(c.calls, tenantID+"|"+sessionID+"|"+uri+"|"+strings.TrimSpace(strings.Repeat(" ", int(size%5))))
	return nil
}

// fakeQuota records every storage-bytes adjustment.
type fakeQuota struct {
	deltas []int64
	err    error
}

func (q *fakeQuota) Adjust(_ context.Context, _ string, delta int64) error {
	q.deltas = append(q.deltas, delta)
	return q.err
}

// TestWriteRecordsArtifactStoreAndBumpsQuota covers the §4.4 line 291
// storage-quota accounting: when MinIO succeeds the writer inserts an
// artifact_store row and then bumps the Redis quota by the confirmed
// size.
//
// spec: §4.4 line 291.
func TestWriteRecordsArtifactStoreAndBumpsQuota(t *testing.T) {
	store := newMemStore()
	uploader := &fakeUploader{}
	catalog := &fakeCatalog{}
	quota := &fakeQuota{}
	w := &evictionfallback.Writer{
		Store:           store,
		ContextUploader: uploader,
		Catalog:         catalog,
		Quota:           quota,
	}
	big := strings.Repeat("x", evictionfallback.MaxInlineContextBytes+100)
	res, err := w.Write(context.Background(), evictionfallback.WriteParams{
		Record:  baseTemplate(),
		Context: []byte(big),
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if res.Outcome != evictionfallback.OutcomeMinIOKey {
		t.Errorf("Outcome = %q, want minio_key", res.Outcome)
	}
	if len(catalog.calls) != 1 {
		t.Errorf("catalog calls = %d, want 1 artifact_store insert", len(catalog.calls))
	}
	if len(quota.deltas) != 1 || quota.deltas[0] != int64(len(big)) {
		t.Errorf("quota deltas = %v, want [%d]", quota.deltas, len(big))
	}
}

// TestWriteSkipsArtifactAccountingOnMinIOFailure asserts the §4.4 line
// 291 guard: on MinIO unavailability no artifact_store row is written
// and no quota increment is issued.
//
// spec: §4.4 line 291 — "On MinIO unavailability ... no MinIO object
// is written and therefore no artifact_store row is inserted and no
// quota increment is issued."
func TestWriteSkipsArtifactAccountingOnMinIOFailure(t *testing.T) {
	store := newMemStore()
	uploader := &fakeUploader{err: errors.New("minio down")}
	catalog := &fakeCatalog{}
	quota := &fakeQuota{}
	w := &evictionfallback.Writer{
		Store:           store,
		ContextUploader: uploader,
		Catalog:         catalog,
		Quota:           quota,
	}
	big := strings.Repeat("y", evictionfallback.MaxInlineContextBytes+50)
	_, err := w.Write(context.Background(), evictionfallback.WriteParams{
		Record:  baseTemplate(),
		Context: []byte(big),
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(catalog.calls) != 0 {
		t.Errorf("catalog calls = %d, want 0 on MinIO failure", len(catalog.calls))
	}
	if len(quota.deltas) != 0 {
		t.Errorf("quota deltas = %v, want empty on MinIO failure", quota.deltas)
	}
}
