// SPDX-License-Identifier: MIT

package exportwire_test

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/delegation/export"
	"github.com/lennylabs/lenny/pkg/gateway/delegation/exportwire"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
)

// TestBlobSinkPersist_spec_8_2_4 asserts the §8.2-step-4 durable hand-off:
// Persist writes the exported file's bytes to the §4.5 blob store under
// the export object class, scoped to the tenant + child session, and the
// returned ref round-trips through ParseURI + Get.
func TestBlobSinkPersist_spec_8_2_4(t *testing.T) {
	store := blobstore.NewMemoryStore(nil)
	sink := exportwire.NewBlobSink(store, 0)

	f := export.ExportedFile{Path: "docs/a.txt", Content: []byte("alpha"), Size: 5}
	ref, err := sink.Persist(context.Background(), "acme", "child-1", f)
	if err != nil {
		t.Fatalf("Persist: %v", err)
	}
	uri, err := blobstore.ParseURI(ref)
	if err != nil {
		t.Fatalf("returned ref is not a valid URI: %v", err)
	}
	if uri.TenantID != "acme" {
		t.Errorf("tenant = %q, want acme (tenant-scoped §12.2 key)", uri.TenantID)
	}
	if uri.ObjectType != blobstore.ObjectTypeExport {
		t.Errorf("objectType = %q, want %q", uri.ObjectType, blobstore.ObjectTypeExport)
	}
	if uri.SessionID != "child-1" {
		t.Errorf("sessionID = %q, want child-1", uri.SessionID)
	}
	_, rc, err := store.Get(uri)
	if err != nil {
		t.Fatalf("Get persisted blob: %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if string(got) != "alpha" {
		t.Errorf("persisted content = %q, want alpha", got)
	}
}

// TestBlobSinkUniquePartIDs asserts two identical-content files persist to
// distinct §4.5 immutable keys rather than colliding on ErrConflict.
func TestBlobSinkUniquePartIDs(t *testing.T) {
	sink := exportwire.NewBlobSink(blobstore.NewMemoryStore(nil), 0)
	f := export.ExportedFile{Path: "a.txt", Content: []byte("same")}
	r1, err := sink.Persist(context.Background(), "acme", "child", f)
	if err != nil {
		t.Fatalf("Persist 1: %v", err)
	}
	r2, err := sink.Persist(context.Background(), "acme", "child", f)
	if err != nil {
		t.Fatalf("Persist 2 (identical content) must not collide: %v", err)
	}
	if r1 == r2 {
		t.Error("two persists minted the same blob ref; part ids must be unique")
	}
}

// TestPodExporterUnboundParent_spec_8_2_3 asserts that exporting from a
// parent with no pod binding on this replica fails with ErrParentUnbound
// rather than silently returning an empty set.
func TestPodExporterUnboundParent_spec_8_2_3(t *testing.T) {
	// A fresh registry has no binding for any session.
	exp := exportwire.NewPodExporter(podsession.NewRegistry())
	_, err := exp.ExportPaths(context.Background(), "missing-parent", export.Spec{Source: "*"})
	if !errors.Is(err, exportwire.ErrParentUnbound) {
		t.Fatalf("err = %v, want ErrParentUnbound", err)
	}
}
