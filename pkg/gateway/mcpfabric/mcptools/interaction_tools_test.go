// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/elicitation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

func TestRequestElicitationResolvedByResponse(t *testing.T) {
	srv, store, interactions := newMCPForElicitation(t, 5*time.Second)
	mkSession(t, store, "sess_e", session.StateRunning, "")
	h := srv.Handler()

	got := make(chan map[string]any, 1)
	go func() {
		got <- call(t, h, "lenny/request_elicitation",
			`{"sessionId":"sess_e","message":"pick one","schema":{},"elicitationId":"elic_x"}`)
	}()
	waitElicitation(t, interactions, "sess_e", "elic_x")

	// A human responds — the path the §15.1 respond endpoint drives.
	if _, err := interactions.Resolve(context.Background(), "acme", "sess_e", "", "elic_x",
		func(i *interactionstore.Interaction) error {
			i.Phase = interactionstore.PhaseResponded
			i.Response = "option-A"
			return nil
		}); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	select {
	case resp := <-got:
		if text := resultText(t, resp); !strings.Contains(text, "option-A") {
			t.Errorf("request_elicitation result = %q, want the human response", text)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("request_elicitation did not return after the response")
	}
}

func TestRequestElicitationDismissed(t *testing.T) {
	srv, store, interactions := newMCPForElicitation(t, 5*time.Second)
	mkSession(t, store, "sess_e", session.StateRunning, "")
	h := srv.Handler()

	got := make(chan map[string]any, 1)
	go func() {
		got <- call(t, h, "lenny/request_elicitation",
			`{"sessionId":"sess_e","message":"pick one","schema":{},"elicitationId":"elic_x"}`)
	}()
	waitElicitation(t, interactions, "sess_e", "elic_x")

	if _, err := interactions.Resolve(context.Background(), "acme", "sess_e", "", "elic_x",
		func(i *interactionstore.Interaction) error {
			i.Phase = interactionstore.PhaseDismissed
			return nil
		}); err != nil {
		t.Fatalf("dismiss: %v", err)
	}

	select {
	case resp := <-got:
		if text := resultText(t, resp); !strings.Contains(text, "dismissed") {
			t.Errorf("request_elicitation result = %q, want a dismissed result", text)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("request_elicitation did not return after the dismissal")
	}
}

// TestRequestElicitationTimeout_spec_9_2 verifies the §9.2 line 103
// timeout path returns a structured ELICITATION_TIMEOUT envelope: the
// lenny code lands in the lenny/error content block and the §15.2.1
// classifier resolves it to (TRANSIENT, retryable=false). F-9.2.18.
func TestRequestElicitationTimeout_spec_9_2(t *testing.T) {
	srv, store, _ := newMCPForElicitation(t, 40*time.Millisecond)
	mkSession(t, store, "sess_e", session.StateRunning, "")

	resp := call(t, srv.Handler(), "lenny/request_elicitation",
		`{"sessionId":"sess_e","message":"pick one","schema":{},"elicitationId":"elic_x"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("a timed-out elicitation should be a tool error: %+v", resp)
	}
	envelope := readLennyErrorEnvelope(t, result)
	if got := envelope["code"]; got != "ELICITATION_TIMEOUT" {
		t.Errorf("envelope.code = %v, want ELICITATION_TIMEOUT", got)
	}
	if got := envelope["category"]; got != "TRANSIENT" {
		t.Errorf("envelope.category = %v, want TRANSIENT", got)
	}
	if got, _ := envelope["retryable"].(bool); got {
		t.Errorf("envelope.retryable = true, want false (the original elicitation is now dismissed)")
	}
	details, _ := envelope["details"].(map[string]any)
	if id, _ := details["elicitationId"].(string); id != "elic_x" {
		t.Errorf("envelope.details.elicitationId = %v, want elic_x", id)
	}
}

func TestRequestElicitationBudgetExceeded(t *testing.T) {
	srv, store, interactions := newMCPForElicitation(t, time.Second)
	mkSession(t, store, "sess_e", session.StateRunning, "")
	// Fill the default §9.1 per-session elicitation budget (50).
	for i := 0; i < 50; i++ {
		if err := interactions.Put(context.Background(), interactionstore.Interaction{
			ID:        "elic-" + strconv.Itoa(i),
			Kind:      interactionstore.KindElicitation,
			SessionID: "sess_e", TenantID: "acme",
		}); err != nil {
			t.Fatalf("seed elicitation %d: %v", i, err)
		}
	}

	resp := call(t, srv.Handler(), "lenny/request_elicitation",
		`{"sessionId":"sess_e","message":"one too many","schema":{},"elicitationId":"elic_over"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("an over-budget elicitation should be a tool error: %+v", resp)
	}
	content, _ := result["content"].([]any)
	c0, _ := content[0].(map[string]any)
	if msg, _ := c0["text"].(string); !strings.Contains(msg, "budget") {
		t.Errorf("error = %q, want an elicitation-budget message", msg)
	}
	// The over-budget elicitation was dropped, not recorded.
	if _, err := interactions.Get(context.Background(), "acme", "sess_e", "", "elic_over"); err == nil {
		t.Error("the dropped elicitation was recorded in the interaction store")
	}
}

// fakeElicitationMetrics records the §9.1 elicitation drop reasons.
func TestRequestElicitationDropRecordsMetric(t *testing.T) {
	store := memstore.New()
	interactions := interactionstore.NewMemory()
	rec := &fakeElicitationMetrics{}
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:                     store,
		Interactions:              interactions,
		ElicitationMetrics:        rec,
		MaxElicitationsPerSession: 1,
		Clock:                     func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:                    func() string { return "elic_gen" },
		TenantID:                  "acme",
	})
	mkSession(t, store, "sess_e", session.StateRunning, "")
	// One elicitation already recorded fills the cap of 1.
	if err := interactions.Put(context.Background(), interactionstore.Interaction{
		ID: "elic_0", Kind: interactionstore.KindElicitation, SessionID: "sess_e", TenantID: "acme",
	}); err != nil {
		t.Fatalf("seed elicitation: %v", err)
	}

	resp := call(t, srv.Handler(), "lenny/request_elicitation",
		`{"sessionId":"sess_e","message":"over budget","schema":{},"elicitationId":"elic_x"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("an over-budget elicitation should be a tool error: %+v", resp)
	}
	if len(rec.reasons) != 1 || rec.reasons[0] != "budget_exceeded" {
		t.Errorf("recorded drop reasons = %v, want [budget_exceeded]", rec.reasons)
	}
}

func TestRequestElicitationSuppressedAtDepth(t *testing.T) {
	store := memstore.New()
	interactions := interactionstore.NewMemory()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:                      store,
		Interactions:               interactions,
		ElicitationDepthPolicy:     elicitation.DepthSuppressAtDepth,
		ElicitationSuppressAtDepth: 2,
		ElicitationTimeout:         time.Second,
		IDFunc:                     func() string { return "elic_gen" },
		TenantID:                   "acme",
	})
	// A delegation tree root → mid → leaf; the leaf sits at depth 2.
	mkSession(t, store, "sess_root", session.StateRunning, "")
	mkSession(t, store, "sess_mid", session.StateRunning, "sess_root")
	mkSession(t, store, "sess_leaf", session.StateRunning, "sess_mid")

	resp := call(t, srv.Handler(), "lenny/request_elicitation",
		`{"sessionId":"sess_leaf","message":"ask","schema":{},"elicitationId":"elic_x"}`)
	// §9.2: a suppressed elicitation returns a SUPPRESSED response (not
	// an error) the originating pod handles as "user declined".
	text := resultText(t, resp)
	if !strings.Contains(text, "suppressed") {
		t.Errorf("result = %q, want a suppressed response", text)
	}
	// The suppressed elicitation was not recorded against any session
	// in the chain.
	for _, sid := range []string{"sess_leaf", "sess_mid", "sess_root"} {
		if _, err := interactions.Get(context.Background(), "acme", sid, "", "elic_x"); err == nil {
			t.Errorf("a suppressed elicitation was recorded against %s", sid)
		}
	}
}

func TestRequestElicitationNotSuppressedBelowDepth(t *testing.T) {
	store := memstore.New()
	interactions := interactionstore.NewMemory()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:                      store,
		Interactions:               interactions,
		ElicitationDepthPolicy:     elicitation.DepthSuppressAtDepth,
		ElicitationSuppressAtDepth: 5, // higher than the session's depth
		ElicitationTimeout:         5 * time.Second,
		IDFunc:                     func() string { return "elic_gen" },
		TenantID:                   "acme",
	})
	mkSession(t, store, "sess_root", session.StateRunning, "")
	mkSession(t, store, "sess_mid", session.StateRunning, "sess_root") // depth 1
	h := srv.Handler()

	got := make(chan map[string]any, 1)
	go func() {
		got <- call(t, h, "lenny/request_elicitation",
			`{"sessionId":"sess_mid","message":"ask","schema":{},"elicitationId":"elic_x"}`)
	}()
	// §9.2: the elicitation was not suppressed below the depth and
	// forwards up the chain to the human-facing root. It is recorded
	// against the chain resolver, sess_root.
	waitElicitation(t, interactions, "sess_root", "elic_x")
	if _, err := interactions.Resolve(context.Background(), "acme", "sess_root", "", "elic_x",
		func(i *interactionstore.Interaction) error {
			i.Phase = interactionstore.PhaseResponded
			i.Response = "ok"
			return nil
		}); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	select {
	case resp := <-got:
		resultText(t, resp) // a non-error result confirms it was not suppressed
	case <-time.After(3 * time.Second):
		t.Fatal("request_elicitation did not return")
	}
}

func TestRequestElicitationRejectsTerminalSession(t *testing.T) {
	srv, store, _ := newMCPForElicitation(t, time.Second)
	mkSession(t, store, "sess_done", session.StateCompleted, "")

	resp := call(t, srv.Handler(), "lenny/request_elicitation",
		`{"sessionId":"sess_done","message":"x","schema":{},"elicitationId":"elic_x"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("a terminal session should be a tool error: %+v", resp)
	}
}

// TestRequestElicitationPublishesElicitationRequestEvent_spec_7_2 asserts
// that lenny/request_elicitation surfaces the elicitation on the
// resolver session's stream as the canonical §7.2 line 136
// `elicitation_request` event, not the pre-fix `elicitation_requested`
// synonym (which appeared nowhere in the §7.2 catalog).
// spec: §7.2 line 136. F-7.2.17.
func TestRequestElicitationPublishesElicitationRequestEvent_spec_7_2(t *testing.T) {
	store := memstore.New()
	interactions := interactionstore.NewMemory()
	bus := sessionevents.NewBus(0)
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:              store,
		Events:             bus,
		Interactions:       interactions,
		ElicitationTimeout: 5 * time.Second,
		Clock:              func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:             func() string { return "elic_gen" },
		TenantID:           "acme",
	})
	mkSession(t, store, "sess_e", session.StateRunning, "")
	h := srv.Handler()

	done := make(chan map[string]any, 1)
	go func() {
		done <- call(t, h, "lenny/request_elicitation",
			`{"sessionId":"sess_e","message":"pick one","schema":{},"elicitationId":"elic_x"}`)
	}()
	waitElicitation(t, interactions, "sess_e", "elic_x")

	hist := bus.History("sess_e", 0)
	if len(hist) != 1 {
		t.Fatalf("event history has %d events, want 1: %+v", len(hist), hist)
	}
	if hist[0].Type != "elicitation_request" {
		t.Errorf("event type = %q, want elicitation_request (§7.2 line 136 canonical name)", hist[0].Type)
	}
	if !strings.Contains(hist[0].Data, "elic_x") || !strings.Contains(hist[0].Data, "pick one") {
		t.Errorf("event data = %q, want it to carry the elicitationId + message", hist[0].Data)
	}

	// Resolve so the goroutine returns cleanly.
	if _, err := interactions.Resolve(context.Background(), "acme", "sess_e", "", "elic_x",
		func(i *interactionstore.Interaction) error {
			i.Phase = interactionstore.PhaseDismissed
			return nil
		}); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("request_elicitation did not return after dismissal")
	}
}
