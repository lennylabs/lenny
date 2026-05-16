// SPDX-License-Identifier: MIT

package blobstore_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
)

// spec: §4.5 blob URI scheme + immutability.

func TestParseURIHappyPath(t *testing.T) {
	got, err := blobstore.ParseURI("lenny-blob://acme/sess_1/part_xyz?ttl=3600&enc=aes256gcm")
	if err != nil {
		t.Fatalf("ParseURI: %v", err)
	}
	if got.TenantID != "acme" || got.SessionID != "sess_1" || got.PartID != "part_xyz" {
		t.Errorf("URI: got %+v", got)
	}
	if got.TTL != time.Hour {
		t.Errorf("TTL: got %v, want 1h", got.TTL)
	}
	if got.Encoding != blobstore.Encoding {
		t.Errorf("Encoding: got %q, want %q", got.Encoding, blobstore.Encoding)
	}
}

func TestParseURIDefaultsEncoding(t *testing.T) {
	got, err := blobstore.ParseURI("lenny-blob://acme/sess_1/part_xyz?ttl=60")
	if err != nil {
		t.Fatalf("ParseURI: %v", err)
	}
	if got.Encoding != blobstore.Encoding {
		t.Errorf("Encoding default: got %q, want %q", got.Encoding, blobstore.Encoding)
	}
}

func TestParseURIRejectsMalformed(t *testing.T) {
	cases := []string{
		"",
		"http://acme/sess/part?ttl=1",
		"lenny-blob://acme/sess?ttl=1",  // missing part
		"lenny-blob:///sess/part?ttl=1", // empty tenant
		"lenny-blob://acme/sess/part",   // missing ttl
		"lenny-blob://acme/sess/part?ttl=0",
		"lenny-blob://acme/sess/part?ttl=-5",
		"lenny-blob://acme/sess/part?ttl=abc",
	}
	for _, raw := range cases {
		_, err := blobstore.ParseURI(raw)
		if !errors.Is(err, blobstore.ErrInvalidURI) {
			t.Errorf("ParseURI(%q): got %v, want ErrInvalidURI", raw, err)
		}
	}
}

func TestURIRoundTrip(t *testing.T) {
	original := "lenny-blob://acme/sess_1/part_xyz?ttl=300&enc=aes256gcm"
	u, err := blobstore.ParseURI(original)
	if err != nil {
		t.Fatalf("ParseURI: %v", err)
	}
	if u.String() != original {
		t.Errorf("round-trip: got %q, want %q", u.String(), original)
	}
}

func TestMemoryStorePutGet(t *testing.T) {
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	s := blobstore.NewMemoryStore(clock)
	u := blobstore.URI{TenantID: "acme", SessionID: "sess_1", PartID: "part_1", TTL: time.Hour}
	if _, err := s.Put(u, "text/plain", strings.NewReader("hello")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	info, body, err := s.Get(u)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer body.Close()
	if info.MimeType != "text/plain" {
		t.Errorf("mimeType: got %q", info.MimeType)
	}
	if info.Size != 5 {
		t.Errorf("size: got %d, want 5", info.Size)
	}
	bs, _ := io.ReadAll(body)
	if string(bs) != "hello" {
		t.Errorf("body: got %q", string(bs))
	}
}

func TestMemoryStorePutRejectsOverwrite(t *testing.T) {
	s := blobstore.NewMemoryStore(nil)
	u := blobstore.URI{TenantID: "acme", SessionID: "sess_1", PartID: "part_1", TTL: time.Hour}
	if _, err := s.Put(u, "text/plain", strings.NewReader("first")); err != nil {
		t.Fatalf("Put 1: %v", err)
	}
	_, err := s.Put(u, "text/plain", strings.NewReader("second"))
	if !errors.Is(err, blobstore.ErrConflict) {
		t.Errorf("Put 2: got %v, want ErrConflict", err)
	}
}

func TestMemoryStoreGetReturnsNotFoundForUnknown(t *testing.T) {
	s := blobstore.NewMemoryStore(nil)
	u := blobstore.URI{TenantID: "acme", SessionID: "sess_1", PartID: "part_x", TTL: time.Hour}
	if _, _, err := s.Get(u); !errors.Is(err, blobstore.ErrNotFound) {
		t.Errorf("Get unknown: got %v, want ErrNotFound", err)
	}
	if _, err := s.Stat(u); !errors.Is(err, blobstore.ErrNotFound) {
		t.Errorf("Stat unknown: got %v, want ErrNotFound", err)
	}
}

func TestMemoryStoreGetReturnsNotFoundAfterTTL(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var now time.Time
	clock := func() time.Time { return now }
	s := blobstore.NewMemoryStore(clock)
	u := blobstore.URI{TenantID: "acme", SessionID: "sess_1", PartID: "part_1", TTL: 60 * time.Second}
	now = t0
	if _, err := s.Put(u, "text/plain", strings.NewReader("hi")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	now = t0.Add(time.Hour) // well past TTL
	if _, _, err := s.Get(u); !errors.Is(err, blobstore.ErrNotFound) {
		t.Errorf("Get post-TTL: got %v, want ErrNotFound", err)
	}
}

func TestMemoryStoreSweepDropsExpired(t *testing.T) {
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	s := blobstore.NewMemoryStore(clock)
	uA := blobstore.URI{TenantID: "acme", SessionID: "sess_1", PartID: "a", TTL: time.Minute}
	uB := blobstore.URI{TenantID: "acme", SessionID: "sess_1", PartID: "b", TTL: time.Hour}
	_, _ = s.Put(uA, "text/plain", strings.NewReader("a"))
	_, _ = s.Put(uB, "text/plain", strings.NewReader("b"))

	dropped := s.Sweep(clock().Add(30 * time.Minute))
	if dropped != 1 {
		t.Errorf("Sweep dropped: got %d, want 1", dropped)
	}
	if _, _, err := s.Get(uA); !errors.Is(err, blobstore.ErrNotFound) {
		t.Errorf("uA should be swept: got %v", err)
	}
	if _, _, err := s.Get(uB); err != nil {
		t.Errorf("uB should be retained: got %v", err)
	}
}

func TestNewPartIDFormat(t *testing.T) {
	got := blobstore.NewPartID()
	if !strings.HasPrefix(got, "part_") || len(got) != 5+16 {
		t.Errorf("NewPartID: got %q (length %d)", got, len(got))
	}
}

func TestMemoryStoreCrossTenantSegregation(t *testing.T) {
	s := blobstore.NewMemoryStore(nil)
	uA := blobstore.URI{TenantID: "acme", SessionID: "sess_1", PartID: "x", TTL: time.Hour}
	uB := blobstore.URI{TenantID: "globex", SessionID: "sess_1", PartID: "x", TTL: time.Hour}
	if _, err := s.Put(uA, "text/plain", strings.NewReader("acme-data")); err != nil {
		t.Fatalf("Put A: %v", err)
	}
	if _, err := s.Put(uB, "text/plain", strings.NewReader("globex-data")); err != nil {
		t.Fatalf("Put B (diff tenant): %v", err)
	}
	_, body, err := s.Get(uA)
	if err != nil {
		t.Fatalf("Get A: %v", err)
	}
	defer body.Close()
	bs, _ := io.ReadAll(body)
	if string(bs) != "acme-data" {
		t.Errorf("acme returned %q (cross-tenant data leak?)", string(bs))
	}
}

// spec: §12.8 GDPR erasure — the blob store's per-session adapter.

func TestMemoryStoreDeleteBySessionRemovesSessionBlobs(t *testing.T) {
	s := blobstore.NewMemoryStore(nil)
	keep := blobstore.URI{TenantID: "acme", SessionID: "sess_keep", PartID: "k", TTL: time.Hour}
	erase1 := blobstore.URI{TenantID: "acme", SessionID: "sess_erase", PartID: "a", TTL: time.Hour}
	erase2 := blobstore.URI{TenantID: "acme", SessionID: "sess_erase", PartID: "b", TTL: time.Hour}
	for _, u := range []blobstore.URI{keep, erase1, erase2} {
		if _, err := s.Put(u, "text/plain", strings.NewReader("x")); err != nil {
			t.Fatalf("Put %s: %v", u.PartID, err)
		}
	}
	deleted, err := s.DeleteBySession(context.Background(), "acme", "sess_erase")
	if err != nil {
		t.Fatalf("DeleteBySession: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2", deleted)
	}
	if _, _, err := s.Get(erase1); !errors.Is(err, blobstore.ErrNotFound) {
		t.Errorf("erased blob still present: got %v", err)
	}
	if _, err := s.Stat(keep); err != nil {
		t.Errorf("blob from another session should be retained: got %v", err)
	}
}

func TestMemoryStoreDeleteBySessionScopedByTenant(t *testing.T) {
	s := blobstore.NewMemoryStore(nil)
	acme := blobstore.URI{TenantID: "acme", SessionID: "sess_1", PartID: "p", TTL: time.Hour}
	globex := blobstore.URI{TenantID: "globex", SessionID: "sess_1", PartID: "p", TTL: time.Hour}
	if _, err := s.Put(acme, "text/plain", strings.NewReader("acme")); err != nil {
		t.Fatalf("Put acme: %v", err)
	}
	if _, err := s.Put(globex, "text/plain", strings.NewReader("globex")); err != nil {
		t.Fatalf("Put globex: %v", err)
	}
	deleted, err := s.DeleteBySession(context.Background(), "acme", "sess_1")
	if err != nil {
		t.Fatalf("DeleteBySession: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1 (same session id in another tenant must survive)", deleted)
	}
	if _, err := s.Stat(globex); err != nil {
		t.Errorf("globex blob erased by acme erasure (cross-tenant leak): %v", err)
	}
}

func TestMemoryStoreDeleteBySessionUnknownSessionIsNoOp(t *testing.T) {
	s := blobstore.NewMemoryStore(nil)
	deleted, err := s.DeleteBySession(context.Background(), "acme", "sess_absent")
	if err != nil {
		t.Fatalf("DeleteBySession: %v", err)
	}
	if deleted != 0 {
		t.Errorf("deleted = %d, want 0 for a session with no blobs", deleted)
	}
}
