// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/erasure"
	"github.com/lennylabs/lenny/pkg/gateway/erasurejob"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/userstore"
)

// spec: §12.8 GDPR user erasure — POST /v1/admin/users/{user_id}/erase
// and GET /v1/admin/erasure-jobs/{job_id}.

var erasureClock = func() time.Time { return time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC) }

// userEraser builds a user-scoped erasure adapter returning n.
func userEraser(name string, n int) erasure.Eraser {
	return erasure.Eraser{
		Name:         name,
		DeleteByUser: func(context.Context, string, string) (int, error) { return n, nil },
	}
}

func newErasureAdmin(t *testing.T, orch *erasure.Orchestrator) (*admin.Router, userstore.Store, erasurejob.Store, *recordingAudit) {
	t.Helper()
	users := userstore.NewMemory()
	jobs := erasurejob.NewMemory()
	audit := &recordingAudit{}
	runner := erasurejob.NewRunner(jobs, orch, erasureClock)
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: erasureClock,
		Audit: audit,
	}).WithUsers(users).WithErasure(runner, jobs)
	return router, users, jobs, audit
}

// awaitTerminal polls the job store until the job reaches a terminal
// phase — the erase handler runs the job in a background goroutine.
func awaitTerminal(t *testing.T, jobs erasurejob.Store, jobID string) erasurejob.Job {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		job, err := jobs.Get(context.Background(), jobID)
		if err != nil {
			t.Fatalf("Get job %q: %v", jobID, err)
		}
		if job.Phase.Terminal() {
			return job
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("erasure job %q did not reach a terminal phase", jobID)
	return erasurejob.Job{}
}

func eraseUser(t *testing.T, h http.Handler, subject string, body admin.EraseUserRequest, as func(*http.Request) *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := as(httptest.NewRequest(http.MethodPost, "/v1/admin/users/"+subject+"/erase", bytes.NewReader(b)))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestEraseUserInitiatesJob(t *testing.T) {
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{userEraser("sessions", 4)}})
	router, users, jobs, audit := newErasureAdmin(t, orch)
	seedUser(t, users, "acme", "alice@acme.com")

	rr := eraseUser(t, router.Handler(), "alice@acme.com",
		admin.EraseUserRequest{TenantID: "acme"}, withAdminPrincipal)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("erase: status %d, body %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	jobID, _ := resp["jobId"].(string)
	if jobID == "" {
		t.Fatal("response is missing a jobId")
	}
	if resp["phase"] != string(erasurejob.PhaseInitiated) {
		t.Errorf("phase = %v, want initiated", resp["phase"])
	}

	job := awaitTerminal(t, jobs, jobID)
	if job.Phase != erasurejob.PhaseCompleted {
		t.Errorf("job phase = %q, want completed", job.Phase)
	}
	if job.Total != 4 {
		t.Errorf("job Total = %d, want 4", job.Total)
	}
	// The initiation event is emitted synchronously, so it is always
	// present and first; the gdpr.erasure_completed receipt follows
	// asynchronously once the job finishes.
	if snap := audit.snapshot(); len(snap) == 0 || snap[0].Type != "admin.user.erasure_initiated" {
		t.Errorf("audit: first event should be admin.user.erasure_initiated, got %+v", snap)
	}
}

// awaitAuditEvent polls the recording audit sink until an event of the
// given type appears, then returns it. The erasure receipt is emitted
// from a background goroutine once the job completes.
func awaitAuditEvent(t *testing.T, audit *recordingAudit, eventType string) admin.AuditEvent {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, ev := range audit.snapshot() {
			if ev.Type == eventType {
				return ev
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("audit event %q was not emitted", eventType)
	return admin.AuditEvent{}
}

func TestEraseUserEmitsCompletionReceipt(t *testing.T) {
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{userEraser("sessions", 6)}})
	router, users, _, audit := newErasureAdmin(t, orch)
	seedUser(t, users, "acme", "ivan@acme.com")

	rr := eraseUser(t, router.Handler(), "ivan@acme.com",
		admin.EraseUserRequest{TenantID: "acme"}, withAdminPrincipal)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("erase: status %d", rr.Code)
	}
	// §12.8: a completed erasure writes a gdpr.erasure_completed receipt
	// to the audit trail.
	receipt := awaitAuditEvent(t, audit, "gdpr.erasure_completed")
	if receipt.TargetResource != "ivan@acme.com" {
		t.Errorf("receipt target = %q, want ivan@acme.com", receipt.TargetResource)
	}
	if receipt.Detail["total"] != 6 {
		t.Errorf("receipt total = %v, want 6", receipt.Detail["total"])
	}
}

// newBillingErasureAdmin builds an erasure admin router whose erasure
// runner has the §12.8 BillingEraser attached. Tenant `acme` is seeded
// with the given billingErasurePolicy.
func newBillingErasureAdmin(t *testing.T, billingPolicy string) (*admin.Router, userstore.Store, *billingstore.Memory, *recordingAudit) {
	t.Helper()
	users := userstore.NewMemory()
	jobs := erasurejob.NewMemory()
	tenants := tenantstore.NewMemory()
	if err := tenants.Create(context.Background(), tenantstore.Tenant{
		ID: "acme", BillingErasurePolicy: billingPolicy,
	}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	billing := billingstore.NewMemory()
	audit := &recordingAudit{}
	runner := erasurejob.NewRunner(jobs, erasure.New(erasure.Config{}), erasureClock).
		WithBilling(erasurejob.NewBillingEraser(billing, tenants))
	router := admin.NewRouter(tenants, admin.Options{Clock: erasureClock, Audit: audit}).
		WithUsers(users).WithErasure(runner, jobs)
	return router, users, billing, audit
}

func TestEraseUserReceiptRecordsBillingPseudonymization(t *testing.T) {
	router, users, billing, audit := newBillingErasureAdmin(t, "")
	seedUser(t, users, "acme", "alice@acme.com")
	for i := 0; i < 3; i++ {
		if _, err := billing.Append(context.Background(), billingstore.Event{
			TenantID: "acme", UserID: "alice@acme.com", SessionID: "s",
			EventType: billingstore.EventSessionCreated,
		}); err != nil {
			t.Fatalf("seed billing event: %v", err)
		}
	}

	rr := eraseUser(t, router.Handler(), "alice@acme.com",
		admin.EraseUserRequest{TenantID: "acme"}, withAdminPrincipal)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("erase: status %d, body %s", rr.Code, rr.Body.String())
	}

	// §12.8: the receipt records that billing events were pseudonymized.
	receipt := awaitAuditEvent(t, audit, "gdpr.erasure_completed")
	be, ok := receipt.Detail["billingErasure"].(map[string]any)
	if !ok {
		t.Fatalf("receipt missing billingErasure detail: %v", receipt.Detail)
	}
	if be["disposition"] != "pseudonymized" {
		t.Errorf("billingErasure.disposition = %v, want pseudonymized", be["disposition"])
	}
	if be["verified"] != true {
		t.Errorf("billingErasure.verified = %v, want true", be["verified"])
	}
	// spec: §12.8 line 851 — the salt-removal verification outcome is
	// recorded explicitly in the receipt.
	if receipt.Detail["verificationOutcome"] != "verified" {
		t.Errorf("verificationOutcome = %v, want verified", receipt.Detail["verificationOutcome"])
	}
	// spec: §12.8 line 762 — the receipt carries the per-phase timeline,
	// ending in the completed transition.
	assertReceiptPhaseLogCompleted(t, receipt)
	if cnt, _ := billing.CountUser(context.Background(), "acme", "alice@acme.com"); cnt != 0 {
		t.Errorf("%d billing events still carry the original user id, want 0", cnt)
	}
}

// assertReceiptPhaseLogCompleted checks the §12.8 receipt phaseLog is a
// non-empty array whose last entry is the completed phase.
func assertReceiptPhaseLogCompleted(t *testing.T, receipt admin.AuditEvent) {
	t.Helper()
	log, ok := receipt.Detail["phaseLog"].([]map[string]any)
	if !ok || len(log) == 0 {
		t.Fatalf("receipt phaseLog missing or empty: %v", receipt.Detail["phaseLog"])
	}
	last := log[len(log)-1]
	if last["phase"] != "completed" {
		t.Errorf("receipt phaseLog last phase = %v, want completed", last["phase"])
	}
	if at, ok := last["at"].(string); !ok || at == "" {
		t.Errorf("receipt phaseLog entry missing at timestamp: %v", last)
	}
}

func TestEraseUserReceiptRecordsBillingExempt(t *testing.T) {
	router, users, billing, audit := newBillingErasureAdmin(t, tenantstore.BillingErasureExempt)
	seedUser(t, users, "acme", "bob@acme.com")
	if _, err := billing.Append(context.Background(), billingstore.Event{
		TenantID: "acme", UserID: "bob@acme.com", SessionID: "s",
		EventType: billingstore.EventSessionCreated,
	}); err != nil {
		t.Fatalf("seed billing event: %v", err)
	}

	rr := eraseUser(t, router.Handler(), "bob@acme.com",
		admin.EraseUserRequest{TenantID: "acme"}, withAdminPrincipal)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("erase: status %d", rr.Code)
	}

	receipt := awaitAuditEvent(t, audit, "gdpr.erasure_completed")
	be, ok := receipt.Detail["billingErasure"].(map[string]any)
	if !ok {
		t.Fatalf("receipt missing billingErasure detail: %v", receipt.Detail)
	}
	if be["disposition"] != "exempt" {
		t.Errorf("billingErasure.disposition = %v, want exempt", be["disposition"])
	}
	// spec: §12.8 line 851 — an exempt tenant runs no salt verification.
	if receipt.Detail["verificationOutcome"] != "exempt" {
		t.Errorf("verificationOutcome = %v, want exempt", receipt.Detail["verificationOutcome"])
	}
	// §12.8 Article 17(3)(b): an exempt tenant retains the original id.
	if cnt, _ := billing.CountUser(context.Background(), "acme", "bob@acme.com"); cnt != 1 {
		t.Errorf("exempt tenant's billing event was rewritten: CountUser=%d, want 1", cnt)
	}
}

// spec: §12.8 lines 762, 851 — when no BillingEraser is wired the
// completion receipt still carries the per-phase timeline and records a
// not_applicable verification outcome (no salt was destroyed).
func TestEraseUserReceiptRecordsPhaseLogWithoutBilling(t *testing.T) {
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{userEraser("sessions", 1)}})
	router, users, _, audit := newErasureAdmin(t, orch)
	seedUser(t, users, "acme", "alice@acme.com")

	rr := eraseUser(t, router.Handler(), "alice@acme.com",
		admin.EraseUserRequest{TenantID: "acme"}, withAdminPrincipal)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("erase: status %d, body %s", rr.Code, rr.Body.String())
	}

	receipt := awaitAuditEvent(t, audit, "gdpr.erasure_completed")
	assertReceiptPhaseLogCompleted(t, receipt)
	if receipt.Detail["verificationOutcome"] != "not_applicable" {
		t.Errorf("verificationOutcome = %v, want not_applicable", receipt.Detail["verificationOutcome"])
	}
}

func TestEraseUserNotFound(t *testing.T) {
	router, _, _, _ := newErasureAdmin(t, erasure.New(erasure.Config{}))
	rr := eraseUser(t, router.Handler(), "ghost@acme.com",
		admin.EraseUserRequest{TenantID: "acme"}, withAdminPrincipal)
	if rr.Code != http.StatusNotFound {
		t.Errorf("erase unknown user: status %d, want 404", rr.Code)
	}
}

func TestEraseUserRequiresAdmin(t *testing.T) {
	router, users, _, _ := newErasureAdmin(t, erasure.New(erasure.Config{}))
	seedUser(t, users, "acme", "alice@acme.com")
	asPlainUser := func(req *http.Request) *http.Request {
		ctx := authmw.WithPrincipal(req.Context(), authmw.Principal{
			Subject: "alice@acme.com", TenantID: "acme",
			Roles: []pkgauth.Role{pkgauth.RoleUser},
		})
		return req.WithContext(ctx)
	}
	rr := eraseUser(t, router.Handler(), "alice@acme.com",
		admin.EraseUserRequest{TenantID: "acme"}, asPlainUser)
	if rr.Code != http.StatusForbidden {
		t.Errorf("plain user erase: status %d, want 403", rr.Code)
	}
}

func TestEraseUserTenantAdminScoped(t *testing.T) {
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{userEraser("sessions", 1)}})
	router, users, jobs, _ := newErasureAdmin(t, orch)
	seedUser(t, users, "acme", "bob@acme.com")

	// A tenant-admin omits tenantId; the handler derives it from the token.
	rr := eraseUser(t, router.Handler(), "bob@acme.com", admin.EraseUserRequest{},
		func(req *http.Request) *http.Request { return withTenantAdminFor(req, "acme") })
	if rr.Code != http.StatusAccepted {
		t.Fatalf("tenant-admin erase: status %d, body %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	job := awaitTerminal(t, jobs, resp["jobId"].(string))
	if job.TenantID != "acme" {
		t.Errorf("job tenant = %q, want acme (derived from the token)", job.TenantID)
	}
}

func TestGetErasureJobReportsCompletion(t *testing.T) {
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{
		userEraser("sessions", 3),
		userEraser("interactions", 2),
	}})
	router, users, jobs, _ := newErasureAdmin(t, orch)
	seedUser(t, users, "acme", "carol@acme.com")

	rr := eraseUser(t, router.Handler(), "carol@acme.com",
		admin.EraseUserRequest{TenantID: "acme"}, withAdminPrincipal)
	var initResp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &initResp)
	jobID := initResp["jobId"].(string)
	awaitTerminal(t, jobs, jobID)

	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/erasure-jobs/"+jobID, nil))
	statusRR := httptest.NewRecorder()
	router.Handler().ServeHTTP(statusRR, req)
	if statusRR.Code != http.StatusOK {
		t.Fatalf("get erasure job: status %d, body %s", statusRR.Code, statusRR.Body.String())
	}
	var status map[string]any
	if err := json.Unmarshal(statusRR.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status["phase"] != string(erasurejob.PhaseCompleted) {
		t.Errorf("phase = %v, want completed", status["phase"])
	}
	if status["completionPercent"] != float64(100) {
		t.Errorf("completionPercent = %v, want 100", status["completionPercent"])
	}
	if status["total"] != float64(5) {
		t.Errorf("total = %v, want 5", status["total"])
	}
	deleted, ok := status["deleted"].(map[string]any)
	if !ok || deleted["sessions"] != float64(3) || deleted["interactions"] != float64(2) {
		t.Errorf("deleted = %v, want sessions=3 interactions=2", status["deleted"])
	}
}

func TestGetErasureJobReportsFailure(t *testing.T) {
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{
		{Name: "broken", DeleteByUser: func(context.Context, string, string) (int, error) {
			return 0, errors.New("store down")
		}},
	}})
	router, users, jobs, _ := newErasureAdmin(t, orch)
	seedUser(t, users, "acme", "dave@acme.com")

	rr := eraseUser(t, router.Handler(), "dave@acme.com",
		admin.EraseUserRequest{TenantID: "acme"}, withAdminPrincipal)
	var initResp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &initResp)
	jobID := initResp["jobId"].(string)
	awaitTerminal(t, jobs, jobID)

	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/erasure-jobs/"+jobID, nil))
	statusRR := httptest.NewRecorder()
	router.Handler().ServeHTTP(statusRR, req)
	if statusRR.Code != http.StatusOK {
		t.Fatalf("get erasure job: status %d", statusRR.Code)
	}
	var status map[string]any
	_ = json.Unmarshal(statusRR.Body.Bytes(), &status)
	if status["phase"] != string(erasurejob.PhaseFailed) {
		t.Errorf("phase = %v, want failed", status["phase"])
	}
	if errMsg, _ := status["error"].(string); errMsg == "" {
		t.Error("a failed job's status must carry the error reason")
	}
}

func TestGetErasureJobNotFound(t *testing.T) {
	router, _, _, _ := newErasureAdmin(t, erasure.New(erasure.Config{}))
	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/erasure-jobs/erasure_absent", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("get unknown job: status %d, want 404", rr.Code)
	}
}

// newErasureAdminWithSessions builds an erasure admin router that also
// has a SessionStore wired, so the §12.8 legal-hold preflight can run.
func newErasureAdminWithSessions(t *testing.T, orch *erasure.Orchestrator) (*admin.Router, userstore.Store, sessionstore.Store, *recordingAudit) {
	t.Helper()
	users := userstore.NewMemory()
	jobs := erasurejob.NewMemory()
	sessions := memstore.New()
	audit := &recordingAudit{}
	runner := erasurejob.NewRunner(jobs, orch, erasureClock)
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: erasureClock,
		Audit: audit,
	}).WithUsers(users).WithErasure(runner, jobs).WithSessions(sessions)
	return router, users, sessions, audit
}

func TestEraseUserBlockedByLegalHold(t *testing.T) {
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{userEraser("sessions", 1)}})
	router, users, sessions, audit := newErasureAdminWithSessions(t, orch)
	seedUser(t, users, "acme", "alice@acme.com")
	seedSession(t, sessions, sessionstore.Session{
		ID: "sess_held", TenantID: "acme", UserID: "alice@acme.com", LegalHold: true,
	})

	rr := eraseUser(t, router.Handler(), "alice@acme.com",
		admin.EraseUserRequest{TenantID: "acme"}, withAdminPrincipal)
	if rr.Code != http.StatusConflict {
		t.Fatalf("erase with a held session: status %d, want 409; body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "ERASURE_BLOCKED_BY_LEGAL_HOLD") {
		t.Errorf("rejection should carry ERASURE_BLOCKED_BY_LEGAL_HOLD: %s", rr.Body.String())
	}
	// §12.8: the job never initiated, so the processing restriction is
	// not applied.
	u, _ := users.Get(context.Background(), "acme", "alice@acme.com")
	if u.ProcessingRestricted {
		t.Error("a hold-blocked erasure must not set the processing restriction")
	}
	found := false
	for _, ev := range audit.snapshot() {
		if ev.Type == "gdpr.erasure_blocked_by_hold" {
			found = true
		}
	}
	if !found {
		t.Error("a hold-blocked erasure must emit gdpr.erasure_blocked_by_hold")
	}
}

// spec: §12.8 line 794(b) — an artifact-level hold on an artifact owned
// by one of the user's sessions blocks the erasure even when the session
// itself is not held.
func TestEraseUserBlockedByArtifactLegalHold(t *testing.T) {
	users := userstore.NewMemory()
	jobs := erasurejob.NewMemory()
	sessions := memstore.New()
	audit := &recordingAudit{}
	holder := newFakeArtifactHolder()
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{userEraser("sessions", 1)}})
	runner := erasurejob.NewRunner(jobs, orch, erasureClock)
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{Clock: erasureClock, Audit: audit}).
		WithUsers(users).WithErasure(runner, jobs).WithSessions(sessions).WithArtifactLegalHold(holder)

	seedUser(t, users, "acme", "alice@acme.com")
	// The session is NOT held, but it owns a held artifact.
	seedSession(t, sessions, sessionstore.Session{
		ID: "sess_free", TenantID: "acme", UserID: "alice@acme.com", LegalHold: false,
	})
	holder.records["blob://acme/sess_free/file"] = artifactcatalog.Record{
		URI: "blob://acme/sess_free/file", TenantID: "acme", SessionID: "sess_free", LegalHold: true,
	}

	rr := eraseUser(t, router.Handler(), "alice@acme.com",
		admin.EraseUserRequest{TenantID: "acme"}, withAdminPrincipal)
	if rr.Code != http.StatusConflict {
		t.Fatalf("erase with a held artifact: status %d, want 409; body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "ERASURE_BLOCKED_BY_LEGAL_HOLD") {
		t.Errorf("rejection should carry ERASURE_BLOCKED_BY_LEGAL_HOLD: %s", rr.Body.String())
	}
	u, _ := users.Get(context.Background(), "acme", "alice@acme.com")
	if u.ProcessingRestricted {
		t.Error("a hold-blocked erasure must not set the processing restriction")
	}
	blocked, ok := findAuditEvent(audit.snapshot(), "gdpr.erasure_blocked_by_hold")
	if !ok {
		t.Fatal("a hold-blocked erasure must emit gdpr.erasure_blocked_by_hold")
	}
	// spec: §12.8 line 794 — the blocked event carries the resource tuples.
	tuples, ok := blocked.Detail["holds"].([]map[string]any)
	if !ok || len(tuples) != 1 || tuples[0]["resourceType"] != "artifact" {
		t.Errorf("blocked event holds = %v, want one artifact tuple", blocked.Detail["holds"])
	}
}

func TestEraseUserProceedsWhenNoHold(t *testing.T) {
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{userEraser("sessions", 1)}})
	router, users, sessions, _ := newErasureAdminWithSessions(t, orch)
	seedUser(t, users, "acme", "bob@acme.com")
	// A session the user owns but which is not under hold must not block.
	seedSession(t, sessions, sessionstore.Session{
		ID: "sess_free", TenantID: "acme", UserID: "bob@acme.com", LegalHold: false,
	})

	rr := eraseUser(t, router.Handler(), "bob@acme.com",
		admin.EraseUserRequest{TenantID: "acme"}, withAdminPrincipal)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("erase with no held session: status %d, want 202; body %s", rr.Code, rr.Body.String())
	}
}

func TestEraseUserHoldOverrideProceeds(t *testing.T) {
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{userEraser("sessions", 1)}})
	router, users, sessions, audit := newErasureAdminWithSessions(t, orch)
	seedUser(t, users, "acme", "alice@acme.com")
	seedSession(t, sessions, sessionstore.Session{
		ID: "sess_held", TenantID: "acme", UserID: "alice@acme.com", LegalHold: true,
	})

	rr := eraseUser(t, router.Handler(), "alice@acme.com", admin.EraseUserRequest{
		TenantID: "acme", AcknowledgeHoldOverride: true, Justification: "court order lifted, ticket-9",
	}, withAdminPrincipal)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("override erase: status %d, want 202; body %s", rr.Code, rr.Body.String())
	}
	// §12.8: the override is recorded synchronously as gdpr.legal_hold_overridden.
	override := awaitAuditEvent(t, audit, "gdpr.legal_hold_overridden")
	// spec: §12.8 line 796 — the event carries the same fields as the
	// receipt (legal_hold_override, override_by, override_justification,
	// override_at, overridden_holds) plus jobId.
	if override.Detail["overrideJustification"] != "court order lifted, ticket-9" {
		t.Errorf("override event overrideJustification = %v", override.Detail["overrideJustification"])
	}
	if override.Detail["legalHoldOverride"] != true {
		t.Errorf("override event should carry legalHoldOverride=true: %+v", override.Detail)
	}
	if at, ok := override.Detail["overrideAt"].(string); !ok || at == "" {
		t.Errorf("override event overrideAt = %v (want non-empty RFC3339 string per §12.8 line 796)", override.Detail["overrideAt"])
	}
	if override.Detail["overriddenHolds"] == nil {
		t.Errorf("override event should carry the overriddenHolds list: %+v", override.Detail)
	}
	// jobId lets an auditor pivot from admin.user.erasure_initiated to the
	// override decision without triangulating on userId alone.
	jobID, ok := override.Detail["jobId"].(string)
	if !ok || jobID == "" {
		t.Errorf("override event jobId = %v (want non-empty string per §12.8 line 796)", override.Detail["jobId"])
	}
	// The completion receipt records that an override was exercised, with
	// the same override_at the event carried.
	receipt := awaitAuditEvent(t, audit, "gdpr.erasure_completed")
	if receipt.Detail["legalHoldOverride"] != true {
		t.Errorf("completion receipt should record legalHoldOverride: %+v", receipt.Detail)
	}
	if receipt.Detail["overrideAt"] != override.Detail["overrideAt"] {
		t.Errorf("receipt overrideAt %v diverges from event overrideAt %v", receipt.Detail["overrideAt"], override.Detail["overrideAt"])
	}
	if receipt.Detail["jobId"] != jobID {
		t.Errorf("override event jobId %q diverges from receipt jobId %v", jobID, receipt.Detail["jobId"])
	}
}

func TestEraseUserHoldOverrideRequiresJustification(t *testing.T) {
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{userEraser("sessions", 1)}})
	router, users, sessions, _ := newErasureAdminWithSessions(t, orch)
	seedUser(t, users, "acme", "bob@acme.com")
	seedSession(t, sessions, sessionstore.Session{
		ID: "sess_b", TenantID: "acme", UserID: "bob@acme.com", LegalHold: true,
	})

	rr := eraseUser(t, router.Handler(), "bob@acme.com", admin.EraseUserRequest{
		TenantID: "acme", AcknowledgeHoldOverride: true,
	}, withAdminPrincipal)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("override without justification: status %d, want 400; body %s", rr.Code, rr.Body.String())
	}
}

func TestEraseUserHoldOverrideRequiresPlatformAdmin(t *testing.T) {
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{userEraser("sessions", 1)}})
	router, users, sessions, _ := newErasureAdminWithSessions(t, orch)
	seedUser(t, users, "acme", "carol@acme.com")
	seedSession(t, sessions, sessionstore.Session{
		ID: "sess_c", TenantID: "acme", UserID: "carol@acme.com", LegalHold: true,
	})

	// A tenant-admin cannot self-override even with a justification.
	rr := eraseUser(t, router.Handler(), "carol@acme.com", admin.EraseUserRequest{
		AcknowledgeHoldOverride: true, Justification: "ticket-9",
	}, func(req *http.Request) *http.Request { return withTenantAdminFor(req, "acme") })
	if rr.Code != http.StatusForbidden {
		t.Errorf("tenant-admin override: status %d, want 403; body %s", rr.Code, rr.Body.String())
	}
}

// awaitUnrestricted polls the user store until the §12.8 processing
// restriction is lifted — the erase handler clears it in a background
// goroutine once the job completes.
func awaitUnrestricted(t *testing.T, users userstore.Store, tenant, subject string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		u, err := users.Get(context.Background(), tenant, subject)
		if err != nil {
			t.Fatalf("Get user: %v", err)
		}
		if !u.ProcessingRestricted {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("processing restriction was not cleared after the erasure job completed")
}

func TestEraseUserSetsProcessingRestriction(t *testing.T) {
	release := make(chan struct{})
	defer close(release) // never leave the background job blocked
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{{
		Name: "sessions",
		DeleteByUser: func(context.Context, string, string) (int, error) {
			<-release // hold the job in store_deleting
			return 1, nil
		},
	}}})
	router, users, _, _ := newErasureAdmin(t, orch)
	seedUser(t, users, "acme", "alice@acme.com")

	rr := eraseUser(t, router.Handler(), "alice@acme.com",
		admin.EraseUserRequest{TenantID: "acme"}, withAdminPrincipal)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("erase: status %d, body %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	jobID := resp["jobId"].(string)

	// The job is held in store_deleting; the restriction must be set.
	u, err := users.Get(context.Background(), "acme", "alice@acme.com")
	if err != nil {
		t.Fatalf("Get user: %v", err)
	}
	if !u.ProcessingRestricted {
		t.Error("erase initiation must set ProcessingRestricted on the user")
	}
	if u.ErasureJobID != jobID {
		t.Errorf("ErasureJobID = %q, want %q", u.ErasureJobID, jobID)
	}
}

func TestEraseUserClearsRestrictionOnCompletion(t *testing.T) {
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{userEraser("sessions", 1)}})
	router, users, _, _ := newErasureAdmin(t, orch)
	seedUser(t, users, "acme", "bob@acme.com")

	rr := eraseUser(t, router.Handler(), "bob@acme.com",
		admin.EraseUserRequest{TenantID: "acme"}, withAdminPrincipal)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("erase: status %d", rr.Code)
	}
	awaitUnrestricted(t, users, "acme", "bob@acme.com")
	u, _ := users.Get(context.Background(), "acme", "bob@acme.com")
	if u.ErasureJobID != "" {
		t.Errorf("ErasureJobID = %q, want empty after the job completed", u.ErasureJobID)
	}
}

func TestEraseUserKeepsRestrictionOnFailure(t *testing.T) {
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{
		{Name: "broken", DeleteByUser: func(context.Context, string, string) (int, error) {
			return 0, errors.New("store down")
		}},
	}})
	router, users, jobs, _ := newErasureAdmin(t, orch)
	seedUser(t, users, "acme", "carol@acme.com")

	rr := eraseUser(t, router.Handler(), "carol@acme.com",
		admin.EraseUserRequest{TenantID: "acme"}, withAdminPrincipal)
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	jobID := resp["jobId"].(string)
	job := awaitTerminal(t, jobs, jobID)
	if job.Phase != erasurejob.PhaseFailed {
		t.Fatalf("job phase = %q, want failed", job.Phase)
	}
	// §12.8: a failed erasure job leaves the restriction in place.
	u, _ := users.Get(context.Background(), "acme", "carol@acme.com")
	if !u.ProcessingRestricted {
		t.Error("a failed erasure job must leave ProcessingRestricted set for an operator to retry")
	}
}

func TestGetErasureJobTenantScoped(t *testing.T) {
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{userEraser("sessions", 1)}})
	router, users, jobs, _ := newErasureAdmin(t, orch)
	seedUser(t, users, "acme", "erin@acme.com")

	rr := eraseUser(t, router.Handler(), "erin@acme.com",
		admin.EraseUserRequest{TenantID: "acme"}, withAdminPrincipal)
	var initResp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &initResp)
	jobID := initResp["jobId"].(string)
	awaitTerminal(t, jobs, jobID)

	// A tenant-admin of a different tenant must not see the acme job.
	req := withTenantAdminFor(
		httptest.NewRequest(http.MethodGet, "/v1/admin/erasure-jobs/"+jobID, nil), "globex",
	)
	statusRR := httptest.NewRecorder()
	router.Handler().ServeHTTP(statusRR, req)
	if statusRR.Code != http.StatusNotFound {
		t.Errorf("cross-tenant job read: status %d, want 404 (no existence leak)", statusRR.Code)
	}
}
