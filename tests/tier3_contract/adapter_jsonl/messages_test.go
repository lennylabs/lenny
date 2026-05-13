//go:build contract

// Package adapter_jsonl_test is the Tier 3 contract suite for the
// adapter ↔ agent binary protocol. Phase 1 ships the schema; Phase 2
// implements the adapter binary that reads and writes JSONL conforming
// to it. The tests below stub out the round-trip checks Phase 2 must
// satisfy and fail with diagnosis strings until the adapter exists.
package adapter_jsonl_test

import (
	"testing"
)

// spec: 15.4
// diagnosis: cmd/lenny-adapter/ does not yet exist. Phase 2 ships the
//
//	adapter sidecar that reads JSONL on stdin and writes JSONL
//	on stdout. This test will exec the adapter, send each
//	canonical message kind from schemas/examples/jsonl.*.json
//	as a line on stdin, and assert the adapter dispatches it
//	without protocol error.
func TestAdapterAcceptsCanonicalMessages(t *testing.T) {
	t.Fatalf("not implemented in Phase 1: cmd/lenny-adapter/ is a Phase 2 deliverable per TESTING.md §13.3")
}

// spec: 15.4
// diagnosis: The adapter must respond to a `heartbeat` JSONL line with a
//
//	`heartbeat_ack` JSONL line within 10s. Phase 2 ships the
//	heartbeat handler.
func TestAdapterHeartbeatAckWithin10s(t *testing.T) {
	t.Fatalf("not implemented in Phase 1: heartbeat handling ships in Phase 2 per spec §15.4")
}

// spec: 15.4
// diagnosis: The adapter must accept inbound `tool_result` envelopes
//
//	whose `id` matches a previously emitted `tool_call.id`,
//	and must drop results with no matching call (logging a
//	protocol error). Phase 2 implements the tool-call/result
//	correlation table.
func TestAdapterToolResultCorrelation(t *testing.T) {
	t.Fatalf("not implemented in Phase 1: tool-call correlation ships in Phase 2 per spec §15.4")
}

// spec: 15.4
// diagnosis: The adapter must normalise the shorthand
//
//	`{"type":"response","text":"..."}` to the canonical
//	`{"type":"response","output":[{"type":"text","inline":"..."}]}`.
//	Phase 2 ships the response normaliser.
func TestAdapterResponseShorthandNormalised(t *testing.T) {
	t.Fatalf("not implemented in Phase 1: response shorthand normalisation ships in Phase 2 per spec §15.4")
}

// spec: 15.4
// diagnosis: Unknown JSONL message types must be ignored with a logged
//
//	warning (forward compatibility). The adapter must not crash
//	or error out on an unrecognised `type`. Phase 2 ships the
//	forward-compat dispatcher.
func TestAdapterIgnoresUnknownMessageTypes(t *testing.T) {
	t.Fatalf("not implemented in Phase 1: unknown-type pass-through ships in Phase 2 per spec §15.4")
}

// spec: 15.4
// diagnosis: Adapter-local tool calls (read_file, write_file, list_dir,
//
//	delete_file) must reject paths that resolve outside
//	/workspace. The error returns to the agent as
//	`tool_result` with `isError: true` and an OutputPart
//	containing `path_outside_workspace`. Phase 2 ships the
//	path-confinement check.
func TestAdapterLocalToolsRejectPathTraversal(t *testing.T) {
	t.Fatalf("not implemented in Phase 1: adapter-local tool guard ships in Phase 2 per spec §15.4")
}
