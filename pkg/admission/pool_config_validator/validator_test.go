// SPDX-License-Identifier: MIT

package pool_config_validator_test

import (
	"strings"
	"testing"

	pcv "github.com/lennylabs/lenny/pkg/admission/pool_config_validator"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
)

// spec: §4.6.2/§4.6.3 (spec/04_system-components.md) and §5.2
// (spec/05_runtime-registry-and-pool-model.md) — the
// lenny-pool-config-validator webhook is the sole admission gate for
// the semantic budget invariants of pool configuration. Every rejection
// carries the INVALID_POOL_CONFIGURATION reason code and HTTP 422.

// assertRejected fails the test unless the decision rejects with HTTP
// 422 and an INVALID_POOL_CONFIGURATION-labeled reason that mentions
// the supplied substring.
func assertRejected(t *testing.T, d pcv.Decision, wantSubstr string) {
	t.Helper()
	if d.Allowed {
		t.Fatalf("expected rejection, got allow: %+v", d)
	}
	if d.Code != 422 {
		t.Errorf("code = %d, want 422", d.Code)
	}
	if !strings.HasPrefix(d.Reason, pcv.ReasonInvalidPoolConfiguration+":") {
		t.Errorf("reason = %q, want %s prefix", d.Reason, pcv.ReasonInvalidPoolConfiguration)
	}
	if wantSubstr != "" && !strings.Contains(d.Reason, wantSubstr) {
		t.Errorf("reason = %q, want substring %q", d.Reason, wantSubstr)
	}
}

// assertAllowed fails the test unless the decision admits with HTTP 200.
func assertAllowed(t *testing.T, d pcv.Decision) {
	t.Helper()
	if !d.Allowed {
		t.Fatalf("expected allow, got rejection: %+v", d)
	}
	if d.Code != 200 {
		t.Errorf("code = %d, want 200", d.Code)
	}
}

// warmPool builds a SandboxWarmPool with the given spec.
func warmPool(spec lennyv1.SandboxWarmPoolSpec) *lennyv1.SandboxWarmPool {
	return &lennyv1.SandboxWarmPool{Spec: spec}
}

// template builds a SandboxTemplate with the given spec.
func template(spec lennyv1.SandboxTemplateSpec) *lennyv1.SandboxTemplate {
	return &lennyv1.SandboxTemplate{Spec: spec}
}

// --- SandboxWarmPool invariants (§4.6.2 / §4.6.3) ---

func TestWarmPool(t *testing.T) {
	tests := []struct {
		name       string
		spec       lennyv1.SandboxWarmPoolSpec
		reject     bool
		wantSubstr string
	}{
		{
			name: "minWarm below maxWarm is admitted",
			spec: lennyv1.SandboxWarmPoolSpec{TemplateRef: "t", MinWarm: 2, MaxWarm: 10},
		},
		{
			name: "minWarm equal to maxWarm is admitted",
			spec: lennyv1.SandboxWarmPoolSpec{TemplateRef: "t", MinWarm: 5, MaxWarm: 5},
		},
		{
			name: "scale-to-zero floor is admitted",
			spec: lennyv1.SandboxWarmPoolSpec{TemplateRef: "t", MinWarm: 0, MaxWarm: 0},
		},
		{
			name:       "minWarm above maxWarm is rejected",
			spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "t", MinWarm: 12, MaxWarm: 10},
			reject:     true,
			wantSubstr: "spec.minWarm (12) exceeds spec.maxWarm (10)",
		},
		{
			name:       "negative minWarm is rejected",
			spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "t", MinWarm: -1, MaxWarm: 10},
			reject:     true,
			wantSubstr: "spec.minWarm (-1) must not be negative",
		},
		{
			name:       "negative maxWarm is rejected",
			spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "t", MinWarm: 0, MaxWarm: -3},
			reject:     true,
			wantSubstr: "spec.maxWarm (-3) must not be negative",
		},
		{
			name: "bootstrapMinWarm within maxWarm is admitted",
			spec: lennyv1.SandboxWarmPoolSpec{
				TemplateRef: "t", MinWarm: 1, MaxWarm: 10,
				ScalePolicy: &lennyv1.ScalePolicy{BootstrapMinWarm: 8},
			},
		},
		{
			name: "bootstrapMinWarm above maxWarm is rejected",
			spec: lennyv1.SandboxWarmPoolSpec{
				TemplateRef: "t", MinWarm: 1, MaxWarm: 10,
				ScalePolicy: &lennyv1.ScalePolicy{BootstrapMinWarm: 25},
			},
			reject:     true,
			wantSubstr: "spec.scalePolicy.bootstrapMinWarm (25) exceeds spec.maxWarm (10)",
		},
		{
			name: "negative bootstrapMinWarm is rejected",
			spec: lennyv1.SandboxWarmPoolSpec{
				TemplateRef: "t", MinWarm: 1, MaxWarm: 10,
				ScalePolicy: &lennyv1.ScalePolicy{BootstrapMinWarm: -2},
			},
			reject:     true,
			wantSubstr: "spec.scalePolicy.bootstrapMinWarm (-2) must not be negative",
		},
		{
			name: "valid schedule window is admitted",
			spec: lennyv1.SandboxWarmPoolSpec{
				TemplateRef: "t", MinWarm: 1, MaxWarm: 10,
				ScalePolicy: &lennyv1.ScalePolicy{
					Schedules: []lennyv1.ScheduleWindow{{Start: "09:00", End: "17:00", MinWarm: 8}},
				},
			},
		},
		{
			name: "schedule window with malformed start is rejected",
			spec: lennyv1.SandboxWarmPoolSpec{
				TemplateRef: "t", MinWarm: 1, MaxWarm: 10,
				ScalePolicy: &lennyv1.ScalePolicy{
					Schedules: []lennyv1.ScheduleWindow{{Start: "9am", End: "17:00", MinWarm: 3}},
				},
			},
			reject:     true,
			wantSubstr: "schedules[0].start",
		},
		{
			name: "schedule window with malformed end is rejected",
			spec: lennyv1.SandboxWarmPoolSpec{
				TemplateRef: "t", MinWarm: 1, MaxWarm: 10,
				ScalePolicy: &lennyv1.ScalePolicy{
					Schedules: []lennyv1.ScheduleWindow{{Start: "09:00", End: "25:61", MinWarm: 3}},
				},
			},
			reject:     true,
			wantSubstr: "schedules[0].end",
		},
		{
			name: "zero-duration schedule window is rejected",
			spec: lennyv1.SandboxWarmPoolSpec{
				TemplateRef: "t", MinWarm: 1, MaxWarm: 10,
				ScalePolicy: &lennyv1.ScalePolicy{
					Schedules: []lennyv1.ScheduleWindow{{Start: "09:00", End: "09:00", MinWarm: 3}},
				},
			},
			reject:     true,
			wantSubstr: "non-zero duration",
		},
		{
			name: "schedule window minWarm above maxWarm is rejected",
			spec: lennyv1.SandboxWarmPoolSpec{
				TemplateRef: "t", MinWarm: 1, MaxWarm: 10,
				ScalePolicy: &lennyv1.ScalePolicy{
					Schedules: []lennyv1.ScheduleWindow{{Start: "09:00", End: "17:00", MinWarm: 40}},
				},
			},
			reject:     true,
			wantSubstr: "schedules[0].minWarm (40) exceeds spec.maxWarm (10)",
		},
		{
			name: "negative schedule window minWarm is rejected",
			spec: lennyv1.SandboxWarmPoolSpec{
				TemplateRef: "t", MinWarm: 1, MaxWarm: 10,
				ScalePolicy: &lennyv1.ScalePolicy{
					Schedules: []lennyv1.ScheduleWindow{{Start: "09:00", End: "17:00", MinWarm: -1}},
				},
			},
			reject:     true,
			wantSubstr: "schedules[0].minWarm (-1) must not be negative",
		},
		{
			name: "second schedule window violation is rejected",
			spec: lennyv1.SandboxWarmPoolSpec{
				TemplateRef: "t", MinWarm: 1, MaxWarm: 10,
				ScalePolicy: &lennyv1.ScalePolicy{
					Schedules: []lennyv1.ScheduleWindow{
						{Start: "09:00", End: "17:00", MinWarm: 8},
						{Start: "22:00", End: "23:00", MinWarm: 99},
					},
				},
			},
			reject:     true,
			wantSubstr: "schedules[1].minWarm (99)",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := pcv.DecideWarmPool(warmPool(tc.spec))
			if tc.reject {
				assertRejected(t, d, tc.wantSubstr)
				return
			}
			assertAllowed(t, d)
		})
	}
}

// --- SandboxTemplate invariants (§5.2) ---

func TestTemplate(t *testing.T) {
	tests := []struct {
		name       string
		spec       lennyv1.SandboxTemplateSpec
		reject     bool
		wantSubstr string
	}{
		{
			name: "session mode carries no pool-config invariant",
			spec: lennyv1.SandboxTemplateSpec{RuntimeRef: "r", ExecutionMode: "session"},
		},
		{
			name: "empty execution mode is treated as session",
			spec: lennyv1.SandboxTemplateSpec{RuntimeRef: "r"},
		},
		{
			name: "service mode carries no pool-config invariant",
			spec: lennyv1.SandboxTemplateSpec{
				RuntimeRef: "r", ExecutionMode: "service", MaxConcurrent: 4,
			},
		},
		{
			// spec: §5.2 (Kata/microvm scrub variant) — the in-place scrub
			// profile reuses the running guest and leaves guest-kernel
			// residual state across tenants, so it requires the explicit
			// acknowledgment. The gate is the only sessionPolicy.recycle
			// acknowledgment the CRD carries; the rest re-key onto the
			// gateway-side poolstore mirror.
			name: "recycle in-place scrub with residual-state acknowledgment is admitted",
			spec: lennyv1.SandboxTemplateSpec{
				RuntimeRef: "r", ExecutionMode: "session", IsolationProfile: "microvm",
				SessionPolicy: &lennyv1.SessionPolicy{
					Recycle: &lennyv1.RecyclePolicy{
						ScrubProfile:                    "in-place",
						AcknowledgeMicrovmResidualState: true,
					},
				},
			},
		},
		{
			name: "recycle in-place scrub without residual-state acknowledgment is rejected",
			spec: lennyv1.SandboxTemplateSpec{
				RuntimeRef: "r", ExecutionMode: "session", IsolationProfile: "microvm",
				SessionPolicy: &lennyv1.SessionPolicy{
					Recycle: &lennyv1.RecyclePolicy{ScrubProfile: "in-place"},
				},
			},
			reject:     true,
			wantSubstr: "acknowledgeMicrovmResidualState",
		},
		{
			name: "recycle vm-restart scrub carries no residual-state gate",
			spec: lennyv1.SandboxTemplateSpec{
				RuntimeRef: "r", ExecutionMode: "session", IsolationProfile: "microvm",
				SessionPolicy: &lennyv1.SessionPolicy{
					Recycle: &lennyv1.RecyclePolicy{ScrubProfile: "vm-restart"},
				},
			},
		},
		{
			name: "recycle standard scrub carries no residual-state gate",
			spec: lennyv1.SandboxTemplateSpec{
				RuntimeRef: "r", ExecutionMode: "session",
				SessionPolicy: &lennyv1.SessionPolicy{
					Recycle: &lennyv1.RecyclePolicy{ScrubProfile: "standard"},
				},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := pcv.DecideTemplate(template(tc.spec))
			if tc.reject {
				assertRejected(t, d, tc.wantSubstr)
				return
			}
			assertAllowed(t, d)
		})
	}
}

// spec: §5.2 line 516 (spec/05_runtime-registry-and-pool-model.md) — the
// SandboxWarmPool admission webhook enforces the per-pod
// `terminationGracePeriodSeconds` floor for concurrent-workspace pools
// (`maxConcurrent × max_tiered_checkpoint_cap +
// checkpointBarrierAckTimeoutSeconds + 30`), warning at >600s and
// rejecting when maxTerminationGracePeriodSeconds is set and breached or
// when the deployer's terminationGracePeriodSeconds falls below the
// floor. spec: §10.1 lines 104-124 — the tier-cap table and the
// BarrierAck floor (`checkpointBarrierAckTimeoutSeconds ≥ tier cap`).
func TestDecideTemplate_TerminationGraceFloor_spec_5_2_516(t *testing.T) {
	int64p := func(v int64) *int64 { return &v }

	baseSpec := func() lennyv1.SandboxTemplateSpec {
		// A service-mode pool fans the per-slot checkpoint cap across
		// maxConcurrent slots, so the §10.1 / §5.2 line 516 termination
		// budget floor applies with the maxConcurrent multiplier exactly as
		// the former concurrent-workspace pool did.
		return lennyv1.SandboxTemplateSpec{
			RuntimeRef:    "r",
			ExecutionMode: "service",
			MaxConcurrent: 2,
		}
	}

	t.Run("default conservative floor under 600s admits without warning", func(t *testing.T) {
		// maxConcurrent=2, unset workspace size → 90s tier, unset ack → 90s,
		// floor = 2*90 + 90 + 30 = 300s.
		d := pcv.DecideTemplate(template(baseSpec()))
		assertAllowed(t, d)
		if len(d.Warnings) != 0 {
			t.Fatalf("did not expect warnings: %v", d.Warnings)
		}
	})

	t.Run("floor above 600s emits warning but admits", func(t *testing.T) {
		// maxConcurrent=8 → 8*90 + 90 + 30 = 840s > 600s.
		spec := baseSpec()
		spec.MaxConcurrent = 8
		d := pcv.DecideTemplate(template(spec))
		assertAllowed(t, d)
		if len(d.Warnings) != 1 {
			t.Fatalf("want one warning, got %v", d.Warnings)
		}
		if !strings.Contains(d.Warnings[0], "840s") || !strings.Contains(d.Warnings[0], "600s") {
			t.Errorf("warning %q does not name the floor or the node-drain limit", d.Warnings[0])
		}
	})

	t.Run("floor breaches maxTerminationGracePeriodSeconds → rejected", func(t *testing.T) {
		spec := baseSpec()
		spec.MaxConcurrent = 8
		spec.MaxTerminationGracePeriodSeconds = int64p(600)
		d := pcv.DecideTemplate(template(spec))
		assertRejected(t, d, "exceeds spec.maxTerminationGracePeriodSeconds (600s)")
	})

	t.Run("deployer terminationGracePeriodSeconds below floor → rejected", func(t *testing.T) {
		// floor = 300s; deployer set 200s.
		spec := baseSpec()
		spec.TerminationGracePeriodSeconds = int64p(200)
		d := pcv.DecideTemplate(template(spec))
		assertRejected(t, d, "below the §5.2 floor for this pool (300s")
	})

	t.Run("deployer terminationGracePeriodSeconds at floor → admitted", func(t *testing.T) {
		spec := baseSpec()
		spec.TerminationGracePeriodSeconds = int64p(300)
		d := pcv.DecideTemplate(template(spec))
		assertAllowed(t, d)
	})

	t.Run("workspaceSizeLimitBytes selects correct tier — 100 MB → 30s", func(t *testing.T) {
		// maxConcurrent=2, 100 MB → 30s tier, BUT BarrierAck default 90s
		// >= 30s tier (OK). floor = 2*30 + 90 + 30 = 180s.
		spec := baseSpec()
		spec.WorkspaceSizeLimitBytes = int64p(100 * 1024 * 1024)
		spec.TerminationGracePeriodSeconds = int64p(180)
		d := pcv.DecideTemplate(template(spec))
		assertAllowed(t, d)
	})

	t.Run("workspaceSizeLimitBytes selects correct tier — 300 MB → 60s", func(t *testing.T) {
		spec := baseSpec()
		spec.WorkspaceSizeLimitBytes = int64p(300 * 1024 * 1024)
		spec.TerminationGracePeriodSeconds = int64p(240) // 2*60 + 90 + 30
		d := pcv.DecideTemplate(template(spec))
		assertAllowed(t, d)
	})

	t.Run("checkpointBarrierAckTimeoutSeconds below tier cap → rejected", func(t *testing.T) {
		// 300 MB workspace → 60s tier; ack=30s → reject.
		spec := baseSpec()
		spec.WorkspaceSizeLimitBytes = int64p(300 * 1024 * 1024)
		spec.CheckpointBarrierAckTimeoutSeconds = int64p(30)
		d := pcv.DecideTemplate(template(spec))
		assertRejected(t, d, "must be >= max_tiered_checkpoint_cap (60s)")
	})

	t.Run("explicit barrier ack overrides default", func(t *testing.T) {
		// 30s tier (100 MB), ack=30s → floor = 2*30 + 30 + 30 = 120s.
		spec := baseSpec()
		spec.WorkspaceSizeLimitBytes = int64p(100 * 1024 * 1024)
		spec.CheckpointBarrierAckTimeoutSeconds = int64p(30)
		spec.TerminationGracePeriodSeconds = int64p(120)
		d := pcv.DecideTemplate(template(spec))
		assertAllowed(t, d)
	})

	// spec: §10.1 line 116 — the budget rule applies to EVERY
	// SandboxTemplate write regardless of executionMode. A session-mode
	// pool that allows a (default-tier 90s) workspace but declares a
	// 1s grace period has a floor of 1*90 + 90 + 30 = 210s and must be
	// rejected, not silently admitted to SIGKILL on drain.
	t.Run("session-mode pool below the floor → rejected (§10.1 line 116)", func(t *testing.T) {
		d := pcv.DecideTemplate(template(lennyv1.SandboxTemplateSpec{
			RuntimeRef:                    "r",
			TerminationGracePeriodSeconds: int64p(1),
		}))
		assertRejected(t, d, "below the §5.2 floor for this pool (210s")
		if !d.BudgetExceeded {
			t.Error("a grace-period-floor rejection must set BudgetExceeded so the counter increments")
		}
	})

	t.Run("session-mode pool at the floor → admitted (§10.1 line 116)", func(t *testing.T) {
		// floor = 1*90 + 90 + 30 = 210s.
		d := pcv.DecideTemplate(template(lennyv1.SandboxTemplateSpec{
			RuntimeRef:                    "r",
			TerminationGracePeriodSeconds: int64p(210),
		}))
		assertAllowed(t, d)
	})

	t.Run("session-mode pool with no grace period set → admitted (no comparison)", func(t *testing.T) {
		// terminationGracePeriodSeconds unset means the deployer accepts
		// the chart/§4.6.1 default; the webhook rejects only an explicit
		// value below the floor.
		d := pcv.DecideTemplate(template(lennyv1.SandboxTemplateSpec{RuntimeRef: "r"}))
		assertAllowed(t, d)
	})

	t.Run("service pool with unit slot below the floor → rejected (§10.1 line 116)", func(t *testing.T) {
		// A service pool with maxConcurrent 1 checkpoints a single
		// workspace, so the multiplier is 1: floor = 1*90 + 90 + 30 = 210s.
		d := pcv.DecideTemplate(template(lennyv1.SandboxTemplateSpec{
			RuntimeRef:                    "r",
			ExecutionMode:                 "service",
			MaxConcurrent:                 1,
			TerminationGracePeriodSeconds: int64p(1),
		}))
		assertRejected(t, d, "below the §5.2 floor for this pool (210s")
		if !d.BudgetExceeded {
			t.Error("a grace-period-floor rejection must set BudgetExceeded so the counter increments")
		}
	})

	t.Run("session pool below the floor → rejected (§10.1 line 116)", func(t *testing.T) {
		// A session pool checkpoints a single workspace: multiplier 1,
		// floor = 1*90 + 90 + 30 = 210s.
		d := pcv.DecideTemplate(template(lennyv1.SandboxTemplateSpec{
			RuntimeRef:                    "r",
			ExecutionMode:                 "session",
			TerminationGracePeriodSeconds: int64p(1),
		}))
		assertRejected(t, d, "below the §5.2 floor for this pool (210s")
		if !d.BudgetExceeded {
			t.Error("a grace-period-floor rejection must set BudgetExceeded so the counter increments")
		}
	})
}

// spec: §10.1 line 119 / §16.1 line 129 — only the two grace-period
// budget rejections (floor > terminationGracePeriodSeconds, floor >
// maxTerminationGracePeriodSeconds) set Decision.BudgetExceeded so the
// webhook increments lenny_pool_termination_budget_exceeded_total. The
// BarrierAck-floor rule (§10.1 line 124) and the warm-count / acknowledgment
// rejections are distinct and must NOT increment that counter.
func TestDecideTemplate_BudgetExceededDiscriminator_spec_10_1_129(t *testing.T) {
	int64p := func(v int64) *int64 { return &v }

	t.Run("maxTerminationGracePeriodSeconds breach sets BudgetExceeded", func(t *testing.T) {
		d := pcv.DecideTemplate(template(lennyv1.SandboxTemplateSpec{
			RuntimeRef:                       "r",
			ExecutionMode:                    "service",
			MaxConcurrent:                    8,
			MaxTerminationGracePeriodSeconds: int64p(600),
		}))
		assertRejected(t, d, "exceeds spec.maxTerminationGracePeriodSeconds")
		if !d.BudgetExceeded {
			t.Error("ceiling breach must set BudgetExceeded")
		}
	})

	t.Run("BarrierAck-floor rejection does NOT set BudgetExceeded", func(t *testing.T) {
		// 300 MB → 60s tier; ack 30s < tier cap → BarrierAck-floor reject.
		d := pcv.DecideTemplate(template(lennyv1.SandboxTemplateSpec{
			RuntimeRef:                         "r",
			WorkspaceSizeLimitBytes:            int64p(300 * 1024 * 1024),
			CheckpointBarrierAckTimeoutSeconds: int64p(30),
		}))
		assertRejected(t, d, "must be >= max_tiered_checkpoint_cap")
		if d.BudgetExceeded {
			t.Error("the BarrierAck-floor rule is distinct from the termination budget and must not set BudgetExceeded")
		}
	})

	t.Run("warm-count rejection does NOT set BudgetExceeded", func(t *testing.T) {
		d := pcv.DecideWarmPool(warmPool(lennyv1.SandboxWarmPoolSpec{MinWarm: 20, MaxWarm: 10}))
		if d.Allowed || d.BudgetExceeded {
			t.Errorf("a warm-count rejection must reject without BudgetExceeded: %+v", d)
		}
	})
}

// spec: §13.2 lines 438-442 (NET-006) — deliveryMode: proxy with
// egressProfile: provider-direct is mutually exclusive. The check is
// independent of executionMode, so it fires for session, task, and
// concurrent pools alike, and names the InvalidPoolEgressDeliveryCombo
// sub-code while keeping the INVALID_POOL_CONFIGURATION reason.
func TestDecideTemplate_EgressDeliveryCombo_spec_13_2_NET006(t *testing.T) {
	t.Run("proxy + provider-direct rejected regardless of execution mode", func(t *testing.T) {
		for _, mode := range []string{"", "session", "task", "concurrent"} {
			d := pcv.DecideTemplate(template(lennyv1.SandboxTemplateSpec{
				ExecutionMode: mode,
				DeliveryMode:  "proxy",
				EgressProfile: "provider-direct",
			}))
			assertRejected(t, d, "InvalidPoolEgressDeliveryCombo")
			if !strings.Contains(d.Reason, "NET-006") {
				t.Errorf("executionMode=%q: reason %q does not cite NET-006", mode, d.Reason)
			}
		}
	})

	t.Run("coherent pairings admitted (egress combo gate alone)", func(t *testing.T) {
		for _, tc := range []struct {
			name               string
			delivery, egr, iso string
		}{
			{"proxy + restricted", "proxy", "restricted", ""},
			{"direct + provider-direct", "direct", "provider-direct", ""},
			{"proxy + empty egress", "proxy", "", ""},
			{"empty delivery + provider-direct", "", "provider-direct", ""},
			// internet requires sandboxed/microvm isolation (NET-002).
			{"direct + internet + sandboxed", "direct", "internet", "sandboxed"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				// session mode carries no other invariant, isolating the combo gate.
				d := pcv.DecideTemplate(template(lennyv1.SandboxTemplateSpec{
					DeliveryMode:     tc.delivery,
					EgressProfile:    tc.egr,
					IsolationProfile: tc.iso,
				}))
				assertAllowed(t, d)
			})
		}
	})

	// spec: §13.2 line 450 (NET-002) — the internet egress profile requires
	// sandboxed/microvm isolation; standard (runc, including the empty
	// default) is rejected. F-13.2.11.
	t.Run("internet requires sandboxed or microvm isolation", func(t *testing.T) {
		for _, iso := range []string{"", "standard"} {
			d := pcv.DecideTemplate(template(lennyv1.SandboxTemplateSpec{
				EgressProfile:    "internet",
				IsolationProfile: iso,
			}))
			if d.Allowed {
				t.Errorf("isolationProfile=%q + internet must be rejected (NET-002)", iso)
			}
			if !strings.Contains(d.Reason, "NET-002") {
				t.Errorf("isolationProfile=%q: reason %q does not cite NET-002", iso, d.Reason)
			}
		}
		for _, iso := range []string{"sandboxed", "microvm"} {
			d := pcv.DecideTemplate(template(lennyv1.SandboxTemplateSpec{
				EgressProfile:    "internet",
				IsolationProfile: iso,
			}))
			assertAllowed(t, d)
		}
	})

	t.Run("combo gate runs before mode-specific rejection", func(t *testing.T) {
		// A task-mode pool missing taskPolicy would normally reject with
		// the §5.2 task message; the NET-006 combo must take precedence so
		// the reported defect is the security-relevant one.
		d := pcv.DecideTemplate(template(lennyv1.SandboxTemplateSpec{
			ExecutionMode: "task",
			DeliveryMode:  "proxy",
			EgressProfile: "provider-direct",
		}))
		assertRejected(t, d, "InvalidPoolEgressDeliveryCombo")
	})
}

// spec: §10.1 lines 104-108 (spec/10_gateway-internals.md) — the tier
// table maps workspace size to the max checkpoint cap. Unset/unknown
// workspace size falls back to the 90s conservative tier per line 108.
func TestMaxTieredCheckpointCapSeconds_spec_10_1_104(t *testing.T) {
	mb := int64(1024 * 1024)
	tests := []struct {
		name string
		size int64
		want int64
	}{
		{"unset → 90s conservative", 0, 90},
		{"negative → 90s conservative", -1, 90},
		{"1 byte → 30s", 1, 30},
		{"100 MB boundary → 30s", 100 * mb, 30},
		{"101 MB → 60s", 100*mb + 1, 60},
		{"300 MB boundary → 60s", 300 * mb, 60},
		{"301 MB → 90s", 300*mb + 1, 90},
		{"512 MB hard limit → 90s", 512 * mb, 90},
		{"absurdly large → 90s", 1024 * 1024 * mb, 90},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := pcv.MaxTieredCheckpointCapSeconds(tc.size); got != tc.want {
				t.Errorf("MaxTieredCheckpointCapSeconds(%d) = %d, want %d", tc.size, got, tc.want)
			}
		})
	}
}

// spec: §4.6.3 line 601 (spec/04_system-components.md) — rule set 2:
// the userInfo authorization backstop admits only the
// PoolScalingController SA and rejects every other principal with HTTP
// 403 and the UNAUTHORIZED_POOL_CONFIG_WRITE reason code.
func TestDecideAuthorization(t *testing.T) {
	tests := []struct {
		name     string
		username string
		allow    bool
	}{
		{"pool scaling controller SA is admitted", pcv.PoolScalingControllerSA, true},
		{"platform admin is rejected", "system:serviceaccount:acme:platform-admin", false},
		{"kubernetes-admin is rejected", "kubernetes-admin", false},
		{"warm pool controller SA is rejected", "system:serviceaccount:lenny-system:lenny-controller", false},
		{"empty principal is rejected", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := pcv.DecideAuthorization(tc.username)
			if tc.allow {
				if !d.Allowed {
					t.Fatalf("expected admit for %q, got %+v", tc.username, d)
				}
				return
			}
			if d.Allowed {
				t.Fatalf("expected rejection for %q, got admit", tc.username)
			}
			if d.Code != 403 {
				t.Errorf("code = %d, want 403", d.Code)
			}
			if !strings.HasPrefix(d.Reason, pcv.ReasonUnauthorizedPoolConfigWrite+":") {
				t.Errorf("reason = %q, want %s prefix", d.Reason, pcv.ReasonUnauthorizedPoolConfigWrite)
			}
		})
	}
}

// spec: §4.6.1 line 400 — the scaleToZero cron window and optional IANA
// timezone are validated at admission so the controller never silently
// fails to parse them.
func TestWarmPoolScaleToZero(t *testing.T) {
	tests := []struct {
		name       string
		policy     *lennyv1.ScaleToZeroPolicy
		reject     bool
		wantSubstr string
	}{
		{
			name:   "valid cron window is admitted",
			policy: &lennyv1.ScaleToZeroPolicy{Schedule: "0 22 * * *", ResumeAt: "0 6 * * *"},
		},
		{
			name:   "valid cron window with timezone is admitted",
			policy: &lennyv1.ScaleToZeroPolicy{Schedule: "0 22 * * *", ResumeAt: "0 6 * * *", Timezone: "America/New_York"},
		},
		{
			name:       "invalid timezone is rejected",
			policy:     &lennyv1.ScaleToZeroPolicy{Schedule: "0 22 * * *", ResumeAt: "0 6 * * *", Timezone: "Mars/Olympus"},
			reject:     true,
			wantSubstr: "scaleToZero.timezone",
		},
		{
			name:       "invalid schedule cron is rejected",
			policy:     &lennyv1.ScaleToZeroPolicy{Schedule: "not a cron", ResumeAt: "0 6 * * *"},
			reject:     true,
			wantSubstr: "scaleToZero.schedule",
		},
		{
			name:       "invalid resumeAt cron is rejected",
			policy:     &lennyv1.ScaleToZeroPolicy{Schedule: "0 22 * * *", ResumeAt: "99 99 * * *"},
			reject:     true,
			wantSubstr: "scaleToZero.resumeAt",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := lennyv1.SandboxWarmPoolSpec{
				TemplateRef: "t", MinWarm: 0, MaxWarm: 10,
				ScalePolicy: &lennyv1.ScalePolicy{ScaleToZero: tc.policy},
			}
			d := pcv.DecideWarmPool(warmPool(spec))
			if tc.reject {
				assertRejected(t, d, tc.wantSubstr)
				return
			}
			assertAllowed(t, d)
		})
	}
}
