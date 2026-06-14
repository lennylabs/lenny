// SPDX-License-Identifier: MIT

package taskdriver_test

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/taskdriver"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
	"github.com/lennylabs/lenny/pkg/sandbox/taskcleanup"
)

// recordingMetrics captures the §5.2 retirement metric emissions.
type recordingMetrics struct {
	scrubGauge  []gauge
	retirements []retirement
	scrubTotals int
}

type gauge struct {
	pool, pod string
	count     int
}

type retirement struct {
	pool   string
	reason taskcleanup.RetireReason
}

func (m *recordingMetrics) SetPodScrubFailureCount(pool, pod string, count int) {
	m.scrubGauge = append(m.scrubGauge, gauge{pool, pod, count})
}

func (m *recordingMetrics) IncPodRetirement(pool string, reason taskcleanup.RetireReason) {
	m.retirements = append(m.retirements, retirement{pool, reason})
}

func (m *recordingMetrics) IncScrubFailureTotal(string) { m.scrubTotals++ }

func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

// spec: §6.2 line 150 — a pod that reaches its session-count cap drains
// with reason session_count_limit, and the retirement metric fires once.
func TestRecordCompletionRetiresAtMaxTasks_spec_6_2_150(t *testing.T) {
	m := &recordingMetrics{}
	e := taskdriver.NewEvaluator(taskdriver.Policy{MaxTasksPerPod: 2}, m)
	st := taskdriver.NewPodTaskState("pod-1", "tp", time.Now())

	// First task: reuse (not at the cap yet).
	d := e.RecordCompletion(st, taskcleanup.ScrubSucceeded, true)
	if d.Retire || d.NextPhase != state.Idle {
		t.Fatalf("task 1 disposition = %+v, want reuse→idle", d)
	}
	// Second task: at the cap, drains.
	d = e.RecordCompletion(st, taskcleanup.ScrubSucceeded, true)
	if !d.Retire || d.Reason != taskcleanup.ReasonSessionCountLimit {
		t.Fatalf("task 2 disposition = %+v, want session_count_limit drain", d)
	}
	if len(m.retirements) != 1 || m.retirements[0].reason != taskcleanup.ReasonSessionCountLimit {
		t.Errorf("retirement metric = %+v, want one session_count_limit", m.retirements)
	}
	if st.TasksCompleted != 2 {
		t.Errorf("TasksCompleted = %d, want 2", st.TasksCompleted)
	}
}

// spec: §6.2 line 149 — the cumulative scrub-failure count reaching
// maxScrubFailures drains the pod; the aggregate and per-pod metrics track
// each failure.
func TestRecordCompletionScrubFailuresExhausted_spec_6_2_149(t *testing.T) {
	m := &recordingMetrics{}
	e := taskdriver.NewEvaluator(taskdriver.Policy{
		MaxTasksPerPod:   100,
		MaxScrubFailures: 2,
		OnCleanupFailure: taskcleanup.OnCleanupWarn,
	}, m)
	st := taskdriver.NewPodTaskState("pod-1", "tp", time.Now())

	// First failed scrub: under the limit, returns to idle with a warning.
	d := e.RecordCompletion(st, taskcleanup.ScrubFailed, true)
	if d.Retire || !d.ScrubWarning {
		t.Fatalf("failure 1 disposition = %+v, want warn reuse", d)
	}
	// Second failed scrub: reaches maxScrubFailures, drains.
	d = e.RecordCompletion(st, taskcleanup.ScrubFailed, true)
	if !d.Retire || d.Reason != taskcleanup.ReasonScrubFailuresExhausted {
		t.Fatalf("failure 2 disposition = %+v, want scrub_failures_exhausted", d)
	}
	if m.scrubTotals != 2 {
		t.Errorf("aggregate scrub-failure total = %d, want 2", m.scrubTotals)
	}
	// The per-pod gauge records the cumulative count after each task.
	last := m.scrubGauge[len(m.scrubGauge)-1]
	if last.count != 2 || last.pod != "pod-1" {
		t.Errorf("scrub-failure gauge = %+v, want count 2 for pod-1", last)
	}
}

// spec: §6.2 line 156 — onCleanupFailure: fail terminates the pod on the
// first scrub failure with reason cleanup_fail_policy.
func TestRecordCompletionFailPolicyTerminates_spec_6_2_156(t *testing.T) {
	m := &recordingMetrics{}
	e := taskdriver.NewEvaluator(taskdriver.Policy{
		MaxTasksPerPod:   10,
		OnCleanupFailure: taskcleanup.OnCleanupFail,
	}, m)
	st := taskdriver.NewPodTaskState("pod-1", "tp", time.Now())

	d := e.RecordCompletion(st, taskcleanup.ScrubFailed, true)
	if !d.Retire || d.NextPhase != state.Failed || d.Reason != taskcleanup.ReasonCleanupFailPolicy {
		t.Fatalf("fail-policy disposition = %+v, want failed/cleanup_fail_policy", d)
	}
	if len(m.retirements) != 1 || m.retirements[0].reason != taskcleanup.ReasonCleanupFailPolicy {
		t.Errorf("retirement metric = %+v, want cleanup_fail_policy", m.retirements)
	}
}

// spec: §6.2 line 151 — a pod past maxPodUptimeSeconds drains with reason
// max_uptime_exceeded.
func TestRecordCompletionRetiresOnUptime_spec_6_2_151(t *testing.T) {
	boot := time.Date(2026, 6, 8, 0, 0, 0, 0, time.UTC)
	e := taskdriver.NewEvaluator(taskdriver.Policy{
		MaxTasksPerPod:      100,
		MaxPodUptimeSeconds: 3600,
	}, nil)
	e.SetClock(fixedClock(boot.Add(2 * time.Hour))) // 7200s > 3600s cap
	st := taskdriver.NewPodTaskState("pod-1", "tp", boot)

	d := e.RecordCompletion(st, taskcleanup.ScrubSucceeded, true)
	if !d.Retire || d.Reason != taskcleanup.ReasonMaxUptimeExceeded {
		t.Fatalf("uptime disposition = %+v, want max_uptime_exceeded", d)
	}
}

// spec: §6.2 lines 152-153, 181 — a preConnect pod on a cordoned host
// drains (host_unschedulable) rather than re-warming.
func TestRecordCompletionPreConnectCordonedHostDrains_spec_6_2_181(t *testing.T) {
	e := taskdriver.NewEvaluator(taskdriver.Policy{
		PreConnect:     true,
		MaxTasksPerPod: 100,
	}, nil)
	st := taskdriver.NewPodTaskState("pod-1", "tp", time.Now())

	// Schedulable host: re-warms through sdk_connecting.
	d := e.RecordCompletion(st, taskcleanup.ScrubSucceeded, true)
	if d.Retire || d.NextPhase != state.SDKConnecting {
		t.Fatalf("schedulable disposition = %+v, want sdk_connecting reuse", d)
	}
	// Cordoned host: drains.
	d = e.RecordCompletion(st, taskcleanup.ScrubSucceeded, false)
	if !d.Retire || d.Reason != taskcleanup.ReasonHostUnschedulable {
		t.Fatalf("cordoned disposition = %+v, want host_unschedulable drain", d)
	}
}

// A ScrubPending result does not advance the counters or emit metrics; the
// driver must wait for the adapter's scrub report.
func TestRecordCompletionPendingDoesNotAdvance(t *testing.T) {
	m := &recordingMetrics{}
	e := taskdriver.NewEvaluator(taskdriver.Policy{MaxTasksPerPod: 2}, m)
	st := taskdriver.NewPodTaskState("pod-1", "tp", time.Now())

	d := e.RecordCompletion(st, taskcleanup.ScrubPending, true)
	if d.Ready {
		t.Fatalf("pending disposition Ready = true, want not-ready")
	}
	if st.TasksCompleted != 0 {
		t.Errorf("TasksCompleted = %d after pending, want 0", st.TasksCompleted)
	}
	if len(m.scrubGauge) != 0 || len(m.retirements) != 0 || m.scrubTotals != 0 {
		t.Errorf("pending emitted metrics: %+v", m)
	}
}

// spec: §6.6 / §5.2 maxTaskRetries — the per-task crash budget permits
// 1 + maxTaskRetries total attempts.
func TestShouldRetryTask_spec_6_6_maxTaskRetries(t *testing.T) {
	e := taskdriver.NewEvaluator(taskdriver.Policy{MaxTaskRetries: 1}, nil)
	if !e.ShouldRetryTask(1) {
		t.Error("attempt 1 with budget 1 should permit a retry")
	}
	if e.ShouldRetryTask(2) {
		t.Error("attempt 2 with budget 1 should not permit a further retry")
	}

	// Zero budget: no retry after the first attempt.
	e0 := taskdriver.NewEvaluator(taskdriver.Policy{MaxTaskRetries: 0}, nil)
	if e0.ShouldRetryTask(1) {
		t.Error("attempt 1 with budget 0 should not permit a retry")
	}

	// Negative budget is clamped to zero.
	eNeg := taskdriver.NewEvaluator(taskdriver.Policy{MaxTaskRetries: -5}, nil)
	if eNeg.ShouldRetryTask(1) {
		t.Error("negative budget should clamp to zero retries")
	}
}

// A non-preConnect pod returns straight to idle and node schedulability
// does not gate it (§6.2 lines 147-148).
func TestNonPreConnectReuseIgnoresHostSchedulable_spec_6_2_147(t *testing.T) {
	e := taskdriver.NewEvaluator(taskdriver.Policy{MaxTasksPerPod: 100}, nil)
	st := taskdriver.NewPodTaskState("pod-1", "tp", time.Now())
	// Even on a cordoned host, a non-preConnect pod reuses to idle.
	d := e.RecordCompletion(st, taskcleanup.ScrubSucceeded, false)
	if d.Retire || d.NextPhase != state.Idle {
		t.Fatalf("non-preConnect disposition = %+v, want idle reuse", d)
	}
}

func TestIsRetirePhase(t *testing.T) {
	if !taskdriver.IsRetirePhase(state.Draining) || !taskdriver.IsRetirePhase(state.Failed) {
		t.Error("draining/failed should be retirement phases")
	}
	if taskdriver.IsRetirePhase(state.Idle) || taskdriver.IsRetirePhase(state.SDKConnecting) {
		t.Error("idle/sdk_connecting should not be retirement phases")
	}
}
