// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"testing"
)

// spec: §8.4 line 521 — `lenny/delegate_task` MUST short-circuit a
// lease declaring `approvalMode: "deny"` with the canonical
// `DELEGATION_DENIED` code before pod allocation and before the §4
// PreDelegation interceptor runs. F-8.4.1.
func TestDelegateTaskRejectsDenyApprovalMode_spec_8_4_521(t *testing.T) {
	srv, store := newDelegateMCPWithChain(t, nil)

	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"child-agent","poolRef":"pool-b","task":{"input":[{"type":"text","inline":"do work"}]},"approvalMode":"deny"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("approvalMode=deny must be a tool error: %+v", resp)
	}
	env := readLennyErrorEnvelope(t, result)
	if env["code"] != "DELEGATION_DENIED" {
		t.Errorf("code = %v, want DELEGATION_DENIED", env["code"])
	}
	// §8.4: no child session must be committed for a denied delegation.
	if _, err := store.Get(context.Background(), "acme", "sess_child"); err == nil {
		t.Error("denied delegation must not commit a child session")
	}
}

// spec: §8.4 — `lenny/delegate_task` MUST reject a malformed
// approvalMode value at the MCP ingress with INVALID_LEASE_FIELD,
// before the parent lookup runs. F-8.4.2.
func TestDelegateTaskRejectsUnknownApprovalMode_spec_8_4(t *testing.T) {
	srv, store := newDelegateMCPWithChain(t, nil)

	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"child-agent","poolRef":"pool-b","approvalMode":"ALLOW"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("invalid approvalMode must be a tool error: %+v", resp)
	}
	env := readLennyErrorEnvelope(t, result)
	if env["code"] != "INVALID_LEASE_FIELD" {
		t.Errorf("code = %v, want INVALID_LEASE_FIELD", env["code"])
	}
	details, _ := env["details"].(map[string]any)
	if details["field"] != "approvalMode" {
		t.Errorf("details.field = %v, want approvalMode", details["field"])
	}
	if details["value"] != "ALLOW" {
		t.Errorf("details.value = %v, want ALLOW", details["value"])
	}
	if _, err := store.Get(context.Background(), "acme", "sess_child"); err == nil {
		t.Error("invalid approvalMode must not commit a child session")
	}
}

// spec: §8.4 line 520 — `lenny/delegate_task` MUST accept
// `approvalMode: "approval"` at the wire boundary and alias it to
// the policy auto-approval path in v1 (no dedicated approval API
// today). The child session MUST be created. F-8.4.1.
func TestDelegateTaskAcceptsApprovalAliasedToPolicy_spec_8_4_520(t *testing.T) {
	srv, store := newDelegateMCPWithChain(t, nil)

	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"child-agent","poolRef":"pool-b","task":{"input":[{"type":"text","inline":"do work"}]},"approvalMode":"approval"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] == true {
		t.Fatalf("approvalMode=approval must be admitted (v1 alias to policy): %+v", resp)
	}
	if _, err := store.Get(context.Background(), "acme", "sess_child"); err != nil {
		t.Fatalf("approvalMode=approval must commit a child session: %v", err)
	}
}

// spec: §8.4 — an omitted approvalMode admits under the spec default
// (policy auto-approve). Confirms the v1 default is not regressed.
func TestDelegateTaskAdmitsWithoutApprovalMode_spec_8_4(t *testing.T) {
	srv, store := newDelegateMCPWithChain(t, nil)

	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"child-agent","poolRef":"pool-b","task":{"input":[{"type":"text","inline":"do work"}]}}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] == true {
		t.Fatalf("omitted approvalMode must default to policy: %+v", resp)
	}
	if _, err := store.Get(context.Background(), "acme", "sess_child"); err != nil {
		t.Fatalf("default approvalMode must commit a child session: %v", err)
	}
}
