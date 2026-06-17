// SPDX-License-Identifier: MIT

package sandboxclaim_guard

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// spec: §4.6.1 (sandboxclaim-guard webhook) — a CREATE with no sibling
// claim for the Sandbox is admitted.
func TestCreateAdmitsWhenNoExistingClaim(t *testing.T) {
	d, err := Decide(Request{
		Operation:  OpCreate,
		ClaimName:  "claim-pod-1",
		SandboxRef: "sandbox-1",
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if !d.Allowed {
		t.Errorf("CREATE with no existing claim should be allowed, got %v", d)
	}
	if d.Code != 200 {
		t.Errorf("Code: want 200, got %d", d.Code)
	}
}

// spec: §4.6.1 — only `released`/`failed` claims are terminal, so a
// CREATE whose only siblings are terminal is admitted.
func TestCreateAdmitsWhenExistingClaimsAreTerminal(t *testing.T) {
	d, err := Decide(Request{
		Operation:  OpCreate,
		ClaimName:  "claim-pod-2",
		SandboxRef: "sandbox-1",
		ExistingClaims: []ExistingClaim{
			{Name: "claim-pod-old", Status: ClaimReleased},
			{Name: "claim-pod-older", Status: ClaimFailed},
		},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if !d.Allowed {
		t.Errorf("CREATE with terminal existing claims should be allowed, got %v", d)
	}
}

// spec: §4.6.1 — per-pod uniqueness: a second non-terminal claim for the
// same Sandbox is rejected with the spec-mandated 403 message. Every
// live binding state (bound, recycling, reserved) triggers the rule.
func TestCreateRejectsWhenNonTerminalClaimExists(t *testing.T) {
	cases := []ClaimStatus{ClaimBound, ClaimRecycling, ClaimReserved}
	for _, s := range cases {
		t.Run(string(s), func(t *testing.T) {
			d, err := Decide(Request{
				Operation:  OpCreate,
				ClaimName:  "claim-pod-2",
				SandboxRef: "sandbox-1",
				ExistingClaims: []ExistingClaim{
					{Name: "claim-pod-1", Status: s},
				},
			})
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			if d.Allowed {
				t.Errorf("CREATE with non-terminal existing claim should be rejected")
			}
			if d.Code != 403 {
				t.Errorf("Code: want 403, got %d", d.Code)
			}
			if !strings.Contains(d.Reason, "SandboxClaim already exists for Sandbox sandbox-1") {
				t.Errorf("Reason does not match spec §4.6.1 wording: %q", d.Reason)
			}
		})
	}
}

// TestCreateRejectsConcurrentClaimWithNoExemption is the proposal 0002
// regression: per-pod uniqueness has no concurrency exemption. A pool
// with maxConcurrentSessions > 1 multiplexes its sessions onto the
// single per-pod claim (§5.2), so a second non-terminal claim for the
// same Sandbox is a duplicate regardless of any slot marker. The guard
// reads no phase and accepts no slot signal. spec: §4.6.1, §5.2.
func TestCreateRejectsConcurrentClaimWithNoExemption(t *testing.T) {
	d, err := Decide(Request{
		Operation:  OpCreate,
		ClaimName:  "claim-pod-2",
		SandboxRef: "sandbox-1",
		ExistingClaims: []ExistingClaim{
			{Name: "claim-pod-1", Status: ClaimBound},
		},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if d.Allowed {
		t.Errorf("a second non-terminal claim must be rejected with no concurrency exemption, got %v", d)
	}
	if d.Code != 403 {
		t.Errorf("Code: want 403, got %d", d.Code)
	}
}

// spec: §4.6.1 — the first non-terminal sibling short-circuits the
// rejection even when terminal siblings precede it in the list.
func TestCreateRejectsWhenAnySiblingIsNonTerminal(t *testing.T) {
	d, err := Decide(Request{
		Operation:  OpCreate,
		ClaimName:  "claim-pod-2",
		SandboxRef: "sandbox-1",
		ExistingClaims: []ExistingClaim{
			{Name: "claim-pod-released", Status: ClaimReleased},
			{Name: "claim-pod-live", Status: ClaimRecycling},
		},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if d.Allowed {
		t.Errorf("a non-terminal sibling among terminal ones must reject the CREATE, got %v", d)
	}
}

// spec: §4.6.1 — SandboxRef is required input; an empty ref is a
// programming error surfaced as ErrMissingSandboxRef rather than a
// rule-based rejection.
func TestDecideRejectsMissingSandboxRef(t *testing.T) {
	_, err := Decide(Request{Operation: OpCreate, ClaimName: "claim-pod-1"})
	if !errors.Is(err, ErrMissingSandboxRef) {
		t.Errorf("expected ErrMissingSandboxRef, got %v", err)
	}
}

// TestDecideRejectsNonCreateOperation guards the CREATE-only contract:
// the webhook is registered on CREATE only, so any other operation
// reaching Decide is a programming error. spec: §4.6.1 — PATCH/PUT are
// admitted without inspection and are not registered with the webhook.
func TestDecideRejectsNonCreateOperation(t *testing.T) {
	for _, op := range []Operation{"PATCH", "PUT", "UPDATE", "DELETE", ""} {
		t.Run(string(op), func(t *testing.T) {
			_, err := Decide(Request{Operation: op, SandboxRef: "sandbox-1"})
			if err == nil {
				t.Errorf("operation %q should return an error from a CREATE-only guard", op)
			}
		})
	}
}

// TestSandboxClaimCRDEnumCoversEveryClaimStatus guards against the
// F-4.6.10 drift: every ClaimStatus the guard recognizes must appear in
// the CRD OpenAPI enum, otherwise the API server rejects any status patch
// setting that binding state. spec: §4.6.3 — the SandboxClaim status.phase
// enumeration lists bound, recycling, reserved, released, failed.
func TestSandboxClaimCRDEnumCoversEveryClaimStatus(t *testing.T) {
	crdPath := filepath.Join("..", "..", "..", "charts", "lenny", "crds", "lenny.dev_sandboxclaims.yaml")
	data, err := os.ReadFile(crdPath)
	if err != nil {
		t.Fatalf("read CRD %s: %v", crdPath, err)
	}
	crd := string(data)
	for _, s := range []ClaimStatus{ClaimBound, ClaimRecycling, ClaimReserved, ClaimReleased, ClaimFailed} {
		if !strings.Contains(crd, "- "+string(s)) {
			t.Errorf("SandboxClaim CRD phase enum is missing %q; the API server will reject writes setting that value", s)
		}
	}
}

// spec: §4.6.3 — only released and failed are terminal binding states.
func TestClaimStatusIsTerminal(t *testing.T) {
	cases := map[ClaimStatus]bool{
		ClaimBound:     false,
		ClaimRecycling: false,
		ClaimReserved:  false,
		ClaimReleased:  true,
		ClaimFailed:    true,
	}
	for s, want := range cases {
		if got := s.IsTerminal(); got != want {
			t.Errorf("ClaimStatus(%q).IsTerminal() = %v, want %v", s, got, want)
		}
	}
}
