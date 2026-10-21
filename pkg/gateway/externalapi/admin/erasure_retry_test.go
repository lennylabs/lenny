// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasure"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob"
)

// spec: §24.12 / §12.8 — operator retry of
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
	if job, _ := jobs.Get(context.Background(), erasurejob.AllTenants, jobID); job.Phase != erasurejob.PhaseFailed {
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

// clearRestriction posts the clear-processing-restriction request for
// jobID with a justification.
func clearRestriction(t *testing.T, router *admin.Router, jobID string) *httptest.ResponseRecorder {
	t.Helper()
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost,
		"/v1/admin/erasure-jobs/"+jobID+"/clear-processing-restriction",
		bytes.NewReader([]byte(`{"justification":"operator review"}`))))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	return rr
}

// TestClearProcessingRestrictionRefusesInProgressJob_spec_15_1 pins the
// clear's refusal of a job that is not failed: while a job is in
// progress, a clear would reopen the creation fence after the drain, so it
// answers 409 ERASURE_JOB_NOT_FAILED, leaves the flag set, and emits no
// audit event.
// spec: 15.1 (REST API), 12.8 (Compliance Interfaces: Processing restriction during erasure (GDPR Article 18))
func TestClearProcessingRestrictionRefusesInProgressJob_spec_15_1(t *testing.T) {
	router, users, jobs, audit := newErasureAdmin(t, erasure.New(erasure.Config{}))
	seedUser(t, users, "acme", "erin@acme.com")
	job, err := jobs.Start(context.Background(), erasurejob.Job{
		ID: "erasure_live", TenantID: "acme", UserID: "erin@acme.com", Phase: erasurejob.PhaseStoreDeleting,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	rr := clearRestriction(t, router, job.ID)
	if rr.Code != http.StatusConflict || errorCode(t, rr.Body.Bytes()) != "ERASURE_JOB_NOT_FAILED" {
		t.Fatalf("clear of an in-progress job: %d %s, want 409 ERASURE_JOB_NOT_FAILED", rr.Code, rr.Body.String())
	}
	if u, _ := users.Get(context.Background(), "acme", "erin@acme.com"); !u.ProcessingRestricted || u.ErasureJobID != job.ID {
		t.Fatalf("a refused clear changed the flag: %+v", u)
	}
	for _, ev := range audit.snapshot() {
		if ev.Type == "gdpr.processing_restriction_cleared" {
			t.Fatal("a refused clear emitted gdpr.processing_restriction_cleared")
		}
	}
}

// TestClearProcessingRestrictionRefusesOtherJobsFlag_spec_15_1 pins the
// clear's refusal when the user's erasure_job_id names another job: 409
// INVALID_STATE_TRANSITION and the other job's flag left set.
// spec: 15.1 (REST API), 12.8 (Compliance Interfaces: Processing restriction during erasure (GDPR Article 18))
func TestClearProcessingRestrictionRefusesOtherJobsFlag_spec_15_1(t *testing.T) {
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{
		{Name: "broken", DeleteByUser: func(context.Context, string, string) (int, error) {
			return 0, context.DeadlineExceeded
		}},
	}})
	router, users, jobs, _ := newErasureAdmin(t, orch)
	seedUser(t, users, "acme", "frank@acme.com")
	jobID := initiateAndFail(t, router, jobs, "frank@acme.com")
	if _, err := users.SetErasureRestriction(context.Background(), "acme", "frank@acme.com", "erasure_other", true, ""); err != nil {
		t.Fatalf("repoint flag: %v", err)
	}

	rr := clearRestriction(t, router, jobID)
	if rr.Code != http.StatusConflict || errorCode(t, rr.Body.Bytes()) != "INVALID_STATE_TRANSITION" {
		t.Fatalf("clear while another job holds the flag: %d %s, want 409 INVALID_STATE_TRANSITION", rr.Code, rr.Body.String())
	}
	if u, _ := users.Get(context.Background(), "acme", "frank@acme.com"); !u.ProcessingRestricted || u.ErasureJobID != "erasure_other" {
		t.Fatalf("a refused clear changed the flag: %+v", u)
	}
}

// TestClearProcessingRestrictionUnknownUserNotFound_spec_15_1 pins the 404
// for a failed job whose users row no longer exists.
// spec: 15.1 (REST API)
func TestClearProcessingRestrictionUnknownUserNotFound_spec_15_1(t *testing.T) {
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{
		{Name: "broken", DeleteByUser: func(context.Context, string, string) (int, error) {
			return 0, context.DeadlineExceeded
		}},
	}})
	router, users, jobs, _ := newErasureAdmin(t, orch)
	seedUser(t, users, "acme", "grace@acme.com")
	jobID := initiateAndFail(t, router, jobs, "grace@acme.com")
	if _, err := users.DeleteByUser(context.Background(), "acme", "grace@acme.com"); err != nil {
		t.Fatalf("drop users row: %v", err)
	}
	if rr := clearRestriction(t, router, jobID); rr.Code != http.StatusNotFound {
		t.Fatalf("clear for an absent user: %d, want 404", rr.Code)
	}
}

// TestRetryWhileAnotherJobInProgress_spec_15_1 pins the retry's refusal of
// a failed job while another job for the user is in progress: 409
// INVALID_STATE_TRANSITION, and the failed job
// stays failed.
// spec: 15.1 (REST API), 12.8 (Compliance Interfaces: User-level erasure invocation)
func TestRetryWhileAnotherJobInProgress_spec_15_1(t *testing.T) {
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{
		{Name: "broken", DeleteByUser: func(context.Context, string, string) (int, error) {
			return 0, context.DeadlineExceeded
		}},
	}})
	router, users, jobs, _ := newErasureAdmin(t, orch)
	seedUser(t, users, "acme", "heidi@acme.com")
	jobID := initiateAndFail(t, router, jobs, "heidi@acme.com")
	if _, err := jobs.Start(context.Background(), erasurejob.Job{
		ID: "erasure_newer", TenantID: "acme", UserID: "heidi@acme.com", Phase: erasurejob.PhaseInitiated,
	}); err != nil {
		t.Fatalf("start newer job: %v", err)
	}

	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/erasure-jobs/"+jobID+"/retry", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict || errorCode(t, rr.Body.Bytes()) != "INVALID_STATE_TRANSITION" {
		t.Fatalf("retry beside an in-progress job: %d %s, want 409 INVALID_STATE_TRANSITION", rr.Code, rr.Body.String())
	}
	if job, _ := jobs.Get(context.Background(), erasurejob.AllTenants, jobID); job.Phase != erasurejob.PhaseFailed {
		t.Fatalf("refused retry moved the job to %q", job.Phase)
	}
}

// racingJobs is a job store whose Restart and ClearRestriction first run a
// concurrent retry of the same job, as one landing between the handler's
// own read and the store's locked phase check would, and then delegate.
type racingJobs struct {
	*erasurejob.Memory
}

func (r racingJobs) Restart(ctx context.Context, id string) (erasurejob.Job, error) {
	r.concurrentRetry(ctx, id)
	return r.Memory.Restart(ctx, id)
}

func (r racingJobs) ClearRestriction(ctx context.Context, id string) (erasurejob.Job, error) {
	r.concurrentRetry(ctx, id)
	return r.Memory.ClearRestriction(ctx, id)
}

func (r racingJobs) concurrentRetry(ctx context.Context, id string) {
	if _, err := r.Memory.Restart(ctx, id); err != nil {
		panic("racingJobs: concurrent retry: " + err.Error())
	}
}

// newRacingErasureAdmin builds the admin router over racingJobs, with the
// runner writing the inner store so the erase that seeds the failed job
// is not raced.
func newRacingErasureAdmin(t *testing.T, orch *erasure.Orchestrator) (*admin.Router, *userstore.Memory, *erasurejob.Memory) {
	t.Helper()
	users := userstore.NewMemory()
	jobs := erasurejob.NewMemory(erasurejob.WithRestrictionWriter(users))
	runner := erasurejob.NewRunner(jobs, orch, erasureClock)
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: erasureClock,
		Audit: &recordingAudit{},
	}).WithUsers(users).WithErasure(runner, racingJobs{Memory: jobs})
	return router, users, jobs
}

// errorMessage decodes the message of an error envelope.
func errorMessage(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode error envelope: %v (body %s)", err, body)
	}
	return env.Error.Message
}

// TestNotFailedRefusalReportsLockedPhase_spec_15_1 pins the phase an
// ERASURE_JOB_NOT_FAILED refusal from the store reports: the handler reads
// the job as failed, a concurrent retry restarts it before the store
// locks the row, and the 409 names the phase the store refused on
// (initiated) rather than the handler's earlier read (failed).
// spec: 15.1 (REST API), 12.8 (Compliance Interfaces: Processing restriction during erasure (GDPR Article 18))
func TestNotFailedRefusalReportsLockedPhase_spec_15_1(t *testing.T) {
	for _, tc := range []struct {
		name, path, body, subject string
	}{
		{"clear", "/clear-processing-restriction", `{"justification":"operator review"}`, "ivan@acme.com"},
		{"retry", "/retry", "", "judy@acme.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{
				{Name: "broken", DeleteByUser: func(context.Context, string, string) (int, error) {
					return 0, context.DeadlineExceeded
				}},
			}})
			router, users, jobs := newRacingErasureAdmin(t, orch)
			seedUser(t, users, "acme", tc.subject)
			jobID := initiateAndFail(t, router, jobs, tc.subject)

			req := withAdminPrincipal(httptest.NewRequest(http.MethodPost,
				"/v1/admin/erasure-jobs/"+jobID+tc.path, bytes.NewReader([]byte(tc.body))))
			rr := httptest.NewRecorder()
			router.Handler().ServeHTTP(rr, req)
			if rr.Code != http.StatusConflict || errorCode(t, rr.Body.Bytes()) != "ERASURE_JOB_NOT_FAILED" {
				t.Fatalf("%s behind a concurrent retry: %d %s, want 409 ERASURE_JOB_NOT_FAILED", tc.name, rr.Code, rr.Body.String())
			}
			msg := errorMessage(t, rr.Body.Bytes())
			if !strings.Contains(msg, "phase "+string(erasurejob.PhaseInitiated)) || strings.Contains(msg, "phase "+string(erasurejob.PhaseFailed)) {
				t.Fatalf("refusal message = %q, want the locked phase initiated", msg)
			}
		})
	}
}
