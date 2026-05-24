// SPDX-License-Identifier: MIT

package interceptor_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
)

// modifyTo returns a fakeInterceptor that rewrites the request content to
// the supplied bytes via ActionModify.
func modifyTo(name string, priority int32, content []byte) *fakeInterceptor {
	return &fakeInterceptor{
		name:     name,
		priority: priority,
		fn: func(_ context.Context, _ interceptor.Request) (interceptor.Result, error) {
			return interceptor.Result{Action: interceptor.ActionModify, ModifiedContent: content}, nil
		},
	}
}

// TestChainModifyImmutableFieldViolation covers spec §4.8 line 1060: a
// MODIFY that alters a per-phase immutable field is rejected with
// INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION, the chain short-circuits, and the
// original payload is preserved.
func TestChainModifyImmutableFieldViolation(t *testing.T) {
	cases := []struct {
		name    string
		phase   interceptor.Phase
		pre     string
		post    string
		wantRej bool
	}{
		{
			name:    "PreToolResult alters id",
			phase:   interceptor.PhasePreToolResult,
			pre:     `{"id":"call_1","content":[],"isError":false}`,
			post:    `{"id":"call_2","content":[],"isError":false}`,
			wantRej: true,
		},
		{
			name:    "PreToolResult modifies content only",
			phase:   interceptor.PhasePreToolResult,
			pre:     `{"id":"call_1","content":["a"]}`,
			post:    `{"id":"call_1","content":["redacted"]}`,
			wantRej: false,
		},
		{
			name:    "PreConnectorRequest alters connector_id",
			phase:   interceptor.PhasePreConnectorRequest,
			pre:     `{"tool_name":"list","arguments":{},"connector_id":"github"}`,
			post:    `{"tool_name":"list","arguments":{},"connector_id":"gitlab"}`,
			wantRej: true,
		},
		{
			name:    "PreConnectorRequest alters tool_name",
			phase:   interceptor.PhasePreConnectorRequest,
			pre:     `{"tool_name":"list","arguments":{},"connector_id":"github"}`,
			post:    `{"tool_name":"delete","arguments":{},"connector_id":"github"}`,
			wantRej: true,
		},
		{
			name:    "PreConnectorRequest redacts arguments only",
			phase:   interceptor.PhasePreConnectorRequest,
			pre:     `{"tool_name":"list","arguments":{"q":"secret"},"connector_id":"github"}`,
			post:    `{"tool_name":"list","arguments":{"q":"***"},"connector_id":"github"}`,
			wantRej: false,
		},
		{
			name:    "PostAuth alters metadata.user_id",
			phase:   interceptor.PhasePostAuth,
			pre:     `{"metadata":{"user_id":"alice","tenant_id":"acme"}}`,
			post:    `{"metadata":{"user_id":"bob","tenant_id":"acme"}}`,
			wantRej: true,
		},
		{
			name:    "PostAuth injects an additional claim",
			phase:   interceptor.PhasePostAuth,
			pre:     `{"metadata":{"user_id":"alice","tenant_id":"acme"}}`,
			post:    `{"metadata":{"user_id":"alice","tenant_id":"acme","extra":"claim"}}`,
			wantRej: false,
		},
		{
			name:    "PostRoute alters resolved_runtime_name",
			phase:   interceptor.PhasePostRoute,
			pre:     `{"resolved_runtime_name":"claude","credential_pool_id":"p1"}`,
			post:    `{"resolved_runtime_name":"gpt","credential_pool_id":"p1"}`,
			wantRej: true,
		},
		{
			name:    "PreRoute removes immutable tenant_id",
			phase:   interceptor.PhasePreRoute,
			pre:     `{"tenant_id":"acme","user_id":"alice","input":[]}`,
			post:    `{"user_id":"alice","input":["x"]}`,
			wantRej: true,
		},
		{
			name:    "PreRoute modifies input keeps identity",
			phase:   interceptor.PhasePreRoute,
			pre:     `{"tenant_id":"acme","user_id":"alice","input":["a"]}`,
			post:    `{"tenant_id":"acme","user_id":"alice","input":["a","preamble"]}`,
			wantRej: false,
		},
		{
			name:    "fully-mutable PreDelegation array payload",
			phase:   interceptor.PhasePreDelegation,
			pre:     `[{"type":"text","text":"a"}]`,
			post:    `[{"type":"text","text":"b"}]`,
			wantRej: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := interceptor.NewChain()
			mustRegister(t, c, tc.phase, modifyTo("mod", 200, []byte(tc.post)))
			res := c.Run(context.Background(), interceptor.Request{Phase: tc.phase, Content: []byte(tc.pre)})
			if tc.wantRej {
				if res.Action != interceptor.ActionReject {
					t.Fatalf("action = %v, want REJECT", res.Action)
				}
				if res.Code != interceptor.CodeInterceptorImmutableFieldViolation {
					t.Errorf("code = %q, want %q", res.Code, interceptor.CodeInterceptorImmutableFieldViolation)
				}
				if string(res.ModifiedContent) != tc.pre {
					t.Errorf("preserved payload = %q, want original %q", res.ModifiedContent, tc.pre)
				}
				if res.RejectedBy != "mod" {
					t.Errorf("RejectedBy = %q, want %q", res.RejectedBy, "mod")
				}
			} else {
				if res.Action != interceptor.ActionModify {
					t.Fatalf("action = %v, want MODIFY", res.Action)
				}
				if string(res.ModifiedContent) != tc.post {
					t.Errorf("modified payload = %q, want %q", res.ModifiedContent, tc.post)
				}
			}
		})
	}
}

// TestChainModifyViolationShortCircuits confirms a downstream interceptor
// never observes a payload an upstream MODIFY illegally rewrote (spec:
// §4.8 line 1060: no subsequent interceptor sees the illegal modification).
func TestChainModifyViolationShortCircuits(t *testing.T) {
	c := interceptor.NewChain()
	var calls []string
	mustRegister(t, c, interceptor.PhasePreToolResult, &fakeInterceptor{
		name: "bad", priority: 200, calls: &calls,
		fn: func(_ context.Context, _ interceptor.Request) (interceptor.Result, error) {
			return interceptor.Result{Action: interceptor.ActionModify, ModifiedContent: []byte(`{"id":"call_2"}`)}, nil
		},
	})
	mustRegister(t, c, interceptor.PhasePreToolResult, &fakeInterceptor{name: "downstream", priority: 300, calls: &calls})

	res := c.Run(context.Background(), interceptor.Request{
		Phase:   interceptor.PhasePreToolResult,
		Content: []byte(`{"id":"call_1"}`),
	})
	if res.Action != interceptor.ActionReject {
		t.Fatalf("action = %v, want REJECT", res.Action)
	}
	if !equal(calls, []string{"bad"}) {
		t.Errorf("calls = %v, want only [bad] (downstream must not run)", calls)
	}
}

// TestChainModifyMalformedRejected confirms a MODIFY that returns a
// non-object payload at a phase with immutable fields is rejected, since
// the structure carrying those fields was destroyed.
func TestChainModifyMalformedRejected(t *testing.T) {
	c := interceptor.NewChain()
	mustRegister(t, c, interceptor.PhasePreToolResult, modifyTo("mod", 200, []byte(`not json`)))
	res := c.Run(context.Background(), interceptor.Request{
		Phase:   interceptor.PhasePreToolResult,
		Content: []byte(`{"id":"call_1"}`),
	})
	if res.Action != interceptor.ActionReject || res.Code != interceptor.CodeInterceptorImmutableFieldViolation {
		t.Fatalf("action=%v code=%q, want REJECT/%s", res.Action, res.Code, interceptor.CodeInterceptorImmutableFieldViolation)
	}
}

// TestPhaseDefaultTimeoutApplied covers spec §4.8 lines 1075, 1077: the
// LLM phases default to 100ms and the connector phases to 200ms, while
// other phases keep the 500ms default, observed via the per-call deadline
// each interceptor sees.
func TestPhaseDefaultTimeoutApplied(t *testing.T) {
	cases := []struct {
		phase interceptor.Phase
		want  time.Duration
	}{
		{interceptor.PhasePreLLMRequest, interceptor.DefaultLLMTimeout},
		{interceptor.PhasePostLLMResponse, interceptor.DefaultLLMTimeout},
		{interceptor.PhasePreConnectorRequest, interceptor.DefaultConnectorTimeout},
		{interceptor.PhasePostConnectorResponse, interceptor.DefaultConnectorTimeout},
		{interceptor.PhasePostAuth, interceptor.DefaultTimeout},
		{interceptor.PhasePreDelegation, interceptor.DefaultTimeout},
	}
	for _, tc := range cases {
		t.Run(string(tc.phase), func(t *testing.T) {
			c := interceptor.NewChain()
			var observed time.Duration
			mustRegister(t, c, tc.phase, &fakeInterceptor{
				name: "probe", priority: 200,
				fn: func(ctx context.Context, _ interceptor.Request) (interceptor.Result, error) {
					if dl, ok := ctx.Deadline(); ok {
						observed = time.Until(dl)
					}
					return interceptor.Result{Action: interceptor.ActionAllow}, nil
				},
			})
			c.Run(context.Background(), interceptor.Request{Phase: tc.phase})
			// The observed remaining time is slightly under the configured
			// deadline; assert it lands in (want/2, want].
			if observed <= tc.want/2 || observed > tc.want {
				t.Errorf("phase %s deadline ~= %v, want close to %v", tc.phase, observed, tc.want)
			}
		})
	}
}

// TestExplicitTimeoutOverridesPhaseDefault confirms an interceptor's own
// positive Timeout() takes precedence over the phase default (spec: §4.8
// line 1075 "Deployers may override this via the timeout field").
func TestExplicitTimeoutOverridesPhaseDefault(t *testing.T) {
	c := interceptor.NewChain()
	var observed time.Duration
	mustRegister(t, c, interceptor.PhasePreLLMRequest, &fakeInterceptor{
		name: "slow", priority: 200, timeout: 350 * time.Millisecond,
		fn: func(ctx context.Context, _ interceptor.Request) (interceptor.Result, error) {
			if dl, ok := ctx.Deadline(); ok {
				observed = time.Until(dl)
			}
			return interceptor.Result{Action: interceptor.ActionAllow}, nil
		},
	})
	c.Run(context.Background(), interceptor.Request{Phase: interceptor.PhasePreLLMRequest})
	if observed <= interceptor.DefaultLLMTimeout {
		t.Errorf("deadline ~= %v, want > the 100ms LLM default (explicit 350ms override)", observed)
	}
}
