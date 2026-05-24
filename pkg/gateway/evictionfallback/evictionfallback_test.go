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

	"github.com/lennylabs/lenny/pkg/gateway/evictionfallback"
	"github.com/lennylabs/lenny/pkg/gateway/evictionstatestore"
)

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

// fakeMetrics records every total-loss increment.
type fakeMetrics struct {
	calls []string
}

func (f *fakeMetrics) IncSessionEvictionTotalLoss(pool string, hadPriorCheckpoint bool) {
	tag := "no_prior"
	if hadPriorCheckpoint {
		tag = "with_prior"
	}
	f.calls = append(f.calls, pool+"|"+tag)
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
		Store:   failingStore{},
		Metrics: metrics,
		Events:  events,
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
	w := &evictionfallback.Writer{Store: failingStore{}}
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
		Store:   failingStore{},
		Metrics: metrics,
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
