// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

func writeWorkspaceFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func exportReq(specs ...*adapterv1.ExportSpec) *adapterv1.ExportPathsRequest {
	return &adapterv1.ExportPathsRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
		Exports:   specs,
	}
}

func filesByPath(resp *adapterv1.ExportPathsResponse) map[string]*adapterv1.ExportedFile {
	m := map[string]*adapterv1.ExportedFile{}
	for _, f := range resp.GetFiles() {
		m[f.GetPath()] = f
	}
	return m
}

// TestExportPathsRebasing_Spec87 exercises the §8.7 rebasing table:
// the glob base path is stripped and matched files are re-rooted under
// destPrefix.
func TestExportPathsRebasing_Spec87(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "exports/export1/foo.ts", "foo")
	writeWorkspaceFile(t, root, "exports/export1/lib/bar.ts", "bar")
	writeWorkspaceFile(t, root, "src/auth.ts", "auth")
	writeWorkspaceFile(t, root, "results.json", "{}")

	s := &Server{WorkspaceRoot: root}
	resp, err := s.ExportPaths(context.Background(), exportReq(
		&adapterv1.ExportSpec{Source: "./exports/export1/*"},
		&adapterv1.ExportSpec{Source: "./src/*", DestPrefix: "project/src/"},
		&adapterv1.ExportSpec{Source: "./results.json"},
	))
	if err != nil {
		t.Fatalf("ExportPaths: %v", err)
	}
	got := filesByPath(resp)
	want := map[string]string{
		"foo.ts":              "foo",
		"lib/bar.ts":          "bar",
		"project/src/auth.ts": "auth",
		"results.json":        "{}",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d files, want %d: %v", len(got), len(want), got)
	}
	for path, content := range want {
		f, ok := got[path]
		if !ok {
			t.Fatalf("missing exported path %q (have %v)", path, keys(got))
		}
		if string(f.GetContent()) != content {
			t.Errorf("path %q content = %q, want %q", path, f.GetContent(), content)
		}
		if f.GetSizeBytes() != int64(len(content)) {
			t.Errorf("path %q size = %d, want %d", path, f.GetSizeBytes(), len(content))
		}
		if f.GetSha256() == "" {
			t.Errorf("path %q missing sha256", path)
		}
	}
	if resp.GetTotalBytes() != int64(len("foo")+len("bar")+len("auth")+len("{}")) {
		t.Errorf("total_bytes = %d", resp.GetTotalBytes())
	}
}

// TestExportPathsLiteralNestedFile confirms a literal nested source
// strips its directory, leaving the file at the child root.
func TestExportPathsLiteralNestedFile(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "config/child-config.json", "cfg")
	s := &Server{WorkspaceRoot: root}
	resp, err := s.ExportPaths(context.Background(), exportReq(
		&adapterv1.ExportSpec{Source: "./config/child-config.json", DestPrefix: ""},
	))
	if err != nil {
		t.Fatalf("ExportPaths: %v", err)
	}
	got := filesByPath(resp)
	if _, ok := got["child-config.json"]; !ok {
		t.Fatalf("want child-config.json, got %v", keys(got))
	}
}

// TestExportPathsLastWriteWins covers the §8.7 overlap rule: later
// exports overwrite earlier ones on path collision.
func TestExportPathsLastWriteWins(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "a/shared.txt", "first")
	writeWorkspaceFile(t, root, "b/shared.txt", "second")
	s := &Server{WorkspaceRoot: root}
	resp, err := s.ExportPaths(context.Background(), exportReq(
		&adapterv1.ExportSpec{Source: "./a/*"},
		&adapterv1.ExportSpec{Source: "./b/*"},
	))
	if err != nil {
		t.Fatalf("ExportPaths: %v", err)
	}
	got := filesByPath(resp)
	if len(got) != 1 || string(got["shared.txt"].GetContent()) != "second" {
		t.Fatalf("last-write-wins failed: %v", got)
	}
}

// TestExportPathsRejectsSymlinkEscape covers the §8.7 validation rule:
// a matched file whose realpath leaves the workspace is rejected.
func TestExportPathsRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "exports"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "exports", "leak")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	s := &Server{WorkspaceRoot: root}
	_, err := s.ExportPaths(context.Background(), exportReq(
		&adapterv1.ExportSpec{Source: "./exports/*"},
	))
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("symlink escape err = %v, want FailedPrecondition", err)
	}
}

// TestExportPathsRejectsBadDestPrefix covers the §8.7 destPrefix
// validation: absolute or escaping prefixes are rejected.
func TestExportPathsRejectsBadDestPrefix(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "f.txt", "x")
	s := &Server{WorkspaceRoot: root}
	for _, prefix := range []string{"/abs", "../escape", ".."} {
		_, err := s.ExportPaths(context.Background(), exportReq(
			&adapterv1.ExportSpec{Source: "./f.txt", DestPrefix: prefix},
		))
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("destPrefix %q err = %v, want InvalidArgument", prefix, err)
		}
	}
}

// TestExportPathsRejectsNonRegularFile covers the §8.7/§13.4 rule that
// only regular files cross the export boundary.
func TestExportPathsRejectsNonRegularFile(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "d"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	fifo := filepath.Join(root, "d", "pipe")
	if err := makeFIFO(fifo); err != nil {
		t.Skipf("mkfifo unsupported: %v", err)
	}
	s := &Server{WorkspaceRoot: root}
	_, err := s.ExportPaths(context.Background(), exportReq(
		&adapterv1.ExportSpec{Source: "./d/*"},
	))
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("fifo export err = %v, want FailedPrecondition", err)
	}
}

func TestExportPathsRequiresSessionAndRoot(t *testing.T) {
	s := &Server{WorkspaceRoot: t.TempDir()}
	if _, err := s.ExportPaths(context.Background(),
		&adapterv1.ExportPathsRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("missing session err = %v, want InvalidArgument", err)
	}
	noRoot := &Server{}
	if _, err := noRoot.ExportPaths(context.Background(),
		exportReq()); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("missing root err = %v, want FailedPrecondition", err)
	}
}

func TestExportBase(t *testing.T) {
	cases := map[string]string{
		"./exports/export1/*":        "exports/export1",
		"./src/*":                    "src",
		"./results.json":             ".",
		"./config/child-config.json": "config",
		"./a/**/b/*":                 "a",
	}
	for source, want := range cases {
		if got := exportBase(source); got != want {
			t.Errorf("exportBase(%q) = %q, want %q", source, got, want)
		}
	}
}

func keys(m map[string]*adapterv1.ExportedFile) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
