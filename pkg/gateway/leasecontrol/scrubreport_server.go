// SPDX-License-Identifier: MIT

package leasecontrol

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
	"github.com/lennylabs/lenny/pkg/sandbox/taskcleanup"
)

// ScrubReportService is the gateway-side surface the §4.7 scrub-report RPCs
// (ReportSessionScrub and ReportPodScrub) drive. The two handlers on the
// leasecontrol Service decode and fail-closed-validate the wire request,
// then hand the typed outcome to this interface, which owns the side
// effects the spec assigns to the gateway: the per-pod recycle-counter
// writes on agent_pod_state, the unhealthy-threshold drain ledger behind
// the lenny.dev/drain-request annotation, the §6.39 host-node
// schedulability read, the taskcleanup recycle disposition, and the
// SandboxClaim binding-state patches that drive the recycle or retire.
//
// The interface is defined at the consumer so leasecontrol stays free of a
// direct dependency on the Kubernetes client, agentpodstate, slothealth,
// podclaim, and taskcleanup. The gateway wires a concrete implementation;
// tests pass a fake.
//
// spec: §4.7 (ReportSessionScrub/ReportPodScrub), §5.2 (scrub model,
// onScrubFailure), §3.4 (recycle disposition, retire triggers), §6.39
// (host-node schedulability retire at recycle disposition).
type ScrubReportService interface {
	// RecordSessionScrub records the §5.2 per-slot cleanup outcome at a
	// session release. It increments sessionsServed on the pod's
	// agent_pod_state row, and when leaked is true it feeds the
	// unhealthy-threshold drain ledger behind lenny.dev/drain-request
	// (§4.6.3). slotID is empty unless the pod runs concurrent sessions.
	// A gateway-side failure is returned as an error the handler maps to a
	// gRPC status. spec: §4.7 (ReportSessionScrub increments sessionsServed;
	// leaked feeds the drain ledger), §5.2.
	RecordSessionScrub(ctx context.Context, podID, sessionID, slotID string, leaked bool) error

	// RecordPodScrub records the §5.2 whole-pod scrub outcome at the
	// occupancy-zero recycle boundary and drives the §3.4 / §6.39 recycle
	// disposition. When failed is true it increments scrubFailureCount on
	// the pod's agent_pod_state row. It then reads the pod's
	// lenny.dev/host-schedulable label, computes the disposition against the
	// pool's sessionPolicy and the pod's uptime via taskcleanup.Decide, and
	// drives it: on recycle, a preConnect pod stamps rewarmStartedAt and
	// coordinates the SDK re-warm before reserving, a non-preConnect pod
	// patches the claim directly to reserved; on retire (a limit reached, or
	// the §6.39 unschedulable-host-node trigger when the label reads "false"
	// or is absent), it writes the terminal disposition so the projection
	// drains. detail carries an optional adapter-side failure description for
	// the audit trail. A gateway-side failure is returned as an error.
	// spec: §4.7 (ReportPodScrub increments scrubFailureCount, computes
	// disposition), §3.4, §5.2, §6.39.
	RecordPodScrub(ctx context.Context, podID string, failed bool, detail string) error
}

// ReportSessionScrub handles the §4.7 adapter→gateway per-slot cleanup
// report. The adapter sends it on every session release. The handler
// fail-closed-validates the request and feeds the typed outcome to the
// ScrubReportService, which increments sessionsServed and routes a leaked
// outcome into the unhealthy-threshold drain ledger.
//
// A SESSION_SCRUB_OUTCOME_UNSPECIFIED outcome is rejected fail-closed: the
// gateway cannot tell a clean release from a leak, and silently treating it
// as either would either drop a leak signal or over-count a drain, so the
// handler returns InvalidArgument rather than guessing. spec: §4.7; §5.2.
func (s *Service) ReportSessionScrub(ctx context.Context, req *adapterv1.ReportSessionScrubRequest) (*adapterv1.ReportSessionScrubResponse, error) {
	if s.scrubReports == nil {
		return nil, status.Error(codes.Unimplemented, "leasecontrol: scrub-report handling is not configured on this gateway")
	}
	podID := req.GetPodId()
	if podID == "" {
		return nil, status.Error(codes.InvalidArgument, "leasecontrol: ReportSessionScrub request carries no pod id")
	}
	sessionID := req.GetSessionId().GetValue()
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "leasecontrol: ReportSessionScrub request carries no session id")
	}
	leaked, err := sessionScrubLeaked(req.GetOutcome())
	if err != nil {
		return nil, err
	}
	// slot_id is set only when the pod runs concurrent sessions; an empty
	// value is the single-session case and the service ignores it.
	slotID := req.GetSlotId().GetValue()
	if err := s.scrubReports.RecordSessionScrub(ctx, podID, sessionID, slotID, leaked); err != nil {
		return nil, status.Errorf(codes.Internal, "leasecontrol: record session scrub for pod %s session %s: %v", podID, sessionID, err)
	}
	return &adapterv1.ReportSessionScrubResponse{}, nil
}

// ReportPodScrub handles the §4.7 adapter→gateway whole-pod scrub report.
// The adapter sends it when occupancy reaches zero on a recycling pod and
// the gateway has patched the claim to recycling. The handler
// fail-closed-validates the request and feeds the binary outcome to the
// ScrubReportService, which increments scrubFailureCount on failure and
// computes and drives the recycle disposition (including the §6.39
// unschedulable-host-node retire).
//
// A POD_SCRUB_OUTCOME_UNSPECIFIED outcome is rejected fail-closed: the
// gateway cannot tell a clean scrub from a failure, so it cannot decide
// between reuse and the scrub-failure retirement path. The handler returns
// InvalidArgument rather than assuming success and risking reuse of a pod
// whose residual state was not cleared. spec: §4.7; §5.2; §3.4; §6.39.
func (s *Service) ReportPodScrub(ctx context.Context, req *adapterv1.ReportPodScrubRequest) (*adapterv1.ReportPodScrubResponse, error) {
	if s.scrubReports == nil {
		return nil, status.Error(codes.Unimplemented, "leasecontrol: scrub-report handling is not configured on this gateway")
	}
	podID := req.GetPodId()
	if podID == "" {
		return nil, status.Error(codes.InvalidArgument, "leasecontrol: ReportPodScrub request carries no pod id")
	}
	failed, err := podScrubFailed(req.GetOutcome())
	if err != nil {
		return nil, err
	}
	if err := s.scrubReports.RecordPodScrub(ctx, podID, failed, req.GetDetail()); err != nil {
		return nil, status.Errorf(codes.Internal, "leasecontrol: record pod scrub for pod %s: %v", podID, err)
	}
	return &adapterv1.ReportPodScrubResponse{}, nil
}

// sessionScrubLeaked maps the §5.2 per-slot scrub outcome enum to the
// boolean leak flag the ledger consumes, rejecting the unspecified value
// fail-closed. RELEASED is a clean release (not leaked); LEAKED feeds the
// drain ledger. spec: §5.2 (scrub model).
func sessionScrubLeaked(o adapterv1.SessionScrubOutcome) (bool, error) {
	switch o {
	case adapterv1.SessionScrubOutcome_SESSION_SCRUB_OUTCOME_RELEASED:
		return false, nil
	case adapterv1.SessionScrubOutcome_SESSION_SCRUB_OUTCOME_LEAKED:
		return true, nil
	default:
		// SESSION_SCRUB_OUTCOME_UNSPECIFIED and any out-of-enum value.
		return false, status.Error(codes.InvalidArgument, "leasecontrol: ReportSessionScrub outcome is unspecified")
	}
}

// podScrubFailed maps the §5.2 whole-pod scrub outcome enum to the boolean
// failure flag the disposition consumes, rejecting the unspecified value
// fail-closed. spec: §5.2 (scrub model, onScrubFailure).
func podScrubFailed(o adapterv1.PodScrubOutcome) (bool, error) {
	switch o {
	case adapterv1.PodScrubOutcome_POD_SCRUB_OUTCOME_SUCCEEDED:
		return false, nil
	case adapterv1.PodScrubOutcome_POD_SCRUB_OUTCOME_FAILED:
		return true, nil
	default:
		// POD_SCRUB_OUTCOME_UNSPECIFIED and any out-of-enum value.
		return false, status.Error(codes.InvalidArgument, "leasecontrol: ReportPodScrub outcome is unspecified")
	}
}

// RecycleCounterStore is the §4.7 recycle-counter seam the ScrubReporter
// drives: the two per-pod counters on the agent_pod_state row the §3.4 /
// §6.39 recycle disposition evaluates. *agentpodstate.Store (the Postgres
// mirror) satisfies it; the interface is kept narrow so leasecontrol does
// not depend on the full mirror surface. A missing pod row reports
// found=false so the caller fails closed (a scrub report for a pod the
// mirror does not know cannot drive a safe disposition).
// spec: §4.7 (sessionsServed / scrubFailureCount increments), §5.2.
type RecycleCounterStore interface {
	// IncrementSessionsServed adds one to the pod's sessions_served counter
	// and returns the new value. found is false when the pod row is absent.
	IncrementSessionsServed(ctx context.Context, podID string) (count int, found bool, err error)
	// IncrementScrubFailureCount adds one to the pod's scrub_failure_count
	// counter and returns the new value. found is false when the pod row is
	// absent.
	IncrementScrubFailureCount(ctx context.Context, podID string) (count int, found bool, err error)
	// RecycleCounters reads both recycle counters back without mutating
	// them, for the disposition on a successful scrub. found is false when
	// the pod row is absent.
	RecycleCounters(ctx context.Context, podID string) (sessionsServed, scrubFailureCount int, found bool, err error)
}

// DrainLedger is the §5.2 unhealthy-threshold drain ledger seam: a leaked
// session-scrub outcome is recorded against the pod so the gateway can
// stamp lenny.dev/drain-request once the pod crosses the threshold.
// *slothealth.Tracker satisfies the recording half; the threshold check and
// the annotation stamp are the ledger's own concern. spec: §4.7 (leaked
// feeds the unhealthy-threshold ledger), §4.6.3 (drain-request).
type DrainLedger interface {
	// RecordLeak records one leaked session-scrub outcome for podID. The
	// ledger decides when the pod crosses the unhealthy threshold and stamps
	// the drain-request annotation.
	RecordLeak(ctx context.Context, podID string) error
}

// PodRecyclePolicy is the resolved §5.2 sessionPolicy.recycle the recycle
// disposition reads, plus the pod facts the disposition needs that do not
// live on the agent_pod_state recycle counters. The ScrubReporter resolves
// it for the pod under report through the injected PodInspector.
type PodRecyclePolicy struct {
	// PreConnect mirrors the pool runtime's capabilities.preConnect: an
	// idle pod is SDK-warm and the recycle reuse path re-warms through
	// sdk_connecting before reserving. spec: §6.2.
	PreConnect bool
	// OnScrubFailure is sessionPolicy.recycle.onScrubFailure (warn or fail).
	OnScrubFailure taskcleanup.CleanupFailurePolicy
	// MaxScrubFailures is recycle.maxScrubFailures (default applied by
	// taskcleanup when <= 0).
	MaxScrubFailures int
	// MaxSessionsPerPod is recycle.maxSessionsPerPod (required, >= 1).
	MaxSessionsPerPod int
	// MaxPodUptimeSeconds is recycle.maxPodUptimeSeconds (0 = no cap).
	MaxPodUptimeSeconds int64
	// HostSchedulable is the lenny.dev/host-schedulable pod label read at the
	// recycle boundary: true only when the label reads "true". An absent or
	// "false" label is fail-safe-unschedulable. spec: §6.39.
	HostSchedulable bool
	// PodUptimeSeconds is the pod's wall-clock uptime in whole seconds,
	// floored at zero. spec: §6.2 (maxPodUptimeSeconds retire).
	PodUptimeSeconds int64
	// Pool is the pod's pool name, the pool dimension on the §16.1
	// recycle metrics (lenny_pod_scrub_failure_total,
	// lenny_pod_scrub_failure_count, lenny_pod_retirement_total). spec:
	// §16.1 (pool label).
	Pool string
	// RuntimeClass is the pod's Kubernetes RuntimeClass name, the
	// runtime_class dimension on the §16.1 recycle metrics. spec: §16.1
	// (runtime_class label).
	RuntimeClass string
}

// PodInspector resolves the §5.2 recycle policy and §6.39 host-node
// schedulability for the pod under a whole-pod scrub report. It reads the
// pod's lenny.dev/host-schedulable label via the gateway's existing get
// access on the Pods resource (no Node verbs), the pool's resolved
// sessionPolicy.recycle, and the pod's uptime. A missing pod (the absent
// label case) reports HostSchedulable false, fail-safe per §6.39.
// spec: §6.39 (host-node schedulability read via Pods get), §5.2 (recycle
// policy resolution).
type PodInspector interface {
	// InspectForRecycle resolves the recycle policy and pod facts for podID
	// at the recycle boundary. found is false when the pod or its claim is
	// gone, in which case the disposition is skipped (nothing to recycle).
	InspectForRecycle(ctx context.Context, podID string) (policy PodRecyclePolicy, found bool, err error)
}

// ClaimDispositionDriver applies a resolved §3.4 recycle disposition to the
// pod's SandboxClaim binding state. The gateway satisfies it with the
// podclaim binding-state writers (WriteRewarmStartedStatus,
// WriteReservedStatus, WriteDispositionStatus). The interface keeps
// leasecontrol free of the Kubernetes client and podclaim import.
// spec: §3.4 (recycle disposition drives the claim binding state), §5.2.
type ClaimDispositionDriver interface {
	// Recycle drives the reuse disposition. On a preConnect pool it stamps
	// rewarmStartedAt on the recycling claim and coordinates the SDK re-warm
	// before the claim enters reserved; on a non-preConnect pool it patches
	// the claim directly to reserved. scrubWarning carries the warn-policy
	// scrub_warning annotation through the transition. spec: §5.2 (recycle
	// lifecycle, line 449), §6.2 (preConnect re-warm).
	Recycle(ctx context.Context, podID string, preConnect, scrubWarning bool) error
	// Retire writes the terminal disposition on the claim so the projection
	// drains the pod. failed selects the claim's `failed` terminal (an
	// onScrubFailure: fail termination) versus its `released` terminal (a
	// limit-reached or §6.39 unschedulable-host retire). scrubWarning stamps
	// the scrub_warning audit annotation onto the retire, set only on the
	// §6.39 cordon-drain-under-warn path where the disposition retains the
	// residual-state marker (the package doc's `draining [scrub_warning]`
	// case); the limit-reached and fail-policy retires clear it. reason is
	// the stable observability label, and detail is the optional
	// adapter-side failure description retained in the audit trail on a
	// FAILED outcome. spec: §3.4 (retire disposition), §6.39, §5.2 (audit
	// retention of the failed pod's metadata).
	Retire(ctx context.Context, podID string, failed, scrubWarning bool, reason taskcleanup.RetireReason, detail string) error
}

// RetirementMetrics records the §16.1 pod-retirement and scrub-failure
// metrics the recycle disposition emits. A nil sink discards them. Every
// method carries the pool and runtime_class dimensions the §16.1 inventory
// mandates on these series so a concrete sink can emit them at the required
// label cardinality. spec: §16.1 (lenny_pod_scrub_failure_total,
// lenny_pod_scrub_failure_count, lenny_pod_retirement_total — labeled by
// pool, runtime_class), §5.2 (onScrubFailure: warn increments both
// scrub-failure series on every failure).
type RetirementMetrics interface {
	// IncScrubFailureTotal increments the aggregate scrub-failure counter
	// (lenny_pod_scrub_failure_total) on every failed whole-pod scrub,
	// independent of whether the failure retires the pod. spec: §5.2
	// (lenny_pod_scrub_failure_total aggregate counter).
	IncScrubFailureTotal(pool, runtimeClass string)
	// SetScrubFailureCount reports the pod's cumulative scrub-failure count
	// (lenny_pod_scrub_failure_count gauge, labeled by k8s_pod_name, pool,
	// runtime_class). spec: §16.1 (lenny_pod_scrub_failure_count).
	SetScrubFailureCount(podID, pool, runtimeClass string, count int)
	// IncRetirement records one pod retirement by reason
	// (lenny_pod_retirement_total{reason, pool, runtime_class}). The caller
	// emits it only for a reason in the frozen §16.1 vocabulary (the three
	// retirement-limit triggers); the §6.39 cordon-drain and the fail-policy
	// termination drain without a retirement-counter increment, so a sink
	// never observes an out-of-vocabulary reason value. spec: §16.1
	// (lenny_pod_retirement_total reason label set), §16.1.1.
	IncRetirement(reason taskcleanup.RetireReason, pool, runtimeClass string)
}

// ErrPodNotInMirror is returned by the ScrubReporter when a scrub report
// names a pod the agent_pod_state mirror does not know. The disposition
// cannot be computed safely without the recycle counters, so the reporter
// fails closed rather than guessing. The handler maps it to Internal.
var ErrPodNotInMirror = errors.New("leasecontrol: pod not found in agent_pod_state mirror")

// ScrubReporter is the gateway-side ScrubReportService implementation. It
// wires the §4.7 recycle-counter writes, the §5.2 unhealthy-threshold drain
// ledger, the §6.39 host-node schedulability read, the pure taskcleanup
// recycle disposition, and the §3.4 claim binding-state patches behind the
// narrow seams above so leasecontrol stays free of the Kubernetes client.
//
// spec: §4.7, §3.4, §5.2, §6.39.
type ScrubReporter struct {
	counters RecycleCounterStore
	ledger   DrainLedger
	inspect  PodInspector
	driver   ClaimDispositionDriver
	metrics  RetirementMetrics
}

// ScrubReporterOptions configures a ScrubReporter.
type ScrubReporterOptions struct {
	// Counters writes and reads the per-pod recycle counters. Required.
	Counters RecycleCounterStore
	// Ledger records leaked session-scrub outcomes for the drain trigger.
	// Required.
	Ledger DrainLedger
	// Inspector resolves the recycle policy and host-node schedulability.
	// Required.
	Inspector PodInspector
	// Driver applies the resolved disposition to the claim binding state.
	// Required.
	Driver ClaimDispositionDriver
	// Metrics records the retirement and scrub-failure metrics. Optional;
	// nil discards them.
	Metrics RetirementMetrics
}

// NewScrubReporter builds a ScrubReporter. Counters, Ledger, Inspector, and
// Driver are required; a nil one is a programming error and returns an
// error rather than panicking later on the request path.
func NewScrubReporter(opts ScrubReporterOptions) (*ScrubReporter, error) {
	if opts.Counters == nil {
		return nil, errors.New("leasecontrol: ScrubReporter Counters is required")
	}
	if opts.Ledger == nil {
		return nil, errors.New("leasecontrol: ScrubReporter Ledger is required")
	}
	if opts.Inspector == nil {
		return nil, errors.New("leasecontrol: ScrubReporter Inspector is required")
	}
	if opts.Driver == nil {
		return nil, errors.New("leasecontrol: ScrubReporter Driver is required")
	}
	metrics := opts.Metrics
	if metrics == nil {
		metrics = noopRetirementMetrics{}
	}
	return &ScrubReporter{
		counters: opts.Counters,
		ledger:   opts.Ledger,
		inspect:  opts.Inspector,
		driver:   opts.Driver,
		metrics:  metrics,
	}, nil
}

// RecordSessionScrub increments sessionsServed on the pod's agent_pod_state
// row and, on a leaked outcome, records the leak in the unhealthy-threshold
// drain ledger. A pod absent from the mirror returns ErrPodNotInMirror so a
// stale report does not silently no-op. spec: §4.7; §5.2.
func (r *ScrubReporter) RecordSessionScrub(ctx context.Context, podID, _, _ string, leaked bool) error {
	_, found, err := r.counters.IncrementSessionsServed(ctx, podID)
	if err != nil {
		return fmt.Errorf("increment sessions_served for pod %s: %w", podID, err)
	}
	if !found {
		return fmt.Errorf("%w: pod %s", ErrPodNotInMirror, podID)
	}
	if leaked {
		// §4.7: a leaked per-slot cleanup feeds the unhealthy-threshold drain
		// ledger behind lenny.dev/drain-request. The ledger owns the threshold
		// check and the annotation stamp.
		if err := r.ledger.RecordLeak(ctx, podID); err != nil {
			return fmt.Errorf("record leak for pod %s: %w", podID, err)
		}
	}
	return nil
}

// RecordPodScrub increments scrubFailureCount on a failed scrub, resolves
// the §3.4 / §6.39 recycle disposition through taskcleanup.Decide against
// the pod's recycle counters, sessionPolicy, uptime, and host-node
// schedulability, and drives the resulting disposition onto the claim
// binding state. detail carries an optional adapter-side failure
// description that the retire path retains in the audit trail on a FAILED
// outcome. spec: §4.7; §3.4; §5.2; §6.39.
func (r *ScrubReporter) RecordPodScrub(ctx context.Context, podID string, failed bool, detail string) error {
	// Resolve the recycle policy first: it carries the pool and
	// runtime_class dimensions the §16.1 scrub-failure and retirement
	// metrics require, so the counter advance below can emit them. A pod or
	// claim that is already gone (a concurrent retirement, a crashed session
	// that already retired the pod, or the orphan GC reclaiming it) leaves
	// nothing to recycle; the report is a no-op.
	policy, found, err := r.inspect.InspectForRecycle(ctx, podID)
	if err != nil {
		return fmt.Errorf("inspect pod %s for recycle: %w", podID, err)
	}
	if !found {
		return nil
	}

	scrubFailureCount, sessionsServed, err := r.advanceScrubCounters(ctx, podID, failed, policy.Pool, policy.RuntimeClass)
	if err != nil {
		return err
	}

	d := taskcleanup.Decide(taskcleanup.Inputs{
		PreConnect:          policy.PreConnect,
		Scrub:               scrubInput(failed),
		OnCleanupFailure:    policy.OnScrubFailure,
		ScrubFailureCount:   scrubFailureCount,
		MaxScrubFailures:    policy.MaxScrubFailures,
		SessionsServed:      sessionsServed,
		MaxSessionsPerPod:   policy.MaxSessionsPerPod,
		PodUptimeSeconds:    policy.PodUptimeSeconds,
		MaxPodUptimeSeconds: policy.MaxPodUptimeSeconds,
		HostSchedulable:     policy.HostSchedulable,
	})
	return r.applyDisposition(ctx, podID, policy.Pool, policy.RuntimeClass, detail, policy.PreConnect, d)
}

// advanceScrubCounters increments the scrub-failure counter on a failed
// scrub (emitting the aggregate lenny_pod_scrub_failure_total counter and
// the per-pod lenny_pod_scrub_failure_count gauge) and returns the
// post-report scrub-failure and sessions-served counts the disposition
// reads. The sessions-served count was already advanced by the preceding
// ReportSessionScrub at the occupancy-zero boundary, so RecordPodScrub reads
// it back rather than re-incrementing it. pool and runtimeClass label the
// §16.1 scrub-failure metrics. spec: §5.2 (both scrub-failure series
// increment on every failure), §16.1.
func (r *ScrubReporter) advanceScrubCounters(ctx context.Context, podID string, failed bool, pool, runtimeClass string) (scrubFailureCount, sessionsServed int, err error) {
	if failed {
		count, found, incErr := r.counters.IncrementScrubFailureCount(ctx, podID)
		if incErr != nil {
			return 0, 0, fmt.Errorf("increment scrub_failure_count for pod %s: %w", podID, incErr)
		}
		if !found {
			return 0, 0, fmt.Errorf("%w: pod %s", ErrPodNotInMirror, podID)
		}
		// §5.2: a failed whole-pod scrub increments BOTH the aggregate
		// counter and the per-pod gauge, independent of whether the failure
		// goes on to retire the pod.
		r.metrics.IncScrubFailureTotal(pool, runtimeClass)
		r.metrics.SetScrubFailureCount(podID, pool, runtimeClass, count)
		scrubFailureCount = count
	}
	served, scrub, found, readErr := r.counters.RecycleCounters(ctx, podID)
	if readErr != nil {
		return 0, 0, fmt.Errorf("read recycle counters for pod %s: %w", podID, readErr)
	}
	if !found {
		return 0, 0, fmt.Errorf("%w: pod %s", ErrPodNotInMirror, podID)
	}
	// The just-incremented value is authoritative on a failed scrub; on a
	// successful scrub the stored count is the disposition input.
	if !failed {
		scrubFailureCount = scrub
	}
	return scrubFailureCount, served, nil
}

// applyDisposition drives a resolved disposition onto the claim binding
// state and emits the retirement metric for a reason in the §16.1 vocabulary.
// A retire writes the terminal disposition (failed vs released), carrying the
// scrub_warning the §6.39 cordon-drain-under-warn path computes and the
// adapter-supplied failure detail for the audit trail; a reuse drives the
// recycle path. pool and runtimeClass label lenny_pod_retirement_total, which
// is incremented only for a reason the §16.1 inventory declares (the three
// retirement-limit triggers); the §6.39 cordon-drain and the fail-policy
// termination drain without a counter increment. spec: §3.4, §6.39, §16.1.
func (r *ScrubReporter) applyDisposition(ctx context.Context, podID, pool, runtimeClass, detail string, preConnect bool, d taskcleanup.Disposition) error {
	if d.Retire {
		// lenny_pod_retirement_total{reason} is frozen to the three §16.1
		// retirement-limit triggers (session_count_limit, uptime_limit,
		// scrub_failure_limit). The §6.39 cordon-drain retire and the
		// onScrubFailure: fail termination drive the drain but are not members
		// of that vocabulary, so the counter is incremented only when the
		// reason is one the inventory declares; emitting d.Reason for a
		// non-vocabulary retire would widen the frozen label set. The audit
		// trail below still receives the full reason. spec: spec/16 §16.1
		// (retirement counter label set), §16.1.1 (reason vocabulary).
		if d.Reason.CountsOnRetirementTotal() {
			r.metrics.IncRetirement(d.Reason, pool, runtimeClass)
		}
		// state.Failed is the onScrubFailure: fail termination → claim
		// `failed`; every other retire (limit reached or §6.39 unschedulable
		// host) is a drain → claim `released`. d.ScrubWarning is set only on
		// the §6.39 cordon-drain-under-warn path; it stamps the scrub_warning
		// audit annotation onto the retire so the residual-state marker the
		// disposition computed is retained. detail carries the adapter-side
		// failure description for the audit trail on a FAILED outcome.
		if err := r.driver.Retire(ctx, podID, d.NextPhase == state.Failed, d.ScrubWarning, d.Reason, detail); err != nil {
			return fmt.Errorf("retire pod %s (%s): %w", podID, d.Reason, err)
		}
		return nil
	}
	if err := r.driver.Recycle(ctx, podID, preConnect, d.ScrubWarning); err != nil {
		return fmt.Errorf("recycle pod %s: %w", podID, err)
	}
	return nil
}

// scrubInput maps the binary failed flag to the taskcleanup scrub result
// the disposition reads. The pending case never reaches the gateway: a
// ReportPodScrub always carries a resolved outcome.
func scrubInput(failed bool) taskcleanup.ScrubResult {
	if failed {
		return taskcleanup.ScrubFailed
	}
	return taskcleanup.ScrubSucceeded
}

// noopRetirementMetrics discards every retirement metric, the default sink
// when no RetirementMetrics is supplied.
type noopRetirementMetrics struct{}

func (noopRetirementMetrics) IncScrubFailureTotal(string, string)              {}
func (noopRetirementMetrics) SetScrubFailureCount(string, string, string, int) {}
func (noopRetirementMetrics) IncRetirement(taskcleanup.RetireReason, string, string) {
}

// Compile-time assertion that ScrubReporter satisfies the service seam.
var _ ScrubReportService = (*ScrubReporter)(nil)
