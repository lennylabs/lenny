// SPDX-License-Identifier: MIT

package leasecontrol_test

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/gateway/leasecontrol"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/sandbox/taskcleanup"
)

// fakeScrubReports is a leasecontrol.ScrubReportService double that records
// the calls the handlers make so the handler-side validation and delegation
// can be asserted without the full orchestrator.
type fakeScrubReports struct {
	sessionCalls []sessionScrubCall
	podCalls     []podScrubCall
	sessionErr   error
	podErr       error
}

type sessionScrubCall struct {
	podID, sessionID, slotID string
	leaked                   bool
}

type podScrubCall struct {
	podID  string
	failed bool
	detail string
}

func (f *fakeScrubReports) RecordSessionScrub(_ context.Context, podID, sessionID, slotID string, leaked bool) error {
	f.sessionCalls = append(f.sessionCalls, sessionScrubCall{podID, sessionID, slotID, leaked})
	return f.sessionErr
}

func (f *fakeScrubReports) RecordPodScrub(_ context.Context, podID string, failed bool, detail string) error {
	f.podCalls = append(f.podCalls, podScrubCall{podID, failed, detail})
	return f.podErr
}

func newServiceWithScrub(t *testing.T, sr leasecontrol.ScrubReportService) *leasecontrol.Service {
	t.Helper()
	budgets := leasecontrol.NewMemoryBudgetSource()
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets:      budgets,
		Tenants:      budgets,
		ScrubReports: sr,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

// TestReportSessionScrubDelegatesLeaked verifies the handler decodes a
// LEAKED outcome and delegates it as leaked=true, with the slot id when set.
// spec: 4.7 (ReportSessionScrub), 5.2 (scrub model)
//
// diagnosis: a failure means the gateway-side ReportSessionScrub handler
// stopped decoding the per-slot scrub report into the leak flag the
// unhealthy-threshold drain ledger consumes, so a leaked slot would not feed
// the drain trigger.
func TestReportSessionScrubDelegatesLeaked_spec_5_2(t *testing.T) {
	f := &fakeScrubReports{}
	svc := newServiceWithScrub(t, f)
	_, err := svc.ReportSessionScrub(context.Background(), &adapterv1.ReportSessionScrubRequest{
		PodId:     "pod-1",
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
		SlotId:    &adapterv1.SlotId{Value: "slot-2"},
		Outcome:   adapterv1.SessionScrubOutcome_SESSION_SCRUB_OUTCOME_LEAKED,
	})
	if err != nil {
		t.Fatalf("ReportSessionScrub: %v", err)
	}
	if len(f.sessionCalls) != 1 {
		t.Fatalf("session calls = %d, want 1", len(f.sessionCalls))
	}
	got := f.sessionCalls[0]
	if got.podID != "pod-1" || got.sessionID != "sess-1" || got.slotID != "slot-2" || !got.leaked {
		t.Errorf("delegated %+v, want pod-1/sess-1/slot-2 leaked", got)
	}
}

// TestReportSessionScrubReleasedNotLeaked verifies a RELEASED outcome
// delegates as leaked=false so a clean release never feeds the drain ledger.
// spec: 5.2 (scrub model)
//
// diagnosis: a failure means a clean session release is being recorded as a
// leak, over-counting the unhealthy-threshold ledger and prematurely draining
// healthy pods.
func TestReportSessionScrubReleasedNotLeaked_spec_5_2(t *testing.T) {
	f := &fakeScrubReports{}
	svc := newServiceWithScrub(t, f)
	if _, err := svc.ReportSessionScrub(context.Background(), &adapterv1.ReportSessionScrubRequest{
		PodId:     "pod-1",
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
		Outcome:   adapterv1.SessionScrubOutcome_SESSION_SCRUB_OUTCOME_RELEASED,
	}); err != nil {
		t.Fatalf("ReportSessionScrub: %v", err)
	}
	if f.sessionCalls[0].leaked {
		t.Error("RELEASED delegated as leaked=true, want false")
	}
}

// TestReportSessionScrubUnspecifiedFailsClosed verifies an unspecified scrub
// outcome is rejected with InvalidArgument and never delegated.
// spec: 4.7 (ReportSessionScrub), 5.2 (scrub model)
//
// diagnosis: a failure means the handler accepted an UNSPECIFIED session
// scrub outcome, which the gateway cannot classify as a clean release or a
// leak, so it would either drop a leak signal or over-count a drain.
func TestReportSessionScrubUnspecifiedFailsClosed_spec_5_2(t *testing.T) {
	f := &fakeScrubReports{}
	svc := newServiceWithScrub(t, f)
	_, err := svc.ReportSessionScrub(context.Background(), &adapterv1.ReportSessionScrubRequest{
		PodId:     "pod-1",
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
		Outcome:   adapterv1.SessionScrubOutcome_SESSION_SCRUB_OUTCOME_UNSPECIFIED,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("err code = %v, want InvalidArgument", status.Code(err))
	}
	if len(f.sessionCalls) != 0 {
		t.Errorf("unspecified outcome delegated %d calls, want 0", len(f.sessionCalls))
	}
}

// TestReportSessionScrubMissingIDs verifies the handler rejects an empty pod
// id or session id with InvalidArgument.
// spec: 4.7 (ReportSessionScrub)
//
// diagnosis: a failure means the handler accepted a scrub report with no pod
// or session id, so the gateway could not key the counter increment or the
// drain ledger to a pod.
func TestReportSessionScrubMissingIDs_spec_4_7(t *testing.T) {
	svc := newServiceWithScrub(t, &fakeScrubReports{})
	if _, err := svc.ReportSessionScrub(context.Background(), &adapterv1.ReportSessionScrubRequest{
		Outcome: adapterv1.SessionScrubOutcome_SESSION_SCRUB_OUTCOME_RELEASED,
	}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("missing pod id: code = %v, want InvalidArgument", status.Code(err))
	}
	if _, err := svc.ReportSessionScrub(context.Background(), &adapterv1.ReportSessionScrubRequest{
		PodId:   "pod-1",
		Outcome: adapterv1.SessionScrubOutcome_SESSION_SCRUB_OUTCOME_RELEASED,
	}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("missing session id: code = %v, want InvalidArgument", status.Code(err))
	}
}

// TestReportPodScrubDelegatesFailed verifies a FAILED whole-pod scrub
// delegates as failed=true with the detail string.
// spec: 4.7 (ReportPodScrub), 3.4 (recycle disposition)
//
// diagnosis: a failure means the handler stopped decoding the whole-pod scrub
// outcome into the failure flag the recycle disposition reads, so a failed
// scrub would not increment scrub_failure_count or drive the retire path.
func TestReportPodScrubDelegatesFailed_spec_3_4(t *testing.T) {
	f := &fakeScrubReports{}
	svc := newServiceWithScrub(t, f)
	if _, err := svc.ReportPodScrub(context.Background(), &adapterv1.ReportPodScrubRequest{
		PodId:   "pod-1",
		Outcome: adapterv1.PodScrubOutcome_POD_SCRUB_OUTCOME_FAILED,
		Detail:  "shred timed out",
	}); err != nil {
		t.Fatalf("ReportPodScrub: %v", err)
	}
	if len(f.podCalls) != 1 || !f.podCalls[0].failed || f.podCalls[0].detail != "shred timed out" {
		t.Errorf("delegated %+v, want pod-1 failed with detail", f.podCalls)
	}
}

// TestReportPodScrubUnspecifiedFailsClosed verifies an unspecified whole-pod
// scrub outcome is rejected with InvalidArgument and never delegated.
// spec: 4.7 (ReportPodScrub), 5.2 (scrub model)
//
// diagnosis: a failure means the handler accepted an UNSPECIFIED pod scrub
// outcome, so the gateway could not tell a clean scrub from a failure and
// might reuse a pod whose residual state was not cleared.
func TestReportPodScrubUnspecifiedFailsClosed_spec_5_2(t *testing.T) {
	f := &fakeScrubReports{}
	svc := newServiceWithScrub(t, f)
	_, err := svc.ReportPodScrub(context.Background(), &adapterv1.ReportPodScrubRequest{
		PodId:   "pod-1",
		Outcome: adapterv1.PodScrubOutcome_POD_SCRUB_OUTCOME_UNSPECIFIED,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("err code = %v, want InvalidArgument", status.Code(err))
	}
	if len(f.podCalls) != 0 {
		t.Errorf("unspecified outcome delegated %d calls, want 0", len(f.podCalls))
	}
}

// TestScrubReportsUnconfigured verifies both RPCs return Unimplemented when
// no ScrubReportService is wired (the §8.6-only GatewayControl deployment).
// spec: 4.7 (ReportSessionScrub/ReportPodScrub)
//
// diagnosis: a failure means a gateway deployment that wires only the §8.6
// lease-extension control plane panics or mis-handles a scrub report instead
// of cleanly reporting the RPC as unimplemented.
func TestScrubReportsUnconfigured_spec_4_7(t *testing.T) {
	svc := newServiceWithScrub(t, nil)
	if _, err := svc.ReportSessionScrub(context.Background(), &adapterv1.ReportSessionScrubRequest{
		PodId: "pod-1", SessionId: &adapterv1.SessionId{Value: "s"},
		Outcome: adapterv1.SessionScrubOutcome_SESSION_SCRUB_OUTCOME_RELEASED,
	}); status.Code(err) != codes.Unimplemented {
		t.Errorf("session scrub unconfigured: code = %v, want Unimplemented", status.Code(err))
	}
	if _, err := svc.ReportPodScrub(context.Background(), &adapterv1.ReportPodScrubRequest{
		PodId: "pod-1", Outcome: adapterv1.PodScrubOutcome_POD_SCRUB_OUTCOME_SUCCEEDED,
	}); status.Code(err) != codes.Unimplemented {
		t.Errorf("pod scrub unconfigured: code = %v, want Unimplemented", status.Code(err))
	}
}

// TestReportPodScrubServiceErrorMapsInternal verifies a gateway-side failure
// from the service surfaces as a gRPC Internal status, not a leaked Go error.
// spec: 4.7 (ReportPodScrub)
//
// diagnosis: a failure means a transient gateway-side recycle failure is
// surfaced to the adapter as a raw error or the wrong code, breaking the
// adapter's retry contract on the scrub report.
func TestReportPodScrubServiceErrorMapsInternal_spec_4_7(t *testing.T) {
	f := &fakeScrubReports{podErr: errors.New("k8s patch failed")}
	svc := newServiceWithScrub(t, f)
	_, err := svc.ReportPodScrub(context.Background(), &adapterv1.ReportPodScrubRequest{
		PodId:   "pod-1",
		Outcome: adapterv1.PodScrubOutcome_POD_SCRUB_OUTCOME_SUCCEEDED,
	})
	if status.Code(err) != codes.Internal {
		t.Fatalf("service error code = %v, want Internal", status.Code(err))
	}
}

// --- ScrubReporter orchestrator tests ---

// fakeCounters is a RecycleCounterStore double with in-memory per-pod
// counters. A pod absent from the map reports found=false.
type fakeCounters struct {
	served  map[string]int
	scrub   map[string]int
	missing map[string]bool
	readErr error
}

func newFakeCounters() *fakeCounters {
	return &fakeCounters{served: map[string]int{}, scrub: map[string]int{}, missing: map[string]bool{}}
}

func (f *fakeCounters) IncrementSessionsServed(_ context.Context, podID string) (int, bool, error) {
	if f.missing[podID] {
		return 0, false, nil
	}
	f.served[podID]++
	return f.served[podID], true, nil
}

func (f *fakeCounters) IncrementScrubFailureCount(_ context.Context, podID string) (int, bool, error) {
	if f.missing[podID] {
		return 0, false, nil
	}
	f.scrub[podID]++
	return f.scrub[podID], true, nil
}

func (f *fakeCounters) RecycleCounters(_ context.Context, podID string) (int, int, bool, error) {
	if f.readErr != nil {
		return 0, 0, false, f.readErr
	}
	if f.missing[podID] {
		return 0, 0, false, nil
	}
	return f.served[podID], f.scrub[podID], true, nil
}

type fakeLedger struct{ leaks []string }

func (f *fakeLedger) RecordLeak(_ context.Context, podID string) error {
	f.leaks = append(f.leaks, podID)
	return nil
}

type fakeInspector struct {
	policy leasecontrol.PodRecyclePolicy
	found  bool
	err    error
}

func (f *fakeInspector) InspectForRecycle(_ context.Context, _ string) (leasecontrol.PodRecyclePolicy, bool, error) {
	return f.policy, f.found, f.err
}

type recycleCall struct {
	podID                    string
	preConnect, scrubWarning bool
}

type retireCall struct {
	podID        string
	failed       bool
	scrubWarning bool
	reason       taskcleanup.RetireReason
	detail       string
}

type fakeDriver struct {
	recycles   []recycleCall
	retires    []retireCall
	recycleErr error
	retireErr  error
}

func (f *fakeDriver) Recycle(_ context.Context, podID string, preConnect, scrubWarning bool) error {
	f.recycles = append(f.recycles, recycleCall{podID, preConnect, scrubWarning})
	return f.recycleErr
}

func (f *fakeDriver) Retire(_ context.Context, podID string, failed, scrubWarning bool, reason taskcleanup.RetireReason, detail string) error {
	f.retires = append(f.retires, retireCall{podID, failed, scrubWarning, reason, detail})
	return f.retireErr
}

// retirementCall records one lenny_pod_retirement_total emission with its
// §16.1 reason, pool, and runtime_class labels.
type retirementCall struct {
	reason       taskcleanup.RetireReason
	pool         string
	runtimeClass string
}

// scrubGauge records one lenny_pod_scrub_failure_count gauge emission with
// its §16.1 pool and runtime_class labels and the cumulative count.
type scrubGauge struct {
	pool         string
	runtimeClass string
	count        int
}

// scrubTotal records one lenny_pod_scrub_failure_total aggregate-counter
// emission with its §16.1 pool and runtime_class labels.
type scrubTotal struct {
	pool         string
	runtimeClass string
}

// recordingRetireMetrics is a RetirementMetrics double that records the
// aggregate scrub-failure counter, the per-pod gauge, and the retirement
// counter emissions (with their §16.1 pool/runtime_class labels) so the
// disposition's metric side effects can be asserted.
type recordingRetireMetrics struct {
	totals      []scrubTotal
	gauges      map[string]scrubGauge
	retirements []retirementCall
}

func newRecordingRetireMetrics() *recordingRetireMetrics {
	return &recordingRetireMetrics{gauges: map[string]scrubGauge{}}
}

func (m *recordingRetireMetrics) IncScrubFailureTotal(pool, runtimeClass string) {
	m.totals = append(m.totals, scrubTotal{pool, runtimeClass})
}

func (m *recordingRetireMetrics) SetScrubFailureCount(podID, pool, runtimeClass string, count int) {
	m.gauges[podID] = scrubGauge{pool, runtimeClass, count}
}

func (m *recordingRetireMetrics) IncRetirement(r taskcleanup.RetireReason, pool, runtimeClass string) {
	m.retirements = append(m.retirements, retirementCall{r, pool, runtimeClass})
}

func newReporter(t *testing.T, c *fakeCounters, l *fakeLedger, i *fakeInspector, d *fakeDriver) *leasecontrol.ScrubReporter {
	t.Helper()
	r, err := leasecontrol.NewScrubReporter(leasecontrol.ScrubReporterOptions{
		Counters: c, Ledger: l, Inspector: i, Driver: d,
	})
	if err != nil {
		t.Fatalf("NewScrubReporter: %v", err)
	}
	return r
}

// TestReporterSessionScrubIncrementsAndLeaks verifies the orchestrator
// increments sessions_served on every release and records a leak only on a
// leaked outcome.
// spec: 4.7 (ReportSessionScrub increments sessionsServed; leaked feeds the ledger), 5.2 (scrub model)
//
// diagnosis: a failure means the gateway's session-release accounting drifted
// — the recycle session-count cap would never trip, or a leaked slot would
// not feed the unhealthy-threshold drain ledger.
func TestReporterSessionScrubIncrementsAndLeaks_spec_4_7(t *testing.T) {
	c, l, i, d := newFakeCounters(), &fakeLedger{}, &fakeInspector{}, &fakeDriver{}
	r := newReporter(t, c, l, i, d)
	if err := r.RecordSessionScrub(context.Background(), "pod-1", "s1", "", false); err != nil {
		t.Fatalf("released: %v", err)
	}
	if err := r.RecordSessionScrub(context.Background(), "pod-1", "s2", "", true); err != nil {
		t.Fatalf("leaked: %v", err)
	}
	if c.served["pod-1"] != 2 {
		t.Errorf("sessions_served = %d, want 2", c.served["pod-1"])
	}
	if len(l.leaks) != 1 || l.leaks[0] != "pod-1" {
		t.Errorf("leaks = %v, want one pod-1", l.leaks)
	}
}

// TestReporterSessionScrubMissingPodFailsClosed verifies a session scrub for a
// pod absent from the mirror fails closed rather than silently no-opping.
// spec: 4.7 (ReportSessionScrub)
//
// diagnosis: a failure means a scrub report for an unknown pod is swallowed,
// so a counter that should have keyed a disposition is never written and the
// pod's recycle accounting silently diverges from reality.
func TestReporterSessionScrubMissingPodFailsClosed_spec_4_7(t *testing.T) {
	c, l, i, d := newFakeCounters(), &fakeLedger{}, &fakeInspector{}, &fakeDriver{}
	c.missing["ghost"] = true
	r := newReporter(t, c, l, i, d)
	err := r.RecordSessionScrub(context.Background(), "ghost", "s1", "", false)
	if !errors.Is(err, leasecontrol.ErrPodNotInMirror) {
		t.Fatalf("missing pod err = %v, want ErrPodNotInMirror", err)
	}
}

// TestReporterPodScrubReuseNonPreConnect verifies a successful scrub under the
// limits on a non-preConnect schedulable host recycles (reserves) the pod.
// spec: 3.4 (recycle disposition), 5.2 (recycle lifecycle)
//
// diagnosis: a failure means a healthy recycling pod is not held for its
// tenant after a clean scrub, so back-to-back same-tenant sessions re-acquire
// a pod instead of rebinding the reserved claim.
func TestReporterPodScrubReuseNonPreConnect_spec_3_4(t *testing.T) {
	c, l, d := newFakeCounters(), &fakeLedger{}, &fakeDriver{}
	c.served["pod-1"] = 1
	i := &fakeInspector{found: true, policy: leasecontrol.PodRecyclePolicy{
		PreConnect: false, OnScrubFailure: taskcleanup.OnCleanupWarn,
		MaxSessionsPerPod: 100, HostSchedulable: true,
	}}
	r := newReporter(t, c, l, i, d)
	if err := r.RecordPodScrub(context.Background(), "pod-1", false, ""); err != nil {
		t.Fatalf("RecordPodScrub: %v", err)
	}
	if len(d.recycles) != 1 || d.recycles[0].preConnect || d.recycles[0].scrubWarning {
		t.Errorf("recycles = %+v, want one non-preConnect no-warning recycle", d.recycles)
	}
	if len(d.retires) != 0 {
		t.Errorf("retires = %+v, want none", d.retires)
	}
}

// TestReporterPodScrubReusePreConnectReWarm verifies a successful scrub on a
// preConnect schedulable host drives the re-warm recycle (preConnect=true).
// spec: 3.4 (recycle disposition), 6.2 (preConnect re-warm)
//
// diagnosis: a failure means a preConnect pod is reserved without re-warming
// the SDK, violating the invariant that every reserved pod in a preConnect
// pool is SDK-warm.
func TestReporterPodScrubReusePreConnectReWarm_spec_6_2(t *testing.T) {
	c, l, d := newFakeCounters(), &fakeLedger{}, &fakeDriver{}
	c.served["pod-1"] = 1
	i := &fakeInspector{found: true, policy: leasecontrol.PodRecyclePolicy{
		PreConnect: true, OnScrubFailure: taskcleanup.OnCleanupWarn,
		MaxSessionsPerPod: 100, HostSchedulable: true,
	}}
	r := newReporter(t, c, l, i, d)
	if err := r.RecordPodScrub(context.Background(), "pod-1", false, ""); err != nil {
		t.Fatalf("RecordPodScrub: %v", err)
	}
	if len(d.recycles) != 1 || !d.recycles[0].preConnect {
		t.Errorf("recycles = %+v, want one preConnect recycle", d.recycles)
	}
}

// TestReporterPodScrubFailedIncrementsAndWarns verifies a failed scrub under
// warn policy increments scrub_failure_count and recycles with the
// scrub_warning carried through.
// spec: 4.7 (scrubFailureCount), 5.2 (onScrubFailure warn)
//
// diagnosis: a failure means a warn-policy scrub failure is not counted toward
// maxScrubFailures, or the scrub_warning annotation is dropped on reuse, so a
// pod with residual-state risk re-enters the pool unmarked.
func TestReporterPodScrubFailedIncrementsAndWarns_spec_5_2(t *testing.T) {
	c, l, d := newFakeCounters(), &fakeLedger{}, &fakeDriver{}
	c.served["pod-1"] = 1
	i := &fakeInspector{found: true, policy: leasecontrol.PodRecyclePolicy{
		PreConnect: false, OnScrubFailure: taskcleanup.OnCleanupWarn,
		MaxScrubFailures: 3, MaxSessionsPerPod: 100, HostSchedulable: true,
	}}
	r := newReporter(t, c, l, i, d)
	if err := r.RecordPodScrub(context.Background(), "pod-1", true, "leftover"); err != nil {
		t.Fatalf("RecordPodScrub: %v", err)
	}
	if c.scrub["pod-1"] != 1 {
		t.Errorf("scrub_failure_count = %d, want 1", c.scrub["pod-1"])
	}
	if len(d.recycles) != 1 || !d.recycles[0].scrubWarning {
		t.Errorf("recycles = %+v, want one recycle with scrub_warning", d.recycles)
	}
}

// TestReporterPodScrubScrubFailuresExhaustedRetires verifies that reaching
// maxScrubFailures under the warn policy retires the pod with the `released`
// terminal (a drain), NOT the `failed` terminal. The scrub-failure-limit
// retirement is the third drain trigger alongside the session-count and
// uptime limits, so it shares their `released` terminal; the `failed`
// terminal is reserved for the onScrubFailure: fail termination and for a
// failed or crashed session (§4.6.3). This pins the finding-2 mapping: the
// landed Retire selects `released` (failed == false) for the warn-policy
// count-limit drain.
// spec: 3.4 (retire disposition), 4.6.3 (released vs failed binding terminals), 5.2 (scrub failure limit under onScrubFailure warn)
//
// diagnosis: a failure means either a pod that has exhausted its scrub-failure
// budget is returned to the pool instead of retired (residual-state risk
// accumulates across sessions), or the count-limit drain is misrouted to the
// `failed` terminal, conflating a lifecycle-limit retirement with a fail-policy
// or crash termination in the audit trail and the projection's drain reason.
func TestReporterPodScrubScrubFailuresExhaustedRetires_spec_5_2(t *testing.T) {
	c, l, d := newFakeCounters(), &fakeLedger{}, &fakeDriver{}
	c.served["pod-1"] = 1
	c.scrub["pod-1"] = 2 // this failure makes 3
	i := &fakeInspector{found: true, policy: leasecontrol.PodRecyclePolicy{
		OnScrubFailure:   taskcleanup.OnCleanupWarn,
		MaxScrubFailures: 3, MaxSessionsPerPod: 100, HostSchedulable: true,
	}}
	r := newReporter(t, c, l, i, d)
	if err := r.RecordPodScrub(context.Background(), "pod-1", true, ""); err != nil {
		t.Fatalf("RecordPodScrub: %v", err)
	}
	if len(d.retires) != 1 {
		t.Fatalf("retires = %+v, want exactly one retire for scrub exhaustion", d.retires)
	}
	got := d.retires[0]
	if got.failed {
		t.Errorf("retire failed = true, want false: scrub_failure_limit drains to the `released` terminal, not `failed`")
	}
	if got.reason != taskcleanup.ReasonScrubFailuresExhausted {
		t.Errorf("retire reason = %q, want %q", got.reason, taskcleanup.ReasonScrubFailuresExhausted)
	}
}

// TestReporterPodScrubFailPolicyTerminates verifies onScrubFailure: fail
// terminates the pod with a failed terminal on any scrub failure, threading
// the adapter-supplied failure detail to the retire path for the audit trail
// and clearing the scrub_warning (the pod leaves the pool for cause).
// spec: 3.4 (retire disposition), 5.2 (onScrubFailure fail; audit retention of the failed pod's metadata)
//
// diagnosis: a failure means a fail-policy pool reuses a pod after a failed
// scrub instead of terminating it, or the adapter-side failure description is
// dropped before the audit write, leaving the terminated pod's audit record
// without the reason it failed.
func TestReporterPodScrubFailPolicyTerminates_spec_5_2(t *testing.T) {
	c, l, d := newFakeCounters(), &fakeLedger{}, &fakeDriver{}
	c.served["pod-1"] = 1
	i := &fakeInspector{found: true, policy: leasecontrol.PodRecyclePolicy{
		OnScrubFailure:   taskcleanup.OnCleanupFail,
		MaxScrubFailures: 3, MaxSessionsPerPod: 100, HostSchedulable: true,
	}}
	r := newReporter(t, c, l, i, d)
	if err := r.RecordPodScrub(context.Background(), "pod-1", true, "shred timed out on /tmp"); err != nil {
		t.Fatalf("RecordPodScrub: %v", err)
	}
	if len(d.retires) != 1 {
		t.Fatalf("retires = %+v, want one failed terminal for fail policy", d.retires)
	}
	got := d.retires[0]
	if !got.failed || got.reason != taskcleanup.ReasonCleanupFailPolicy {
		t.Errorf("retire = %+v, want failed terminal for fail policy", got)
	}
	if got.detail != "shred timed out on /tmp" {
		t.Errorf("retire detail = %q, want the adapter failure description threaded through", got.detail)
	}
	if got.scrubWarning {
		t.Errorf("retire scrubWarning = true, want false (fail-policy termination clears it)")
	}
}

// TestReporterPodScrubWarnFailedCordonDrainCarriesWarning verifies the §6.39
// cordon-drain-under-warn path stamps the scrub_warning audit annotation onto
// the retire: a warn-policy scrub failure that retires because the host node
// is cordoned retains the residual-state marker the disposition computed (the
// `draining [scrub_warning]` case). The adapter-supplied detail is threaded
// to the audit trail.
// spec: 6.39 (host-node schedulability retire, the draining [scrub_warning] cordon-drain), 5.2 (onScrubFailure warn marker persists; audit retention)
//
// diagnosis: a failure means a warn-policy scrub failure that retires on a
// cordoned node writes the released terminal with no scrub_warning, losing the
// residual-state audit marker §6.39/§5.2 retain, or drops the failure detail.
func TestReporterPodScrubWarnFailedCordonDrainCarriesWarning_spec_6_39(t *testing.T) {
	c, l, d := newFakeCounters(), &fakeLedger{}, &fakeDriver{}
	c.served["pod-1"] = 1
	i := &fakeInspector{found: true, policy: leasecontrol.PodRecyclePolicy{
		OnScrubFailure:   taskcleanup.OnCleanupWarn,
		MaxScrubFailures: 3, MaxSessionsPerPod: 100,
		HostSchedulable: false, // cordoned host, warn policy
		Pool:            "agents-pool", RuntimeClass: "gvisor",
	}}
	r := newReporter(t, c, l, i, d)
	if err := r.RecordPodScrub(context.Background(), "pod-1", true, "in-place residue"); err != nil {
		t.Fatalf("RecordPodScrub: %v", err)
	}
	if len(d.retires) != 1 {
		t.Fatalf("retires = %+v, want one cordon-drain retire", d.retires)
	}
	got := d.retires[0]
	if got.failed || got.reason != taskcleanup.ReasonHostUnschedulable {
		t.Errorf("retire = %+v, want released retire for host_unschedulable", got)
	}
	if !got.scrubWarning {
		t.Errorf("retire scrubWarning = false, want true (cordon-drain retains the warn marker)")
	}
	if got.detail != "in-place residue" {
		t.Errorf("retire detail = %q, want the adapter failure description threaded through", got.detail)
	}
}

// TestReporterPodScrubUnschedulableHostRetiresBothPools verifies the §6.39
// unschedulable-host-node retire applies on both preConnect and non-preConnect
// pools even when no limit is reached.
// spec: 6.39 (host-node schedulability retire), 3.4 (retire disposition)
//
// diagnosis: a failure means a recycling pod on a cordoned node is held in
// reserved or re-warmed instead of retired, so the next session is handed a
// soon-to-be-evicted pod.
func TestReporterPodScrubUnschedulableHostRetiresBothPools_spec_6_39(t *testing.T) {
	for _, preConnect := range []bool{false, true} {
		c, l, d := newFakeCounters(), &fakeLedger{}, &fakeDriver{}
		c.served["pod-1"] = 1
		i := &fakeInspector{found: true, policy: leasecontrol.PodRecyclePolicy{
			PreConnect: preConnect, OnScrubFailure: taskcleanup.OnCleanupWarn,
			MaxScrubFailures: 3, MaxSessionsPerPod: 100,
			HostSchedulable: false, // cordoned, no limit reached
		}}
		r := newReporter(t, c, l, i, d)
		if err := r.RecordPodScrub(context.Background(), "pod-1", false, ""); err != nil {
			t.Fatalf("preConnect=%v: RecordPodScrub: %v", preConnect, err)
		}
		if len(d.retires) != 1 || d.retires[0].failed || d.retires[0].reason != taskcleanup.ReasonHostUnschedulable {
			t.Errorf("preConnect=%v: retires = %+v, want one released retire for host_unschedulable", preConnect, d.retires)
		}
		if len(d.recycles) != 0 {
			t.Errorf("preConnect=%v: recycles = %+v, want none (retired)", preConnect, d.recycles)
		}
	}
}

// TestReporterPodScrubMissingPodIsNoOp verifies a whole-pod scrub for a pod
// whose claim is gone is a no-op (nothing left to recycle), not an error.
// spec: 3.4 (recycle disposition)
//
// diagnosis: a failure means a scrub report racing a concurrent retirement
// (the pod or claim already gone) errors or mis-patches a non-existent claim
// instead of cleanly skipping.
func TestReporterPodScrubMissingPodIsNoOp_spec_3_4(t *testing.T) {
	c, l, d := newFakeCounters(), &fakeLedger{}, &fakeDriver{}
	c.served["pod-1"] = 1
	i := &fakeInspector{found: false} // claim gone
	r := newReporter(t, c, l, i, d)
	if err := r.RecordPodScrub(context.Background(), "pod-1", false, ""); err != nil {
		t.Fatalf("RecordPodScrub: %v", err)
	}
	if len(d.recycles) != 0 || len(d.retires) != 0 {
		t.Errorf("missing claim drove dispositions: recycles=%+v retires=%+v", d.recycles, d.retires)
	}
}

// TestNewScrubReporterRequiresDeps verifies the constructor fails closed when
// a required seam is nil rather than panicking on the request path.
// spec: 4.7 (ReportSessionScrub/ReportPodScrub)
//
// diagnosis: a failure means a misconfigured gateway builds a ScrubReporter
// with a nil counter store, ledger, inspector, or driver and panics on the
// first scrub report instead of failing at construction.
func TestNewScrubReporterRequiresDeps_spec_4_7(t *testing.T) {
	full := leasecontrol.ScrubReporterOptions{
		Counters: newFakeCounters(), Ledger: &fakeLedger{},
		Inspector: &fakeInspector{}, Driver: &fakeDriver{},
	}
	cases := map[string]func(*leasecontrol.ScrubReporterOptions){
		"no counters":  func(o *leasecontrol.ScrubReporterOptions) { o.Counters = nil },
		"no ledger":    func(o *leasecontrol.ScrubReporterOptions) { o.Ledger = nil },
		"no inspector": func(o *leasecontrol.ScrubReporterOptions) { o.Inspector = nil },
		"no driver":    func(o *leasecontrol.ScrubReporterOptions) { o.Driver = nil },
	}
	for name, mutate := range cases {
		opts := full
		mutate(&opts)
		if _, err := leasecontrol.NewScrubReporter(opts); err == nil {
			t.Errorf("%s: NewScrubReporter err = nil, want non-nil", name)
		}
	}
}

// TestReporterPodScrubEmitsMetrics verifies the §16.1 retirement and
// scrub-failure metrics are emitted on a failed scrub that retires, carrying
// their mandated pool and runtime_class label dimensions. The aggregate
// lenny_pod_scrub_failure_total counter increments alongside the per-pod
// lenny_pod_scrub_failure_count gauge.
// spec: 16.1 (lenny_pod_scrub_failure_total, lenny_pod_scrub_failure_count, lenny_pod_retirement_total labeled by pool, runtime_class), 5.2 (both scrub-failure series increment on failure)
//
// diagnosis: a failure means the operator-facing recycle observability is
// missing — the aggregate scrub-failure counter is not emitted, a retiring
// pod's reason is not counted, the per-pod scrub-failure gauge does not track
// the cumulative count, or the pool/runtime_class dimensions the §16.1
// inventory mandates are dropped.
func TestReporterPodScrubEmitsMetrics_spec_16_1(t *testing.T) {
	c, l, d := newFakeCounters(), &fakeLedger{}, &fakeDriver{}
	c.served["pod-1"] = 1
	c.scrub["pod-1"] = 2 // this failure makes 3, exhausting maxScrubFailures
	i := &fakeInspector{found: true, policy: leasecontrol.PodRecyclePolicy{
		OnScrubFailure: taskcleanup.OnCleanupWarn, MaxScrubFailures: 3,
		MaxSessionsPerPod: 100, HostSchedulable: true,
		Pool: "agents-pool", RuntimeClass: "gvisor",
	}}
	m := newRecordingRetireMetrics()
	r, err := leasecontrol.NewScrubReporter(leasecontrol.ScrubReporterOptions{
		Counters: c, Ledger: l, Inspector: i, Driver: d, Metrics: m,
	})
	if err != nil {
		t.Fatalf("NewScrubReporter: %v", err)
	}
	if err := r.RecordPodScrub(context.Background(), "pod-1", true, ""); err != nil {
		t.Fatalf("RecordPodScrub: %v", err)
	}
	if len(m.totals) != 1 || m.totals[0].pool != "agents-pool" || m.totals[0].runtimeClass != "gvisor" {
		t.Errorf("scrub-failure total = %+v, want one agents-pool/gvisor increment", m.totals)
	}
	g := m.gauges["pod-1"]
	if g.count != 3 || g.pool != "agents-pool" || g.runtimeClass != "gvisor" {
		t.Errorf("scrub-failure gauge = %+v, want count 3 labeled agents-pool/gvisor", g)
	}
	if len(m.retirements) != 1 {
		t.Fatalf("retirements = %v, want one", m.retirements)
	}
	if got := m.retirements[0]; got.reason != taskcleanup.ReasonScrubFailuresExhausted || got.pool != "agents-pool" || got.runtimeClass != "gvisor" {
		t.Errorf("retirement = %+v, want scrub_failure_limit labeled agents-pool/gvisor", got)
	}
}

// TestReporterPodScrubCordonDrainEmitsNoRetirementCounter verifies the §6.39
// cordon-drain retire drains the pod without incrementing
// lenny_pod_retirement_total: the counter's reason label is frozen to the
// three §16.1 limit triggers (session_count_limit, uptime_limit,
// scrub_failure_limit) and host_unschedulable is outside that vocabulary, so
// the disposition drives the drain and records the reason on the claim audit
// trail but emits no retirement-counter increment with an out-of-vocabulary
// label value. The driver still receives the cordon-drain retire.
// spec: 16.1 (lenny_pod_retirement_total reason label set), 16.1.1 (reason is the lifecycle limit triggers only), 6.39 (cordon-drain operational retire)
//
// diagnosis: a failure means the §6.39 cordon-drain emits
// lenny_pod_retirement_total{reason="host_unschedulable"}, a fourth reason
// value the §16.1 inventory does not declare, widening the frozen label set
// and breaking the metric-catalog vocabulary contract.
func TestReporterPodScrubCordonDrainEmitsNoRetirementCounter_spec_16_1(t *testing.T) {
	c, l, d := newFakeCounters(), &fakeLedger{}, &fakeDriver{}
	c.served["pod-1"] = 1
	i := &fakeInspector{found: true, policy: leasecontrol.PodRecyclePolicy{
		OnScrubFailure: taskcleanup.OnCleanupWarn, MaxScrubFailures: 3,
		MaxSessionsPerPod: 100, HostSchedulable: false, // cordoned, no limit reached
		Pool: "agents-pool", RuntimeClass: "gvisor",
	}}
	m := newRecordingRetireMetrics()
	r, err := leasecontrol.NewScrubReporter(leasecontrol.ScrubReporterOptions{
		Counters: c, Ledger: l, Inspector: i, Driver: d, Metrics: m,
	})
	if err != nil {
		t.Fatalf("NewScrubReporter: %v", err)
	}
	if err := r.RecordPodScrub(context.Background(), "pod-1", false, ""); err != nil {
		t.Fatalf("RecordPodScrub: %v", err)
	}
	// The pod is drained on the cordon-drain disposition.
	if len(d.retires) != 1 || d.retires[0].reason != taskcleanup.ReasonHostUnschedulable {
		t.Fatalf("retires = %+v, want one host_unschedulable cordon-drain", d.retires)
	}
	// But the retirement counter is NOT incremented: host_unschedulable is
	// outside the §16.1 vocabulary.
	if len(m.retirements) != 0 {
		t.Errorf("retirements = %+v, want none (host_unschedulable is outside the §16.1 vocabulary)", m.retirements)
	}
}

// TestReporterPodScrubFailPolicyEmitsNoRetirementCounter verifies the
// onScrubFailure: fail termination drains to the claim `failed` terminal
// without incrementing lenny_pod_retirement_total: cleanup_fail_policy is a
// failure-driven retire that §16.1.1 classifies under error_type rather than
// the retirement-counter reason vocabulary, so the counter is not widened.
// spec: 16.1 (lenny_pod_retirement_total reason label set), 16.1.1 (failures use error_type, not reason), 5.2 (onScrubFailure: fail)
//
// diagnosis: a failure means the fail-policy termination emits a
// lenny_pod_retirement_total{reason="cleanup_fail_policy"} increment, a value
// the §16.1 inventory does not declare.
func TestReporterPodScrubFailPolicyEmitsNoRetirementCounter_spec_16_1(t *testing.T) {
	c, l, d := newFakeCounters(), &fakeLedger{}, &fakeDriver{}
	c.served["pod-1"] = 1
	i := &fakeInspector{found: true, policy: leasecontrol.PodRecyclePolicy{
		OnScrubFailure: taskcleanup.OnCleanupFail, MaxScrubFailures: 3,
		MaxSessionsPerPod: 100, HostSchedulable: true,
		Pool: "agents-pool", RuntimeClass: "gvisor",
	}}
	m := newRecordingRetireMetrics()
	r, err := leasecontrol.NewScrubReporter(leasecontrol.ScrubReporterOptions{
		Counters: c, Ledger: l, Inspector: i, Driver: d, Metrics: m,
	})
	if err != nil {
		t.Fatalf("NewScrubReporter: %v", err)
	}
	if err := r.RecordPodScrub(context.Background(), "pod-1", true, "shred failed"); err != nil {
		t.Fatalf("RecordPodScrub: %v", err)
	}
	if len(d.retires) != 1 || !d.retires[0].failed || d.retires[0].reason != taskcleanup.ReasonCleanupFailPolicy {
		t.Fatalf("retires = %+v, want one failed-terminal cleanup_fail_policy retire", d.retires)
	}
	if len(m.retirements) != 0 {
		t.Errorf("retirements = %+v, want none (cleanup_fail_policy is outside the §16.1 vocabulary)", m.retirements)
	}
	// The aggregate scrub-failure series still increments on the failed scrub.
	if len(m.totals) != 1 {
		t.Errorf("scrub-failure totals = %+v, want one (the failed scrub still counts on the aggregate)", m.totals)
	}
}

// TestReporterPodScrubSuccessEmitsNoScrubFailureMetrics verifies a clean
// whole-pod scrub increments neither the aggregate lenny_pod_scrub_failure_total
// counter nor the per-pod gauge, so a healthy reuse does not pollute the
// scrub-failure series.
// spec: 5.2 (both scrub-failure series increment only on a FAILED outcome)
//
// diagnosis: a failure means a successful scrub is being counted as a scrub
// failure, inflating the aggregate counter and the per-pod gauge and
// triggering the maxScrubFailures retire prematurely.
func TestReporterPodScrubSuccessEmitsNoScrubFailureMetrics_spec_5_2(t *testing.T) {
	c, l, d := newFakeCounters(), &fakeLedger{}, &fakeDriver{}
	c.served["pod-1"] = 1
	i := &fakeInspector{found: true, policy: leasecontrol.PodRecyclePolicy{
		OnScrubFailure: taskcleanup.OnCleanupWarn, MaxSessionsPerPod: 100,
		HostSchedulable: true, Pool: "agents-pool", RuntimeClass: "runc",
	}}
	m := newRecordingRetireMetrics()
	r, err := leasecontrol.NewScrubReporter(leasecontrol.ScrubReporterOptions{
		Counters: c, Ledger: l, Inspector: i, Driver: d, Metrics: m,
	})
	if err != nil {
		t.Fatalf("NewScrubReporter: %v", err)
	}
	if err := r.RecordPodScrub(context.Background(), "pod-1", false, ""); err != nil {
		t.Fatalf("RecordPodScrub: %v", err)
	}
	if len(m.totals) != 0 {
		t.Errorf("scrub-failure totals = %+v, want none on a clean scrub", m.totals)
	}
	if _, ok := m.gauges["pod-1"]; ok {
		t.Errorf("scrub-failure gauge emitted on a clean scrub: %+v", m.gauges)
	}
}

// TestReporterPodScrubCounterReadErrorPropagates verifies a recycle-counter
// read failure surfaces as an error rather than driving a disposition off a
// zero count.
// spec: 4.7 (ReportPodScrub)
//
// diagnosis: a failure means a transient agent_pod_state read fault is
// swallowed and the disposition runs against an unreliable counter, risking
// an unwarranted reuse or retirement.
func TestReporterPodScrubCounterReadErrorPropagates_spec_4_7(t *testing.T) {
	c, l, d := newFakeCounters(), &fakeLedger{}, &fakeDriver{}
	c.served["pod-1"] = 1
	c.readErr = errors.New("postgres unavailable")
	i := &fakeInspector{found: true, policy: leasecontrol.PodRecyclePolicy{
		OnScrubFailure: taskcleanup.OnCleanupWarn, MaxSessionsPerPod: 100, HostSchedulable: true,
	}}
	r := newReporter(t, c, l, i, d)
	if err := r.RecordPodScrub(context.Background(), "pod-1", false, ""); err == nil {
		t.Fatal("counter read error: err = nil, want non-nil")
	}
	if len(d.recycles) != 0 || len(d.retires) != 0 {
		t.Errorf("counter read error drove a disposition: recycles=%+v retires=%+v", d.recycles, d.retires)
	}
}

// TestReporterPodScrubDriverErrorPropagates verifies a claim binding-state
// patch failure surfaces as an error so the adapter can retry the scrub
// report.
// spec: 3.4 (recycle disposition drives the claim binding state)
//
// diagnosis: a failure means a failed reserved/retire claim patch is
// swallowed, so the handler reports success and the adapter never retries the
// scrub report, leaving the pod's claim in `recycling` with no disposition.
// This step's handler only surfaces the error for the retry; the recycle
// coordinator that patches `bound → recycling` and bounds a never-arriving
// report owns the missing-report backstop, so a swallowed error here strands
// the claim until that separately-wired backstop or the orphan GC reclaims it.
func TestReporterPodScrubDriverErrorPropagates_spec_3_4(t *testing.T) {
	c, l := newFakeCounters(), &fakeLedger{}
	c.served["pod-1"] = 1
	i := &fakeInspector{found: true, policy: leasecontrol.PodRecyclePolicy{
		OnScrubFailure: taskcleanup.OnCleanupWarn, MaxSessionsPerPod: 100, HostSchedulable: true,
	}}
	d := &fakeDriver{recycleErr: errors.New("ssa patch conflict")}
	r := newReporter(t, c, l, i, d)
	if err := r.RecordPodScrub(context.Background(), "pod-1", false, ""); err == nil {
		t.Fatal("recycle driver error: err = nil, want non-nil")
	}
}
