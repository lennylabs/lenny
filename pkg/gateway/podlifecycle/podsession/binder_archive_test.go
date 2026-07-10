// SPDX-License-Identifier: MIT

package podsession_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/core/subsystem"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/upload"
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

// archiveTarGz builds an in-memory gzip-compressed tar from name→body
// entries. Some §13.4 ceilings (the 100:1 decompression-ratio bomb) only
// manifest under a compressed format, so the plain archiveTar helper
// above cannot exercise them.
func archiveTarGz(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var gz bytes.Buffer
	gw := gzip.NewWriter(&gz)
	tw := tar.NewWriter(gw)
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
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return gz.Bytes()
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

// TestBindUploadArchiveAbortRecordsMetric_MaliciousArchives confirms three
// more §13.4 malicious-archive ceilings — a decompression-ratio bomb, a
// path-traversal entry, and an over-limit entry count — each abort
// Binder.Bind with the documented UPLOAD_ARCHIVE_LIMIT_EXCEEDED sub-code
// and increment lenny_upload_extraction_aborted_total, through the same
// blob-store-to-adapter-materialize path TestBindExtractsUploadArchive
// exercises for the happy path. The pkg/upload/archive unit tests prove
// the validator in isolation; this proves it is actually invoked on the
// real extraction path reached from Binder.Bind, not just callable in a
// test harness that talks to it directly. F-7.4.11, F-13.4.1.
func TestBindUploadArchiveAbortRecordsMetric_MaliciousArchives(t *testing.T) {
	cases := []struct {
		name       string
		format     string
		data       []byte
		wantReason upload.Reason
	}{
		{
			name:   "decompression_ratio_bomb",
			format: "tar.gz",
			// spec: §13.4 line 659 ("Maximum decompression ratio
			// (compressed:uncompressed): 100:1.") — a run of identical
			// bytes gzips far below 1% of its size.
			data:       archiveTarGz(t, map[string]string{"bomb.txt": strings.Repeat("A", 2*1024*1024)}),
			wantReason: upload.ReasonMaxDecompressionRatio,
		},
		{
			name:   "path_traversal",
			format: "tar",
			// spec: §13.4 line 663 ("Path traversal protection: reject `..`
			// components, absolute paths, and symlinks whose canonicalized
			// target escapes the workspace root.")
			data:       archiveTar(t, map[string]string{"../escape.txt": "pwned"}),
			wantReason: upload.ReasonPathEscapesRoot,
		},
		{
			name:   "over_entry_count",
			format: "tar",
			// spec: §13.4 line 661 ("Maximum entry count per archive:
			// 10 000.")
			data:       manyEntryTarForTest(t, upload.MaxEntryCount+1),
			wantReason: upload.ReasonMaxEntryCount,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := &fakeRuntime{}
			srv := adapter.New("adapter-test")
			srv.WorkspaceRoot = t.TempDir()
			srv.StagingDir = t.TempDir()
			srv.Runtime = rt

			blobs := blobstore.NewMemoryStore(nil)
			ref := putArchiveBlob(t, blobs, "arch-mal-"+tc.name, tc.data)

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
						{Type: "uploadArchive", UploadRef: ref, Format: tc.format},
					},
				},
			})
			if err == nil {
				t.Fatalf("Bind succeeded on a %s archive, want abort with reason %q", tc.name, tc.wantReason)
			}
			var vErr *upload.ValidationError
			if !errors.As(err, &vErr) {
				t.Fatalf("Bind error %v is not a *upload.ValidationError", err)
			}
			if vErr.Reason != tc.wantReason {
				t.Errorf("ValidationError.Reason = %q, want %q", vErr.Reason, tc.wantReason)
			}
			if gotType != string(tc.wantReason) {
				t.Errorf("ExtractionAbort error_type = %q, want %q (lenny_upload_extraction_aborted_total label)", gotType, tc.wantReason)
			}
		})
	}
}

// manyEntryTarForTest builds an in-memory tar carrying count zero-byte
// regular entries — a memory-cheap way to exceed upload.MaxEntryCount
// without allocating the 256 MiB / 64 MiB size ceilings.
func manyEntryTarForTest(t *testing.T, count int) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for i := 0; i < count; i++ {
		if err := tw.WriteHeader(&tar.Header{Name: fmt.Sprintf("f%06d.txt", i), Typeflag: tar.TypeReg, Mode: 0o644, Size: 0}); err != nil {
			t.Fatalf("tar header %d: %v", i, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	return buf.Bytes()
}
