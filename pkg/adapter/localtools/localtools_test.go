// SPDX-License-Identifier: MIT

package localtools_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter/localtools"
)

func args(t *testing.T, m map[string]string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return b
}

func TestDispatchReadFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := localtools.Dispatch(root, localtools.ToolReadFile, args(t, map[string]string{"path": "notes.txt"}))
	if got.IsError {
		t.Fatalf("read_file errored: %s", got.Content)
	}
	if got.Content != "hello" {
		t.Errorf("read_file content = %q, want hello", got.Content)
	}
}

func TestDispatchWriteFile(t *testing.T) {
	root := t.TempDir()
	got := localtools.Dispatch(root, localtools.ToolWriteFile,
		args(t, map[string]string{"path": "out.txt", "content": "written"}))
	if got.IsError {
		t.Fatalf("write_file errored: %s", got.Content)
	}
	b, err := os.ReadFile(filepath.Join(root, "out.txt"))
	if err != nil || string(b) != "written" {
		t.Errorf("write_file wrote %q (err %v), want written", b, err)
	}
}

func TestDispatchListDir(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := localtools.Dispatch(root, localtools.ToolListDir, args(t, map[string]string{"path": "."}))
	if got.IsError {
		t.Fatalf("list_dir errored: %s", got.Content)
	}
	if got.Content != "a.txt\nsub/" {
		t.Errorf("list_dir content = %q, want \"a.txt\\nsub/\"", got.Content)
	}
}

func TestDispatchDeleteFile(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "gone.txt")
	if err := os.WriteFile(target, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got := localtools.Dispatch(root, localtools.ToolDeleteFile, args(t, map[string]string{"path": "gone.txt"}))
	if got.IsError {
		t.Fatalf("delete_file errored: %s", got.Content)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Error("delete_file did not remove the file")
	}
}

func TestDispatchRejectsEscapingPath(t *testing.T) {
	root := t.TempDir()
	for _, tool := range []string{
		localtools.ToolReadFile, localtools.ToolWriteFile,
		localtools.ToolListDir, localtools.ToolDeleteFile,
	} {
		got := localtools.Dispatch(root, tool, args(t, map[string]string{"path": "../escape.txt"}))
		if !got.IsError || got.Content != localtools.PathOutsideWorkspace {
			t.Errorf("%s with an escaping path = %+v, want a %q error", tool, got, localtools.PathOutsideWorkspace)
		}
	}
}

func TestDispatchRejectsAbsolutePathOutside(t *testing.T) {
	root := t.TempDir()
	got := localtools.Dispatch(root, localtools.ToolReadFile, args(t, map[string]string{"path": "/etc/passwd"}))
	if !got.IsError || got.Content != localtools.PathOutsideWorkspace {
		t.Errorf("read_file of an absolute outside path = %+v, want a %q error", got, localtools.PathOutsideWorkspace)
	}
}

func TestDispatchAcceptsAbsolutePathInside(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, "inside.txt")
	if err := os.WriteFile(abs, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := localtools.Dispatch(root, localtools.ToolReadFile, args(t, map[string]string{"path": abs}))
	if got.IsError || got.Content != "ok" {
		t.Errorf("read_file of an absolute in-workspace path = %+v, want ok", got)
	}
}

func TestDispatchRejectsDeletingWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	got := localtools.Dispatch(root, localtools.ToolDeleteFile, args(t, map[string]string{"path": "."}))
	if !got.IsError {
		t.Error("delete_file of the workspace root succeeded, want a rejection")
	}
	if _, err := os.Stat(root); err != nil {
		t.Errorf("the workspace root was removed: %v", err)
	}
}

func TestDispatchUnknownTool(t *testing.T) {
	got := localtools.Dispatch(t.TempDir(), "exec_shell", args(t, map[string]string{"path": "x"}))
	if !got.IsError {
		t.Error("an unknown tool did not yield an error result")
	}
}

func TestDispatchMalformedArguments(t *testing.T) {
	got := localtools.Dispatch(t.TempDir(), localtools.ToolReadFile, json.RawMessage(`not json`))
	if !got.IsError {
		t.Error("malformed arguments did not yield an error result")
	}
}

func TestDispatchReadMissingFile(t *testing.T) {
	got := localtools.Dispatch(t.TempDir(), localtools.ToolReadFile, args(t, map[string]string{"path": "absent.txt"}))
	if !got.IsError {
		t.Error("read_file of a missing file did not yield an error result")
	}
}
