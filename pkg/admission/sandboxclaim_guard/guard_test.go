// SPDX-License-Identifier: MIT

package sandboxclaim_guard

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateAdmitsWhenNoExistingClaim(t *testing.T) {
	d, err := Decide(Request{
		Operation:  OpCreate,
		ClaimName:  "claim-1",
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

func TestCreateAdmitsWhenExistingClaimsAreTerminal(t *testing.T) {
	d, err := Decide(Request{
		Operation:  OpCreate,
		ClaimName:  "claim-2",
		SandboxRef: "sandbox-1",
		ExistingClaims: []ExistingClaim{
			{Name: "claim-1", Status: ClaimReleased},
			{Name: "claim-old", Status: ClaimFailed},
		},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if !d.Allowed {
		t.Errorf("CREATE with terminal existing claims should be allowed, got %v", d)
	}
}

func TestCreateRejectsWhenNonTerminalClaimExists(t *testing.T) {
	cases := []ClaimStatus{ClaimBound, ClaimActive}
	for _, s := range cases {
		t.Run(string(s), func(t *testing.T) {
			d, err := Decide(Request{
				Operation:  OpCreate,
				ClaimName:  "claim-2",
				SandboxRef: "sandbox-1",
				ExistingClaims: []ExistingClaim{
					{Name: "claim-1", Status: s},
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

// spec: §5.2 — a slot-bearing claim is concurrent-mode by definition.
// The duplicate-claim rule from §4.6.1 (added to prevent the
// Postgres-fallback claim path from racing with the CRD claim path
// for session-mode pods) does not apply: concurrent-mode dispatch
// opens up to maxConcurrent simultaneous claims against the same
// Sandbox, and the maxConcurrent cap is enforced upstream by the
// gateway's Redis Lua slot counter (§5.2 atomic GET-compare-INCR).
// The webhook must admit a slot-bearing claim even when an existing
// non-terminal sibling claim references the same Sandbox.
func TestCreateAdmitsSlotClaimEvenWithExistingSiblings(t *testing.T) {
	d, err := Decide(Request{
		Operation:    OpCreate,
		ClaimName:    "claim-session-2",
		SandboxRef:   "sandbox-1",
		HasSlotID:    true,
		SandboxPhase: PhaseIdle, // first slot: phase mirror has not yet been patched
		ExistingClaims: []ExistingClaim{
			{Name: "claim-session-1", Status: ClaimBound},
		},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if !d.Allowed {
		t.Errorf("CREATE of a slot-bearing claim must be admitted regardless of siblings: %v", d)
	}
}

// spec: §5.2 — the first slot reservation transitions the Sandbox
// from idle to slot_active. The phase patch is the SSA mirror of the
// Redis counter and lands after the SandboxClaim CREATE, so the
// webhook must admit the first slot-bearing claim while the phase
// still reads idle. The slot-counter (not the webhook) enforces the
// maxConcurrent cap.
func TestCreateAdmitsFirstSlotClaimWithIdlePhase(t *testing.T) {
	d, err := Decide(Request{
		Operation:    OpCreate,
		ClaimName:    "claim-session-1",
		SandboxRef:   "sandbox-1",
		HasSlotID:    true,
		SandboxPhase: PhaseIdle,
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if !d.Allowed {
		t.Errorf("first slot-bearing claim against idle Sandbox must be admitted: %v", d)
	}
}

// spec: §4.6.1 — a session-mode (non-slot) duplicate must still be
// rejected. HasSlotID=false leaves the original session-mode rule in
// force.
func TestCreateRejectsSessionDuplicateEvenWithSlotIDFlagDefault(t *testing.T) {
	d, err := Decide(Request{
		Operation:  OpCreate,
		ClaimName:  "claim-2",
		SandboxRef: "sandbox-1",
		HasSlotID:  false,
		ExistingClaims: []ExistingClaim{
			{Name: "claim-1", Status: ClaimBound},
		},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if d.Allowed {
		t.Errorf("session-mode duplicate must still be rejected when HasSlotID=false")
	}
}

func TestPatchAdmitsWhenSandboxClaimed(t *testing.T) {
	d, err := Decide(Request{
		Operation:    OpPatch,
		ClaimName:    "claim-1",
		SandboxRef:   "sandbox-1",
		SandboxPhase: PhaseClaimed,
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if !d.Allowed {
		t.Errorf("PATCH with sandbox in claimed should be allowed, got %v", d)
	}
}

func TestPutAdmitsWhenSandboxClaimed(t *testing.T) {
	d, err := Decide(Request{
		Operation:    OpPut,
		ClaimName:    "claim-1",
		SandboxRef:   "sandbox-1",
		SandboxPhase: PhaseClaimed,
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if !d.Allowed {
		t.Errorf("PUT with sandbox in claimed should be allowed, got %v", d)
	}
}

func TestPatchRejectsWhenSandboxNotClaimed(t *testing.T) {
	stalePhases := []SandboxPhase{
		PhaseIdle, PhaseReceivingUploads, PhaseFinalizingWorkspace,
		PhaseRunningSetup, PhaseAttached, PhaseDraining,
		PhaseTerminated, PhaseFailed, PhaseWarming,
	}
	for _, p := range stalePhases {
		t.Run(string(p), func(t *testing.T) {
			d, err := Decide(Request{
				Operation:    OpPatch,
				ClaimName:    "claim-1",
				SandboxRef:   "sandbox-1",
				SandboxPhase: p,
			})
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			if d.Allowed {
				t.Errorf("PATCH with sandbox in %q should be rejected", p)
			}
			if d.Code != 403 {
				t.Errorf("Code: want 403, got %d", d.Code)
			}
			if !strings.Contains(d.Reason, "SandboxClaim stale: referenced Sandbox sandbox-1") {
				t.Errorf("Reason does not match spec §4.6.1 wording: %q", d.Reason)
			}
			if !strings.Contains(d.Reason, string(p)) {
				t.Errorf("Reason should embed the observed phase %q: %q", p, d.Reason)
			}
		})
	}
}

func TestPatchAdmitsClaimUnderDeletion(t *testing.T) {
	// A SandboxClaim being deleted is exempt from the staleness rule
	// even when its referenced Sandbox is gone or past the claimed
	// phase, so a finalizer-removal write can release the deletion.
	for _, op := range []Operation{OpPatch, OpPut} {
		t.Run(string(op), func(t *testing.T) {
			d, err := Decide(Request{
				Operation:     op,
				ClaimName:     "claim-1",
				SandboxRef:    "sandbox-gone",
				SandboxPhase:  "",
				UnderDeletion: true,
			})
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			if !d.Allowed {
				t.Errorf("%s on a claim under deletion should be allowed, got %v", op, d)
			}
			if d.Code != 200 {
				t.Errorf("Code: want 200, got %d", d.Code)
			}
		})
	}
}

func TestDecideRejectsMissingSandboxRef(t *testing.T) {
	_, err := Decide(Request{Operation: OpCreate, ClaimName: "claim-1"})
	if !errors.Is(err, ErrMissingSandboxRef) {
		t.Errorf("expected ErrMissingSandboxRef, got %v", err)
	}
}

func TestDecideRejectsUnsupportedOperation(t *testing.T) {
	_, err := Decide(Request{Operation: "DELETE", SandboxRef: "sandbox-1"})
	if err == nil {
		t.Errorf("unsupported operation should return an error")
	}
}

// TestSandboxClaimCRDEnumCoversEveryClaimStatus guards against the
// F-4.6.10 drift: the guard recognizes `active` but the CRD OpenAPI
// enum must admit it too, otherwise the API server rejects any write
// setting status.phase: active. spec: §4.6.3 — the SandboxClaim
// status.phase enumeration lists bound, active, released, failed.
func TestSandboxClaimCRDEnumCoversEveryClaimStatus(t *testing.T) {
	crdPath := filepath.Join("..", "..", "..", "charts", "lenny", "crds", "lenny.dev_sandboxclaims.yaml")
	data, err := os.ReadFile(crdPath)
	if err != nil {
		t.Fatalf("read CRD %s: %v", crdPath, err)
	}
	crd := string(data)
	for _, s := range []ClaimStatus{ClaimBound, ClaimActive, ClaimReleased, ClaimFailed} {
		if !strings.Contains(crd, "- "+string(s)) {
			t.Errorf("SandboxClaim CRD phase enum is missing %q; the API server will reject writes setting that value", s)
		}
	}
}

func TestClaimStatusIsTerminal(t *testing.T) {
	cases := map[ClaimStatus]bool{
		ClaimBound:    false,
		ClaimActive:   false,
		ClaimReleased: true,
		ClaimFailed:   true,
	}
	for s, want := range cases {
		if got := s.IsTerminal(); got != want {
			t.Errorf("ClaimStatus(%q).IsTerminal() = %v, want %v", s, got, want)
		}
	}
}
