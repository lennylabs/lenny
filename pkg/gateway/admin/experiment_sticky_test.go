// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/experimentstore"
)

// fakeFlusher records the (tenant, experiment) flush calls the PATCH handler
// makes so the §10.7 line 1096 invalidation wiring can be asserted.
type fakeFlusher struct {
	mu    sync.Mutex
	calls [][3]string
}

func (f *fakeFlusher) Flush(_ context.Context, tenantID, experimentID, transition string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, [3]string{tenantID, experimentID, transition})
	return 0, nil
}

func (f *fakeFlusher) snapshot() [][3]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][3]string, len(f.calls))
	copy(out, f.calls)
	return out
}

func newStickyExperimentAdmin(t *testing.T) (*admin.Router, *fakeFlusher) {
	t.Helper()
	flusher := &fakeFlusher{}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC) },
		Audit: &recordingAudit{},
	}).WithExperiments(experimentstore.NewMemory()).WithStickyFlusher(flusher)
	return router, flusher
}

func patchStatus(t *testing.T, h http.Handler, id, status string) *int {
	t.Helper()
	rr := doAdminReq(t, h, http.MethodPatch, "/v1/admin/experiments/"+id+"?tenantId=acme",
		map[string]string{"status": status}, withAdminPrincipal)
	return &rr.Code
}

// spec: §10.7 line 1096 — a transition to paused or concluded flushes the
// experiment's sticky cache; a paused→active transition does not.
func TestPatchExperiment_FlushesStickyOnPauseAndConclude_spec_10_7(t *testing.T) {
	router, flusher := newStickyExperimentAdmin(t)
	h := router.Handler()

	if code := *patchStatusCreate(t, h); code != http.StatusCreated {
		t.Fatalf("create: status %d", code)
	}

	// active -> paused: flush.
	if code := *patchStatus(t, h, "exp_s", "paused"); code != http.StatusOK {
		t.Fatalf("patch to paused: status %d", code)
	}
	// paused -> active: no flush.
	if code := *patchStatus(t, h, "exp_s", "active"); code != http.StatusOK {
		t.Fatalf("patch to active: status %d", code)
	}
	// active -> concluded: flush.
	if code := *patchStatus(t, h, "exp_s", "concluded"); code != http.StatusOK {
		t.Fatalf("patch to concluded: status %d", code)
	}

	got := flusher.snapshot()
	if len(got) != 2 {
		t.Fatalf("flush calls = %d (%v), want 2 (paused, concluded)", len(got), got)
	}
	wantTransitions := []string{"paused", "concluded"}
	for i, c := range got {
		if c[0] != "acme" || c[1] != "exp_s" || c[2] != wantTransitions[i] {
			t.Fatalf("flush call %d = %v, want {acme exp_s %s}", i, c, wantTransitions[i])
		}
	}
}

// A no-op PATCH (status unchanged) must not flush the cache.
func TestPatchExperiment_NoOpDoesNotFlush(t *testing.T) {
	router, flusher := newStickyExperimentAdmin(t)
	h := router.Handler()
	if code := *patchStatusCreate(t, h); code != http.StatusCreated {
		t.Fatalf("create: status %d", code)
	}
	if code := *patchStatus(t, h, "exp_s", "active"); code != http.StatusOK {
		t.Fatalf("patch to active (no-op): status %d", code)
	}
	if got := flusher.snapshot(); len(got) != 0 {
		t.Fatalf("flush calls = %v, want none on a no-op transition", got)
	}
}

func patchStatusCreate(t *testing.T, h http.Handler) *int {
	t.Helper()
	body := validExperimentPayload("exp_s")
	rr := doAdminReq(t, h, http.MethodPost, "/v1/admin/experiments", body, withAdminPrincipal)
	return &rr.Code
}
