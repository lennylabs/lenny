// SPDX-License-Identifier: MIT

// Package localtools implements the §15 adapter-local tools — the
// read_file, write_file, list_dir, and delete_file operations the
// adapter serves to the runtime over the §15.4.1 tool_call binary
// protocol. Every operation is confined to the pod's workspace volume:
// a path that resolves outside the workspace is rejected.
package localtools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Tool names of the built-in adapter-local tools (§15).
const (
	ToolReadFile   = "read_file"
	ToolWriteFile  = "write_file"
	ToolListDir    = "list_dir"
	ToolDeleteFile = "delete_file"
)

// PathOutsideWorkspace is the §15 error content for a tool call whose
// path resolves outside the pod's workspace volume.
const PathOutsideWorkspace = "path_outside_workspace"

// Result is the outcome of an adapter-local tool call: Content becomes
// the tool_result's content[0].inline value and IsError sets the
// tool_result isError flag.
type Result struct {
	Content string
	IsError bool
}

// Descriptor advertises one adapter-local tool in the §4.7 manifest's
// adapterLocalTools array: the tool name, a human-readable description,
// and the JSON Schema for its arguments object.
type Descriptor struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// pathSchema is the §15 inputSchema for a tool that takes a single
// workspace path argument.
var pathSchema = json.RawMessage(`{"type":"object","properties":` +
	`{"path":{"type":"string","description":"Workspace-relative or absolute path within /workspace."}},` +
	`"required":["path"]}`)

// Descriptors returns the §4.7 manifest descriptors of the built-in
// adapter-local tools, in a stable order. The adapter populates the
// manifest's adapterLocalTools array from this set, and every name is
// dispatchable by Dispatch.
func Descriptors() []Descriptor {
	return []Descriptor{
		{ToolReadFile, "Read the contents of a file in the workspace.", pathSchema},
		{ToolWriteFile, "Write content to a file in the workspace.", json.RawMessage(
			`{"type":"object","properties":` +
				`{"path":{"type":"string","description":"Workspace-relative or absolute path within /workspace."},` +
				`"content":{"type":"string","description":"File content to write."}},` +
				`"required":["path","content"]}`)},
		{ToolListDir, "List the entries of a directory in the workspace.", pathSchema},
		{ToolDeleteFile, "Delete a file in the workspace.", pathSchema},
	}
}

// handler executes one tool against an already workspace-resolved path.
type handler func(fullPath, content string) Result

var toolHandlers = map[string]handler{
	ToolReadFile:   func(p, _ string) Result { return readFile(p) },
	ToolWriteFile:  func(p, c string) Result { return writeFile(p, c) },
	ToolListDir:    func(p, _ string) Result { return listDir(p) },
	ToolDeleteFile: func(p, _ string) Result { return deleteFile(p) },
}

// IsLocalTool reports whether name is a built-in adapter-local tool.
// The §15.4.1 tool_call dispatcher uses it to tell an adapter-local
// call apart from a platform MCP tool call (lenny/...).
func IsLocalTool(name string) bool {
	_, ok := toolHandlers[name]
	return ok
}

// Dispatch executes an adapter-local tool confined to workspaceRoot.
// An unknown tool name, malformed arguments, a path that resolves
// outside the workspace, or a filesystem error yields an error Result.
func Dispatch(workspaceRoot, tool string, arguments json.RawMessage) Result {
	exec, ok := toolHandlers[tool]
	if !ok {
		return errResult("unknown adapter-local tool " + tool)
	}
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(arguments, &args); err != nil {
		return errResult("malformed tool arguments")
	}
	full, ok := resolveWorkspacePath(workspaceRoot, args.Path)
	if !ok {
		return Result{Content: PathOutsideWorkspace, IsError: true}
	}
	if tool == ToolDeleteFile && full == filepath.Clean(workspaceRoot) {
		return errResult("delete_file: refusing to delete the workspace root")
	}
	return exec(full, args.Content)
}

// resolveWorkspacePath resolves a tool path against the workspace root
// and confirms the result stays within it. A relative path is joined
// onto root; an absolute path is used as given. ok is false when the
// resolved path escapes the workspace.
func resolveWorkspacePath(root, p string) (string, bool) {
	if p == "" {
		return "", false
	}
	rootClean := filepath.Clean(root)
	var full string
	if filepath.IsAbs(p) {
		full = filepath.Clean(p)
	} else {
		full = filepath.Join(rootClean, p)
	}
	if full == rootClean || strings.HasPrefix(full, rootClean+string(filepath.Separator)) {
		return full, true
	}
	return "", false
}

func readFile(full string) Result {
	b, err := os.ReadFile(full)
	if err != nil {
		return errResult(fmt.Sprintf("read_file: %v", err))
	}
	return Result{Content: string(b)}
}

func writeFile(full, content string) Result {
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		return errResult(fmt.Sprintf("write_file: %v", err))
	}
	return Result{Content: "ok"}
}

func listDir(full string) Result {
	entries, err := os.ReadDir(full)
	if err != nil {
		return errResult(fmt.Sprintf("list_dir: %v", err))
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return Result{Content: strings.Join(names, "\n")}
}

func deleteFile(full string) Result {
	if err := os.Remove(full); err != nil {
		return errResult(fmt.Sprintf("delete_file: %v", err))
	}
	return Result{Content: "ok"}
}

func errResult(msg string) Result {
	return Result{Content: msg, IsError: true}
}
