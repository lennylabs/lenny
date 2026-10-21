// SPDX-License-Identifier: MIT

package erasurejob_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob"
)

// TestRunnerResumesFromPersistedPhase_spec_12_8 pins crash resume: Run
// starts at the job's persisted phase and re-runs no step of an earlier
// phase. A job resumed in verifying with ErasedAt zero runs Verify exactly
// once, before the receipt, and a job with ErasedAt set runs only the
// receipt and Complete.
// spec: 12.8 (Erasure job phase field (crash recovery)), 12.8 (Processing restriction during erasure (GDPR Article 18))
func TestRunnerResumesFromPersistedPhase_spec_12_8(t *testing.T) {
	cases := []struct {
		name  string
		setup func(h *runHarness, id string)
		// want maps each step event to its call count.
		want map[string]int
	}{
		{
			name:  "initiated",
			setup: func(*runHarness, string) {},
			want:  map[string]int{"preflight": 1, "drain:acme": 1, "hold": 1, "delete:acme": 1, "pseudonymize:acme": 1, "verify:acme": 1, "receipt:acme": 1},
		},
		{
			name: "store_deleting",
			setup: func(h *runHarness, id string) {
				h.moveTo(id, erasurejob.PhaseStoreDeleting, nil)
			},
			want: map[string]int{"preflight": 0, "drain:acme": 0, "hold": 0, "delete:acme": 1, "pseudonymize:acme": 1, "verify:acme": 1, "receipt:acme": 1},
		},
		{
			name: "pseudonymizing",
			setup: func(h *runHarness, id string) {
				h.moveTo(id, erasurejob.PhaseStoreDeleting, nil)
				h.moveTo(id, erasurejob.PhasePseudonymizing, nil)
			},
			want: map[string]int{"preflight": 0, "drain:acme": 0, "hold": 0, "delete:acme": 0, "pseudonymize:acme": 1, "verify:acme": 1, "receipt:acme": 1},
		},
		{
			name: "verifying",
			setup: func(h *runHarness, id string) {
				h.moveTo(id, erasurejob.PhaseStoreDeleting, nil)
				h.moveTo(id, erasurejob.PhasePseudonymizing, nil)
				h.moveTo(id, erasurejob.PhaseVerifying, func(j *erasurejob.Job) {
					j.Billing = erasurejob.BillingErasureOutcome{Disposition: "pseudonymized", Pseudonymized: 2}
				})
			},
			want: map[string]int{"preflight": 0, "drain:acme": 0, "hold": 0, "delete:acme": 0, "pseudonymize:acme": 0, "verify:acme": 1, "receipt:acme": 1},
		},
		{
			name: "erased",
			setup: func(h *runHarness, id string) {
				h.moveTo(id, erasurejob.PhaseStoreDeleting, nil)
				h.moveTo(id, erasurejob.PhasePseudonymizing, nil)
				h.moveTo(id, erasurejob.PhaseVerifying, func(j *erasurejob.Job) {
					j.Billing = erasurejob.BillingErasureOutcome{Disposition: "pseudonymized", Verified: true}
					j.ErasedAt = h.clock()
				})
			},
			want: map[string]int{"preflight": 0, "drain:acme": 0, "hold": 0, "delete:acme": 0, "pseudonymize:acme": 0, "verify:acme": 0, "receipt:acme": 1},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newRunHarness(t)
			j := h.start("acme", "alice", nil)
			tc.setup(h, j.ID)
			if err := h.runner.Run(context.Background(), j.ID); err != nil {
				t.Fatalf("Run: %v", err)
			}
			for ev, n := range tc.want {
				if got := h.count(ev); got != n {
					t.Errorf("%s ran %d times, want %d (events %v)", ev, got, n, h.order())
				}
			}
			ev := h.order()
			if v, r := indexOf(ev, "verify:acme"), indexOf(ev, "receipt:acme"); v >= 0 && v > r {
				t.Errorf("Verify ran after the receipt: %v", ev)
			}
			got := h.job(j.ID)
			if got.Phase != erasurejob.PhaseCompleted {
				t.Errorf("phase = %s, want completed", got.Phase)
			}
			if r, _ := h.restricted("acme", "alice"); r {
				t.Error("restriction still set after completion")
			}
		})
	}
}

// TestRunnerMissingHookFailsJob_spec_12_8 pins the missing-hook rule: a
// runner with no session-drain hook, or no legal-hold re-check hook, fails
// the job under store_delete, increments the failure counter once, leaves
// the restriction set, and runs no eraser.
// spec: 12.8 (Processing restriction during erasure (GDPR Article 18)), 12.8 (DeleteByUser dependency-ordered deletion sequence)
func TestRunnerMissingHookFailsJob_spec_12_8(t *testing.T) {
	for _, tc := range []struct {
		name  string
		strip func(r *erasurejob.Runner)
		cause error
	}{
		{"no drain hook", func(r *erasurejob.Runner) { r.WithSessionDrain(nil) }, erasurejob.ErrSessionDrainUnwired},
		{"no hold-check hook", func(r *erasurejob.Runner) { r.WithHoldCheck(nil) }, erasurejob.ErrHoldCheckUnwired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newRunHarness(t)
			tc.strip(h.runner)
			j := h.start("acme", "alice", nil)
			if err := h.runner.Run(context.Background(), j.ID); !errors.Is(err, tc.cause) {
				t.Fatalf("Run = %v, want %v", err, tc.cause)
			}
			got := h.job(j.ID)
			if got.Phase != erasurejob.PhaseFailed {
				t.Errorf("phase = %s, want failed", got.Phase)
			}
			if f := h.failureCount(); len(f) != 1 || f[0] != "acme:"+erasurejob.FailurePhaseStoreDelete {
				t.Errorf("failures = %v, want one acme:store_delete", f)
			}
			if r, id := h.restricted("acme", "alice"); !r || id != j.ID {
				t.Errorf("restriction = %v naming %q, want set naming %s", r, id, j.ID)
			}
			if n := h.count("delete:acme"); n != 0 {
				t.Errorf("DeleteByUser ran %d times, want 0", n)
			}
		})
	}
}

// TestRunnerReceiptErrorKeepsJobInProgress_spec_12_8 pins receipt before
// completion: a receipt error, or a runner with no receipt hook, leaves the
// job in progress with the restriction set; the next RunPending re-runs no
// eraser and writes a receipt byte-identical to the first attempt's.
// spec: 12.8 (Processing restriction during erasure (GDPR Article 18))
func TestRunnerReceiptErrorKeepsJobInProgress_spec_12_8(t *testing.T) {
	t.Run("receipt error", func(t *testing.T) {
		h := newRunHarness(t)
		h.set(func() { h.receiptErr = errBoom })
		j := h.start("acme", "alice", nil)
		if err := h.runner.Run(context.Background(), j.ID); !errors.Is(err, errBoom) {
			t.Fatalf("Run = %v, want the receipt error", err)
		}
		got := h.job(j.ID)
		if got.Phase.Terminal() || got.ErasedAt.IsZero() || got.ReceiptAt.IsZero() {
			t.Fatalf("job = phase %s ErasedAt %v ReceiptAt %v, want in progress with both set", got.Phase, got.ErasedAt, got.ReceiptAt)
		}
		if r, _ := h.restricted("acme", "alice"); !r {
			t.Fatal("restriction cleared before a receipt was written")
		}
		h.set(func() { h.receiptErr = nil })
		if err := h.runner.RunPending(context.Background()); err != nil {
			t.Fatalf("RunPending: %v", err)
		}
		if n := h.count("delete:acme"); n != 1 {
			t.Errorf("DeleteByUser ran %d times across both attempts, want 1", n)
		}
		rs := h.receiptList()
		if len(rs) != 2 || string(rs[0]) != string(rs[1]) {
			t.Errorf("receipts = %d, identical %v; want 2 byte-identical", len(rs), len(rs) == 2 && string(rs[0]) == string(rs[1]))
		}
		var a, b erasurejob.Job
		_ = json.Unmarshal(rs[0], &a)
		_ = json.Unmarshal(rs[1], &b)
		if a.Total != b.Total || a.Deleted["sessions"] != b.Deleted["sessions"] {
			t.Errorf("receipt counts differ: %v vs %v", a.Deleted, b.Deleted)
		}
		if got := h.job(j.ID); got.Phase != erasurejob.PhaseCompleted {
			t.Errorf("phase after the retry = %s, want completed", got.Phase)
		}
	})
	t.Run("no receipt hook", func(t *testing.T) {
		h := newRunHarness(t)
		h.runner.WithCompletionReceipt(nil)
		j := h.start("acme", "alice", nil)
		if err := h.runner.Run(context.Background(), j.ID); !errors.Is(err, erasurejob.ErrReceiptUnwired) {
			t.Fatalf("Run = %v, want ErrReceiptUnwired", err)
		}
		if got := h.job(j.ID); got.Phase.Terminal() {
			t.Errorf("phase = %s, want in progress", got.Phase)
		}
		if r, _ := h.restricted("acme", "alice"); !r {
			t.Error("restriction cleared with no receipt sink")
		}
	})
}

// TestRunnerStoreDeletingRerunKeepsCounts_spec_12_8 pins the per-store
// maximum: a re-run of store_deleting that deletes nothing never lowers a
// recorded count or the total.
// spec: 12.8 (Erasure job phase field (crash recovery))
func TestRunnerStoreDeletingRerunKeepsCounts_spec_12_8(t *testing.T) {
	h := newRunHarness(t)
	h.set(func() { h.deleteCount = 0 })
	j := h.start("acme", "alice", nil)
	h.moveTo(j.ID, erasurejob.PhaseStoreDeleting, func(j *erasurejob.Job) {
		j.Deleted = map[string]int{"sessions": 5, "memory": 2}
		j.Total = 7
	})
	if err := h.runner.Run(context.Background(), j.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := h.job(j.ID)
	if got.Deleted["sessions"] != 5 || got.Deleted["memory"] != 2 || got.Total != 7 {
		t.Errorf("Deleted = %v Total = %d, want sessions=5 memory=2 total 7", got.Deleted, got.Total)
	}
}

// TestRunnerRetryAfterClearRedrains_spec_12_8 pins the retry after an
// operator clear of a job that failed in verifying: the retried job drains
// before DeleteByUser, the drain reaches a session created while the
// restriction was lifted, and Pseudonymize runs again before Verify.
// spec: 12.8 (User-level erasure invocation), 12.8 (Lifecycle fence and drain), 24.12 (Erasure Job Management)
func TestRunnerRetryAfterClearRedrains_spec_12_8(t *testing.T) {
	h := newRunHarness(t)
	live := map[string]bool{}
	var drained []string
	h.drainGate = func(context.Context, string) {
		h.mu.Lock()
		defer h.mu.Unlock()
		for id := range live {
			drained = append(drained, id)
			delete(live, id)
		}
	}
	h.set(func() { h.billing.failVerify = true })
	j := h.start("acme", "alice", nil)
	_ = h.runner.Run(context.Background(), j.ID)
	if got := h.job(j.ID); got.Phase != erasurejob.PhaseFailed {
		t.Fatalf("precondition: phase = %s, want failed in verifying", got.Phase)
	}
	if _, err := h.jobs.ClearRestriction(context.Background(), j.ID); err != nil {
		t.Fatalf("ClearRestriction: %v", err)
	}
	// A session created while the restriction was lifted.
	h.set(func() { live["sess_reopened"] = true; h.events = nil; h.billing.failVerify = false })
	if _, err := h.jobs.Restart(context.Background(), j.ID); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if err := h.runner.RunPending(context.Background()); err != nil {
		t.Fatalf("RunPending: %v", err)
	}
	ev := h.order()
	d, del, p, v := indexOf(ev, "drain:acme"), indexOf(ev, "delete:acme"), indexOf(ev, "pseudonymize:acme"), indexOf(ev, "verify:acme")
	if d < 0 || del < 0 || d > del {
		t.Errorf("drain did not precede DeleteByUser: %v", ev)
	}
	if p < 0 || v < 0 || p > v {
		t.Errorf("Pseudonymize did not run again before Verify: %v", ev)
	}
	if len(drained) != 1 || drained[0] != "sess_reopened" {
		t.Errorf("drained %v, want the reopened session", drained)
	}
	if got := h.job(j.ID); got.Phase != erasurejob.PhaseCompleted {
		t.Errorf("phase = %s, want completed", got.Phase)
	}
}

// TestRunnerInitiatedFailures_spec_12_8 pins the initiated phase's failure
// outcomes and the override exemption: each failure cause fails the job
// under its label with the counter incremented once, the restriction set,
// and no eraser run; a hold listed in OverrideReceipt is exempt only while
// the job has never failed, in the form the erase handler builds and after
// a JSON round trip; and a crash-resumed job in store_deleting runs no
// re-check.
// spec: 12.8 (DeleteByUser dependency-ordered deletion sequence), 12.8 (Processing restriction during erasure (GDPR Article 18)), 12.8 (Erasure job phase field (crash recovery))
func TestRunnerInitiatedFailures_spec_12_8(t *testing.T) {
	held := erasurejob.Hold{ResourceType: "session", ResourceID: "sess_held"}
	for _, tc := range []struct {
		name   string
		knob   func(h *runHarness)
		label  string
		reason string
	}{
		{"memory preflight", func(h *runHarness) { h.preflightErr = errBoom }, erasurejob.FailurePhaseMemoryStorePreflight, "memory_store_preflight_failed"},
		{"drain failure", func(h *runHarness) { h.drainErr = errBoom }, erasurejob.FailurePhaseStoreDelete, "session drain"},
		{"hold found", func(h *runHarness) { h.holds = []erasurejob.Hold{held} }, erasurejob.FailurePhaseStoreDelete, "session:sess_held"},
		{"hold read error", func(h *runHarness) { h.holdErr = errBoom }, erasurejob.FailurePhaseStoreDelete, "legal-hold re-check"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newRunHarness(t)
			h.set(func() { tc.knob(h) })
			j := h.start("acme", "alice", nil)
			_ = h.runner.Run(context.Background(), j.ID)
			got := h.job(j.ID)
			if got.Phase != erasurejob.PhaseFailed || !strings.Contains(got.Failure, tc.reason) {
				t.Errorf("job = %s %q, want failed naming %q", got.Phase, got.Failure, tc.reason)
			}
			if f := h.failureCount(); len(f) != 1 || f[0] != "acme:"+tc.label {
				t.Errorf("failures = %v, want one acme:%s", f, tc.label)
			}
			if r, _ := h.restricted("acme", "alice"); !r {
				t.Error("restriction cleared on a failure")
			}
			if n := h.count("delete:acme"); n != 0 {
				t.Errorf("DeleteByUser ran %d times, want 0", n)
			}
		})
	}

	handlerForm := map[string]any{
		"legalHoldOverride": true,
		"holds":             []map[string]any{{"resourceType": "session", "resourceId": "sess_held"}},
	}
	var roundTripped map[string]any
	b, _ := json.Marshal(handlerForm)
	_ = json.Unmarshal(b, &roundTripped)
	for name, override := range map[string]map[string]any{"handler form": handlerForm, "json round trip": roundTripped} {
		t.Run("override exempts "+name, func(t *testing.T) {
			h := newRunHarness(t)
			h.set(func() { h.holds = []erasurejob.Hold{held} })
			j := h.start("acme", "alice", override)
			if err := h.runner.Run(context.Background(), j.ID); err != nil {
				t.Fatalf("Run with an overridden hold: %v", err)
			}
			if got := h.job(j.ID); got.Phase != erasurejob.PhaseCompleted {
				t.Fatalf("phase = %s, want completed", got.Phase)
			}
		})
		t.Run("retried job exempts nothing "+name, func(t *testing.T) {
			h := newRunHarness(t)
			h.set(func() { h.drainErr = errBoom; h.holds = []erasurejob.Hold{held} })
			j := h.start("acme", "alice", override)
			_ = h.runner.Run(context.Background(), j.ID)
			if _, err := h.jobs.Restart(context.Background(), j.ID); err != nil {
				t.Fatalf("Restart: %v", err)
			}
			h.set(func() { h.drainErr = nil })
			_ = h.runner.Run(context.Background(), j.ID)
			got := h.job(j.ID)
			if got.Phase != erasurejob.PhaseFailed || !strings.Contains(got.Failure, "sess_held") {
				t.Errorf("retried job = %s %q, want failed on the hold", got.Phase, got.Failure)
			}
			if n := h.count("delete:acme"); n != 0 {
				t.Errorf("DeleteByUser ran %d times, want 0", n)
			}
		})
	}

	t.Run("resume in store_deleting runs no re-check", func(t *testing.T) {
		h := newRunHarness(t)
		h.set(func() { h.holds = []erasurejob.Hold{held} })
		j := h.start("acme", "alice", nil)
		h.moveTo(j.ID, erasurejob.PhaseStoreDeleting, nil)
		if err := h.runner.Run(context.Background(), j.ID); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if n := h.count("hold"); n != 0 {
			t.Errorf("hold re-check ran %d times on a store_deleting resume, want 0", n)
		}
		if got := h.job(j.ID); got.Phase != erasurejob.PhaseCompleted {
			t.Errorf("phase = %s, want completed", got.Phase)
		}
	})
}
