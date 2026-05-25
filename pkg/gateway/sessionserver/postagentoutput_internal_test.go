// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
)

// spec: §4.8 line 1054 — with no chain configured, runPostAgentOutput
// returns the agent output parts unchanged and does not reject.
func TestRunPostAgentOutputNoChainPassesThrough(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	in := []executor.OutputPart{{Type: "text", Text: "hello"}}
	out, rejected := s.runPostAgentOutput(context.Background(), rec, "acme", "sess_x", in)
	if rejected {
		t.Fatal("empty chain rejected the output")
	}
	if len(out) != 1 || out[0].Text != "hello" {
		t.Errorf("output mutated by empty chain: %+v", out)
	}
}

// spec: §4.8 line 1054 — a deliberate PostAgentOutput REJECT blocks
// delivery and writes a 403 INTERCEPTOR_REJECTED envelope carrying the phase.
func TestRunPostAgentOutputRejectReturns403(t *testing.T) {
	s := &Server{interceptors: newRouteChain(t, interceptor.PhasePostAgentOutput, rejectInterceptor{})}
	rec := httptest.NewRecorder()
	in := []executor.OutputPart{{Type: "text", Text: "secret"}}
	if _, rejected := s.runPostAgentOutput(context.Background(), rec, "acme", "sess_x", in); !rejected {
		t.Fatal("runPostAgentOutput admitted a REJECT")
	}
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

// spec: §4.8 line 1054 — a PostAgentOutput MODIFY rewrites the output parts
// the gateway delivers to the client.
func TestRunPostAgentOutputModifyRewritesParts(t *testing.T) {
	modified, _ := json.Marshal([]executor.OutputPart{{Type: "text", Text: "redacted"}})
	s := &Server{interceptors: newRouteChain(t, interceptor.PhasePostAgentOutput,
		routeModifyInterceptor{priority: 150, out: modified})}
	rec := httptest.NewRecorder()
	in := []executor.OutputPart{{Type: "text", Text: "original"}}
	out, rejected := s.runPostAgentOutput(context.Background(), rec, "acme", "sess_x", in)
	if rejected {
		t.Fatalf("runPostAgentOutput rejected a legal MODIFY: status %d", rec.Code)
	}
	if len(out) != 1 || out[0].Text != "redacted" {
		t.Errorf("output = %+v, want a single redacted part", out)
	}
}
