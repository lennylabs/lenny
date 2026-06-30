// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// recordingScanner is an external (non-built-in) PreMessageDelivery /
// PreDelegation content scanner that records each invocation and returns
// a fixed result. It stands in for a deployer's external content
// classifier named by contentPolicy.interceptorRef.
type recordingScanner struct {
	name   string
	calls  *[]string
	result interceptor.Result
}

func (s recordingScanner) Name() string                       { return s.name }
func (s recordingScanner) Priority() int32                    { return 500 }
func (s recordingScanner) Builtin() bool                      { return false }
func (s recordingScanner) FailPolicy() interceptor.FailPolicy { return interceptor.FailClosed }
func (s recordingScanner) Timeout() time.Duration             { return 0 }
func (s recordingScanner) Intercept(context.Context, interceptor.Request) (interceptor.Result, error) {
	if s.calls != nil {
		*s.calls = append(*s.calls, s.name)
	}
	return s.result, nil
}

// fakeContentPolicy is a static mcptools.ContentPolicyResolver.
type fakeContentPolicy struct {
	max int
	ref string
	ok  bool
}

func (f fakeContentPolicy) ResolveContentPolicy(context.Context, string, string) (int, string, bool) {
	return f.max, f.ref, f.ok
}

func newMCPContentPolicy(t *testing.T, chain *interceptor.Chain, cp mcptools.ContentPolicyResolver) (*mcp.Server, sessionstore.Store) {
	t.Helper()
	store := memstore.New()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:           store,
		Executor:        executor.NewEchoExecutor(),
		Interceptors:    chain,
		ContentPolicies: cp,
		Clock:           func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:          func() string { return "sess_mcp" },
		TenantID:        "acme",
	})
	return srv, store
}

// spec: §4.8 line 1040 / §13.5 mitigation 3 — lenny/send_message enforces
// the target session's effective contentPolicy.maxInputSize on the
// message body, rejecting an oversize body with INPUT_TOO_LARGE before
// delivery. F-13.5.2.
func TestSendMessage_maxInputSize_spec_4_8_1040(t *testing.T) {
	chain := interceptor.NewChain()
	srv, store := newMCPContentPolicy(t, chain, fakeContentPolicy{max: 4, ref: "", ok: true})
	runningSession(t, store)

	resp := call(t, srv.Handler(), "lenny/send_message", `{"to":"sess_x","message":"hello"}`)
	if code := errorEnvelope(t, resp); code != "INPUT_TOO_LARGE" {
		t.Errorf("code = %q, want INPUT_TOO_LARGE for a 5-byte body over a 4-byte cap", code)
	}
}

// spec: §8.3 lines 157-188 / §4.8 line 1040 — only the policy-named
// external scanner runs at PreMessageDelivery; an external interceptor the
// policy does not name is not invoked. The named scanner's MODIFY rewrites
// the delivered body. F-8.2.9 / F-13.5.2.
func TestSendMessage_runsOnlyNamedScanner_spec_8_3_157(t *testing.T) {
	var calls []string
	chain := interceptor.NewChain()
	mustRegisterScanner(t, chain, recordingScanner{
		name: "scanner-a", calls: &calls,
		result: interceptor.Result{Action: interceptor.ActionModify, ModifiedContent: []byte("redacted")},
	})
	mustRegisterScanner(t, chain, recordingScanner{
		name: "scanner-b", calls: &calls,
		result: interceptor.Result{Action: interceptor.ActionAllow},
	})

	srv, store := newMCPContentPolicy(t, chain, fakeContentPolicy{ref: "scanner-a", ok: true})
	runningSession(t, store)

	resp := call(t, srv.Handler(), "lenny/send_message", `{"to":"sess_x","message":"ping"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] == true {
		t.Fatalf("unexpected error: %+v", result)
	}
	if len(calls) != 1 || calls[0] != "scanner-a" {
		t.Errorf("scanners called = %v, want only [scanner-a] (scanner-b must not run)", calls)
	}
	// The echo executor returns the delivered body; the MODIFY must have
	// rewritten it before delivery.
	content, _ := result["content"].([]any)
	joined := ""
	for _, raw := range content {
		block, _ := raw.(map[string]any)
		if txt, _ := block["text"].(string); txt != "" {
			joined += txt
		}
	}
	if !strings.Contains(joined, "redacted") {
		t.Errorf("delivered body did not carry the scanner MODIFY; content = %q", joined)
	}
}

// spec: §8.3 line 157 — a policy with interceptorRef: null runs no external
// content scanner; the message is delivered without invoking any registered
// scanner. F-13.5.2.
func TestSendMessage_nullRef_runsNoScanner_spec_8_3_157(t *testing.T) {
	var calls []string
	chain := interceptor.NewChain()
	mustRegisterScanner(t, chain, recordingScanner{
		name: "scanner-a", calls: &calls,
		result: interceptor.Result{Action: interceptor.ActionAllow},
	})

	srv, store := newMCPContentPolicy(t, chain, fakeContentPolicy{ref: "", ok: true})
	runningSession(t, store)

	resp := call(t, srv.Handler(), "lenny/send_message", `{"to":"sess_x","message":"ping"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] == true {
		t.Fatalf("unexpected error: %+v", result)
	}
	if len(calls) != 0 {
		t.Errorf("scanners called = %v, want none for interceptorRef: null", calls)
	}
}

// spec: §4.8 line 1032 — a contentPolicy.interceptorRef naming an
// interceptor not registered in this gateway process fails closed with
// INTERCEPTOR_TIMEOUT rather than silently delivering unscanned content.
// F-8.2.9 / F-13.5.2.
func TestSendMessage_unresolvableRef_failsClosed_spec_4_8_1032(t *testing.T) {
	chain := interceptor.NewChain()
	// scanner-a is registered, but the policy names a different (absent) ref.
	mustRegisterScanner(t, chain, recordingScanner{
		name:   "scanner-a",
		result: interceptor.Result{Action: interceptor.ActionAllow},
	})

	srv, store := newMCPContentPolicy(t, chain, fakeContentPolicy{ref: "ghost-scanner", ok: true})
	runningSession(t, store)

	resp := call(t, srv.Handler(), "lenny/send_message", `{"to":"sess_x","message":"ping"}`)
	if code := errorEnvelope(t, resp); code != interceptor.CodeInterceptorTimeout {
		t.Errorf("code = %q, want %q for an unresolvable interceptorRef", code, interceptor.CodeInterceptorTimeout)
	}
}

func mustRegisterScanner(t *testing.T, c *interceptor.Chain, s recordingScanner) {
	t.Helper()
	if err := c.Register(interceptor.PhasePreMessageDelivery, s); err != nil {
		t.Fatalf("register scanner %s: %v", s.name, err)
	}
}
