// SPDX-License-Identifier: MIT

package exportwire_test

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation/export"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation/exportwire"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
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
	// The session core reports no live binding for the parent.
	exp := exportwire.NewPodExporter(fakePodFiles{err: seam.ErrNoLiveBinding})
	_, err := exp.ExportPaths(context.Background(), "missing-parent", export.Spec{Source: "*"})
	if !errors.Is(err, exportwire.ErrParentUnbound) {
		t.Fatalf("err = %v, want ErrParentUnbound", err)
	}
}

// fakePodFiles is the session core's pod file export, answering with fixed
// files or a fixed error and recording the specifications it was asked for.
type fakePodFiles struct {
	files []seam.ExportedFile
	err   error
	got   *[]seam.ExportSpec
}

func (f fakePodFiles) ExportPaths(_ context.Context, _ string, specs []seam.ExportSpec) ([]seam.ExportedFile, error) {
	if f.got != nil {
		*f.got = specs
	}
	return f.files, f.err
}

// TestPodExporterReadsTheParentsFilesThroughTheSessionCore checks that the
// exporter asks the session core for the one specification it was given
// and returns each file the core read with its path, content, digest, and
// size, and that a failure of the core other than a missing binding comes
// back as it came.
// spec: GWSEAM-ExportPaths
func TestPodExporterReadsTheParentsFilesThroughTheSessionCore(t *testing.T) {
	var got []seam.ExportSpec
	exp := exportwire.NewPodExporter(fakePodFiles{
		files: []seam.ExportedFile{{Path: "out/a.txt", Content: []byte("alpha"), SHA256: "h", Size: 5}},
		got:   &got,
	})
	files, err := exp.ExportPaths(context.Background(), "parent", export.Spec{Source: "src/*", DestPrefix: "out/"})
	if err != nil {
		t.Fatalf("ExportPaths: %v", err)
	}
	if len(got) != 1 || got[0] != (seam.ExportSpec{Source: "src/*", DestPrefix: "out/"}) {
		t.Fatalf("specs sent to the core = %+v", got)
	}
	want := export.ExportedFile{Path: "out/a.txt", Content: []byte("alpha"), SHA256: "h", Size: 5}
	if len(files) != 1 || files[0].Path != want.Path || string(files[0].Content) != "alpha" || files[0].SHA256 != "h" || files[0].Size != 5 {
		t.Fatalf("files = %+v, want %+v", files, want)
	}

	boom := errors.New("adapter unavailable")
	if _, err := exportwire.NewPodExporter(fakePodFiles{err: boom}).ExportPaths(context.Background(), "parent", export.Spec{}); !errors.Is(err, boom) || errors.Is(err, exportwire.ErrParentUnbound) {
		t.Fatalf("err = %v, want the core's error as it came", err)
	}
}
