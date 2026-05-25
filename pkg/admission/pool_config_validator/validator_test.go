// SPDX-License-Identifier: MIT

package pool_config_validator_test

import (
	"strings"
	"testing"

	pcv "github.com/lennylabs/lenny/pkg/admission/pool_config_validator"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
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
			name: "task mode with acknowledged scrub and reuse limit is admitted",
			spec: lennyv1.SandboxTemplateSpec{
				RuntimeRef: "r", ExecutionMode: "task",
				TaskPolicy: &lennyv1.TaskPolicy{AcknowledgeBestEffortScrub: true, MaxTasksPerPod: 50},
			},
		},
		{
			name:       "task mode without taskPolicy is rejected",
			spec:       lennyv1.SandboxTemplateSpec{RuntimeRef: "r", ExecutionMode: "task"},
			reject:     true,
			wantSubstr: "spec.taskPolicy is absent",
		},
		{
			name: "task mode without scrub acknowledgment is rejected",
			spec: lennyv1.SandboxTemplateSpec{
				RuntimeRef: "r", ExecutionMode: "task",
				TaskPolicy: &lennyv1.TaskPolicy{AcknowledgeBestEffortScrub: false, MaxTasksPerPod: 50},
			},
			reject:     true,
			wantSubstr: "acknowledgeBestEffortScrub must be true",
		},
		{
			name: "task mode without maxTasksPerPod is rejected",
			spec: lennyv1.SandboxTemplateSpec{
				RuntimeRef: "r", ExecutionMode: "task",
				TaskPolicy: &lennyv1.TaskPolicy{AcknowledgeBestEffortScrub: true},
			},
			reject:     true,
			wantSubstr: "maxTasksPerPod (0) must be at least 1",
		},
		{
			name: "cross-tenant reuse on microvm pool is admitted",
			spec: lennyv1.SandboxTemplateSpec{
				RuntimeRef: "r", ExecutionMode: "task", IsolationProfile: "microvm",
				TaskPolicy: &lennyv1.TaskPolicy{
					AcknowledgeBestEffortScrub: true, MaxTasksPerPod: 50, AllowCrossTenantReuse: true,
				},
			},
		},
		{
			name: "cross-tenant reuse on standard pool is rejected",
			spec: lennyv1.SandboxTemplateSpec{
				RuntimeRef: "r", ExecutionMode: "task", IsolationProfile: "standard",
				TaskPolicy: &lennyv1.TaskPolicy{
					AcknowledgeBestEffortScrub: true, MaxTasksPerPod: 50, AllowCrossTenantReuse: true,
				},
			},
			reject:     true,
			wantSubstr: "permitted only with isolationProfile: microvm",
		},
		{
			name: "cross-tenant reuse with unset isolation profile is rejected",
			spec: lennyv1.SandboxTemplateSpec{
				RuntimeRef: "r", ExecutionMode: "task",
				TaskPolicy: &lennyv1.TaskPolicy{
					AcknowledgeBestEffortScrub: true, MaxTasksPerPod: 50, AllowCrossTenantReuse: true,
				},
			},
			reject:     true,
			wantSubstr: "standard (unset)",
		},
		{
			name: "in-place microvm scrub with residual-state acknowledgment is admitted",
			spec: lennyv1.SandboxTemplateSpec{
				RuntimeRef: "r", ExecutionMode: "task", IsolationProfile: "microvm",
				TaskPolicy: &lennyv1.TaskPolicy{
					AcknowledgeBestEffortScrub: true, MaxTasksPerPod: 50, AllowCrossTenantReuse: true,
					MicrovmScrubMode: "in-place", AcknowledgeMicrovmResidualState: true,
				},
			},
		},
		{
			name: "in-place microvm scrub without residual-state acknowledgment is rejected",
			spec: lennyv1.SandboxTemplateSpec{
				RuntimeRef: "r", ExecutionMode: "task", IsolationProfile: "microvm",
				TaskPolicy: &lennyv1.TaskPolicy{
					AcknowledgeBestEffortScrub: true, MaxTasksPerPod: 50, AllowCrossTenantReuse: true,
					MicrovmScrubMode: "in-place",
				},
			},
			reject:     true,
			wantSubstr: "acknowledgeMicrovmResidualState",
		},
		{
			name: "concurrent-workspace mode with acknowledgment and budget is admitted",
			spec: lennyv1.SandboxTemplateSpec{
				RuntimeRef: "r", ExecutionMode: "concurrent", ConcurrencyStyle: "workspace",
				MaxConcurrent: 4,
				ConcurrentWorkspacePolicy: &lennyv1.ConcurrentWorkspacePolicy{
					AcknowledgeProcessLevelIsolation: true, CleanupTimeoutSeconds: 60,
				},
			},
		},
		{
			name: "concurrent stateless mode carries no concurrent-workspace invariant",
			spec: lennyv1.SandboxTemplateSpec{
				RuntimeRef: "r", ExecutionMode: "concurrent", ConcurrencyStyle: "stateless",
			},
		},
		{
			name: "concurrent-workspace mode without policy is rejected",
			spec: lennyv1.SandboxTemplateSpec{
				RuntimeRef: "r", ExecutionMode: "concurrent", ConcurrencyStyle: "workspace",
			},
			reject:     true,
			wantSubstr: "spec.concurrentWorkspacePolicy is absent",
		},
		{
			name: "concurrent-workspace mode without process-isolation acknowledgment is rejected",
			spec: lennyv1.SandboxTemplateSpec{
				RuntimeRef: "r", ExecutionMode: "concurrent", ConcurrencyStyle: "workspace",
				MaxConcurrent: 2,
				ConcurrentWorkspacePolicy: &lennyv1.ConcurrentWorkspacePolicy{
					AcknowledgeProcessLevelIsolation: false, CleanupTimeoutSeconds: 60,
				},
			},
			reject:     true,
			wantSubstr: "acknowledgeProcessLevelIsolation must be true",
		},
		{
			name: "concurrent-workspace cleanup budget below per-slot floor is rejected",
			spec: lennyv1.SandboxTemplateSpec{
				RuntimeRef: "r", ExecutionMode: "concurrent", ConcurrencyStyle: "workspace",
				MaxConcurrent: 8,
				ConcurrentWorkspacePolicy: &lennyv1.ConcurrentWorkspacePolicy{
					AcknowledgeProcessLevelIsolation: true, CleanupTimeoutSeconds: 30,
				},
			},
			reject:     true,
			wantSubstr: "cleanupTimeoutSeconds >= maxConcurrent x 5 (40s)",
		},
		{
			name: "concurrent-workspace cleanup budget exactly at floor is admitted",
			spec: lennyv1.SandboxTemplateSpec{
				RuntimeRef: "r", ExecutionMode: "concurrent", ConcurrencyStyle: "workspace",
				MaxConcurrent: 8,
				ConcurrentWorkspacePolicy: &lennyv1.ConcurrentWorkspacePolicy{
					AcknowledgeProcessLevelIsolation: true, CleanupTimeoutSeconds: 40,
				},
			},
		},
		{
			name: "concurrent-workspace cross-tenant reuse via taskPolicy is rejected",
			spec: lennyv1.SandboxTemplateSpec{
				RuntimeRef: "r", ExecutionMode: "concurrent", ConcurrencyStyle: "workspace",
				MaxConcurrent: 2,
				ConcurrentWorkspacePolicy: &lennyv1.ConcurrentWorkspacePolicy{
					AcknowledgeProcessLevelIsolation: true, CleanupTimeoutSeconds: 60,
				},
				TaskPolicy: &lennyv1.TaskPolicy{AllowCrossTenantReuse: true},
			},
			reject:     true,
			wantSubstr: "no isolation boundary in concurrent-workspace mode",
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
