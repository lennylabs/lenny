// SPDX-License-Identifier: MIT

package blobstore_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
)

// spec: §12.9 line 1048 — "Tier mismatches (e.g., writing T4 data to a
// store not configured for envelope encryption) are rejected at write
// time with a CLASSIFICATION_CONTROL_VIOLATION error"; §15.1 line 1078 —
// the details.reason value `tier_store_mismatch`.

// t4Guard returns a guard that classifies the given tenant ids as T4
// (requireEnvelope=true) and everything else as non-T4.
func t4Guard(t4 ...string) blobstore.TierGuardFunc {
	set := map[string]bool{}
	for _, id := range t4 {
		set[id] = true
	}
	return func(tenantID string) (bool, error) { return set[tenantID], nil }
}

func TestMemoryStoreTierGuardRejectsT4_spec_12_9_1048(t *testing.T) {
	s := blobstore.NewMemoryStore(nil)
	var fired string
	s.SetOnTierStoreMismatch(func(tenantID string) { fired = tenantID })
	s.SetTierGuard(t4Guard("restricted"))

	u := blobstore.URI{TenantID: "restricted", SessionID: "sess_1", PartID: "part_1", TTL: time.Hour}
	_, err := s.Put(u, "text/plain", strings.NewReader("secret"))
	if !errors.Is(err, blobstore.ErrClassificationControlViolation) {
		t.Fatalf("Put T4: got %v, want ErrClassificationControlViolation", err)
	}
	if !errors.Is(err, blobstore.ErrTierStoreMismatch) {
		t.Errorf("Put T4: error does not wrap ErrTierStoreMismatch: %v", err)
	}
	if fired != "restricted" {
		t.Errorf("onTierStoreMismatch fired with %q, want %q", fired, "restricted")
	}
	// The body must not have been persisted.
	if _, _, gerr := s.Get(u); !errors.Is(gerr, blobstore.ErrNotFound) {
		t.Errorf("blob persisted despite rejection: Get err %v", gerr)
	}
}

func TestMemoryStoreTierGuardAdmitsNonT4_spec_12_9_1048(t *testing.T) {
	s := blobstore.NewMemoryStore(nil)
	s.SetTierGuard(t4Guard("restricted"))
	u := blobstore.URI{TenantID: "acme", SessionID: "sess_1", PartID: "part_1", TTL: time.Hour}
	if _, err := s.Put(u, "text/plain", strings.NewReader("ok")); err != nil {
		t.Fatalf("Put non-T4: %v", err)
	}
}

func TestMemoryStoreTierGuardNilIsNoop_spec_12_9_1048(t *testing.T) {
	// No guard installed (dev deployment with no tenant tier source): a
	// T4-named tenant writes normally.
	s := blobstore.NewMemoryStore(nil)
	u := blobstore.URI{TenantID: "restricted", SessionID: "sess_1", PartID: "part_1", TTL: time.Hour}
	if _, err := s.Put(u, "text/plain", strings.NewReader("ok")); err != nil {
		t.Fatalf("Put without guard: %v", err)
	}
}

func TestMemoryStoreTierGuardLookupErrorPassesThrough_spec_12_9_1048(t *testing.T) {
	// A guard lookup error is indeterminate and must not wedge the write:
	// the guard only fires on a confirmed-T4 classification.
	s := blobstore.NewMemoryStore(nil)
	s.SetTierGuard(func(string) (bool, error) { return true, errors.New("tenant store unreachable") })
	u := blobstore.URI{TenantID: "acme", SessionID: "sess_1", PartID: "part_1", TTL: time.Hour}
	if _, err := s.Put(u, "text/plain", strings.NewReader("ok")); err != nil {
		t.Fatalf("Put on guard error: got %v, want pass-through", err)
	}
}

func TestMemoryStoreTierGuardRejectsCopyToT4_spec_12_9_1048(t *testing.T) {
	s := blobstore.NewMemoryStore(nil)
	src := blobstore.URI{TenantID: "restricted", SessionID: "parent", PartID: "part_1", TTL: time.Hour}
	// Seed the source while the guard is absent so a copy is possible.
	if _, err := s.Put(src, "text/plain", strings.NewReader("snapshot")); err != nil {
		t.Fatalf("seed Put: %v", err)
	}
	s.SetTierGuard(t4Guard("restricted"))
	dst := blobstore.URI{TenantID: "restricted", SessionID: "child", PartID: "part_1", TTL: time.Hour}
	if err := s.Copy(src, dst); !errors.Is(err, blobstore.ErrTierStoreMismatch) {
		t.Fatalf("Copy to T4 dst: got %v, want ErrTierStoreMismatch", err)
	}
}

func TestFilesystemStoreTierGuardRejectsT4_spec_12_9_1048(t *testing.T) {
	fs, err := blobstore.NewFilesystemStore(filepath.Join(t.TempDir(), "blobs"), nil)
	if err != nil {
		t.Fatalf("NewFilesystemStore: %v", err)
	}
	fs.SetTierGuard(t4Guard("restricted"))
	u := blobstore.URI{TenantID: "restricted", SessionID: "sess_1", PartID: "part_1", TTL: time.Hour}
	_, err = fs.Put(u, "text/plain", strings.NewReader("secret"))
	if !errors.Is(err, blobstore.ErrClassificationControlViolation) || !errors.Is(err, blobstore.ErrTierStoreMismatch) {
		t.Fatalf("filesystem Put T4: got %v, want CLASSIFICATION_CONTROL_VIOLATION/tier_store_mismatch", err)
	}
}

func TestFilesystemStoreTierGuardAdmitsNonT4_spec_12_9_1048(t *testing.T) {
	fs, err := blobstore.NewFilesystemStore(filepath.Join(t.TempDir(), "blobs"), nil)
	if err != nil {
		t.Fatalf("NewFilesystemStore: %v", err)
	}
	fs.SetTierGuard(t4Guard("restricted"))
	u := blobstore.URI{TenantID: "acme", SessionID: "sess_1", PartID: "part_1", TTL: time.Hour}
	if _, err := fs.Put(u, "text/plain", strings.NewReader("ok")); err != nil {
		t.Fatalf("filesystem Put non-T4: %v", err)
	}
}
