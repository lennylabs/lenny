// SPDX-License-Identifier: MIT

package blobstore_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
)

// spec: §12.1 line 5 — every blob backend (and the wrappers wired in
// front of them) satisfies the mandatory-erasure Eraser interface, so
// a backend that omits either primitive cannot compile in.
func TestBlobBackendsSatisfyEraser_spec_12_1(t *testing.T) {
	t.Parallel()
	var (
		_ blobstore.Eraser = (*blobstore.MemoryStore)(nil)
		_ blobstore.Eraser = (*blobstore.TenantScoped)(nil)
	)
}

// spec: §12.1 line 5 / §12.8 step 7 — artifact erasure is
// session-scoped, so the whole-user DeleteByUser is a no-op that leaves
// the user's session blobs in place; the orchestrator erases them per
// session via DeleteBySession.
func TestDeleteByUserIsSessionScopedNoOp_spec_12_8_step7(t *testing.T) {
	t.Parallel()
	s := blobstore.NewMemoryStore(nil)
	u := blobstore.URI{TenantID: "acme", ObjectType: blobstore.ObjectTypeUpload, SessionID: "sess_1", PartID: "part_1", TTL: time.Hour}
	if _, err := s.Put(u, "text/plain", strings.NewReader("hello")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	n, err := s.DeleteByUser(context.Background(), "acme", "alice@acme.com")
	if err != nil || n != 0 {
		t.Fatalf("DeleteByUser = (%d, %v), want (0, nil)", n, err)
	}
	// The blob survives a whole-user call; only DeleteBySession removes it.
	if _, err := s.Stat(u); err != nil {
		t.Fatalf("blob should survive DeleteByUser: %v", err)
	}
	if got, err := s.DeleteBySession(context.Background(), "acme", "sess_1"); err != nil || got != 1 {
		t.Fatalf("DeleteBySession = (%d, %v), want (1, nil)", got, err)
	}
}
