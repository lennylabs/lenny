// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// spec: §28.5.3 — frameSlotID reads the demultiplexing key off a
// runtime output frame. A frame with no slotId, or one that does not parse
// as a JSON object, yields the empty string so the Attach base path
// delivers it to every stream rather than dropping it.
func TestFrameSlotID(t *testing.T) {
	cases := []struct {
		name  string
		frame string
		want  string
	}{
		{"tagged", `{"type":"response","slotId":"slot-a"}`, "slot-a"},
		{"absent", `{"type":"heartbeat_ack"}`, ""},
		{"empty value", `{"type":"response","slotId":""}`, ""},
		{"not an object", `not json`, ""},
		{"empty", ``, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := frameSlotID([]byte(tc.frame)); got != tc.want {
				t.Errorf("frameSlotID(%q) = %q, want %q", tc.frame, got, tc.want)
			}
		})
	}
}

// spec: §28.5.3 — frameSlotAddress resolves the address an addressing
// decision compares. An absent slotId is the empty address; a slotId that
// is present but is not a JSON string is no address at all, so the probe
// reports it as malformed rather than collapsing it to the empty address
// the way frameSlotID does for the demultiplexer.
func TestFrameSlotAddress(t *testing.T) {
	cases := []struct {
		name  string
		frame string
		want  string
		ok    bool
	}{
		{"tagged", `{"type":"set_tracing_context","slotId":"slot-a"}`, "slot-a", true},
		{"absent", `{"type":"set_tracing_context"}`, "", true},
		{"empty value", `{"type":"set_tracing_context","slotId":""}`, "", true},
		{"number", `{"type":"set_tracing_context","slotId":1}`, "", false},
		{"null", `{"type":"set_tracing_context","slotId":null}`, "", false},
		{"object", `{"type":"set_tracing_context","slotId":{"id":"slot-a"}}`, "", false},
		{"array", `{"type":"set_tracing_context","slotId":["slot-a"]}`, "", false},
		{"not an object", `not json`, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := frameSlotAddress([]byte(tc.frame))
			if got != tc.want || ok != tc.ok {
				t.Errorf("frameSlotAddress(%q) = (%q, %v), want (%q, %v)", tc.frame, got, ok, tc.want, tc.ok)
			}
		})
	}
}

// spec: §6.4; §28.5.3 — stampSlotID injects slotId
// onto an inbound envelope so the shared runtime routes it to the slot's
// cwd. A non-object frame is forwarded unchanged so a non-envelope frame is
// not dropped.
func TestStampSlotID(t *testing.T) {
	got, err := stampSlotID([]byte(`{"type":"message","id":"m1"}`), "slot-a")
	if err != nil {
		t.Fatalf("stampSlotID: %v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(got, &obj); err != nil {
		t.Fatalf("decode stamped frame: %v", err)
	}
	if obj["slotId"] != "slot-a" {
		t.Errorf("stamped slotId = %v, want slot-a", obj["slotId"])
	}
	if obj["type"] != "message" || obj["id"] != "m1" {
		t.Errorf("stampSlotID dropped fields: %v", obj)
	}

	// A non-object frame passes through unchanged.
	raw := []byte(`not-json`)
	through, err := stampSlotID(raw, "slot-a")
	if err != nil {
		t.Fatalf("stampSlotID(non-object): %v", err)
	}
	if string(through) != "not-json" {
		t.Errorf("non-object frame = %q, want it unchanged", through)
	}
}

// spec: §6.4; §28.5.3 — a malformed JSON object fails closed:
// stampSlotID returns an error rather than writing an envelope with no
// slotId, which would misroute it to a sibling slot on a concurrent pod.
func TestStampSlotIDFailsClosedOnMalformedObject(t *testing.T) {
	if _, err := stampSlotID([]byte(`{"type":"message"`), "slot-a"); err == nil {
		t.Error("stampSlotID must fail on a malformed JSON object rather than drop the slotId")
	}
}

// spec: §28.5.3 — demuxSlotOutput keeps a slot's own frames and a
// no-slotId protocol frame, and drops a sibling slot's frame, so a per-slot
// Attach stream sees only its slot's output.
func TestDemuxSlotOutput(t *testing.T) {
	in := make(chan []byte, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := demuxSlotOutput(ctx, in, "slot-a")

	in <- []byte(`{"type":"response","slotId":"slot-a"}`) // kept
	in <- []byte(`{"type":"response","slotId":"slot-b"}`) // dropped
	in <- []byte(`{"type":"heartbeat_ack"}`)              // no slotId: kept

	want := []string{
		`{"type":"response","slotId":"slot-a"}`,
		`{"type":"heartbeat_ack"}`,
	}
	for _, w := range want {
		select {
		case got := <-out:
			if string(got) != w {
				t.Errorf("demux delivered %q, want %q", got, w)
			}
		case <-time.After(time.Second):
			t.Fatalf("demux did not deliver %q", w)
		}
	}

	// Closing the input closes the demux output so the Attach loop ends.
	close(in)
	select {
	case _, ok := <-out:
		if ok {
			t.Error("demux output should close when the input closes")
		}
	case <-time.After(time.Second):
		t.Fatal("demux output did not close after the input closed")
	}
}

// spec: §28.5.3 — demuxSlotOutput stops on ctx cancellation so a
// closed Attach stream does not leak the filter goroutine.
func TestDemuxSlotOutputStopsOnContextCancel(t *testing.T) {
	in := make(chan []byte) // unbuffered: a send would block without a reader
	ctx, cancel := context.WithCancel(context.Background())
	out := demuxSlotOutput(ctx, in, "slot-a")
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
