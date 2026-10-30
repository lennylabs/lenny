// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration tests for the Open Responses warm-turn forward on two
// gateway replicas of the lifecycle harness. Each replica's POST
// /v1/responses is the production Open Responses translator handler, which
// Harness.WireOpenResponses wires as the gateway wires it, with a binder that
// holds every session it binds. A continuation sent to the replica that does
// not coordinate the held session crosses the replicas' CH-HOLDERFORWARD
// listeners with real gateway certificates.
package tier4_integration_test

import (
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

// newResponsesForwardCompose builds the two-replica harness with the Open
// Responses handler on both replicas and runs a first turn on the first
// replica, which binds a held session that replica coordinates. It returns
// the harness, the coordinator, the serving replica, the session id, and the
// first response id.
func newResponsesForwardCompose(t *testing.T) (*lifecycleharness.Harness, *lifecycleharness.Replica, *lifecycleharness.Replica, string, string) {
	t.Helper()
	h, coordinator, peer := newForwardCompose(t, lifecycleharness.Options{})
	h.WireOpenResponses(true)
	first := responsesTurnOn(t, h, coordinator, "", "turn-1")
	return h, coordinator, peer, sessionOf(t, first), first
}

// postResponsesTurn posts one Open Responses continuation of prev to r.
func postResponsesTurn(h *lifecycleharness.Harness, r *lifecycleharness.Replica, prev, input string) (int, map[string]any) {
	return h.Do(r, http.MethodPost, "/v1/responses", map[string]any{
		"model": lifecycleharness.RuntimeRef, "input": input, "user": sessionOwner, "previous_response_id": prev,
	})
}

// requireNativeRefusal fails unless the answer is the native-envelope 409
// TARGET_NOT_READY carrying current_state running.
func requireNativeRefusal(t *testing.T, code int, body map[string]any) {
	t.Helper()
	e, _ := body["error"].(map[string]any)
	if code != http.StatusConflict || e["code"] != "TARGET_NOT_READY" || e["current_state"] != "running" {
		t.Fatalf("answer = %d %v, want the native 409 TARGET_NOT_READY with current_state running", code, body)
	}
}

// spec: 29.3 (Interactive message send) Forwarding rules 1, 2, and 4, 15 (Open Responses conversation sessions)
// diagnosis: an Open Responses warm turn sent to the replica that does not
// coordinate its held session was served by that replica, went cold, or
// the coordinator's forward listener did not route POST /v1/responses to its
// Open Responses handler, rather than the turn reaching the REG-COORDLEASE
// holder over CH-HOLDERFORWARD and running warm on the holder's pod. Check
// handleCreate in pkg/gateway/environment/translator/open_responses.go,
// ForwardResponsesTurn in pkg/gateway/sessionserver/holder_forward.go, and
// the listener's route in cmd/lenny-gateway/holder_forward_listener.go.
func TestOffHolderResponsesTurnIsServedOnTheHoldersPod_spec_29_3(t *testing.T) {
	h, coordinator, peer, id, first := newResponsesForwardCompose(t)
	holder := leaseHolder(t, h, id)
	forwards := countForwards(coordinator)

	code, body := postResponsesTurn(h, peer, first, "warm-via-peer")
	if want := id + ".2"; code != http.StatusOK || body["id"] != want {
		t.Fatalf("turn on the serving replica = %d %v, want 200 with id %s (warm on the held session)", code, body, want)
	}
	if n := forwards.Load(); n != 1 {
		t.Errorf("the coordinator's forward listener received %d requests, want 1", n)
	}
	h.Settle()
	if n := receivedCount(h, id, "warm-via-peer"); n != 1 {
		t.Errorf("the runtime received the turn %d times, want once", n)
	}
	if got := leaseHolder(t, h, id); got != holder {
		t.Errorf("coordination lease holder = %q, want unchanged %q", got, holder)
	}
	if _, ok := peer.Registry.Get(id); ok {
		t.Error("the serving replica bound the held session rather than forwarding the turn")
	}

	code, body = postResponsesTurn(h, coordinator, id+".2", "warm-local")
	if want := id + ".3"; code != http.StatusOK || body["id"] != want {
		t.Fatalf("turn on the coordinator = %d %v, want 200 with id %s served locally", code, body, want)
	}
	if n := forwards.Load(); n != 1 {
		t.Errorf("a turn on the coordinator crossed the forward listener: %d requests, want 1", n)
	}
	h.Settle()
	if n := receivedCount(h, id, "warm-local"); n != 1 {
		t.Errorf("the runtime received the local turn %d times, want once", n)
	}
}

// spec: 29.3 (Interactive message send) Forwarding rules 6 and 7, 15 (Open Responses conversation sessions)
// diagnosis: an Open Responses warm turn whose forward to the coordinator was
// not delivered or had an unknown outcome was served by the serving replica,
// held in the session DLQ, delivered to the runtime, recorded as a response,
// or answered in the Lenny envelope, rather than refused with 409
// TARGET_NOT_READY in the adapter's native envelope. Check refuseForward in
// pkg/gateway/sessionserver/holder_forward.go and writeTargetNotReady in
// pkg/gateway/environment/translator/openai_chat.go.
func TestUndeliveredResponsesTurnFailsClosedInTheNativeEnvelope_spec_29_3(t *testing.T) {
	for _, mode := range undeliveredModes {
		t.Run(mode.name, func(t *testing.T) {
			h, coordinator, peer, id, first := newResponsesForwardCompose(t)
			mode.arm(t, coordinator)
			code, body := postResponsesTurn(h, peer, first, "never-delivered")
			requireNativeRefusal(t, code, body)
			if _, lenny := body["error"].(map[string]any)["details"]; lenny {
				t.Errorf("refusal carries the Lenny envelope's details: %v", body)
			}
			h.Settle()
			if n := receivedCount(h, id, "never-delivered"); n != 0 {
				t.Errorf("the runtime received the refused turn %d times, want none", n)
			}
			requireEmptyDLQ(t, h, id)
			if n := inboxLen(t, h, id); n != 0 {
				t.Errorf("inbox holds %d entries, want none", n)
			}
			if n := len(h.Row(id).Responses); n != 1 {
				t.Errorf("held session carries %d response records, want only the first turn's", n)
			}
		})
	}
}
