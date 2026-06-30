// SPDX-License-Identifier: MIT

package executor_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
)

// approvalRuntime emits a tool_call(approvalRequired) in answer to the
// first `message`, then a `response` once it receives the gateway's
// verdict frame (the forwarded tool_call on approve, or the tool_result
// error on deny). With emitResponseEagerly it pushes the response on the
// same turn as the tool_call so the nil-gate skip path can complete
// without a verdict round-trip.
type approvalRuntime struct {
	out                 chan []byte
	emitResponseEagerly bool

	mu       sync.Mutex
	lastDeny []byte // the tool_result the runtime received on a denial
}

func (r *approvalRuntime) Start(context.Context, string) error { return nil }

func (r *approvalRuntime) WriteEnvelope(_ string, env []byte) error {
	var probe struct {
		Type    string `json:"type"`
		IsError bool   `json:"isError"`
	}
	_ = json.Unmarshal(env, &probe)
	switch probe.Type {
	case "message":
		r.out <- []byte(`{"type":"tool_call","id":"tc-1","name":"lenny/deploy","arguments":{"target":"prod"},"approvalRequired":true}`)
		if r.emitResponseEagerly {
			r.out <- []byte(`{"type":"response","text":"ack"}`)
		}
	case "tool_call":
		// Approved call forwarded back for execution.
		r.out <- []byte(`{"type":"response","text":"executed"}`)
	case "tool_result":
		// Denied call: the gateway wrote a tool_result error back.
		r.mu.Lock()
		r.lastDeny = append([]byte(nil), env...)
		r.mu.Unlock()
		r.out <- []byte(`{"type":"response","text":"denied"}`)
	}
	return nil
}

func (r *approvalRuntime) Output(context.Context, string) (<-chan []byte, error) {
	return r.out, nil
}
func (r *approvalRuntime) Interrupt(context.Context, string, bool) error { return nil }
func (r *approvalRuntime) Close(context.Context, string) error           { return nil }

// recordingGate is a fake executor.ApprovalGate that records the call it
// was asked to gate and returns a preset verdict.
type recordingGate struct {
	decision executor.ApprovalDecision
	err      error

	mu   sync.Mutex
	seen []executor.PendingToolCall
}

func (g *recordingGate) AwaitApproval(_ context.Context, tenantID, sessionID string, call executor.PendingToolCall) (executor.ApprovalDecision, error) {
	g.mu.Lock()
	g.seen = append(g.seen, call)
	g.mu.Unlock()
	return g.decision, g.err
}

// TestPodExecutorToolUseApprove_spec_7_2 covers the §7.2 line 124 approve
// path: an approval-required tool_call drives the gate, and on approval
// the executor forwards the call so the runtime executes it. F-7.2.9,
// F-7.2.18.
func TestPodExecutorToolUseApprove_spec_7_2(t *testing.T) {
	cl := dialPodAdapter(t, &approvalRuntime{out: make(chan []byte, 8)})
	reg := podsession.NewRegistry()
	reg.Put(&podsession.BindResult{SessionID: "sess-pod", TenantID: "acme", Adapter: cl})

	gate := &recordingGate{decision: executor.ApprovalDecision{Approved: true}}
	pe := executor.NewPodExecutor(reg, nil)
	pe.SetApprovalGate(gate)

	out, err := pe.Send(context.Background(), "sess-pod", []executor.Message{{Role: "user", Content: "deploy"}})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(out.Parts) != 1 || out.Parts[0].Text != "executed" {
		t.Fatalf("Send output = %+v, want one \"executed\" part (the approved call ran)", out)
	}
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if len(gate.seen) != 1 {
		t.Fatalf("gate called %d times, want 1", len(gate.seen))
	}
	got := gate.seen[0]
	if got.ID != "tc-1" || got.Name != "lenny/deploy" {
		t.Errorf("gated call = {ID:%q Name:%q}, want {tc-1 lenny/deploy}", got.ID, got.Name)
	}
	if string(got.Arguments) != `{"target":"prod"}` {
		t.Errorf("gated args = %s, want {\"target\":\"prod\"}", got.Arguments)
	}
}

// TestPodExecutorToolUseDeny_spec_7_2 covers the §7.2 line 125 deny path:
// the executor writes a tool_result(isError) carrying the deny reason
// back to the runtime instead of forwarding the call. F-7.2.18.
func TestPodExecutorToolUseDeny_spec_7_2(t *testing.T) {
	rt := &approvalRuntime{out: make(chan []byte, 8)}
	cl := dialPodAdapter(t, rt)
	reg := podsession.NewRegistry()
	reg.Put(&podsession.BindResult{SessionID: "sess-pod", TenantID: "acme", Adapter: cl})

	gate := &recordingGate{decision: executor.ApprovalDecision{Approved: false, Reason: "policy denied"}}
	pe := executor.NewPodExecutor(reg, nil)
	pe.SetApprovalGate(gate)

	out, err := pe.Send(context.Background(), "sess-pod", []executor.Message{{Role: "user", Content: "deploy"}})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(out.Parts) != 1 || out.Parts[0].Text != "denied" {
		t.Fatalf("Send output = %+v, want one \"denied\" part", out)
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.lastDeny == nil {
		t.Fatal("runtime never received a tool_result for the denied call")
	}
	var res struct {
		Type    string `json:"type"`
		ID      string `json:"id"`
		IsError bool   `json:"isError"`
		Content []struct {
			Type   string `json:"type"`
			Inline string `json:"inline"`
		} `json:"content"`
	}
	if err := json.Unmarshal(rt.lastDeny, &res); err != nil {
		t.Fatalf("deny frame is not valid JSON: %v", err)
	}
	if res.Type != "tool_result" || res.ID != "tc-1" || !res.IsError {
		t.Errorf("deny frame = %+v, want tool_result{id:tc-1, isError:true}", res)
	}
	if len(res.Content) != 1 || res.Content[0].Inline != "policy denied" {
		t.Errorf("deny content = %+v, want the deny reason", res.Content)
	}
}

// TestPodExecutorToolUseNoGate_spec_7_2 verifies the prior behavior is
// preserved when no gate is wired: the approval-required frame is skipped
// like any other intermediate frame and the response is returned.
func TestPodExecutorToolUseNoGate_spec_7_2(t *testing.T) {
	cl := dialPodAdapter(t, &approvalRuntime{out: make(chan []byte, 8), emitResponseEagerly: true})
	reg := podsession.NewRegistry()
	reg.Put(&podsession.BindResult{SessionID: "sess-pod", TenantID: "acme", Adapter: cl})

	pe := executor.NewPodExecutor(reg, nil) // no gate
	out, err := pe.Send(context.Background(), "sess-pod", []executor.Message{{Role: "user", Content: "deploy"}})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(out.Parts) != 1 || out.Parts[0].Text != "ack" {
		t.Errorf("Send output = %+v, want one \"ack\" part (frame skipped)", out)
	}
}
