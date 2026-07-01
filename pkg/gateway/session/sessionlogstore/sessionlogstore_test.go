// SPDX-License-Identifier: MIT

package sessionlogstore

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// spec: §4.4 line 226 — canonical session-log object key path.
func TestSessionLogObjectKey(t *testing.T) {
	got := SessionLogObjectKey("acme", "sess_alice_1")
	want := "/acme/sessions/sess_alice_1/stderr.log"
	if got != want {
		t.Fatalf("SessionLogObjectKey: got %q want %q", got, want)
	}
}

// spec: §4.4 line 226 — Noop store accepts well-formed records.
func TestNoop_PutAcceptsRecord(t *testing.T) {
	store := Noop{}
	err := store.Put(context.Background(), Record{
		TenantID:  "acme",
		SessionID: "sess_alice_1",
		Body:      []byte("ok"),
	})
	if err != nil {
		t.Fatalf("Noop.Put: unexpected error %v", err)
	}
}

// spec: §4.4 line 226 — Noop store rejects malformed records.
func TestNoop_PutValidatesTenantAndSession(t *testing.T) {
	store := Noop{}
	cases := []Record{
		{TenantID: "", SessionID: "s"},
		{TenantID: "t", SessionID: ""},
	}
	for _, r := range cases {
		if err := store.Put(context.Background(), r); err == nil {
			t.Fatalf("Noop.Put(%+v): want error, got nil", r)
		}
	}
}

// spec: §4.4 line 226 — MinIOStore.Put requires tenant + session.
func TestMinIOStore_PutValidates(t *testing.T) {
	store := &MinIOStore{Uploader: &fakeUploader{}}
	if err := store.Put(context.Background(), Record{}); err == nil {
		t.Fatal("MinIOStore.Put: empty record should error")
	}
}

// spec: §4.4 line 226 — empty body is dropped without an upload.
func TestMinIOStore_PutDropsEmptyBody(t *testing.T) {
	up := &fakeUploader{}
	cat := &fakeCatalog{}
	quota := &fakeQuota{}
	store := &MinIOStore{Uploader: up, Catalog: cat, Quota: quota}
	err := store.Put(context.Background(), Record{
		TenantID:  "acme",
		SessionID: "sess",
		Body:      []byte{},
	})
	if err != nil {
		t.Fatalf("Put empty: unexpected error %v", err)
	}
	if up.calls != 0 || cat.calls != 0 || quota.calls != 0 {
		t.Fatalf("empty body should not call uploader/catalog/quota; got uploader=%d catalog=%d quota=%d",
			up.calls, cat.calls, quota.calls)
	}
}

// spec: §4.4 line 226 — happy path: upload + catalog + quota all fire.
func TestMinIOStore_PutHappyPath(t *testing.T) {
	up := &fakeUploader{}
	cat := &fakeCatalog{}
	quota := &fakeQuota{}
	store := &MinIOStore{Uploader: up, Catalog: cat, Quota: quota}

	body := []byte("hello stderr world\n")
	err := store.Put(context.Background(), Record{
		TenantID:  "acme",
		SessionID: "sess_alice_1",
		Body:      body,
	})
	if err != nil {
		t.Fatalf("Put: unexpected error %v", err)
	}
	if up.calls != 1 {
		t.Fatalf("uploader: want 1 call, got %d", up.calls)
	}
	if up.gotURI != SessionLogObjectKey("acme", "sess_alice_1") {
		t.Fatalf("uploader URI: got %q", up.gotURI)
	}
	if up.gotSize != len(body) {
		t.Fatalf("uploader size: got %d want %d", up.gotSize, len(body))
	}
	if cat.calls != 1 {
		t.Fatalf("catalog: want 1 call, got %d", cat.calls)
	}
	if cat.gotURI != SessionLogObjectKey("acme", "sess_alice_1") {
		t.Fatalf("catalog URI: got %q", cat.gotURI)
	}
	if cat.gotSize != int64(len(body)) {
		t.Fatalf("catalog size: got %d want %d", cat.gotSize, len(body))
	}
	if quota.calls != 1 {
		t.Fatalf("quota: want 1 call, got %d", quota.calls)
	}
	if quota.gotDelta != int64(len(body)) {
		t.Fatalf("quota delta: got %d want %d", quota.gotDelta, len(body))
	}
}

// spec: §4.4 line 226 — uploader failure is swallowed (best-effort).
func TestMinIOStore_UploadFailureIsBestEffort(t *testing.T) {
	up := &fakeUploader{err: errors.New("minio down")}
	cat := &fakeCatalog{}
	quota := &fakeQuota{}
	var logged []string
	store := &MinIOStore{
		Uploader: up, Catalog: cat, Quota: quota,
		Logf: func(format string, args ...any) {
			logged = append(logged, format)
		},
	}
	err := store.Put(context.Background(), Record{
		TenantID:  "acme",
		SessionID: "sess",
		Body:      []byte("x"),
	})
	if err != nil {
		t.Fatalf("Put: best-effort upload should not return error; got %v", err)
	}
	if cat.calls != 0 {
		t.Fatal("catalog must not be invoked on upload failure")
	}
	if quota.calls != 0 {
		t.Fatal("quota must not be invoked on upload failure")
	}
	if len(logged) == 0 {
		t.Fatal("upload failure must be logged")
	}
}

// spec: §4.4 line 226 — catalog failure does not block quota bump.
func TestMinIOStore_CatalogFailureContinues(t *testing.T) {
	up := &fakeUploader{}
	cat := &fakeCatalog{err: errors.New("pg down")}
	quota := &fakeQuota{}
	store := &MinIOStore{Uploader: up, Catalog: cat, Quota: quota}
	err := store.Put(context.Background(), Record{
		TenantID:  "acme",
		SessionID: "sess",
		Body:      []byte("body"),
	})
	if err != nil {
		t.Fatalf("Put: unexpected error %v", err)
	}
	if quota.calls != 1 {
		t.Fatalf("quota must still bump on catalog failure; got %d calls", quota.calls)
	}
}

// spec: §4.4 line 226 — quota failure does not block catalog row.
func TestMinIOStore_QuotaFailureContinues(t *testing.T) {
	up := &fakeUploader{}
	cat := &fakeCatalog{}
	quota := &fakeQuota{err: errors.New("redis down")}
	store := &MinIOStore{Uploader: up, Catalog: cat, Quota: quota}
	err := store.Put(context.Background(), Record{
		TenantID:  "acme",
		SessionID: "sess",
		Body:      []byte("body"),
	})
	if err != nil {
		t.Fatalf("Put: unexpected error %v", err)
	}
	if cat.calls != 1 {
		t.Fatalf("catalog must still insert on quota failure; got %d calls", cat.calls)
	}
}

// spec: §4.4 line 226 — body larger than MaxLogBytes is truncated.
func TestMinIOStore_TruncatesLargeBody(t *testing.T) {
	up := &fakeUploader{}
	store := &MinIOStore{Uploader: up}
	body := make([]byte, MaxLogBytes+1024)
	err := store.Put(context.Background(), Record{
		TenantID:  "acme",
		SessionID: "sess",
		Body:      body,
	})
	if err != nil {
		t.Fatalf("Put: unexpected error %v", err)
	}
	if up.gotSize != MaxLogBytes {
		t.Fatalf("truncate: uploaded %d bytes, want %d", up.gotSize, MaxLogBytes)
	}
}

// spec: §4.4 line 226 — Store without Uploader is a no-op (dev mode).
func TestMinIOStore_NoUploaderIsNoOp(t *testing.T) {
	store := &MinIOStore{}
	err := store.Put(context.Background(), Record{
		TenantID:  "acme",
		SessionID: "sess",
		Body:      []byte("x"),
	})
	if err != nil {
		t.Fatalf("Put: unexpected error %v", err)
	}
}

// spec: §4.4 line 226 — CloseHook routes to the Store.
func TestCloseHook_OnSessionTerminal(t *testing.T) {
	captured := &capturingStore{}
	hook := &CloseHook{Store: captured}
	body := []byte("end of session")
	if err := hook.OnSessionTerminal(context.Background(), "acme", "sess_alice_1", body, true); err != nil {
		t.Fatalf("OnSessionTerminal: %v", err)
	}
	if len(captured.records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(captured.records))
	}
	r := captured.records[0]
	if r.TenantID != "acme" || r.SessionID != "sess_alice_1" {
		t.Fatalf("identity: got tenant=%q session=%q", r.TenantID, r.SessionID)
	}
	if !r.Truncated {
		t.Fatal("truncated flag should propagate")
	}
	if string(r.Body) != "end of session" {
		t.Fatalf("body: got %q", string(r.Body))
	}
}

// spec: §4.4 line 226 — CloseHook validates Store is wired.
func TestCloseHook_NilStoreErrors(t *testing.T) {
	hook := &CloseHook{}
	err := hook.OnSessionTerminal(context.Background(), "acme", "sess", nil, false)
	if err == nil {
		t.Fatal("expected error when Store is unwired")
	}
}

// fakeUploader counts upload calls and captures the canonical URI.
type fakeUploader struct {
	calls   int
	gotURI  string
	gotSize int
	err     error
}

func (f *fakeUploader) Upload(_ context.Context, tenantID, sessionID string, body io.Reader, sizeBytes int) error {
	f.calls++
	f.gotURI = SessionLogObjectKey(tenantID, sessionID)
	f.gotSize = sizeBytes
	if body != nil {
		// Drain body so the test can validate Reader behavior.
		_, _ = io.Copy(io.Discard, body)
	}
	return f.err
}

type fakeCatalog struct {
	calls   int
	gotURI  string
	gotSize int64
	err     error
}

func (f *fakeCatalog) RecordSessionLog(_ context.Context, _, _ string, uri string, sizeBytes int64) error {
	f.calls++
	f.gotURI = uri
	f.gotSize = sizeBytes
	return f.err
}

type fakeQuota struct {
	calls    int
	gotDelta int64
	err      error
}

func (f *fakeQuota) Adjust(_ context.Context, _ string, delta int64) error {
	f.calls++
	f.gotDelta = delta
	return f.err
}

type capturingStore struct {
	records []Record
}

func (c *capturingStore) Put(_ context.Context, r Record) error {
	c.records = append(c.records, r)
	return nil
}

// spec: §4.4 line 226 — sliceReader sustains short-buffer reads.
func TestSliceReader_ShortReads(t *testing.T) {
	r := newReader([]byte("abcdef"))
	buf := make([]byte, 2)
	var got strings.Builder
	for {
		n, err := r.Read(buf)
		if n > 0 {
			got.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("sliceReader: unexpected error %v", err)
		}
	}
	if got.String() != "abcdef" {
		t.Fatalf("sliceReader: got %q", got.String())
	}
}
