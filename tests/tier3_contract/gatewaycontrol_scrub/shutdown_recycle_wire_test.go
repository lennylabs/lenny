//go:build contract

// SPDX-License-Identifier: MIT

// This file extends the Tier 3 GatewayControl scrub-report contract suite
// with the §4.7 Gateway → Adapter recycle-scrub trigger carried on
// ShutdownRequest. It pins two properties of the wire contract: a
// ShutdownRequest with `recycle` unset (the terminate path) encodes
// byte-identically to the pre-change form so the recycle field is a
// backward-compatible addition, and a populated RecycleScrub carries all
// four scrub parameters (pod_id, cleanup_commands, cleanup_timeout_seconds,
// scrub_profile) through a binary encode/decode. spec: §4.7 (Shutdown
// recycle disposition); §5.2 (whole-pod scrub trigger).
package gatewaycontrol_scrub_test

import (
	"testing"

	"google.golang.org/protobuf/proto"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// TestShutdownRequestUnsetRecycleWireIdentical pins that a ShutdownRequest
// with `recycle` unset (the terminate path) encodes to exactly the bytes a
// pre-change ShutdownRequest carrying only fields 1-4 produced. The recycle
// sub-message is field 5; proto3 emits nothing for an unset message field,
// so the terminate path must stay byte-identical to before the recycle
// field was added. The expected bytes are the deterministic encoding of the
// session_id, reason, deadline_ms, and slot_id fields alone.
// spec: 4.7 (Shutdown recycle disposition), 5.2 (whole-pod scrub trigger)
//
// diagnosis: a failure means adding RecycleScrub to ShutdownRequest changed
// the terminate-path wire encoding — a field was renumbered or the recycle
// field emits bytes when unset — so a gateway on the new schema and an
// adapter on the old (or vice versa) would disagree on a plain Shutdown and
// the non-recycle shutdown path would drift.
func TestShutdownRequestUnsetRecycleWireIdentical_spec_4_7(t *testing.T) {
	req := &adapterv1.ShutdownRequest{
		SessionId:  &adapterv1.SessionId{Value: "sess-1"},
		Reason:     "drain",
		DeadlineMs: 5000,
		SlotId:     &adapterv1.SlotId{Value: "slot-3"},
		// Recycle left nil: the terminate path.
	}
	got, err := proto.MarshalOptions{Deterministic: true}.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Golden encoding of fields 1-4 only, computed independently of the
	// ShutdownRequest type so the assertion would fail if the recycle field
	// (field 5) emitted any bytes when unset. Field wire tags:
	//   field 1 (session_id, message): tag 0x0a
	//   field 2 (reason, string):      tag 0x12
	//   field 3 (deadline_ms, varint): tag 0x18
	//   field 4 (slot_id, message):    tag 0x22
	want := []byte{
		0x0a, 0x08, 0x0a, 0x06, 's', 'e', 's', 's', '-', '1', // session_id { value: "sess-1" }
		0x12, 0x05, 'd', 'r', 'a', 'i', 'n', // reason: "drain"
		0x18, 0x88, 0x27, // deadline_ms: 5000
		0x22, 0x08, 0x0a, 0x06, 's', 'l', 'o', 't', '-', '3', // slot_id { value: "slot-3" }
	}
	if string(got) != string(want) {
		t.Fatalf("terminate-path encoding drifted:\n got % x\nwant % x", got, want)
	}

	// The golden bytes must also round-trip back to an equal message so the
	// hand-computed encoding is not merely different-but-self-consistent.
	var back adapterv1.ShutdownRequest
	if err := proto.Unmarshal(want, &back); err != nil {
		t.Fatalf("unmarshal golden: %v", err)
	}
	if !proto.Equal(req, &back) {
		t.Errorf("golden bytes decoded to %v, want %v", &back, req)
	}
	if back.GetRecycle() != nil {
		t.Errorf("terminate-path ShutdownRequest decoded with a non-nil recycle: %v", back.GetRecycle())
	}
}

// TestShutdownRequestRecycleScrubRoundTrip pins that a ShutdownRequest
// carrying a populated RecycleScrub survives a proto binary
// marshal/unmarshal with all four scrub parameters intact (pod_id,
// cleanup_commands, cleanup_timeout_seconds, scrub_profile), so the gateway
// and the adapter agree on the recycle-scrub trigger.
// spec: 4.7 (Shutdown recycle disposition), 5.2 (whole-pod scrub trigger)
//
// diagnosis: a failure means a field of RecycleScrub was renumbered,
// retyped, or dropped in schemas/lenny-adapter.proto without regenerating
// the Go, so the recycle-scrub parameters no longer round-trip and the
// adapter would run the §5.2 whole-pod scrub with truncated or misread
// cleanup commands, timeout, or profile.
func TestShutdownRequestRecycleScrubRoundTrip_spec_5_2(t *testing.T) {
	in := &adapterv1.ShutdownRequest{
		SessionId:  &adapterv1.SessionId{Value: "sess-1"},
		Reason:     "recycle",
		DeadlineMs: 5000,
		Recycle: &adapterv1.RecycleScrub{
			PodId:                 "sandbox-42",
			CleanupCommands:       []string{"rm -rf /tmp/scratch", "truncate --size 0 /var/log/agent.log"},
			CleanupTimeoutSeconds: 30,
			ScrubProfile:          "vm-restart",
		},
	}
	raw, err := proto.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out adapterv1.ShutdownRequest
	if err := proto.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !proto.Equal(in, &out) {
		t.Fatalf("round-trip mismatch:\n got %v\nwant %v", &out, in)
	}

	// Assert each of the four RecycleScrub fields explicitly so a silent
	// drop of one field (that still round-trips because both sides drop it)
	// is caught.
	rc := out.GetRecycle()
	if rc == nil {
		t.Fatal("recycle sub-message lost in round-trip")
	}
	if rc.GetPodId() != "sandbox-42" {
		t.Errorf("pod_id = %q, want %q", rc.GetPodId(), "sandbox-42")
	}
	if got := rc.GetCleanupCommands(); len(got) != 2 ||
		got[0] != "rm -rf /tmp/scratch" ||
		got[1] != "truncate --size 0 /var/log/agent.log" {
		t.Errorf("cleanup_commands = %q, want the two submitted commands", got)
	}
	if rc.GetCleanupTimeoutSeconds() != 30 {
		t.Errorf("cleanup_timeout_seconds = %d, want 30", rc.GetCleanupTimeoutSeconds())
	}
	if rc.GetScrubProfile() != "vm-restart" {
		t.Errorf("scrub_profile = %q, want %q", rc.GetScrubProfile(), "vm-restart")
	}
}
