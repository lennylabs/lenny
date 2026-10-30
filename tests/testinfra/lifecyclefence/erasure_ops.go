// SPDX-License-Identifier: MIT

package lifecyclefence

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasure"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob"
)

// Step names one erasure step the runner calls out to.
type Step string

// The erasure steps, in the order a job runs them.
const (
	StepDrain        Step = "drain"
	StepHoldCheck    Step = "hold-check"
	StepErase        Step = "erase"
	StepPseudonymize Step = "pseudonymize"
	StepVerify       Step = "verify"
	StepReceipt      Step = "receipt"
)

// Erasure runs user erasures of the World's tenant through the production
// erasure job store and Runner, with every step the runner calls out to
// counted and open to a fault.
type Erasure struct {
	w *World
	// Jobs is the erasure job store every runner of this Erasure uses. It
	// asserts, on each committed write that moves a job out of initiated,
	// that the user holds no non-terminal session.
	Jobs erasurejob.Store

	mu sync.Mutex
	// fault, when set, is called before each step runs. A non-nil error
	// fails the step without running it.
	fault func(Step) error
	// afterAdvance, when set, runs after each committed phase change.
	afterAdvance func(from, to erasurejob.Phase)
	steps        map[Step]int
	startFaults  int
	// faultedStarts lists the job IDs whose Start ran with the start fault
	// armed, in order.
	faultedStarts []string
	failures      []string
}

// NewErasure builds the World's erasure runner set.
func (w *World) NewErasure() *Erasure {
	e := &Erasure{w: w, steps: map[Step]int{}}
	e.Jobs = &watchedErasureJobs{Store: w.S.Jobs, e: e}
	return e
}

// SetFault installs the step fault hook. A nil hook clears it.
func (e *Erasure) SetFault(f func(Step) error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.fault = f
}

// SetAfterAdvance installs the phase-change hook. A nil hook clears it.
func (e *Erasure) SetAfterAdvance(f func(from, to erasurejob.Phase)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.afterAdvance = f
}

// FailStarts arms the start fault for the next n job Starts: each one
// runs the store's Start with its restriction write failing inside the
// store, after the Postgres store has inserted the job row in the same
// transaction, and answers an error wrapping ErrInjected.
func (e *Erasure) FailStarts(n int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.startFaults = n
}

// FaultedStarts returns the job IDs whose Start ran with the start fault
// armed, in order.
func (e *Erasure) FaultedStarts() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.faultedStarts...)
}

// Steps returns how many times each step ran to completion.
func (e *Erasure) Steps() map[Step]int {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make(map[Step]int, len(e.steps))
	for k, v := range e.steps {
		out[k] = v
	}
	return out
}

// Failures returns the failure-phase labels the runners counted, in order.
func (e *Erasure) Failures() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.failures...)
}

// step runs fn as step s: it returns ctx's error when ctx has ended, as a
// replica that has stopped runs nothing, then the fault hook's error, and
// otherwise fn's, counting the step when fn succeeds.
func (e *Erasure) step(ctx context.Context, s Step, fn func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	fault := e.fault
	e.mu.Unlock()
	if fault != nil {
		if err := fault(s); err != nil {
			return err
		}
	}
	if err := fn(); err != nil {
		return err
	}
	e.mu.Lock()
	e.steps[s]++
	e.mu.Unlock()
	return nil
}

// Runner builds one replica's runner over the shared job store.
func (e *Erasure) Runner() *erasurejob.Runner {
	w := e.w
	return erasurejob.NewRunner(e.Jobs, e.orchestrator(), nil).
		WithLogger(w.Logger).
		WithBilling(erasurejob.NewBillingEraser(billingLedger{e}, w.S.Tenants)).
		WithSessionDrain(func(ctx context.Context, tenantID, userID string) error {
			return e.step(ctx, StepDrain, func() error {
				if tenantID != w.Tenant {
					return fmt.Errorf("erasure drain of foreign tenant %s", tenantID)
				}
				_, err := w.Drain(ctx, userID)
				return err
			})
		}).
		WithHoldCheck(func(ctx context.Context, _, _ string) ([]erasurejob.Hold, error) {
			return nil, e.step(ctx, StepHoldCheck, func() error { return nil })
		}).
		// The World's sessions run no terminal pipeline, so no drained
		// session is ever in its post-terminal window and the gate admits
		// every job. spec: §12.8 (Lifecycle fence and drain).
		WithReleaseGate(func(context.Context, string, string) ([]string, error) { return nil, nil }).
		WithFailureObserver(func(_, phase string) {
			e.mu.Lock()
			e.failures = append(e.failures, phase)
			e.mu.Unlock()
		}).
		WithCompletionReceipt(func(ctx context.Context, j erasurejob.Job) error {
			return e.step(ctx, StepReceipt, func() error {
				w.mu.Lock()
				w.erasedAt[j.UserID] = w.next()
				w.mu.Unlock()
				return nil
			})
		})
}

// orchestrator is the erasure over the World's stores, following the §12.8
// DeleteByUser dependency-ordered deletion sequence for the stores a World
// holds: a lister over
// the user's session rows from the primary and the IDs reserved for the
// user; the session-scoped artifacts, checkpoint_manifests, and transcripts
// erasers in rank order; and the user-scoped sessions eraser, which also
// deletes the user's reservations and is the StepErase step, followed by
// the tokens eraser over the TokenStore, which runs only while ctx is live.
//
// spec: §12.8 (Compliance Interfaces: DeleteByUser dependency-ordered deletion sequence)
func (e *Erasure) orchestrator() *erasure.Orchestrator {
	w := e.w
	scoped := []erasure.SessionEraser{
		{Name: "artifacts", DeleteBySession: e.sessionEraser(w.Objects.DeleteBySession)},
		{Name: "checkpoint_manifests", DeleteBySession: e.sessionEraser(w.Manifests.DeleteBySession)},
	}
	if w.S.TranscriptEraser != nil {
		scoped = append(scoped, erasure.SessionEraser{Name: w.S.TranscriptEraser.Name, DeleteBySession: e.sessionEraser(w.S.TranscriptEraser.DeleteBySession)})
	}
	return erasure.New(erasure.Config{
		Sessions:      w.userSessionIDs,
		SessionScoped: scoped,
		UserScoped: []erasure.Eraser{
			{Name: "sessions", DeleteByUser: e.userEraser(w.S.Sessions.DeleteByUser)},
			{Name: "tokens", DeleteByUser: liveUserEraser(w.Tokens.DeleteByUser)},
		},
	})
}

// userEraser runs a user-scoped eraser as the StepErase step.
func (e *Erasure) userEraser(fn erasure.DeleteByUserFunc) erasure.DeleteByUserFunc {
	return func(ctx context.Context, tenantID, userID string) (int, error) {
		var n int
		err := e.step(ctx, StepErase, func() error {
			var err error
			n, err = fn(ctx, tenantID, userID)
			return err
		})
		return n, err
	}
}

// liveUserEraser runs a user-scoped eraser only while ctx is live, so a
// stopped replica deletes nothing.
func liveUserEraser(fn erasure.DeleteByUserFunc) erasure.DeleteByUserFunc {
	return func(ctx context.Context, tenantID, userID string) (int, error) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		return fn(ctx, tenantID, userID)
	}
}

// sessionEraser runs a session-scoped eraser only while ctx is live, so a
// stopped replica deletes nothing.
func (e *Erasure) sessionEraser(fn erasure.DeleteBySessionFunc) erasure.DeleteBySessionFunc {
	return func(ctx context.Context, tenantID, sessionID string) (int, error) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		return fn(ctx, tenantID, sessionID)
	}
}

// userSessionIDs is the erasure's session lister: the user's rows from the
// primary and the IDs reserved for the user.
func (w *World) userSessionIDs(ctx context.Context, tenantID, userID string) ([]string, error) {
	rows, err := w.S.Sessions.List(ctx, tenantID, sessionstore.ListFilter{UserID: userID, Primary: true})
	if err != nil {
		return nil, err
	}
	ids, err := w.S.Sessions.ReservedSessionIDs(ctx, tenantID, userID)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	return ids, nil
}

// billingLedger is an empty billing ledger whose pseudonymize and count
// calls are the StepPseudonymize and StepVerify steps.
type billingLedger struct{ e *Erasure }

func (b billingLedger) PseudonymizeUser(ctx context.Context, _, _ string, _ []byte) (int, error) {
	return 0, b.e.step(ctx, StepPseudonymize, func() error { return nil })
}

func (b billingLedger) CountUser(ctx context.Context, _, _ string) (int, error) {
	return 0, b.e.step(ctx, StepVerify, func() error { return nil })
}

// Start requests an erasure of user twice concurrently, as two erase
// requests racing on two replicas would, and returns the job that was
// started. Exactly one Start must succeed; the other must answer the
// store's *erasurejob.ErrActiveJob.
func (e *Erasure) Start(ctx context.Context, user string) (erasurejob.Job, error) {
	e.w.markTouched(user)
	r := e.Runner()
	var jobs [2]erasurejob.Job
	var errs [2]error
	var wg sync.WaitGroup
	for i := range jobs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			jobs[i], errs[i] = r.Start(ctx, e.w.Tenant, user, "carol@acme.com", nil)
		}(i)
	}
	wg.Wait()
	var active *erasurejob.ErrActiveJob
	switch {
	case errs[0] == nil && errors.As(errs[1], &active):
		return jobs[0], nil
	case errs[1] == nil && errors.As(errs[0], &active):
		return jobs[1], nil
	case errs[0] != nil && errs[1] != nil:
		return erasurejob.Job{}, fmt.Errorf("start erasure of %s: %w; %w", user, errs[0], errs[1])
	default:
		e.w.Violatef("two concurrent erasure Starts for %s answered %v and %v; want one job and one ErrActiveJob", user, errs[0], errs[1])
		return jobs[0], nil
	}
}

// RunToCompletion runs RunPending on a fresh replica's runner until the
// job completes, at most maxPasses times, restarting a failed job as the
// retry endpoint does. While the job is in progress the user's
// restriction must stay set naming the job. A scenario that arms a fault
// uses it; RunUnfaulted is the run for one that arms none.
func (e *Erasure) RunToCompletion(ctx context.Context, jobID string, maxPasses int) (erasurejob.Job, error) {
	return e.run(ctx, jobID, maxPasses, true)
}

// RunUnfaulted runs the job as RunToCompletion does in a scenario that arms
// no fault, and records a violation for every failure a runner counted and
// for a job that fails. It does not restart a failed job. With no fault
// armed, the only way a job fails is an incomplete drain, and the drain
// comes back incomplete only when a create of the user committed after the
// restriction did; a restart would drain that session and erase its row,
// leaving the breach with no witness.
//
// spec: §12.8 (Lifecycle fence and drain: Creation fence, Drain), §12.8 (Processing restriction during erasure (GDPR Article 18))
func (e *Erasure) RunUnfaulted(ctx context.Context, jobID string, maxPasses int) (erasurejob.Job, error) {
	j, err := e.run(ctx, jobID, maxPasses, false)
	for _, phase := range e.Failures() {
		e.w.Violatef("unfaulted erasure job %s failed at %s: a create of %s committed after its restriction", jobID, phase, j.UserID)
	}
	if j.Phase == erasurejob.PhaseFailed {
		e.w.Violatef("unfaulted erasure job %s of %s ended failed: %s", jobID, j.UserID, j.Failure)
	}
	return j, err
}

// run is the RunPending loop of RunToCompletion and RunUnfaulted. When
// restart is false a failed job ends the loop with an error.
func (e *Erasure) run(ctx context.Context, jobID string, maxPasses int, restart bool) (erasurejob.Job, error) {
	for i := 0; i < maxPasses; i++ {
		j, err := e.Jobs.Get(ctx, erasurejob.AllTenants, jobID)
		if err != nil {
			return j, fmt.Errorf("read erasure job %s: %w", jobID, err)
		}
		switch j.Phase {
		case erasurejob.PhaseCompleted:
			return j, nil
		case erasurejob.PhaseFailed:
			e.AssertRestricted(ctx, j)
			if !restart {
				return j, fmt.Errorf("erasure job %s failed: %s", jobID, j.Failure)
			}
			if _, err := e.Jobs.Restart(ctx, jobID); err != nil {
				return j, fmt.Errorf("restart erasure job %s: %w", jobID, err)
			}
			continue
		}
		e.AssertRestricted(ctx, j)
		_ = e.Runner().RunPending(ctx)
	}
	j, err := e.Jobs.Get(ctx, erasurejob.AllTenants, jobID)
	if err == nil && j.Phase != erasurejob.PhaseCompleted {
		err = fmt.Errorf("erasure job %s still %s after %d passes (last failure %q)", jobID, j.Phase, maxPasses, j.Failure)
	}
	return j, err
}

// AssertRestricted records a violation unless the job's user carries the
// restriction naming the job.
func (e *Erasure) AssertRestricted(ctx context.Context, j erasurejob.Job) {
	u, err := e.w.S.Users.Get(context.WithoutCancel(ctx), j.TenantID, j.UserID)
	if err != nil {
		e.w.Violatef("read user %s: %v", j.UserID, err)
		return
	}
	if !u.ProcessingRestricted || u.ErasureJobID != j.ID {
		e.w.Violatef("erasure job %s is %s but user %s has processing_restricted=%v naming %q",
			j.ID, j.Phase, j.UserID, u.ProcessingRestricted, u.ErasureJobID)
	}
}

// watchedErasureJobs is the erasure job store with the Start fault and the
// phase-change assertions and hook.
type watchedErasureJobs struct {
	erasurejob.Store
	e *Erasure
}

// Start implements erasurejob.Store.
func (s *watchedErasureJobs) Start(ctx context.Context, j erasurejob.Job) (erasurejob.Job, error) {
	s.e.mu.Lock()
	fire := s.e.startFaults > 0
	if fire {
		s.e.startFaults--
	}
	s.e.mu.Unlock()
	if fire {
		return s.e.startWithFault(ctx, s.Store, j)
	}
	return s.Store.Start(ctx, j)
}

// Update implements erasurejob.Store.
func (s *watchedErasureJobs) Update(ctx context.Context, id string, mutate func(*erasurejob.Job) error) (erasurejob.Job, error) {
	var from, to erasurejob.Phase
	var user string
	j, err := s.Store.Update(ctx, id, func(j *erasurejob.Job) error {
		from, user = j.Phase, j.UserID
		if err := mutate(j); err != nil {
			return err
		}
		to = j.Phase
		return nil
	})
	if err != nil || from == to {
		return j, err
	}
	if from == erasurejob.PhaseInitiated && to != erasurejob.PhaseFailed {
		s.e.w.assertScopeDrained(context.WithoutCancel(ctx), user, fmt.Sprintf("when erasure job %s left initiated for %s", id, to))
	}
	s.e.mu.Lock()
	hook := s.e.afterAdvance
	s.e.mu.Unlock()
	if hook != nil {
		hook(from, to)
	}
	return j, err
}
