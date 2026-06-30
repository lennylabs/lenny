// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasure"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob"
)

// spec: §24.12 lines 143-144 / §12.8 lines 764, 766 — operator retry of
// a failed erasure job and the manual clear of the GDPR Article 18
// processing restriction.

// flakyEraser returns an erasure adapter whose DeleteByUser fails on its
// first call and succeeds (returning n) on every call after, so a retry
// can be exercised: the first job run lands in `failed`, the retry run
// completes.
func flakyEraser(name string, n int) erasure.Eraser {
	var mu sync.Mutex
	calls := 0
	return erasure.Eraser{
		Name: name,
		DeleteByUser: func(context.Context, string, string) (int, error) {
			mu.Lock()
			defer mu.Unlock()
			calls++
			if calls == 1 {
				return 0, context.DeadlineExceeded
			}
			return n, nil
		},
	}
}

// initiateAndFail erases the seeded user with the supplied orchestrator
// and returns the job id once the job reaches a terminal phase.
func initiateAndFail(t *testing.T, router *admin.Router, jobs erasurejob.Store, subject string) string {
	t.Helper()
	rr := eraseUser(t, router.Handler(), subject,
		admin.EraseUserRequest{TenantID: "acme"}, withAdminPrincipal)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("erase: status %d, body %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode erase response: %v", err)
	}
	jobID, _ := resp["jobId"].(string)
	if jobID == "" {
		t.Fatal("erase response missing jobId")
	}
	awaitTerminal(t, jobs, jobID)
	return jobID
}

func TestRetryFailedErasureJobCompletes_spec_24_12_143(t *testing.T) {
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{flakyEraser("sessions", 4)}})
	router, users, jobs, audit := newErasureAdmin(t, orch)
	seedUser(t, users, "acme", "alice@acme.com")

	jobID := initiateAndFail(t, router, jobs, "alice@acme.com")
	if job, _ := jobs.Get(context.Background(), jobID); job.Phase != erasurejob.PhaseFailed {
		t.Fatalf("precondition: job phase = %q, want failed", job.Phase)
	}
	// A failed job leaves the restriction set.
	if u, _ := users.Get(context.Background(), "acme", "alice@acme.com"); !u.ProcessingRestricted {
		t.Fatal("precondition: a failed job must leave processing_restricted set")
	}

	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/erasure-jobs/"+jobID+"/retry", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("retry: status %d, body %s", rr.Code, rr.Body.String())
	}

	job := awaitTerminal(t, jobs, jobID)
	if job.Phase != erasurejob.PhaseCompleted {
		t.Fatalf("after retry, job phase = %q, want completed", job.Phase)
	}
	if job.Failure != "" {
		t.Errorf("after a successful retry, Failure must be cleared, got %q", job.Failure)
	}
	// On completion the restriction is lifted.
	if u, _ := users.Get(context.Background(), "acme", "alice@acme.com"); u.ProcessingRestricted {
		t.Error("a completed retry must clear processing_restricted")
	}
	// The retry is recorded in the audit trail with operator identity.
	ev := awaitAuditEvent(t, audit, "gdpr.erasure_job_retried")
	if ev.Detail["jobId"] != jobID {
		t.Errorf("retry audit event jobId = %v, want %q", ev.Detail["jobId"], jobID)
	}
	if ev.Detail["userId"] != "alice@acme.com" {
		t.Errorf("retry audit event userId = %v", ev.Detail["userId"])
	}
}

func TestRetryNonFailedJobRejected_spec_24_12_143(t *testing.T) {
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{userEraser("sessions", 2)}})
	router, users, jobs, _ := newErasureAdmin(t, orch)
	seedUser(t, users, "acme", "bob@acme.com")

	// A clean erasure completes; retrying a completed job is rejected.
	rr := eraseUser(t, router.Handler(), "bob@acme.com",
		admin.EraseUserRequest{TenantID: "acme"}, withAdminPrincipal)
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	jobID := resp["jobId"].(string)
	job := awaitTerminal(t, jobs, jobID)
	if job.Phase != erasurejob.PhaseCompleted {
		t.Fatalf("precondition: job phase = %q, want completed", job.Phase)
	}

	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/erasure-jobs/"+jobID+"/retry", nil))
	retryRR := httptest.NewRecorder()
	router.Handler().ServeHTTP(retryRR, req)
	if retryRR.Code != http.StatusConflict {
		t.Fatalf("retry of a completed job: status %d, want 409", retryRR.Code)
	}
	var errResp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(retryRR.Body.Bytes(), &errResp)
	if errResp.Error.Code != "ERASURE_JOB_NOT_FAILED" {
		t.Errorf("retry rejection code = %q, want ERASURE_JOB_NOT_FAILED", errResp.Error.Code)
	}
}

func TestRetryUnknownJobNotFound_spec_24_12_143(t *testing.T) {
	router, _, _, _ := newErasureAdmin(t, erasure.New(erasure.Config{}))
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/erasure-jobs/erasure_absent/retry", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("retry of unknown job: status %d, want 404", rr.Code)
	}
}

func TestClearProcessingRestrictionLiftsFlag_spec_24_12_144(t *testing.T) {
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{
		{Name: "broken", DeleteByUser: func(context.Context, string, string) (int, error) {
			return 0, context.DeadlineExceeded
		}},
	}})
	router, users, jobs, audit := newErasureAdmin(t, orch)
	seedUser(t, users, "acme", "carol@acme.com")

	jobID := initiateAndFail(t, router, jobs, "carol@acme.com")
	if u, _ := users.Get(context.Background(), "acme", "carol@acme.com"); !u.ProcessingRestricted {
		t.Fatal("precondition: failed job must leave processing_restricted set")
	}

	body, _ := json.Marshal(map[string]any{"justification": "failure is unrecoverable; manual erasure performed"})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost,
		"/v1/admin/erasure-jobs/"+jobID+"/clear-processing-restriction", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("clear-restriction: status %d, body %s", rr.Code, rr.Body.String())
	}

	u, err := users.Get(context.Background(), "acme", "carol@acme.com")
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if u.ProcessingRestricted {
		t.Error("clear-restriction must clear processing_restricted")
	}
	if u.ErasureJobID != "" {
		t.Errorf("clear-restriction must clear the erasure job id, got %q", u.ErasureJobID)
	}
	ev := awaitAuditEvent(t, audit, "gdpr.processing_restriction_cleared")
	if ev.Detail["justification"] == "" {
		t.Error("clear-restriction audit event must record the operator justification")
	}
	if ev.ActorSubject != "admin@acme.com" {
		t.Errorf("clear-restriction audit event actor = %q, want admin@acme.com", ev.ActorSubject)
	}
}

func TestClearProcessingRestrictionRequiresJustification_spec_24_12_144(t *testing.T) {
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{
		{Name: "broken", DeleteByUser: func(context.Context, string, string) (int, error) {
			return 0, context.DeadlineExceeded
		}},
	}})
	router, users, jobs, _ := newErasureAdmin(t, orch)
	seedUser(t, users, "acme", "dave@acme.com")
	jobID := initiateAndFail(t, router, jobs, "dave@acme.com")

	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost,
		"/v1/admin/erasure-jobs/"+jobID+"/clear-processing-restriction", bytes.NewReader([]byte(`{}`))))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("clear-restriction without justification: status %d, want 400", rr.Code)
	}
	// The user remains restricted because the clear was rejected.
	if u, _ := users.Get(context.Background(), "acme", "dave@acme.com"); !u.ProcessingRestricted {
		t.Error("a rejected clear must not lift the restriction")
	}
}

func TestClearProcessingRestrictionUnknownJobNotFound_spec_24_12_144(t *testing.T) {
	router, _, _, _ := newErasureAdmin(t, erasure.New(erasure.Config{}))
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost,
		"/v1/admin/erasure-jobs/erasure_absent/clear-processing-restriction",
		bytes.NewReader([]byte(`{"justification":"x"}`))))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("clear-restriction on unknown job: status %d, want 404", rr.Code)
	}
}

// TestClearProcessingRestrictionMemoryStore proves the userstore.Memory
// privileged clear lifts the flag directly (the Article 18 trigger is a
// Postgres-only control). spec: §12.8 line 764.
func TestClearProcessingRestrictionMemoryStore_spec_12_8_764(t *testing.T) {
	users := userstore.NewMemory()
	ctx := context.Background()
	if err := users.Create(ctx, userstore.User{Subject: "erin@acme.com", TenantID: "acme"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := users.Update(ctx, "acme", "erin@acme.com", func(u *userstore.User) error {
		u.ProcessingRestricted = true
		u.ErasureJobID = "erasure_x"
		return nil
	}); err != nil {
		t.Fatalf("set restriction: %v", err)
	}
	got, err := users.ClearProcessingRestriction(ctx, "acme", "erin@acme.com")
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if got.ProcessingRestricted || got.ErasureJobID != "" {
		t.Errorf("clear left restricted=%v jobID=%q", got.ProcessingRestricted, got.ErasureJobID)
	}
	if _, err := users.ClearProcessingRestriction(ctx, "acme", "absent@acme.com"); err != userstore.ErrNotFound {
		t.Errorf("clear of absent user: err = %v, want ErrNotFound", err)
	}
}
