// SPDX-License-Identifier: MIT

package adapter_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter"
)

func toolCallFrame(t *testing.T, id, name string, args map[string]string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"type": "tool_call", "id": id, "name": name, "arguments": args,
	})
	if err != nil {
		t.Fatalf("marshal tool_call: %v", err)
	}
	return b
}

type toolResult struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Content []struct {
		Type   string `json:"type"`
		Inline string `json:"inline"`
	} `json:"content"`
	IsError bool `json:"isError"`
}

func decodeToolResult(t *testing.T, frame []byte) toolResult {
	t.Helper()
	var tr toolResult
	if err := json.Unmarshal(frame, &tr); err != nil {
		t.Fatalf("decode tool_result: %v", err)
	}
	return tr
}

func TestHandleToolCallReadFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("contents"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, handled := adapter.HandleToolCall(
		toolCallFrame(t, "tc_read1", "read_file", map[string]string{"path": "f.txt"}), root)
	if !handled {
		t.Fatal("read_file tool_call was not handled")
	}
	tr := decodeToolResult(t, result)
	if tr.Type != "tool_result" || tr.ID != "tc_read1" {
		t.Errorf("tool_result type/id = %q/%q, want tool_result/tc_read1", tr.Type, tr.ID)
	}
	if tr.IsError {
		t.Error("read_file of an existing file reported isError")
	}
	if len(tr.Content) != 1 || tr.Content[0].Inline != "contents" {
		t.Errorf("tool_result content = %+v, want one inline 'contents' part", tr.Content)
	}
}

func TestHandleToolCallWriteFile(t *testing.T) {
	root := t.TempDir()
	frame := toolCallFrame(t, "tc_write1", "write_file",
		map[string]string{"path": "out.txt", "content": "saved"})
	result, handled := adapter.HandleToolCall(frame, root)
	if !handled {
		t.Fatal("write_file tool_call was not handled")
	}
	if decodeToolResult(t, result).IsError {
		t.Error("write_file reported isError")
	}
	if b, err := os.ReadFile(filepath.Join(root, "out.txt")); err != nil || string(b) != "saved" {
		t.Errorf("write_file wrote %q (err %v), want saved", b, err)
	}
}

func TestHandleToolCallErrorResult(t *testing.T) {
	// read_file of a missing file is still an adapter-local tool: it is
	// handled, and the tool_result carries isError.
	result, handled := adapter.HandleToolCall(
		toolCallFrame(t, "tc_miss", "read_file", map[string]string{"path": "absent"}), t.TempDir())
	if !handled {
		t.Fatal("read_file of a missing file was not handled")
	}
	if !decodeToolResult(t, result).IsError {
		t.Error("read_file of a missing file did not set isError")
	}
}

func TestHandleToolCallPlatformToolNotHandled(t *testing.T) {
	_, handled := adapter.HandleToolCall(
		toolCallFrame(t, "tc_deleg", "lenny/delegate_task", map[string]string{}), t.TempDir())
	if handled {
		t.Error("a platform MCP tool_call was handled as an adapter-local tool")
	}
}

func TestHandleToolCallNonToolCallFrame(t *testing.T) {
	frame := []byte(`{"type":"response","text":"hello"}`)
	if _, handled := adapter.HandleToolCall(frame, t.TempDir()); handled {
		t.Error("a non-tool_call frame was handled")
	}
}

func TestHandleToolCallMalformedFrame(t *testing.T) {
	if _, handled := adapter.HandleToolCall([]byte("not json"), t.TempDir()); handled {
		t.Error("a malformed frame was handled")
	}
}
