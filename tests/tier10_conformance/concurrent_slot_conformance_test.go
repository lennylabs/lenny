// SPDX-License-Identifier: MIT

//go:build conformance

// Tier-10 conformance case for the concurrent-workspace per-slot dispatch
// loop. A pool whose sessionPolicy.maxConcurrentSessions is above 1
// multiplexes simultaneous sessions onto one pod, each in its own slot,
// over a single runtime process. The §15.4.1 contract requires a runtime
// serving such a pool to implement a dispatch loop keyed on slotId: every
// inbound frame carries a slotId, the runtime derives each slot's cwd as
// /workspace/slots/{slotId}/current/, and every outbound frame echoes the
// originating slotId so the adapter routes the response back to the right
// slot. cmd/runtimes/echo-concurrent is the reference runtime that
// implements that loop.
//
// This case drives the freshly built echo-concurrent binary over its
// §15.4 stdin/stdout contract transport (the same transport the
// conformance harness and the Tier 3 contract tests use) with frames
// interleaved across two distinct slotIds plus one frame that carries no
// slotId. It asserts the three properties the per-slot model rests on:
//
//   - Per-slot response slotId tagging: each slot's response comes back
//     tagged with the slot that sent it, and each slot keeps an
//     independent sequence counter, so the adapter can demultiplex the
//     interleaved output by slotId.
//   - Per-slot cwd derivation: the runtime derives each slot's cwd as
//     /workspace/slots/{slotId}/current/ rather than assuming the global
//     /workspace/current. The reference echo runtime performs no file
//     operations, so it surfaces the derived cwd as a per-slot stderr
//     diagnostic; this case reads that diagnostic to confirm the
//     derivation.
//   - The no-slotId whole-pod path: a frame without a slotId is served on
//     the single-session whole-pod path with a response carrying no
//     slotId, so the same runtime serves a maxConcurrentSessions: 1 pod,
//     where runtimes never see slotId.
//
// spec: 5.2 (slotId multiplexing over stdin, dispatch loop keyed on
//
//	slotId, line 509), 15.4.1 (single stdin channel carrying slotId when
//	maxConcurrentSessions > 1, line 1459), 6.4 (per-slot cwd
//	/workspace/slots/{slotId}/current/, no global /workspace/current when
//	maxConcurrentSessions > 1, line 384).

package tier10_conformance_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// concurrentFrame is the subset of an outbound JSONL frame the
// concurrent-slot conformance case reads: the discriminator, the slotId
// the dispatch loop stamps, and the echoed text parts. The §15.4.1
// outbound schema carries slotId alone for concurrent multiplexing, so
// the case asserts on slotId and never on a cwd wire field; the per-slot
// cwd is an internal derivation read from the runtime's stderr diagnostic.
type concurrentFrame struct {
	Type   string `json:"type"`
	SlotID string `json:"slotId"`
	Output []struct {
		Inline string `json:"inline"`
	} `json:"output"`
}

// concurrentResult holds the decoded stdout response frames and the raw
// stderr the echo-concurrent runtime emitted while serving a batch of
// interleaved frames.
type concurrentResult struct {
	responses []concurrentFrame
	stderr    string
}

// runConcurrentRuntime execs the echo-concurrent binary over its §15.4
// stdin/stdout contract transport, feeds it the frames, and returns the
// decoded `response` frames plus the captured stderr. A trailing shutdown
// is appended so the dispatch loop drains every slot deterministically
// before the process exits.
func runConcurrentRuntime(t *testing.T, binary string, frames []string) concurrentResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	input := strings.Join(frames, "") +
		`{"type":"shutdown","reason":"session_complete","deadline_ms":1}` + "\n"

	cmd := exec.CommandContext(ctx, binary)
	cmd.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("echo-concurrent exited with error (stderr: %s): %v", stderr.String(), err)
	}

	var responses []concurrentFrame
	for _, line := range strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		var f concurrentFrame
		if err := json.Unmarshal([]byte(line), &f); err != nil {
			t.Fatalf("decode outbound frame %q: %v", line, err)
		}
		if f.Type == "response" {
			responses = append(responses, f)
		}
	}
	return concurrentResult{responses: responses, stderr: stderr.String()}
}

// concurrentMessage builds a `message` JSONL frame carrying an optional
// slotId and a single inline text part. An empty slotID omits the field,
// producing a single-session whole-pod frame.
func concurrentMessage(slotID, text string) string {
	m := map[string]any{
		"type":  "message",
		"id":    "m_" + text,
		"input": []map[string]any{{"type": "text", "inline": text}},
	}
	if slotID != "" {
		m["slotId"] = slotID
	}
	b, _ := json.Marshal(m)
	return string(b) + "\n"
}

// inlineText concatenates the inline text parts of a response frame.
func inlineText(f concurrentFrame) string {
	var b strings.Builder
	for _, p := range f.Output {
		b.WriteString(p.Inline)
	}
	return b.String()
}

// spec: 5.2 (slotId multiplexing over stdin, dispatch loop keyed on slotId,
//
//	line 509), 15.4.1 (single stdin channel carrying slotId when
//	maxConcurrentSessions > 1, line 1459), 6.4 (per-slot cwd derivation,
//	line 384).
//
// diagnosis: The reference echo-concurrent runtime no longer demultiplexes
//
//	interleaved frames for distinct slotIds over a single stdin channel:
//	either a slot's response is not tagged with its slotId, the per-slot
//	cwd is no longer derived as /workspace/slots/{slotId}/current/, or a
//	no-slotId frame no longer falls through to the whole-pod path. The
//	§15.4.1 concurrent-session dispatch loop regressed and a
//	maxConcurrentSessions > 1 pod can no longer serve a second slot over
//	one runtime process.
func TestConcurrentSlotDispatchConformance(t *testing.T) {
	a := buildArtifacts(t)

	const (
		slot01 = "slot-01"
		slot02 = "slot-02"
	)

	// Interleave two slots and a no-slotId whole-pod frame on one stdin
	// channel: slot-01 sends two messages, slot-02 one, and a final frame
	// carries no slotId. The single dispatch loop must demultiplex all
	// three streams.
	frames := []string{
		concurrentMessage(slot01, "a1"),
		concurrentMessage(slot02, "b1"),
		concurrentMessage(slot01, "a2"),
		concurrentMessage("", "solo"),
	}
	result := runConcurrentRuntime(t, a.echoConcurrent, frames)

	bySlot := map[string][]concurrentFrame{}
	for _, f := range result.responses {
		bySlot[f.SlotID] = append(bySlot[f.SlotID], f)
	}

	// Per-slot response slotId tagging: each slot's responses come back
	// tagged with that slot's slotId, and the no-slotId frame's response
	// carries no slotId (the empty-string bucket).
	if got := len(bySlot[slot01]); got != 2 {
		t.Errorf("slot-01 got %d responses, want 2: %+v", got, bySlot[slot01])
	}
	if got := len(bySlot[slot02]); got != 1 {
		t.Errorf("slot-02 got %d responses, want 1: %+v", got, bySlot[slot02])
	}

	// Independent per-slot sequence counters: slot-01's two responses are
	// seq=1 then seq=2; slot-02's single response is an independent seq=1,
	// not seq=3, proving the dispatch loop does not share a counter across
	// slots. A shared counter would let one slot observe another slot's
	// ordering, breaking the per-slot stream isolation §15.4.1 promises.
	if len(bySlot[slot01]) == 2 {
		if got := inlineText(bySlot[slot01][0]); !strings.Contains(got, "[echo seq=1]") || !strings.Contains(got, "a1") {
			t.Errorf("slot-01 first response = %q, want a seq=1 echo of a1", got)
		}
		if got := inlineText(bySlot[slot01][1]); !strings.Contains(got, "[echo seq=2]") || !strings.Contains(got, "a2") {
			t.Errorf("slot-01 second response = %q, want a seq=2 echo of a2", got)
		}
	}
	if len(bySlot[slot02]) == 1 {
		if got := inlineText(bySlot[slot02][0]); !strings.Contains(got, "[echo seq=1]") || !strings.Contains(got, "b1") {
			t.Errorf("slot-02 response = %q, want an independent seq=1 echo of b1", got)
		}
	}

	// The no-slotId frame falls through to the whole-pod path: its response
	// carries no slotId, so the same runtime serves a maxConcurrentSessions:
	// 1 pod, where runtimes never see slotId.
	whole := bySlot[""]
	if got := len(whole); got != 1 {
		t.Fatalf("no-slotId whole-pod path got %d responses, want 1: %+v", got, whole)
	}
	if whole[0].SlotID != "" {
		t.Errorf("whole-pod response slotId = %q, want empty (a single-session pod carries no slotId)", whole[0].SlotID)
	}
	if got := inlineText(whole[0]); !strings.Contains(got, "[echo seq=1]") || !strings.Contains(got, "solo") {
		t.Errorf("whole-pod response = %q, want an independent seq=1 echo of solo", got)
	}

	// Per-slot cwd derivation (§6.4 line 384): the runtime derives each
	// active slot's cwd as /workspace/slots/{slotId}/current/ rather than
	// the global /workspace/current. The echo runtime performs no file
	// operations, so it surfaces the derived cwd on stderr; the diagnostic
	// confirms the derivation runs for each slot the dispatch loop opens.
	for _, slotID := range []string{slot01, slot02} {
		wantCwd := "/workspace/slots/" + slotID + "/current/"
		if !strings.Contains(result.stderr, wantCwd) {
			t.Errorf("stderr does not record per-slot cwd %q for %q; got:\n%s", wantCwd, slotID, result.stderr)
		}
	}
	// The whole-pod default session must not derive a per-slot cwd: a
	// /workspace/slots/ path with an empty slotId would mean a no-slotId
	// frame leaked into the per-slot tree.
	if strings.Contains(result.stderr, "/workspace/slots//") {
		t.Errorf("stderr records an empty-slotId per-slot cwd; the no-slotId frame must keep the global /workspace/current path:\n%s", result.stderr)
	}
}
