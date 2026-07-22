// SPDX-License-Identifier: MIT

// Tier-11 doc/spec-consistency check for the §4.6.1 agent-pod
// disruption-protection clarification: how an evicted agent pod's preStop
// reaches the gateway replica that can drive its eviction checkpoint. §4.6.1
// states the preStop cannot open the gateway-driven `Checkpoint` stream
// itself, so it signals its coordinating gateway replica over the per-pod
// adapter-to-gateway control channel (the same transport that carries the
// §4.7 `AdapterTerminating` event); the coordinating replica is the
// session-coordination-lease holder and drives the existing `Checkpoint` RPC
// with the `TriggerEviction` trigger under its held lease; an unreachable
// coordinator is recovered through the §10.1 TTL-driven coordinator handoff,
// and no unfenced replica ever drives the checkpoint.
//
// The clarification names two surfaces a reader must be able to trace: the
// control-channel event (the §4.7 `AdapterTerminating` transport, which
// pkg/adapter/controlchannel.go emits) and the checkpoint trigger (the
// existing `CheckpointWithTrigger` path in
// pkg/gateway/checkpoint/checkpointer/checkpointer.go, which references
// `checkpoint.TriggerEviction`). This test pins that the §4.6.1 sentence
// resolves to those real current surfaces and that its inline anchor links
// (the §4.7 self-link and the §10.1 cross-file link) point at live headings.
// The non-happy path it guards is a §4.6.1 sentence referencing a
// control-channel event or a checkpoint trigger the spec and code do not
// define, which a reader cannot trace.
//
// The test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 4.6.1 (agent-pod disruption protection), 4.7 (adapter-to-gateway
// control channel), 4.4 (eviction checkpoint).

package tier11_docs_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// spec: 4.6.1, 4.7, 4.4
// diagnosis: The §4.6.1 disruption-protection clarification names a
//
//	control-channel event or a checkpoint trigger that the spec and code do
//	not define, so a reader cannot trace the coordinator-direct eviction
//	route. §4.6.1 states the agent pod's preStop signals its coordinating
//	gateway replica over the per-pod control channel (the §4.7
//	`AdapterTerminating` transport), and the coordinating lease holder drives
//	the existing `Checkpoint` RPC with the `TriggerEviction` trigger under its
//	held lease. A failure here means the §4.6.1 sentence drifted from those
//	surfaces: it dropped the coordinator-direct route, named a control-channel
//	event the §4.7 table and pkg/adapter/controlchannel.go do not carry, named
//	a checkpoint trigger pkg/gateway/checkpoint/checkpointer does not drive, or
//	dropped the fail-closed rule that no unfenced replica drives the
//	checkpoint.
func TestEvictionCoordinatorRouteResolvesToSurfaces(t *testing.T) {
	root := repoRoot(t)
	specDir := filepath.Join(root, "spec")

	// §4.6.1 disruption-protection paragraph: the whole clarification is a
	// single logical markdown line. Scope to it so a match elsewhere in the
	// large §4.6.1 section does not mask a drift in the sentence itself.
	s461 := specSection(t, filepath.Join(specDir, "04_system-components.md"), "#### 4.6.1 ")
	disruptionPara := requireLine(t, s461, "Disruption protection for agent pods")
	requireAllContain(t, "§4.6.1 disruption-protection paragraph", disruptionPara, []string{
		// The coordinator-direct route: the preStop cannot open the stream and
		// signals its coordinating replica over the control channel.
		"cannot open the gateway-driven `Checkpoint` stream itself",
		"signals its coordinating gateway replica over the per-pod adapter-to-gateway control channel",
		// The control-channel transport it reuses is the §4.7 AdapterTerminating one.
		"the same channel that carries `AdapterTerminating`",
		"[§4.7](#47-runtime-adapter)",
		// The coordinating replica is the session-coordination-lease holder.
		"The coordinating replica is the replica holding the session-coordination lease",
		"[§10.1](10_gateway-internals.md#101-horizontal-scaling)",
		// It drives the existing Checkpoint RPC with the TriggerEviction trigger.
		"drives the eviction checkpoint through the existing `Checkpoint` RPC with the `TriggerEviction` trigger under its held lease",
		// An unreachable coordinator is recovered via the §10.1 TTL-driven handoff.
		"recovered through the [§10.1](10_gateway-internals.md#101-horizontal-scaling) TTL-driven coordinator handoff once its coordination lease lapses",
		// Fail closed: no unfenced replica ever drives the checkpoint.
		"no unfenced replica ever drives the checkpoint",
	})

	// The control-channel event the §4.6.1 sentence names is the §4.7
	// `AdapterTerminating` transport. §4.7 must declare it as the adapter's
	// self-initiated terminal notification.
	s47 := specSection(t, filepath.Join(specDir, "04_system-components.md"), "### 4.7 ")
	terminatingRow := requireLine(t, s47, "`AdapterTerminating`")
	requireAllContain(t, "§4.7 AdapterTerminating row", terminatingRow, []string{
		"Adapter's self-initiated terminal notification",
	})

	// pkg/adapter/controlchannel.go emits that event. Both the exported
	// entry point and the event-type constant must be present, so the §4.6.1
	// sentence resolves to a real code surface rather than a spec-only name.
	controlChannel := readDocPage(t, filepath.Join(root, "pkg", "adapter", "controlchannel.go"))
	requireAllContain(t, "pkg/adapter/controlchannel.go", controlChannel, []string{
		"func (s *Server) EmitAdapterTerminating(",
		`eventAdapterTerminating = "AdapterTerminating"`,
	})

	// The checkpoint trigger the §4.6.1 sentence names is the existing
	// CheckpointWithTrigger path, which drives checkpoint.TriggerEviction.
	checkpointer := readDocPage(t, filepath.Join(root, "pkg", "gateway", "checkpoint", "checkpointer", "checkpointer.go"))
	requireAllContain(t, "pkg/gateway/checkpoint/checkpointer/checkpointer.go", checkpointer, []string{
		"func (c *Checkpointer) CheckpointWithTrigger(",
		"checkpoint.TriggerEviction",
	})
}

// spec: 4.6.1, 4.7
// diagnosis: An inline anchor link in the §4.6.1 disruption-protection
//
//	clarification points at a heading that no longer exists. The sentence
//	carries a §4.7 self-link (`#47-runtime-adapter`) to the runtime-adapter
//	section that owns the control channel, and a §10.1 cross-file link
//	(`10_gateway-internals.md#101-horizontal-scaling`) to the coordinator-lease
//	and TTL-handoff section. A reader following a published link that no longer
//	resolves gets a 404 to the section anchor. Re-point the link or restore the
//	heading.
func TestEvictionCoordinatorRouteCrossRefsResolve(t *testing.T) {
	root := repoRoot(t)
	specDir := filepath.Join(root, "spec")

	// The §4.6.1 sentence links to §4.7 (same file) and §10.1 (cross file).
	// Both anchors must resolve to a live heading in the target file.
	refs := []struct {
		targetFile string
		anchor     string
		label      string
	}{
		{"04_system-components.md", "47-runtime-adapter", "§4.6.1 → §4.7 self-link"},
		{"10_gateway-internals.md", "101-horizontal-scaling", "§4.6.1 → §10.1 cross-file link"},
	}
	for _, ref := range refs {
		slugs, err := headingSlugs(filepath.Join(specDir, ref.targetFile))
		if err != nil {
			t.Fatalf("read heading slugs from %s: %v", ref.targetFile, err)
		}
		if !slugs[ref.anchor] {
			t.Errorf("%s (#%s) does not resolve to any heading in spec/%s", ref.label, ref.anchor, ref.targetFile)
		}
	}

	// Confirm the link syntax is actually present in the §4.6.1 paragraph, so
	// the cross-reference exists rather than merely the anchor being resolvable.
	s461 := specSection(t, filepath.Join(specDir, "04_system-components.md"), "#### 4.6.1 ")
	disruptionPara := requireLine(t, s461, "Disruption protection for agent pods")
	if !strings.Contains(disruptionPara, "[§4.7](#47-runtime-adapter)") {
		t.Error("§4.6.1 disruption-protection paragraph does not self-link to §4.7; the control-channel transport must cross-reference the runtime-adapter section")
	}
	if !strings.Contains(disruptionPara, "[§10.1](10_gateway-internals.md#101-horizontal-scaling)") {
		t.Error("§4.6.1 disruption-protection paragraph does not link to §10.1; the coordinating-lease holder and TTL-driven handoff must cross-reference the horizontal-scaling section")
	}
}
