// SPDX-License-Identifier: MIT

package mcptools

import (
	"context"
	"strings"

	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/task"
)

// delegationTargetKind classifies a resolved §8.2 delegation target. The
// kind is server-internal: the opaque `target` wire field does not reveal
// it to the calling runtime, so a runtime cannot tell whether the target
// is a standalone runtime, a derived runtime, or an external registered
// agent.
//
// spec: §8.2 line 23 — "Target id is opaque — the runtime does not know
// whether the target is a standalone runtime, derived runtime, or
// external registered agent."
type delegationTargetKind string

const (
	targetKindStandalone delegationTargetKind = "standalone_runtime"
	targetKindDerived    delegationTargetKind = "derived_runtime"
	targetKindMCP        delegationTargetKind = "mcp"
)

// resolveDelegationTarget resolves an opaque §8.2 `target` id into the
// concrete runtime reference the gateway delegates to, and classifies the
// resolution kind for the server-internal `type: mcp` gate and audit. The
// runtime never supplies (and never learns) the kind: it hands the
// gateway an opaque id and the gateway resolves it through the registry.
//
// The returned reference is the canonical runtime name when the target
// resolves in the registry, and the raw target otherwise, so the §10.6
// environment-scope gate (runtimeAuthorizedForCaller / crossEnvReachable,
// which is the parentSessionId-aware half of the indirection) still
// rejects an unresolvable target with TARGET_NOT_IN_SCOPE rather than
// this resolver short-circuiting it. When no registry is wired (the dev
// path), the opaque id passes through unchanged.
//
// spec: §8.2 lines 12-23 (opaque target signature); §8.2 line 50
// (type: mcp targets rejected with target_not_an_agent).
func resolveDelegationTarget(ctx context.Context, deps Deps, target string) (ref string, kind delegationTargetKind) {
	if deps.Runtimes == nil || target == "" {
		return target, targetKindStandalone
	}
	// The raw registry entry carries the derived marker. runtimestore.Resolve
	// merges a derived runtime against its base and clears BaseRuntime, so the
	// derived classification is only visible pre-merge.
	raw, err := deps.Runtimes.Get(ctx, target)
	if err != nil {
		// Unresolved: leave the raw target so the §10.6 scope gate rejects it.
		return target, targetKindStandalone
	}
	// The resolved (merged) runtime carries the effective type a derived
	// runtime inherits from its base, so the type:mcp gate reads it.
	resolved, err := runtimestore.Resolve(ctx, deps.Runtimes, target)
	if err != nil {
		// A derived runtime whose base is missing is unresolvable: leave the
		// raw target so the scope gate rejects it rather than admitting it.
		return target, targetKindStandalone
	}
	switch {
	case resolved.Type == runtimestore.TypeMCP:
		return resolved.Name, targetKindMCP
	case raw.IsDerived():
		return resolved.Name, targetKindDerived
	default:
		return resolved.Name, targetKindStandalone
	}
}

// flattenTaskInput projects a §8.2 `task.input` OutputPart[] into the
// plain-text content the §4 interceptor chain inspects and the gateway
// delivers to the child as its first message. v1 producers emit `text`
// parts; the inline text of every text part (including nested parts) is
// concatenated in order. Non-text parts (ref-only attachments, images)
// contribute no text and are skipped, so a delegation that carries only
// non-text parts flattens to the empty string and the child receives no
// initial message — the same behaviour as an omitted input.
//
// spec: §8.2 lines 25-28 (TaskSpec.input is OutputPart[]); §15.4.1
// lines 1483-1540 (OutputPart envelope, v1 `text` parts).
func flattenTaskInput(parts []task.OutputPart) string {
	var b strings.Builder
	for _, p := range parts {
		appendPartText(&b, p)
	}
	return b.String()
}

func appendPartText(b *strings.Builder, p task.OutputPart) {
	if p.Type == "text" && p.Inline != "" {
		b.WriteString(p.Inline)
	}
	for _, sub := range p.Parts {
		appendPartText(b, sub)
	}
}
