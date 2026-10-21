// SPDX-License-Identifier: MIT

package lifecyclefence

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"

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
// not resume the tenant concurrently.
func (w *World) Suspend(ctx context.Context) SuspendResult {
	rr := w.serveAdmin(ctx, "/v1/admin/tenants/"+w.Tenant+"/suspend")
	res := SuspendResult{Status: rr.Code, Body: rr.Body.String()}
	if rr.Code != http.StatusOK {
		return res
	}
	var out struct {
		Suspended      bool     `json:"suspended"`
		FailedSessions []string `json:"failedSessions"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		w.Violatef("suspend: decode %q: %v", res.Body, err)
		return res
	}
	res.FailedSessions = out.FailedSessions
	if !out.Suspended {
		w.Violatef("suspend answered 200 with suspended=false: %s", res.Body)
	}
	if len(out.FailedSessions) == 0 {
		w.assertScopeDrained(context.WithoutCancel(ctx), "", "after a suspend that answered no failed sessions")
	}
	return res
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
		Eraser:     noopEraser{},
		Receipts:   noopReceipts{},
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
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
// whole tenant.
type phase2Drain struct{ w *World }

func (p phase2Drain) TerminateTenantSessions(ctx context.Context, tenantID string) error {
	if tenantID != p.w.Tenant {
		return fmt.Errorf("phase 2 drain of foreign tenant %s", tenantID)
	}
	if _, err := p.w.Drain(ctx, ""); err != nil {
		return fmt.Errorf("tenant deletion drain: %w", err)
	}
	return nil
}

type noopEraser struct{}

func (noopEraser) DeleteByTenant(context.Context, string) (map[string]int, error) {
	return map[string]int{}, nil
}

type noopReceipts struct{}

func (noopReceipts) WriteReceipt(context.Context, tenantdeletion.Receipt) error { return nil }
