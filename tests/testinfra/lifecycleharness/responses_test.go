// SPDX-License-Identifier: MIT

package lifecycleharness

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// postResponses posts one Open Responses turn, continuing prev when it is
// set, to replica r.
func postResponses(h *Harness, r *Replica, model, prev, input string) (int, map[string]any) {
	body := map[string]any{"model": model, "input": input, "user": "alice@acme.com"}
	if prev != "" {
		body["previous_response_id"] = prev
	}
	return h.Do(r, http.MethodPost, "/v1/responses", body)
}

// TestHarnessWiresOpenResponsesOnEveryReplica_spec_15 pins the harness's
// Open Responses wiring: POST /v1/responses answers 404 until the handler is
// set, a first turn binds a held session on the replica that receives it and
// the harness tracks it, and a continuation sent to the other replica is
// served warm on the same session through the coordinator's forward listener
// rather than bound on the peer.
//
// spec: §15 (Open Responses conversation sessions), §29.3 (Interactive
// message send) Forwarding
func TestHarnessWiresOpenResponsesOnEveryReplica_spec_15(t *testing.T) {
	h := New(t, Options{Replicas: 2, Pods: 4})
	coordinator, peer := h.Replicas[0], h.Replicas[1]

	if code, body := postResponses(h, coordinator, RuntimeRef, "", "unwired"); code != http.StatusNotFound {
		t.Fatalf("turn before wiring = %d %v, want 404", code, body)
	}

	h.WireOpenResponses(true)
	code, body := postResponses(h, coordinator, RuntimeRef, "", "turn-1")
	first, _ := body["id"].(string)
	if code != http.StatusOK || !strings.HasSuffix(first, ".1") {
		t.Fatalf("first turn = %d %v, want 200 with ordinal 1", code, body)
	}
	id := strings.TrimSuffix(first, ".1")
	if got := h.Sessions(); len(got) != 1 || got[0] != id {
		t.Errorf("tracked sessions = %v, want [%s]", got, id)
	}

	code, body = postResponses(h, peer, RuntimeRef, first, "turn-2")
	if want := id + ".2"; code != http.StatusOK || body["id"] != want {
		t.Fatalf("continuation on the peer = %d %v, want 200 with id %s", code, body, want)
	}
	if _, ok := peer.Registry.Get(id); ok {
		t.Error("the peer bound the held session rather than forwarding the turn")
	}
}

// TestHarnessResponsesBinderMapsARefusal_spec_15 pins the harness binder's
// refusal mapping: a first turn whose create-and-start the session server
// refuses answers with an error status rather than 200, and the harness
// tracks no session.
//
// spec: §15 (Open Responses conversation sessions), §15.2.1 (create-and-start)
func TestHarnessResponsesBinderMapsARefusal_spec_15(t *testing.T) {
	h := New(t, Options{Replicas: 1, Pods: 4})
	h.WireOpenResponses(false)
	if code, body := postResponses(h, h.Replicas[0], "no-such-runtime", "", "x"); code < http.StatusBadRequest {
		t.Fatalf("turn on an unknown runtime = %d %v, want a refusal", code, body)
	}
	if n := len(h.Sessions()); n != 0 {
		t.Errorf("tracked sessions = %d, want none", n)
	}
}

// TestResponsesForwarderReportsAForwardedRequest_spec_29_3 pins the harness
// forwarder's Forwarded: true exactly on a context the forward listener
// marked, as the gateway's forwarder adapter answers.
//
// spec: §29.3 (Interactive message send) Forwarding, §15 (Open Responses
// conversation sessions)
func TestResponsesForwarderReportsAForwardedRequest_spec_29_3(t *testing.T) {
	f := responsesForwarder{}
	if f.Forwarded(context.Background()) {
		t.Error("Forwarded on an unmarked context = true, want false")
	}
	if !f.Forwarded(sessionserver.WithForwarded(context.Background())) {
		t.Error("Forwarded on a marked context = false, want true")
	}
}
