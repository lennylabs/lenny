// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// spec: §28.5.3 — frameSessionID reads the demultiplexing key
// off a runtime output frame. A frame with no sessionId, or one that does
// not parse as a JSON object, yields the empty string, which is the
// address demuxSessionOutput resolves against the pod's slot count.
func TestFrameSessionID(t *testing.T) {
	cases := []struct {
		name  string
		frame string
		want  string
	}{
		{"tagged", `{"type":"response","sessionId":"sess-a"}`, "sess-a"},
		{"absent", `{"type":"heartbeat_ack"}`, ""},
		{"empty value", `{"type":"response","sessionId":""}`, ""},
		{"not an object", `not json`, ""},
		{"empty", ``, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := frameSessionID([]byte(tc.frame)); got != tc.want {
				t.Errorf("frameSessionID(%q) = %q, want %q", tc.frame, got, tc.want)
			}
		})
	}
}

// spec: §6.4; §28.5.3 — stampSessionID injects sessionId onto an inbound
// envelope so the shared runtime routes it to the session's cwd. A
// non-object frame is forwarded unchanged so a non-envelope frame is not
// dropped.
func TestStampSessionID(t *testing.T) {
	got, err := stampSessionID([]byte(`{"type":"message","id":"m1"}`), "sess-a")
	if err != nil {
		t.Fatalf("stampSessionID: %v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(got, &obj); err != nil {
		t.Fatalf("decode stamped frame: %v", err)
	}
	if obj["sessionId"] != "sess-a" {
		t.Errorf("stamped sessionId = %v, want sess-a", obj["sessionId"])
	}
	if obj["type"] != "message" || obj["id"] != "m1" {
		t.Errorf("stampSessionID dropped fields: %v", obj)
	}

	// A non-object frame passes through unchanged.
	raw := []byte(`not-json`)
	through, err := stampSessionID(raw, "sess-a")
	if err != nil {
		t.Fatalf("stampSessionID(non-object): %v", err)
	}
	if string(through) != "not-json" {
		t.Errorf("non-object frame = %q, want it unchanged", through)
	}
}

// spec: §6.4; §28.5.3 — a malformed JSON object fails closed:
// stampSessionID returns an error rather than writing an envelope with no
// address, which would misroute it on a pod holding more than one slot.
func TestStampSessionIDFailsClosedOnMalformedObject(t *testing.T) {
	if _, err := stampSessionID([]byte(`{"type":"message"`), "sess-a"); err == nil {
		t.Error("stampSessionID must fail on a malformed JSON object rather than drop the sessionId")
	}
}

// fixedSlotCount is the pod slot count the demultiplexer reads, held
// constant for a case that does not change the pod's population.
func fixedSlotCount(n int) func() int {
	return func() int { return n }
}

// drainDemux collects every frame the demultiplexer delivers until its
// input closes, so a case can assert what it relayed and what it withheld.
func drainDemux(t *testing.T, out <-chan []byte) []string {
	t.Helper()
	var got []string
	for {
		select {
		case line, ok := <-out:
			if !ok {
				return got
			}
			got = append(got, string(line))
		case <-time.After(time.Second):
			t.Fatalf("demux did not close after its input closed; delivered %q", got)
		}
	}
}

// spec: §28.5.3 — the type-scoped resolve-or-reject rule:
// demuxSessionOutput narrows by frame type. A protocol frame passes
// through on every pod, a session-scoped frame carrying this session's
// address is delivered, one carrying a co-tenant's is withheld, and one
// carrying no address resolves to the receiving stream's own binding on a
// pod holding at most one slot and is rejected on a pod holding more.
func TestDemuxSessionOutput(t *testing.T) {
	const (
		ownFrame       = `{"type":"response","sessionId":"sess-a"}`
		siblingFrame   = `{"type":"response","sessionId":"sess-b"}`
		unaddressed    = `{"type":"response","n":1}`
		heartbeat      = `{"type":"heartbeat"}`
		heartbeatAck   = `{"type":"heartbeat_ack"}`
		unaddressedSTC = `{"type":"set_tracing_context","context":{}}`
	)
	cases := []struct {
		name      string
		slots     int
		in        []string
		want      []string
		wantRejec float64
	}{
		{
			name:  "an unaddressed session-scoped frame resolves on a pod holding one slot",
			slots: 1,
			in:    []string{ownFrame, unaddressed, siblingFrame, heartbeat, heartbeatAck},
			want:  []string{ownFrame, unaddressed, heartbeat, heartbeatAck},
		},
		{
			// releaseSlot deletes the registry entry while the ending
			// session's stream still drains the runtime's output, so a
			// count of zero falls in the resolving arm.
			name:  "an unaddressed session-scoped frame resolves on a pod holding no slot",
			slots: 0,
			in:    []string{unaddressed},
			want:  []string{unaddressed},
		},
		{
			name:      "an unaddressed session-scoped frame is rejected on a pod holding two slots",
			slots:     2,
			in:        []string{ownFrame, unaddressed, siblingFrame, heartbeat, heartbeatAck},
			want:      []string{ownFrame, heartbeat, heartbeatAck},
			wantRejec: 1,
		},
		{
			name:      "an unaddressed set_tracing_context frame is rejected on a pod holding two slots",
			slots:     2,
			in:        []string{unaddressedSTC},
			want:      nil,
			wantRejec: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := testutil.ToFloat64(unaddressedFrameRejected.WithLabelValues("response")) +
				testutil.ToFloat64(unaddressedFrameRejected.WithLabelValues("set_tracing_context"))
			in := make(chan []byte, len(tc.in))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			out := demuxSessionOutput(ctx, in, "sess-a", fixedSlotCount(tc.slots))
			for _, line := range tc.in {
				in <- []byte(line)
			}
			close(in)
			got := drainDemux(t, out)
			if len(got) != len(tc.want) {
				t.Fatalf("demux delivered %q, want %q", got, tc.want)
			}
			for i, w := range tc.want {
				if got[i] != w {
					t.Errorf("demux delivered [%d] = %q, want %q", i, got[i], w)
				}
			}
			after := testutil.ToFloat64(unaddressedFrameRejected.WithLabelValues("response")) +
				testutil.ToFloat64(unaddressedFrameRejected.WithLabelValues("set_tracing_context"))
			if after-before != tc.wantRejec {
				t.Errorf("unaddressed-frame counter moved by %v, want %v", after-before, tc.wantRejec)
			}
		})
	}
}

// spec: §28.5.3 (the slot count is every registry entry, bound or
// registered-but-unbound), §28.5.3 — a pod holding one bound session and
// one registered-but-unbound entry counts two slots, so an unaddressed
// frame is rejected rather than resolved to the incumbent while the
// second session's workspace is being prepared.
func TestDemuxSessionOutputCountsAnUnboundSlot_spec_28_5_3(t *testing.T) {
	s := New("test")
	if err := s.claimSessionForTest("sess-a"); err != nil {
		t.Fatalf("claim sess-a: %v", err)
	}
	if err := s.RegisterUnboundSlotForTest("sess-b"); err != nil {
		t.Fatalf("register unbound slot for sess-b: %v", err)
	}
	if n := s.slotCount(); n != 2 {
		t.Fatalf("slotCount() = %d, want 2 (one bound entry and one registered-but-unbound)", n)
	}

	before := testutil.ToFloat64(unaddressedFrameRejected.WithLabelValues("status"))
	in := make(chan []byte, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := demuxSessionOutput(ctx, in, "sess-a", s.slotCount)
	in <- []byte(`{"type":"status","state":"thinking"}`)
	close(in)
	if got := drainDemux(t, out); len(got) != 0 {
		t.Errorf("demux delivered %q, want nothing", got)
	}
	if moved := testutil.ToFloat64(unaddressedFrameRejected.WithLabelValues("status")) - before; moved != 1 {
		t.Errorf("unaddressed-frame counter moved by %v, want 1", moved)
	}
}

// spec: §28.5.3 — demuxSessionOutput stops on ctx cancellation so a
// closed Attach stream does not leak the filter goroutine.
func TestDemuxSessionOutputStopsOnContextCancel(t *testing.T) {
	in := make(chan []byte) // unbuffered: a send would block without a reader
	ctx, cancel := context.WithCancel(context.Background())
	out := demuxSessionOutput(ctx, in, "sess-a", fixedSlotCount(1))
	cancel()
	select {
	case _, ok := <-out:
		if ok {
			t.Error("demux output should close on ctx cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("demux output did not close after ctx cancellation")
	}
}
