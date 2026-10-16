// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
)

// spec: §4.8 — with no chain configured, runPostAgentOutput
// returns the agent output parts unchanged and does not reject.
func TestRunPostAgentOutputNoChainPassesThrough(t *testing.T) {
	s := &Server{}
	in := []executor.MessagePart{{Type: "text", Text: "hello"}}
	out, rejected, auditErr := s.runPostAgentOutput(context.Background(), "acme", "sess_x", in)
	if rejected != nil || auditErr != nil {
		t.Fatalf("empty chain rejected the output: %v / %v", rejected, auditErr)
	}
	if len(out) != 1 || out[0].Text != "hello" {
		t.Errorf("output mutated by empty chain: %+v", out)
	}
}

// spec: §4.8 — a deliberate PostAgentOutput REJECT blocks delivery: the
// chain's result is returned with no response written, and the REST
// envelope half answers 403 INTERCEPTOR_REJECTED carrying the phase.
func TestRunPostAgentOutputRejectReturns403(t *testing.T) {
	s := &Server{interceptors: newRouteChain(t, interceptor.PhasePostAgentOutput, rejectInterceptor{})}
	in := []executor.MessagePart{{Type: "text", Text: "secret"}}
	_, rejected, auditErr := s.runPostAgentOutput(context.Background(), "acme", "sess_x", in)
	if rejected == nil {
		t.Fatal("runPostAgentOutput admitted a REJECT")
	}
	rec := httptest.NewRecorder()
	s.writeRouteRejection(rec, interceptor.PhasePostAgentOutput, *rejected, auditErr)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	body := decodeEnvelope(t, rec)
	if body.Code != "INTERCEPTOR_REJECTED" {
		t.Errorf("code = %q, want INTERCEPTOR_REJECTED", body.Code)
	}
	if got := body.Details["phase"]; got != string(interceptor.PhasePostAgentOutput) {
		t.Errorf("phase = %v, want PostAgentOutput", got)
	}
}

// spec: §4.8 — a PostAgentOutput MODIFY rewrites the output parts
// the gateway delivers to the client.
func TestRunPostAgentOutputModifyRewritesParts(t *testing.T) {
	modified, _ := json.Marshal([]executor.MessagePart{{Type: "text", Text: "redacted"}})
	s := &Server{interceptors: newRouteChain(t, interceptor.PhasePostAgentOutput,
		routeModifyInterceptor{priority: 150, out: modified})}
	in := []executor.MessagePart{{Type: "text", Text: "original"}}
	out, rejected, _ := s.runPostAgentOutput(context.Background(), "acme", "sess_x", in)
	if rejected != nil {
		t.Fatalf("runPostAgentOutput rejected a legal MODIFY: %v", rejected)
	}
	if len(out) != 1 || out[0].Text != "redacted" {
		t.Errorf("output = %+v, want a single redacted part", out)
	}
}
