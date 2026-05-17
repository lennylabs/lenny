// SPDX-License-Identifier: MIT

package workspace_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

func buildTar(t *testing.T, gzipIt bool, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	var sink = &buf
	gz := gzip.NewWriter(sink)
	var tw *tar.Writer
	if gzipIt {
		tw = tar.NewWriter(gz)
	} else {
		tw = tar.NewWriter(sink)
	}
	for name, content := range entries {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatalf("write tar header: %v", err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("write tar content: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if gzipIt {
		if err := gz.Close(); err != nil {
			t.Fatalf("close gzip: %v", err)
		}
	}
	return buf.Bytes()
}

func buildZip(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create zip entry: %v", err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("write zip content: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func materializeArchive(t *testing.T, format, prefix string, strip int, archive []byte) (string, error) {
	t.Helper()
	root := t.TempDir()
	staging := t.TempDir()
	if err := os.WriteFile(filepath.Join(staging, "arch"), archive, 0o600); err != nil {
		t.Fatalf("stage archive: %v", err)
	}
	src := &adapterv1.WorkspaceSource{
		Type:            "uploadArchive",
		Path:            prefix,
		UploadRef:       "arch",
		Format:          format,
		StripComponents: int32(strip),
	}
	return root, workspace.Materialize(root, staging, []*adapterv1.WorkspaceSource{src})
}

func TestMaterializeUploadArchiveTar(t *testing.T) {
	root, err := materializeArchive(t, "tar", "", 0, buildTar(t, false, map[string]string{
		"readme.md":   "hello",
		"src/main.go": "package main",
	}))
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "src", "main.go"))
	if err != nil {
		t.Fatalf("read extracted file: %v", err)
	}
	if string(got) != "package main" {
		t.Errorf("extracted content = %q, want %q", got, "package main")
	}
}

func TestMaterializeUploadArchiveTarGz(t *testing.T) {
	root, err := materializeArchive(t, "tar.gz", "", 0, buildTar(t, true, map[string]string{
		"notes.txt": "compressed",
	}))
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "notes.txt"))
	if err != nil {
		t.Fatalf("read extracted file: %v", err)
	}
	if string(got) != "compressed" {
		t.Errorf("extracted content = %q, want %q", got, "compressed")
	}
}

func TestMaterializeUploadArchiveZip(t *testing.T) {
	root, err := materializeArchive(t, "zip", "", 0, buildZip(t, map[string]string{
		"data/values.json": `{"k":1}`,
	}))
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "data", "values.json"))
	if err != nil {
		t.Fatalf("read extracted file: %v", err)
	}
	if string(got) != `{"k":1}` {
		t.Errorf("extracted content = %q, want %q", got, `{"k":1}`)
	}
}

func TestMaterializeUploadArchiveAppliesPathPrefix(t *testing.T) {
	root, err := materializeArchive(t, "tar", "vendor/lib", 0, buildTar(t, false, map[string]string{
		"x.txt": "y",
	}))
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "vendor", "lib", "x.txt")); err != nil {
		t.Errorf("entry was not extracted under the pathPrefix: %v", err)
	}
}

func TestMaterializeUploadArchiveStripComponents(t *testing.T) {
	root, err := materializeArchive(t, "tar", "", 1, buildTar(t, false, map[string]string{
		"project/cmd/run.go": "package main",
	}))
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "cmd", "run.go")); err != nil {
		t.Errorf("stripComponents=1 did not drop the leading segment: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "project")); err == nil {
		t.Error("the stripped leading segment should not appear in the workspace")
	}
}

func TestMaterializeUploadArchiveSkipsShallowEntries(t *testing.T) {
	// §14: an entry with no more than stripComponents segments is
	// skipped, not a fatal error.
	root, err := materializeArchive(t, "tar", "", 2, buildTar(t, false, map[string]string{
		"top.txt":          "skipped",
		"a/b/deep.txt":     "kept",
		"a/b/c/deeper.txt": "also kept",
	}))
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "deep.txt")); err != nil {
		t.Errorf("a deep entry should survive stripComponents=2: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "c", "deeper.txt")); err != nil {
		t.Errorf("a deeper entry should survive stripComponents=2: %v", err)
	}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if e.Name() == "top.txt" {
			t.Error("a shallow entry should be skipped under stripComponents=2")
		}
	}
}

func TestMaterializeUploadArchiveRejectsEscapingEntry(t *testing.T) {
	_, err := materializeArchive(t, "tar", "", 0, buildTar(t, false, map[string]string{
		"../escape.txt": "malicious",
	}))
	if err == nil {
		t.Fatal("Materialize should reject an archive entry that escapes the workspace root")
	}
}

func TestMaterializeUploadArchiveRejectsUnknownFormat(t *testing.T) {
	_, err := materializeArchive(t, "rar", "", 0, []byte("not a real archive"))
	if err == nil {
		t.Fatal("Materialize should reject an unsupported archive format")
	}
}

func TestMaterializeUploadArchiveWithoutStagingDir(t *testing.T) {
	root := t.TempDir()
	src := &adapterv1.WorkspaceSource{
		Type: "uploadArchive", UploadRef: "arch", Format: "tar",
	}
	if err := workspace.Materialize(root, "", []*adapterv1.WorkspaceSource{src}); err == nil {
		t.Fatal("Materialize should fail for an uploadArchive source with no staging directory")
	}
}
