// SPDX-License-Identifier: MIT

package policy

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
)

// spec: §4.8 lines 972, 1025–1028 — AuthEvaluator admits a request that
// carries an authenticated tenant and fails closed when none is present.
func TestAuthEvaluator_IdentityGate_spec_4_8_972(t *testing.T) {
	t.Parallel()
	e := NewAuthEvaluator()

	cases := []struct {
		name       string
		req        interceptor.Request
		wantAction interceptor.Action
	}{
		{
			name:       "tenant in metadata",
			req:        interceptor.Request{Metadata: map[string]string{MetadataTenantID: "acme", MetadataUserID: "alice"}},
			wantAction: interceptor.ActionAllow,
		},
		{
			name:       "tenant on request field only",
			req:        interceptor.Request{TenantID: "acme"},
			wantAction: interceptor.ActionAllow,
		},
		{
			name:       "no tenant fails closed",
			req:        interceptor.Request{Metadata: map[string]string{MetadataUserID: "alice"}},
			wantAction: interceptor.ActionReject,
		},
		{
			name:       "blank tenant fails closed",
			req:        interceptor.Request{Metadata: map[string]string{MetadataTenantID: "  "}},
			wantAction: interceptor.ActionReject,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := e.Intercept(context.Background(), tc.req)
			if err != nil {
				t.Fatalf("Intercept: %v", err)
			}
			if res.Action != tc.wantAction {
				t.Fatalf("action = %v, want %v", res.Action, tc.wantAction)
			}
			if tc.wantAction == interceptor.ActionReject && res.Code != CodeAuthRequired {
				t.Fatalf("code = %q, want %q", res.Code, CodeAuthRequired)
			}
		})
	}
}

// spec: §4.8 lines 972, 1021, 1023 — AuthEvaluator is a built-in at the
// reserved priority 100 and may register on the PreAuth phase, which
// external interceptors cannot.
func TestAuthEvaluator_Contract_spec_4_8_1021(t *testing.T) {
	t.Parallel()
	e := NewAuthEvaluator()
	if e.Priority() != AuthEvaluatorPriority || e.Priority() != 100 {
		t.Fatalf("priority = %d, want 100", e.Priority())
	}
	if !e.Builtin() {
		t.Fatal("Builtin() = false, want true")
	}
	if e.FailPolicy() != interceptor.FailClosed {
		t.Fatalf("FailPolicy = %q, want fail-closed", e.FailPolicy())
	}
	chain := interceptor.NewChain()
	if err := chain.Register(interceptor.PhasePreAuth, e); err != nil {
		t.Fatalf("Register on PreAuth: %v", err)
	}
}
