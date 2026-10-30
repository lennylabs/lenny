// SPDX-License-Identifier: MIT

package erasurejob

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/storage/erasure"
)

// Runner executes §12.8 erasure jobs. Start records a job; Run, RunPending,
// and Poll drive recorded jobs through their phases from the phase each job
// persisted, so a job resumes after a crash on whichever replica runs it
// next and never re-runs a step of an earlier phase.
//
// The phases are: initiated (the MemoryStore preflight, the session drain,
// and the legal-hold re-check), store_deleting (the dependency-ordered
// DeleteByUser sequence and the dead-letter redaction), pseudonymizing and
// verifying (the billing erasure, when a BillingEraser is attached), and
// then the completion receipt followed by Complete.
//
// Every job write the runner makes is guarded by SameAttempt against the job
// the runner last read or wrote, so a stale runner's write returns
// ErrJobMoved, changes nothing, and stops that runner's work on the job. The
// gateway-leader gate that runs Poll does not guarantee a single executor:
// a former leader's runs continue until they observe their ended context,
// and the guard is what keeps two executors from both writing one attempt.
//
// spec: §12.8 (Processing restriction during erasure (GDPR Article 18)),
// §12.8 (Erasure job phase field (crash recovery)), §24.12 (Erasure Job
// Management).
type Runner struct {
	jobs             Store
	erase            *erasure.Orchestrator
	billing          *BillingEraser
	memoryPreflight  func(context.Context) error
	deadLetterRedact func(ctx context.Context, tenantID, userID string) (int, error)
	drain            func(ctx context.Context, tenantID, userID string) error
	holdCheck        func(ctx context.Context, tenantID, userID string) ([]Hold, error)
	releaseGate      func(ctx context.Context, tenantID, userID string) ([]string, error)
	receipt          func(ctx context.Context, j Job) error
	crossTenantRead  CrossTenantReadFunc
	onFailure        func(tenantID, failurePhase string)
	onComplete       func(ctx context.Context, tenantID, userID string)
	lifecycle        LifecycleMetrics
	deadlineFor      func(ctx context.Context, tenantID string) time.Duration
	clock            func() time.Time
	logger           *slog.Logger
	maxConcurrent    int

	// mu guards inFlight, which maps the id of every job a RunPending
	// goroutine is running to the job's tenant. Its size is the in-flight
	// count across overlapping RunPending calls.
	mu       sync.Mutex
	inFlight map[string]string
}

// Hold is one §12.8 legal hold the runner's re-check found on the erasure
// target: a held session, or a held artifact named by its session.
type Hold struct {
	ResourceType string
	ResourceID   string
}

// CrossTenantReadFunc records one §12.3 cross_tenant_read event with
// category erasure_job for a read made under the all-tenants sentinel.
// endpoint names the reading path, callerSubject is the requesting
// principal (empty for a background worker), and rowCount is the number of
// rows the read returned. A non-nil error fails the reading path closed.
type CrossTenantReadFunc func(ctx context.Context, endpoint, callerSubject string, rowCount int) error

// Endpoints the background erasure paths record on their cross_tenant_read
// events.
const (
	CrossTenantReadEndpointRunner  = "erasure_job_runner"
	CrossTenantReadEndpointSampler = "erasure_job_sampler"
)

// LifecycleMetrics receives the §12.8 per-job duration observation.
// *gatewaymetrics.Metrics satisfies it. The in-progress gauge is set by the
// Sampler from the job store rather than by the runner.
type LifecycleMetrics interface {
	ObserveErasureJobDuration(seconds float64)
}

// FailurePhase labels for the §12.8 CMP-026 lenny_erasure_job_failed_total
// counter. The MemoryStore erasure preflight (§12.8) adds
// PhaseMemoryStorePreflight to the store_delete / pseudonymization /
// verification phases the spec enumerates. A session-drain failure and a
// legal hold the runner's re-check finds are labelled store_delete.
const (
	FailurePhaseMemoryStorePreflight = "memory_store_preflight"
	FailurePhaseStoreDelete          = "store_delete"
	FailurePhaseDeadLetterRedaction  = "deadletter_redaction"
	FailurePhasePseudonymization     = "pseudonymization"
	FailurePhaseVerification         = "verification"
)

// Errors the runner records as a job's failure cause or returns to a
// synchronous caller.
var (
	// errMemoryStorePreflight is the §12.8 abort cause recorded when the
	// per-job MemoryStore erasure preflight fails.
	errMemoryStorePreflight = errors.New("memory_store_preflight_failed")
	// ErrSessionDrainUnwired is the failure cause of a job run by a runner
	// with no session-drain hook. Erasing with live sessions would leave
	// them writing data the erasure has already passed, so a missing hook
	// fails the job rather than skipping the drain.
	ErrSessionDrainUnwired = errors.New("erasurejob: no session drain is wired")
	// ErrHoldCheckUnwired is the failure cause of a job run by a runner
	// with no legal-hold re-check hook.
	ErrHoldCheckUnwired = errors.New("erasurejob: no legal-hold re-check is wired")
	// ErrReceiptUnwired is returned when a job has finished its erasers
	// but the runner has no completion-receipt hook. The job stays in
	// progress, because §12.8 lifts the restriction only after the receipt
	// is written.
	ErrReceiptUnwired = errors.New("erasurejob: no completion receipt sink is wired")
	// ErrReleasePending is returned, wrapped with a count, when the
	// store_deleting phase finds a session of the user still in its §7.1
	// post-terminal window. The job makes no write and stays in
	// store_deleting for the next Poll tick; the wait ends within the
	// window cap, so it is not a job failure.
	ErrReleasePending = errors.New("erasurejob: a drained session's pod release is not yet recorded")
	// ErrReleaseGateUnwired is the failure cause of a job that reaches
	// store_deleting on a runner with no release gate. Deleting the stores
	// while a seal may still upload would leave objects of the erased user
	// behind, so a missing gate fails the job.
	ErrReleaseGateUnwired = errors.New("erasurejob: no post-terminal release gate is wired")
	// ErrBillingUnwired is the failure cause of a job persisted in a
	// billing phase that a runner with no BillingEraser resumes.
	ErrBillingUnwired = errors.New("erasurejob: no billing eraser is wired")
	// errBillingVerification is the failure cause recorded when the §12.8
	// post-pseudonymization check does not pass.
	errBillingVerification = errors.New(
		"billing erasure verification failed: the erasure salt or the original user id survived pseudonymization",
	)
)

// NewRunner builds a Runner over the job registry and the erasure
// orchestrator. Pass nil for clock to default to time.Now. The runner runs
// one job at a time until WithMaxConcurrentJobs raises the limit.
func NewRunner(jobs Store, orch *erasure.Orchestrator, clock func() time.Time) *Runner {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Runner{
		jobs: jobs, erase: orch, clock: clock,
		logger:        slog.Default(),
		maxConcurrent: 1,
		inFlight:      map[string]string{},
	}
}

// WithBilling attaches the §12.8 BillingEraser so Run drives the
// pseudonymizing and verifying phases after store deletion. It returns
// the Runner for call chaining.
func (r *Runner) WithBilling(b *BillingEraser) *Runner {
	r.billing = b
	return r
}

// WithMemoryPreflight attaches the §12.8 per-job MemoryStore erasure
// preflight. The check re-runs the seeded-row write/erase/requery cycle in
// the initiated phase, before any store deletion; a backend that passed the
// startup preflight but later regressed aborts the job here rather than
// reporting a successful erasure while memories persist. A nil preflight
// disables the per-job check. Returns the Runner for call chaining.
func (r *Runner) WithMemoryPreflight(preflight func(context.Context) error) *Runner {
	r.memoryPreflight = preflight
	return r
}

// WithDeadLetterRedaction attaches the §12.8 step-14 OCSF dead-letter PII
// redaction, run in the store_deleting phase after DeleteByUser. A failure
// aborts the job under FailurePhaseDeadLetterRedaction with the store
// deletion already durable. A nil hook skips step 14. Returns the Runner
// for call chaining.
//
// spec: §12.8.
func (r *Runner) WithDeadLetterRedaction(hook func(ctx context.Context, tenantID, userID string) (int, error)) *Runner {
	r.deadLetterRedact = hook
	return r
}

// WithSessionDrain attaches the §12.8 session drain the initiated phase
// runs before any store deletion: it brings every session the user owns to
// a terminal state and returns an error unless the drain verified. A drain
// error fails the job under store_delete with the restriction left set. A
// runner with no drain hook fails every job it runs past initiated the same
// way. Returns the Runner for call chaining.
//
// spec: §12.8 (Lifecycle fence and drain), §12.8 (Processing restriction
// during erasure (GDPR Article 18)).
func (r *Runner) WithSessionDrain(drain func(ctx context.Context, tenantID, userID string) error) *Runner {
	r.drain = drain
	return r
}

// WithReleaseGate attaches the §12.8 post-terminal release gate the
// store_deleting phase runs before DeleteByUser. The gate returns the
// identifiers of the user's sessions in their §7.1 post-terminal window.
// While it returns any, or returns an error, the phase makes no job write
// and returns, so the job waits in store_deleting for a later run; a job
// resumed in store_deleting passes the gate again. A runner with no gate
// fails every job at store_delete. Returns the Runner for call chaining.
//
// spec: §12.8 (Lifecycle fence and drain), §7.1 (Post-terminal window).
func (r *Runner) WithReleaseGate(gate func(ctx context.Context, tenantID, userID string) ([]string, error)) *Runner {
	r.releaseGate = gate
	return r
}

// WithHoldCheck attaches the legal-hold re-check the initiated phase runs
// after the session drain and before the advance to store_deleting. A hold
// it returns fails the job under store_delete, unless the job's
// OverrideReceipt lists the hold and the job has never failed: a retried
// job exempts no hold, because the retry ran its own preflight with no
// override. A read error fails the job the same way, and so does a runner
// with no hold-check hook. A hold set after the job advances to
// store_deleting is not re-checked; the re-check closes the window between
// the handler's preflight and the first eraser. Returns the Runner for call
// chaining.
//
// spec: §12.8 (DeleteByUser dependency-ordered deletion sequence).
func (r *Runner) WithHoldCheck(check func(ctx context.Context, tenantID, userID string) ([]Hold, error)) *Runner {
	r.holdCheck = check
	return r
}

// WithCompletionReceipt attaches the sink that writes the §12.8
// gdpr.erasure_completed receipt. The runner writes the receipt before it
// calls Complete, and only a nil return lets the job complete and lift the
// restriction. A sink error, or a runner with no sink, leaves the job in
// progress for the next run, which writes a byte-identical receipt because
// the job's ReceiptAt is chosen once. Returns the Runner for call chaining.
//
// spec: §12.8 (Processing restriction during erasure (GDPR Article 18)).
func (r *Runner) WithCompletionReceipt(sink func(ctx context.Context, j Job) error) *Runner {
	r.receipt = sink
	return r
}

// WithCrossTenantReadAudit attaches the §12.3 cross_tenant_read recorder
// RunPending calls once per invocation after its all-tenants ListActive
// read. An emit error makes RunPending start no run. A nil function emits
// nothing. Returns the Runner for call chaining.
//
// spec: §12.3 (Postgres HA Requirements).
func (r *Runner) WithCrossTenantReadAudit(f CrossTenantReadFunc) *Runner {
	r.crossTenantRead = f
	return r
}

// WithMaxConcurrentJobs sets how many jobs RunPending runs at once across
// overlapping calls. A value below 1 keeps the limit at 1. Returns the
// Runner for call chaining.
func (r *Runner) WithMaxConcurrentJobs(n int) *Runner {
	if n < 1 {
		n = 1
	}
	r.maxConcurrent = n
	return r
}

// WithLogger sets the logger the runner reports drain timings and
// background run errors to. A nil logger keeps slog.Default. Returns the
// Runner for call chaining.
func (r *Runner) WithLogger(l *slog.Logger) *Runner {
	if l != nil {
		r.logger = l
	}
	return r
}

// WithFailureObserver attaches the §12.8 CMP-026 failure counter hook,
// invoked with the tenant id and failure phase once per failure write that
// applied. A nil observer disables emission. Returns the Runner for call
// chaining.
func (r *Runner) WithFailureObserver(obs func(tenantID, failurePhase string)) *Runner {
	r.onFailure = obs
	return r
}

// WithLifecycleMetrics attaches the §12.8 per-job duration histogram. The
// runner observes a job's duration after the write that completes it or
// fails it applied. A nil m disables emission. Returns the Runner for call
// chaining.
func (r *Runner) WithLifecycleMetrics(m LifecycleMetrics) *Runner {
	r.lifecycle = m
	return r
}

// WithDeadlineResolver attaches the §12.8 tier-specific SLA resolver. Start
// stamps the resolved deadline (T3 72h, T4 1h) onto the job so the overdue
// sampler and the completion receipt can present it. A nil resolver leaves
// Job.Deadline zero. Returns the Runner for call chaining.
func (r *Runner) WithDeadlineResolver(f func(ctx context.Context, tenantID string) time.Duration) *Runner {
	r.deadlineFor = f
	return r
}

// WithCompletionHook attaches the §12.5 erasure-completion hook, invoked
// with the job's tenant and user id after Complete applied. The gateway
// wires it to the `gcPriority: high` immediate-sweep trigger. The hook is
// best-effort and a nil hook disables it. Returns the Runner for call
// chaining. spec: §12.5.
func (r *Runner) WithCompletionHook(hook func(ctx context.Context, tenantID, userID string)) *Runner {
	r.onComplete = hook
	return r
}

// Start records a fresh erasure job for the user in PhaseInitiated and
// returns it. The store writes the job and sets the user's
// processing_restricted flag, naming the job, in one transaction, so no job
// exists without its restriction. initiatedBy is the requesting principal's
// subject and overrideReceipt the legal-hold override fields, or nil; both
// persist with the job so the completion receipt written on any replica
// records them. When the user already has a job in progress it returns the
// store's *ErrActiveJob and records nothing. The job is not executed here:
// the leader's Poll runs it.
//
// spec: §12.8 (Processing restriction during erasure (GDPR Article 18)),
// §12.8 (User-level erasure invocation).
func (r *Runner) Start(ctx context.Context, tenantID, userID, initiatedBy string, overrideReceipt map[string]any) (Job, error) {
	now := r.clock()
	var deadline time.Duration
	if r.deadlineFor != nil {
		deadline = r.deadlineFor(ctx, tenantID)
	}
	return r.jobs.Start(ctx, Job{
		ID:              NewID(),
		TenantID:        tenantID,
		UserID:          userID,
		Phase:           PhaseInitiated,
		StartedAt:       now,
		Deadline:        deadline,
		PhaseLog:        []PhaseTransition{{Phase: PhaseInitiated, At: now}},
		InitiatedBy:     initiatedBy,
		OverrideReceipt: overrideReceipt,
	})
}

// Run drives one job from its persisted phase until it completes, fails,
// or stops. A job in a terminal phase is left untouched. A job whose
// ErasedAt is set goes straight to its receipt and Complete.
//
// Run returns nil when the job completed or was already terminal, the
// failure cause when the job failed, ErrJobMoved when another executor
// wrote the job first, and otherwise the error that left the job in
// progress for a later run (a receipt error, a store error, an ended
// context, ErrReleasePending while a drained session is still in its
// post-terminal window, or a release-gate error). When ctx has ended, a failing step writes nothing and the job
// stays in its persisted phase for the next leader.
func (r *Runner) Run(ctx context.Context, jobID string) error {
	// The runner is a background worker that reads any tenant's job.
	job, err := r.jobs.Get(ctx, AllTenants, jobID)
	if err != nil {
		return err
	}
	for {
		if job.Phase.Terminal() {
			return nil
		}
		if !job.ErasedAt.IsZero() {
			return r.finish(ctx, job)
		}
		var step func(context.Context, Job) (Job, error)
		switch job.Phase {
		case PhaseInitiated:
			step = r.runInitiated
		case PhaseStoreDeleting:
			step = r.runStoreDeleting
		case PhasePseudonymizing:
			step = r.runPseudonymizing
		case PhaseVerifying:
			step = r.runVerifying
		default:
			return r.fail(ctx, job, FailurePhaseStoreDelete, errors.New("erasurejob: unknown phase "+string(job.Phase)), nil)
		}
		if job, err = step(ctx, job); err != nil {
			return err
		}
	}
}
