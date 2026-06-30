// SPDX-License-Identifier: MIT

package mcptools

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/sessionrecord"
)

// spec: §8.2 lines 12-28 — the opaque `target` resolver and the
// `task.input` MessagePart[] projection. F-8.2.1.

func TestFlattenTaskInput_spec_8_2(t *testing.T) {
	cases := []struct {
		name  string
		parts []sessionrecord.MessagePart
		want  string
	}{
		{"nil", nil, ""},
		{"single text", []sessionrecord.MessagePart{{Type: "text", Inline: "do work"}}, "do work"},
		{
			"multiple text concatenated in order",
			[]sessionrecord.MessagePart{
				{Type: "text", Inline: "part one "},
				{Type: "text", Inline: "part two"},
			},
			"part one part two",
		},
		{
			"non-text parts skipped",
			[]sessionrecord.MessagePart{
				{Type: "text", Inline: "keep"},
				{Type: "image", Ref: "blob://x"},
				{Type: "file", Ref: "blob://y"},
			},
			"keep",
		},
		{
			"nested parts flattened",
			[]sessionrecord.MessagePart{
				{Type: "text", Inline: "a"},
				{Type: "group", Parts: []sessionrecord.MessagePart{
					{Type: "text", Inline: "b"},
					{Type: "text", Inline: "c"},
				}},
			},
			"abc",
		},
		{"empty inline text contributes nothing", []sessionrecord.MessagePart{{Type: "text", Inline: ""}}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := flattenTaskInput(tc.parts); got != tc.want {
				t.Errorf("flattenTaskInput = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveDelegationTargetClassifiesKind_spec_8_2_23(t *testing.T) {
	ctx := context.Background()
	rt := runtimestore.NewMemory()
	mustCreate(t, rt, runtimestore.Runtime{Name: "claude", Type: runtimestore.TypeAgent, Image: "lenny/claude@sha256:a", IntegrationLevel: "basic"})
	mustCreate(t, rt, runtimestore.Runtime{Name: "claude-fast", BaseRuntime: "claude"})
	mustCreate(t, rt, runtimestore.Runtime{Name: "fs-mcp", Type: runtimestore.TypeMCP, Image: "lenny/fs-mcp@sha256:b"})
	deps := Deps{Runtimes: rt}

	cases := []struct {
		name     string
		target   string
		wantRef  string
		wantKind delegationTargetKind
	}{
		{"standalone runtime", "claude", "claude", targetKindStandalone},
		{"derived runtime classified pre-merge", "claude-fast", "claude-fast", targetKindDerived},
		{"mcp runtime", "fs-mcp", "fs-mcp", targetKindMCP},
		// An unresolved target passes through unchanged so the §10.6 scope
		// gate (not this resolver) rejects it with TARGET_NOT_IN_SCOPE.
		{"unresolved passes through", "ghost", "ghost", targetKindStandalone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref, kind := resolveDelegationTarget(ctx, deps, tc.target)
			if ref != tc.wantRef {
				t.Errorf("ref = %q, want %q", ref, tc.wantRef)
			}
			if kind != tc.wantKind {
				t.Errorf("kind = %q, want %q", kind, tc.wantKind)
			}
		})
	}
}

func TestResolveDelegationTargetNoRegistryPassesThrough_spec_8_2(t *testing.T) {
	ref, kind := resolveDelegationTarget(context.Background(), Deps{}, "anything")
	if ref != "anything" || kind != targetKindStandalone {
		t.Errorf("resolveDelegationTarget(no registry) = (%q, %q), want (\"anything\", standalone)", ref, kind)
	}
}

func mustCreate(t *testing.T, s runtimestore.Store, r runtimestore.Runtime) {
	t.Helper()
	if err := s.Create(context.Background(), r); err != nil {
		t.Fatalf("seed runtime %q: %v", r.Name, err)
	}
}
