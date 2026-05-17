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

// handler executes one tool against an already workspace-resolved path.
type handler func(fullPath, content string) Result

var toolHandlers = map[string]handler{
	ToolReadFile:   func(p, _ string) Result { return readFile(p) },
	ToolWriteFile:  func(p, c string) Result { return writeFile(p, c) },
	ToolListDir:    func(p, _ string) Result { return listDir(p) },
	ToolDeleteFile: func(p, _ string) Result { return deleteFile(p) },
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
