// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"testing"
)

// spec: §8.3 — `lenny/delegate_task` MUST reject a malformed
// credentialPropagation value at the MCP ingress with
// INVALID_LEASE_FIELD, before the parent lookup runs, and MUST NOT
// commit a child session.
func TestDelegateTaskRejectsUnknownCredentialPropagation_spec_8_3(t *testing.T) {
	srv, store := newDelegateMCPWithChain(t, nil)

	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"child-agent","poolRef":"pool-b","task":{"input":[{"type":"text","inline":"do work"}]},"credentialPropagation":"share"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("invalid credentialPropagation must be a tool error: %+v", resp)
	}
	env := readLennyErrorEnvelope(t, result)
	if env["code"] != "INVALID_LEASE_FIELD" {
		t.Errorf("code = %v, want INVALID_LEASE_FIELD", env["code"])
	}
	details, _ := env["details"].(map[string]any)
	if details["field"] != "credentialPropagation" {
		t.Errorf("details.field = %v, want credentialPropagation", details["field"])
	}
	if details["value"] != "share" {
		t.Errorf("details.value = %v, want share", details["value"])
	}
	if _, err := store.Get(context.Background(), "acme", "sess_child"); err == nil {
		t.Error("invalid credentialPropagation must not commit a child session")
	}
}

// spec: §8.3 — an omitted credentialPropagation admits under the
// independent default. Confirms the default hop is not routed into the
// inherit-only compatibility gate and commits a child session.
func TestDelegateTaskAdmitsWithoutCredentialPropagation_spec_8_3(t *testing.T) {
	srv, store := newDelegateMCPWithChain(t, nil)

	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"child-agent","poolRef":"pool-b","task":{"input":[{"type":"text","inline":"do work"}]}}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] == true {
		t.Fatalf("omitted credentialPropagation must default to independent: %+v", resp)
	}
	if _, err := store.Get(context.Background(), "acme", "sess_child"); err != nil {
		t.Fatalf("default credentialPropagation must commit a child session: %v", err)
	}
}

// spec: §8.3 — `lenny/delegate_task` MUST accept the documented
// credentialPropagation enum values (independent, deny) at the wire
// boundary and commit a child session. inherit is exercised by the
// tier-4 cross-environment compatibility test.
func TestDelegateTaskAcceptsCredentialPropagationEnum_spec_8_3(t *testing.T) {
	for _, mode := range []string{"independent", "deny"} {
		t.Run(mode, func(t *testing.T) {
			srv, store := newDelegateMCPWithChain(t, nil)

			resp := call(t, srv.Handler(), "lenny/delegate_task",
				`{"parentSessionId":"sess_parent","target":"child-agent","poolRef":"pool-b","task":{"input":[{"type":"text","inline":"do work"}]},"credentialPropagation":"`+mode+`"}`)
			result, _ := resp["result"].(map[string]any)
			if result["isError"] == true {
				t.Fatalf("credentialPropagation=%s must be admitted: %+v", mode, resp)
			}
			if _, err := store.Get(context.Background(), "acme", "sess_child"); err != nil {
				t.Fatalf("credentialPropagation=%s must commit a child session: %v", mode, err)
			}
		})
	}
}
