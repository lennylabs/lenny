// SPDX-License-Identifier: MIT

package lifecyclefence

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/controller/tenantdeletion"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// transitionCounter is the tenant store the admin router writes through. It
// counts each committed write that moved the suspension marker from false
// to true, which is one marker transition.
type transitionCounter struct {
	tenantstore.Store
	w *World
}

// Update implements tenantstore.Store.
func (c transitionCounter) Update(ctx context.Context, id string, mutate func(*tenantstore.Tenant) error) (tenantstore.Tenant, error) {
	var before, after bool
	row, err := c.Store.Update(ctx, id, func(t *tenantstore.Tenant) error {
		before = t.Suspended
		if err := mutate(t); err != nil {
			return err
		}
		after = t.Suspended
		return nil
	})
	if err == nil && !before && after {
		c.w.mu.Lock()
		c.w.suspendTransitions++
		c.w.mu.Unlock()
	}
	return row, err
}

// adminHandler returns the World's admin router: the production suspend and
// resume handlers over the counted tenant store, with the drain's listing
// and forced termination behind the World's faults.
func (w *World) adminHandler() http.Handler {
	w.adminOnce.Do(func() {
		w.adminH = admin.NewRouter(transitionCounter{Store: w.S.Tenants, w: w}, admin.Options{Audit: w.Audit}).
			WithSessions(w.Lister).
			WithSessionAdmin(w.Terminator).
			Handler()
	})
	return w.adminH
}

// SuspendResult is a suspend request's answer.
type SuspendResult struct {
	Status         int
	FailedSessions []string
	Body           string
}

// Suspend issues POST /v1/admin/tenants/{id}/suspend as a platform admin on
// ctx. A 200 with no failed sessions is a drain that succeeded, so Suspend
// then asserts the tenant holds no non-terminal session; the caller must
// not resume the tenant concurrently. SuspendRacingResume is the suspend
// whose drain a resume overtakes.
func (w *World) Suspend(ctx context.Context) SuspendResult {
	res, ok := w.suspend(ctx)
	if ok && len(res.FailedSessions) == 0 {
		w.assertScopeDrained(context.WithoutCancel(ctx), "", "after a suspend that answered no failed sessions")
	}
	return res
}

// SuspendUnfaulted issues a suspend in a scenario that arms no drain fault
// and runs no concurrent resume, and records a violation unless it answers
// 200 with an empty failedSessions. Once the marker commits, the fence must
// refuse every create of the tenant, so with no fault armed the only way
// the drain's verifying listing can still see a live session is a create
// the fence admitted after the marker committed. A 200 that reports such a
// session is that breach, so it is a violation here rather than the
// partial-failure answer a faulted drain gives.
//
// spec: §12.8 (Lifecycle fence and drain: Creation fence, Drain), §15.1 (REST API)
func (w *World) SuspendUnfaulted(ctx context.Context) SuspendResult {
	res := w.Suspend(ctx)
	switch {
	case res.Status != http.StatusOK:
		w.Violatef("unfaulted suspend answered %d: %s", res.Status, res.Body)
	case len(res.FailedSessions) > 0:
		w.Violatef("unfaulted suspend reported failedSessions %v: a create committed into the tenant after its suspension marker", res.FailedSessions)
	}
	return res
}

// suspend issues the suspend request and decodes a 200 answer. It reports
// whether the answer was a well-formed 200, recording a violation for a 200
// whose body is malformed or reports the tenant not suspended.
func (w *World) suspend(ctx context.Context) (SuspendResult, bool) {
	rr := w.serveAdmin(ctx, "/v1/admin/tenants/"+w.Tenant+"/suspend")
	res := SuspendResult{Status: rr.Code, Body: rr.Body.String()}
	if rr.Code != http.StatusOK {
		return res, false
	}
	var out struct {
		Suspended      bool     `json:"suspended"`
		FailedSessions []string `json:"failedSessions"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		w.Violatef("suspend: decode %q: %v", res.Body, err)
		return res, false
	}
	res.FailedSessions = out.FailedSessions
	if !out.Suspended {
		w.Violatef("suspend answered 200 with suspended=false: %s", res.Body)
	}
	return res, true
}

// RacedSuspendResult is the answer of a suspend whose drain a tenant resume
// overtook.
type RacedSuspendResult struct {
	SuspendResult
	// ResumeSeq is the sequence number taken just before the resume was
	// issued. A create whose CommittedSeq is higher committed after the
	// resume reopened the creation fence.
	ResumeSeq int64
	// PostResume is the session of bob the race created after the resume
	// and before the drain's verifying listing, or "" when that create
	// failed.
	PostResume string
}

// SuspendRacingResume issues a suspend whose drain a tenant resume
// overtakes: after the drain's first primary listing and before its
// verifying listing, the resume commits and one session of bob is created,
// which the reopened fence must admit. It then asserts the resume-during-
// drain rule in place of the drained-scope rule Suspend asserts: the
// suspend answers 200, it reports as failed only sessions created after the
// resume (no forced termination fails here), and every non-terminal session
// of the tenant just after the answer is either reported in failedSessions
// or was created after the resume. Check judges the last two once every
// create has recorded its outcome. Every session that predates the resume is
// therefore drained. The caller must not resume the tenant concurrently and
// must arm no forced-termination fault.
//
// spec: §12.8 (Lifecycle fence and drain: Drain), §15.1 (REST API)
func (w *World) SuspendRacingResume(ctx context.Context) RacedSuspendResult {
	var raced RacedSuspendResult
	w.Faults.BeforeTenantDrainList(func() {
		w.Faults.BeforeTenantDrainList(func() { w.resumeAndCreate(ctx, &raced) })
	})
	res, ok := w.suspend(ctx)
	w.Faults.BeforeTenantDrainList(nil)
	raced.SuspendResult = res
	if raced.ResumeSeq == 0 {
		w.Violatef("raced suspend: the resume never ran between the drain's listings: %d %s", res.Status, res.Body)
		return raced
	}
	if !ok {
		w.Violatef("raced suspend answered %d, want 200: %s", res.Status, res.Body)
		return raced
	}
	w.observeResumeDuringDrain(context.WithoutCancel(ctx), raced)
	return raced
}

// resumeAndCreate is the race's step between the drain's listings: it
// resumes the tenant and creates one session of bob, which must be admitted.
func (w *World) resumeAndCreate(ctx context.Context, raced *RacedSuspendResult) {
	raced.ResumeSeq = w.next()
	if code := w.Resume(ctx); code != http.StatusOK {
		w.Violatef("raced suspend: resume during the drain answered %d", code)
		return
	}
	if o := w.Create(ctx, PathCreate, "bob"); o.Err == nil {
		raced.PostResume = o.ID
	} else {
		w.Violatef("raced suspend: create after the resume refused: %v", o.Err)
	}
}

// racedObservation is what a raced suspend observed when it answered: its
// report, the resume's sequence number, and the tenant's live sessions read
// from the primary just after the answer. The checker judges it once every
// create of the scenario has recorded its outcome, because a create that
// committed after the resume can be live in the listing before its
// outcome, and with it its commit sequence number, is recorded.
type racedObservation struct {
	raced RacedSuspendResult
	live  []string
}

// observeResumeDuringDrain reads the tenant's live sessions and stores the
// observation for the checker.
func (w *World) observeResumeDuringDrain(ctx context.Context, raced RacedSuspendResult) {
	live, err := w.LiveInScope(ctx, "")
	if err != nil {
		w.Violatef("raced suspend: %v", err)
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.racedSuspends = append(w.racedSuspends, racedObservation{raced: raced, live: live})
}

// resumeDuringDrainViolations returns what a raced suspend misreported: a
// reported session that predates the resume, a live session that predates
// the resume and is not reported, or the post-resume session live and not
// reported. Call it once every create has recorded its outcome.
func (w *World) resumeDuringDrainViolations(o racedObservation) []string {
	var out []string
	postResume := w.committedAfter(o.raced.ResumeSeq)
	reported := make(map[string]bool, len(o.raced.FailedSessions))
	for _, id := range o.raced.FailedSessions {
		reported[id] = true
		if !postResume[id] {
			out = append(out, fmt.Sprintf("raced suspend reported %s in failedSessions, which predates the resume and should have been drained", id))
		}
	}
	for _, id := range o.live {
		if reported[id] {
			continue
		}
		switch {
		case id == o.raced.PostResume:
			out = append(out, fmt.Sprintf("raced suspend: session %s created after the resume is live and absent from failedSessions", id))
		case !postResume[id]:
			out = append(out, fmt.Sprintf("raced suspend left %s live, neither reported in failedSessions nor created after the resume", id))
		}
	}
	return out
}

// committedAfter returns the IDs of the creates, and of the delegation
// parents, whose insert committed after the sequence number seq.
func (w *World) committedAfter(seq int64) map[string]bool {
	out := map[string]bool{}
	for _, o := range w.Outcomes() {
		if o.Err == nil && o.CommittedSeq > seq {
			out[o.ID] = true
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for id, at := range w.parentCommits {
		if at > seq {
			out[id] = true
		}
	}
	return out
}

// Resume issues POST /v1/admin/tenants/{id}/resume and returns its status.
func (w *World) Resume(ctx context.Context) int {
	return w.serveAdmin(ctx, "/v1/admin/tenants/"+w.Tenant+"/resume").Code
}

func (w *World) serveAdmin(ctx context.Context, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req = req.WithContext(authmw.WithPrincipal(ctx, authmw.Principal{
		TenantID: "platform", Subject: "carol@acme.com",
		Roles: []pkgauth.Role{pkgauth.RolePlatformAdmin},
	}))
	rr := httptest.NewRecorder()
	w.adminHandler().ServeHTTP(rr, req)
	return rr
}

// SuspendTransitions returns the number of suspension-marker transitions.
func (w *World) SuspendTransitions() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.suspendTransitions
}

// Deletion is one replica's tenant-deletion reconciler for the World's
// tenant, over its own in-memory job store as the gateway's tenant-deletion
// runner keeps it. Its Phase 1 and Phase 2 seams match cmd/lenny-gateway's:
// Phase 1 moves the tenant row to disabling, and Phase 2 runs the shared
// drain over the whole tenant.
type Deletion struct {
	w    *World
	Jobs tenantdeletion.Store
	rec  *tenantdeletion.Reconciler
}

// NewDeletion builds a replica's reconciler.
func (w *World) NewDeletion() *Deletion {
	jobs := &watchedDeletionJobs{Store: tenantdeletion.NewMemory(), w: w}
	return &Deletion{w: w, Jobs: jobs, rec: &tenantdeletion.Reconciler{
		Jobs:       jobs,
		Disabler:   disabler{w.S.Tenants},
		Terminator: phase2Drain{w},
		Revoker:    noopRevoker{},
		Eraser:     noopEraser{},
		Receipts:   noopReceipts{},
		Logger:     w.Logger,
	}}
}

// Start records the deletion job at Phase 1, as the runner does for a
// tenant whose row is in disabling.
func (d *Deletion) Start(ctx context.Context) error { return d.rec.Start(ctx, d.w.Tenant, "T3") }

// Tick runs one reconcile pass and returns the job's phase after it.
func (d *Deletion) Tick(ctx context.Context) (tenantdeletion.Phase, error) {
	err := d.rec.ReconcileTenant(ctx, d.w.Tenant)
	j, gerr := d.Jobs.Get(context.WithoutCancel(ctx), d.w.Tenant)
	if gerr != nil {
		return "", fmt.Errorf("read deletion job: %w", gerr)
	}
	return j.Phase, err
}

// ThroughPhase2 ticks until the job has left Phase 2, at most maxTicks
// times, and returns the job's phase.
func (d *Deletion) ThroughPhase2(ctx context.Context, maxTicks int) (tenantdeletion.Phase, error) {
	var phase tenantdeletion.Phase
	for i := 0; i < maxTicks; i++ {
		p, err := d.Tick(ctx)
		if err != nil {
			return p, err
		}
		phase = p
		if p != tenantdeletion.PhaseSoftDisable && p != tenantdeletion.PhaseTerminateSessions {
			return p, nil
		}
	}
	return phase, fmt.Errorf("deletion job still at %s after %d ticks", phase, maxTicks)
}

// watchedDeletionJobs asserts, on every committed write that moves the job
// out of Phase 2, that the tenant holds no non-terminal session.
type watchedDeletionJobs struct {
	tenantdeletion.Store
	w *World
}

// Update implements tenantdeletion.Store.
func (s *watchedDeletionJobs) Update(ctx context.Context, tenantID string, mutate func(*tenantdeletion.Job) error) (tenantdeletion.Job, error) {
	var from, to tenantdeletion.Phase
	j, err := s.Store.Update(ctx, tenantID, func(j *tenantdeletion.Job) error {
		from = j.Phase
		if err := mutate(j); err != nil {
			return err
		}
		to = j.Phase
		return nil
	})
	if err == nil && from == tenantdeletion.PhaseTerminateSessions && to != from {
		s.w.assertScopeDrained(context.WithoutCancel(ctx), "", fmt.Sprintf("when the deletion job left Phase 2 for %s", to))
	}
	return j, err
}

// disabler is the Phase 1 SoftDisabler: it moves an active tenant row to
// disabling.
type disabler struct{ tenants tenantstore.Store }

func (d disabler) SoftDisableTenant(ctx context.Context, tenantID string) error {
	_, err := d.tenants.Update(ctx, tenantID, func(t *tenantstore.Tenant) error {
		if t.State == "" || t.State == tenantstore.TenantStateActive {
			t.State = tenantstore.TenantStateDisabling
		}
		return nil
	})
	return err
}

// phase2Drain is the Phase 2 SessionTerminator: the shared drain over the
// whole tenant. It logs each drain's elapsed time, because a long Phase 2
// drain delays the tenant-deletion tick and the tier-8 runs make that delay
// observable through the test log.
type phase2Drain struct{ w *World }

// spec: §12.8 (Tenant deletion lifecycle), §12.8 (Lifecycle fence and drain)
func (p phase2Drain) TerminateTenantSessions(ctx context.Context, tenantID string) error {
	if tenantID != p.w.Tenant {
		return fmt.Errorf("phase 2 drain of foreign tenant %s", tenantID)
	}
	start := time.Now()
	res, err := p.w.Drain(ctx, "")
	p.logDrain(ctx, tenantID, time.Since(start), res, err)
	if err != nil {
		return fmt.Errorf("tenant deletion drain: %w", err)
	}
	return nil
}

// logDrain records one Phase 2 drain: the tenant, how long it took, how
// many sessions it terminated, and the error it returned, if any.
func (p phase2Drain) logDrain(ctx context.Context, tenantID string, elapsed time.Duration, res admin.DrainResult, err error) {
	p.w.Logger.InfoContext(ctx, "tenant deletion phase 2 drain finished",
		"tenant_id", tenantID,
		"elapsed_seconds", elapsed.Seconds(),
		"terminated", res.Terminated,
		"error", errString(err))
}

// errString renders err for a log attribute, empty when err is nil.
func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// noopRevoker stands in for the Phase 3 revoker, which the reconciler
// requires. The harness models no credential leases or TokenStore rows, so
// the phase has nothing to act on here.
type noopRevoker struct{}

func (noopRevoker) RevokeTenantCredentials(context.Context, string) error { return nil }

type noopEraser struct{}

func (noopEraser) DeleteByTenant(context.Context, string) (map[string]int, error) {
	return map[string]int{}, nil
}

type noopReceipts struct{}

func (noopReceipts) WriteReceipt(context.Context, tenantdeletion.Receipt) error { return nil }
