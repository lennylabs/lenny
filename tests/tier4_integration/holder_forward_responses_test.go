// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration tests for the Open Responses warm-turn forward on two
// gateway replicas of the lifecycle harness. Each replica's POST
// /v1/responses is a handler that calls its session server's
// ForwardResponsesTurn on the session the request names and, when that
// reports local, delivers the turn to the session's pod through the
// replica's executor. The forward crosses the replicas' CH-HOLDERFORWARD
// listeners with real gateway certificates.
package tier4_integration_test

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

// responsesTurn is the request body of the stand-in responses handler.
type responsesTurn struct {
	Session string `json:"session"`
	Input   string `json:"input"`
}

// responsesStandIn returns r's responses handler: it forwards the turn on the
// named session through ForwardResponsesTurn with a native-envelope refusal,
// and on a local outcome delivers the input to the session's pod and answers
// which replica served it.
func responsesStandIn(h *lifecycleharness.Harness, r *lifecycleharness.Replica) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		var turn responsesTurn
		if err != nil || json.Unmarshal(body, &turn) != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		row, err := h.Inner.Get(req.Context(), lifecycleharness.Tenant, turn.Session)
		if err != nil {
			http.NotFound(w, req)
			return
		}
		refuse := func(w http.ResponseWriter, currentState string) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
				"type": "invalid_request_error", "code": "TARGET_NOT_READY", "current_state": currentState,
			}})
		}
		if r.Server.ForwardResponsesTurn(w, req, row, body, refuse) != sessionserver.ForwardLocal {
			return
		}
		if _, err := r.Executor.Send(req.Context(), turn.Session, []executor.Message{{Role: "user", Content: turn.Input}}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"served_by": r.Name, "forwarded": sessionserver.Forwarded(req.Context())})
	})
}

// newResponsesForwardCompose builds the two-replica harness with the
// stand-in responses handler on both replicas and starts a session the first
// replica coordinates.
func newResponsesForwardCompose(t *testing.T) (*lifecycleharness.Harness, *lifecycleharness.Replica, *lifecycleharness.Replica, string) {
	t.Helper()
	h, coordinator, peer := newForwardCompose(t, lifecycleharness.Options{})
	coordinator.SetResponsesHandler(responsesStandIn(h, coordinator))
	peer.SetResponsesHandler(responsesStandIn(h, peer))
	return h, coordinator, peer, h.StartSession(coordinator)
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
// coordinate its held session was served by that replica, or the
// coordinator's forward listener did not route POST /v1/responses to its
// responses handler, rather than the turn reaching the REG-COORDLEASE holder
// over CH-HOLDERFORWARD and running on the holder's pod. Check
// ForwardResponsesTurn in pkg/gateway/sessionserver/holder_forward.go and
// the listener's route in cmd/lenny-gateway/holder_forward_listener.go.
func TestOffHolderResponsesTurnIsServedOnTheHoldersPod_spec_29_3(t *testing.T) {
	h, coordinator, peer, id := newResponsesForwardCompose(t)
	forwards := countForwards(coordinator)
	code, body := h.Do(peer, http.MethodPost, "/v1/responses", responsesTurn{Session: id, Input: "warm-via-peer"})
	if code != http.StatusOK || body["served_by"] != coordinator.Name || body["forwarded"] != true {
		t.Fatalf("turn on the serving replica = %d %v, want 200 served by %s as a forwarded request", code, body, coordinator.Name)
	}
	if n := forwards.Load(); n != 1 {
		t.Errorf("the coordinator's forward listener received %d requests, want 1", n)
	}
	h.Settle()
	if n := receivedCount(h, id, "warm-via-peer"); n != 1 {
		t.Errorf("the runtime received the turn %d times, want once", n)
	}

	code, body = h.Do(coordinator, http.MethodPost, "/v1/responses", responsesTurn{Session: id, Input: "warm-local"})
	if code != http.StatusOK || body["served_by"] != coordinator.Name || body["forwarded"] != false {
		t.Fatalf("turn on the coordinator = %d %v, want 200 served locally", code, body)
	}
	if n := forwards.Load(); n != 1 {
		t.Errorf("a turn on the coordinator crossed the forward listener: %d requests, want 1", n)
	}
}

// spec: 29.3 (Interactive message send) Forwarding rules 6 and 7, 15 (Open Responses conversation sessions)
// diagnosis: an Open Responses warm turn whose forward to the coordinator was
// not delivered or had an unknown outcome was served by the serving replica,
// held in the session DLQ, delivered to the runtime, or answered in the Lenny
// envelope, rather than refused with 409 TARGET_NOT_READY in the adapter's
// native envelope. Check refuseForward in
// pkg/gateway/sessionserver/holder_forward.go.
func TestUndeliveredResponsesTurnFailsClosedInTheNativeEnvelope_spec_29_3(t *testing.T) {
	for _, mode := range undeliveredModes {
		t.Run(mode.name, func(t *testing.T) {
			h, coordinator, peer, id := newResponsesForwardCompose(t)
			mode.arm(t, coordinator)
			code, body := h.Do(peer, http.MethodPost, "/v1/responses", responsesTurn{Session: id, Input: "never-delivered"})
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
		})
	}
}
