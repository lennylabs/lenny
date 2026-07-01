// SPDX-License-Identifier: MIT

package podsession_test

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/core/subsystem"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// archiveTar builds an in-memory tar from name→body entries.
func archiveTar(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, body := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatalf("tar header %q: %v", name, err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("tar body %q: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	return buf.Bytes()
}

func putArchiveBlob(t *testing.T, blobs blobstore.Store, part string, data []byte) string {
	t.Helper()
	uri := blobstore.URI{TenantID: "acme", SessionID: "sess-1", PartID: part, TTL: time.Hour, Encoding: blobstore.Encoding}
	if _, err := blobs.Put(uri, "application/octet-stream", bytes.NewReader(data)); err != nil {
		t.Fatalf("put archive blob: %v", err)
	}
	return uri.String()
}

// TestBindExtractsUploadArchive is the end-to-end §7.4 line 448 contract:
// the gateway decompresses an uploadArchive source, rewrites it into
// per-file sources, and the adapter materializes the extracted tree —
// without the pod ever decompressing the archive. F-7.4.1, F-13.4.1.
func TestBindExtractsUploadArchive(t *testing.T) {
	rt := &fakeRuntime{}
	srv := adapter.New("adapter-test")
	root := t.TempDir()
	srv.WorkspaceRoot = root
	srv.StagingDir = t.TempDir()
	srv.Runtime = rt

	blobs := blobstore.NewMemoryStore(nil)
	ref := putArchiveBlob(t, blobs, "arch-1", archiveTar(t, map[string]string{
		"src/main.go": "package main",
		"README.md":   "hello",
		"docs/g.md":   "guide",
	}))

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))
	binder.Blobs = blobs

	res, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
		Plan: &adapterv1.WorkspacePlan{
			SchemaVersion: 1,
			Sources: []*adapterv1.WorkspaceSource{
				{Type: "uploadArchive", Path: "proj", UploadRef: ref, Format: "tar"},
			},
		},
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	defer res.Adapter.Close()

	for rel, want := range map[string]string{
		"proj/src/main.go": "package main",
		"proj/README.md":   "hello",
		"proj/docs/g.md":   "guide",
	} {
		got, rerr := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if rerr != nil {
			t.Fatalf("read %q: %v", rel, rerr)
		}
		if string(got) != want {
			t.Errorf("%q = %q, want %q", rel, got, want)
		}
	}
}

// TestBindUploadArchiveStripComponentsWarning confirms the §7.4 line 459
// strip-skip warning now originates gateway-side and rides back on the
// bind result for SSE republish. F-7.4.15.
func TestBindUploadArchiveStripComponentsWarning(t *testing.T) {
	rt := &fakeRuntime{}
	srv := adapter.New("adapter-test")
	root := t.TempDir()
	srv.WorkspaceRoot = root
	srv.StagingDir = t.TempDir()
	srv.Runtime = rt

	blobs := blobstore.NewMemoryStore(nil)
	ref := putArchiveBlob(t, blobs, "arch-2", archiveTar(t, map[string]string{
		"toplevel.txt":      "skipped",
		"keep/nested/f.txt": "kept",
	}))

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))
	binder.Blobs = blobs

	res, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
		Plan: &adapterv1.WorkspacePlan{
			SchemaVersion: 1,
			Sources: []*adapterv1.WorkspaceSource{
				{Type: "uploadArchive", Path: "", UploadRef: ref, Format: "tar", StripComponents: 1},
			},
		},
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	defer res.Adapter.Close()

	var found bool
	for _, w := range res.WorkspacePlanWarnings {
		if w.GetCode() == "workspace_plan_strip_components_skip" && w.GetEntryPath() == "toplevel.txt" {
			found = true
		}
	}
	if !found {
		t.Fatalf("strip-skip warning not surfaced; warnings=%+v", res.WorkspacePlanWarnings)
	}
	// The kept entry lands with its leading segment stripped.
	if got, _ := os.ReadFile(filepath.Join(root, "nested", "f.txt")); string(got) != "kept" {
		t.Errorf("stripped entry = %q, want kept", got)
	}
}

// TestBindUploadArchiveAbortRecordsMetric confirms a §13.4 violation
// aborts the bind and increments lenny_upload_extraction_aborted_total
// with the typed sub-code. F-7.4.11.
func TestBindUploadArchiveAbortRecordsMetric(t *testing.T) {
	rt := &fakeRuntime{}
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.StagingDir = t.TempDir()
	srv.Runtime = rt

	blobs := blobstore.NewMemoryStore(nil)
	// A tar carrying a hardlink entry aborts with non_regular_entry.
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	if err := tw.WriteHeader(&tar.Header{Name: "h", Typeflag: tar.TypeLink, Linkname: "x", Mode: 0o644}); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	_ = tw.Close()
	ref := putArchiveBlob(t, blobs, "arch-3", raw.Bytes())

	var gotType string
	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))
	binder.Blobs = blobs
	binder.ExtractionAbort = func(errorType string) { gotType = errorType }
	binder.UploadGate = &subsystem.Subsystem{Name: "upload_handler", Limiter: &subsystem.Limiter{MaxConcurrent: 4}}

	_, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
		Plan: &adapterv1.WorkspacePlan{
			SchemaVersion: 1,
			Sources: []*adapterv1.WorkspaceSource{
				{Type: "uploadArchive", Path: "x", UploadRef: ref, Format: "tar"},
			},
		},
	})
	if err == nil {
		t.Fatal("Bind succeeded on a hardlink archive, want abort")
	}
	if gotType != "non_regular_entry" {
		t.Errorf("ExtractionAbort error_type = %q, want non_regular_entry", gotType)
	}
}
