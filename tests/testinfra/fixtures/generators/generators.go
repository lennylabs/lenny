// SPDX-License-Identifier: MIT

// Package generators ships rapid-driven fixture generators for the
// §18.2 reference document types: WorkspacePlan, TaskRecord,
// OutputPart. Each generator is a *rapid.Generator-typed factory so
// callers can compose them inside their own rapid.Check loops.
//
// The generators emit map[string]any (rather than typed structs)
// because the consumer packages don't exist yet — pkg/workspaceplan
// and pkg/task are Phase 2/Phase 5 deliverables. When those land,
// each generator gains a typed sibling that satisfies the parser's
// input shape.
package generators

import (
	"pgregory.net/rapid"
)

// WorkspacePlan returns a rapid generator that emits documents
// conformant to schemas/workspaceplan-v1.json. The breadth/depth
// knobs cap recursion so a single draw stays bounded.
func WorkspacePlan() *rapid.Generator[map[string]any] {
	return rapid.Custom(func(rt *rapid.T) map[string]any {
		sources := []map[string]any{}
		n := rapid.IntRange(0, 8).Draw(rt, "source count")
		for i := 0; i < n; i++ {
			sources = append(sources, drawSource(rt))
		}
		return map[string]any{
			"schemaVersion": 1,
			"sources":       sources,
		}
	})
}

func drawSource(rt *rapid.T) map[string]any {
	kind := rapid.SampledFrom([]string{"inlineFile", "uploadFile", "uploadArchive", "mkdir", "gitClone"}).Draw(rt, "source.type")
	switch kind {
	case "inlineFile":
		return map[string]any{
			"type":    "inlineFile",
			"path":    drawSafePath(rt),
			"content": rapid.StringMatching(`[a-zA-Z0-9 \n]{0,256}`).Draw(rt, "content"),
			"mode":    drawMode(rt),
		}
	case "uploadFile":
		return map[string]any{
			"type":     "uploadFile",
			"path":     drawSafePath(rt),
			"uploadId": rapid.StringMatching(`[a-z0-9]{8,32}`).Draw(rt, "uploadId"),
			"mode":     drawMode(rt),
		}
	case "uploadArchive":
		return map[string]any{
			"type":     "uploadArchive",
			"path":     drawSafePath(rt),
			"uploadId": rapid.StringMatching(`[a-z0-9]{8,32}`).Draw(rt, "uploadId"),
		}
	case "mkdir":
		return map[string]any{
			"type": "mkdir",
			"path": drawSafePath(rt),
			"mode": drawMode(rt),
		}
	case "gitClone":
		return map[string]any{
			"type": "gitClone",
			"path": drawSafePath(rt),
			"url":  "https://github.com/" + rapid.StringMatching(`[a-z]{3,8}/[a-z]{3,16}`).Draw(rt, "repo"),
			"ref":  rapid.StringMatching(`[0-9a-f]{7,40}|[a-zA-Z][a-zA-Z0-9_/-]{0,32}`).Draw(rt, "ref"),
		}
	}
	return map[string]any{"type": kind}
}

func drawSafePath(rt *rapid.T) string {
	return rapid.StringMatching(`(?:[a-zA-Z][a-zA-Z0-9_-]{0,16})(?:/[a-zA-Z][a-zA-Z0-9_-]{0,16}){0,4}`).Draw(rt, "path")
}

func drawMode(rt *rapid.T) string {
	return rapid.SampledFrom([]string{"0644", "0755", "0664", "0775"}).Draw(rt, "mode")
}

// TaskRecord returns a rapid generator for the §6 TaskRecord
// envelope. The fields cover the mandatory subset; consumers may
// post-process the result to add optional fields.
func TaskRecord() *rapid.Generator[map[string]any] {
	return rapid.Custom(func(rt *rapid.T) map[string]any {
		return map[string]any{
			"id":     "task-" + rapid.StringMatching(`[a-z0-9]{12,24}`).Draw(rt, "id"),
			"state":  rapid.SampledFrom([]string{"submitted", "running", "completed", "failed", "cancelled", "expired", "input_required"}).Draw(rt, "state"),
			"runtime": rapid.SampledFrom([]string{"echo", "streaming-echo", "delegation-echo", "claude-code"}).Draw(rt, "runtime"),
			"prompt": rapid.StringMatching(`[a-zA-Z0-9 ]{1,128}`).Draw(rt, "prompt"),
		}
	})
}

// OutputPart returns a rapid generator for the §7 OutputPart shape.
// Each draw is a single part; tests that need a list compose with
// rapid.SliceOf.
func OutputPart() *rapid.Generator[map[string]any] {
	return rapid.Custom(func(rt *rapid.T) map[string]any {
		kind := rapid.SampledFrom([]string{"text", "tool_use", "tool_result", "reasoning", "file"}).Draw(rt, "part.type")
		part := map[string]any{
			"type":          kind,
			"schemaVersion": rapid.IntRange(1, 3).Draw(rt, "schemaVersion"),
		}
		switch kind {
		case "text":
			part["text"] = rapid.StringMatching(`[a-zA-Z0-9 .,]{0,256}`).Draw(rt, "text")
		case "tool_use":
			part["tool"] = rapid.StringMatching(`[a-z][a-z_]{3,32}`).Draw(rt, "tool")
		case "tool_result":
			part["resultRef"] = "tr-" + rapid.StringMatching(`[a-z0-9]{12}`).Draw(rt, "resultRef")
		case "reasoning":
			part["text"] = rapid.StringMatching(`[a-zA-Z0-9 .,]{0,128}`).Draw(rt, "reasoning")
		case "file":
			part["ref"] = "blob-" + rapid.StringMatching(`[a-z0-9]{12}`).Draw(rt, "ref")
			part["mime"] = rapid.SampledFrom([]string{"text/plain", "application/json", "image/png"}).Draw(rt, "mime")
		}
		return part
	})
}
