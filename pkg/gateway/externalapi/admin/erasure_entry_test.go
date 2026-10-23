// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasure"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob"
)

// entryFixture is the admin router over a Memory job store, a session
// store for the legal-hold preflight, and a runner the test drives.
type entryFixture struct {
	router   *admin.Router
	users    *userstore.Memory
	jobs     *erasurejob.Memory
	sessions sessionstore.Store
	audit    *recordingAudit
}

func newEntryFixture(t *testing.T) *entryFixture {
	t.Helper()
	f := &entryFixture{
		users:    userstore.NewMemory(),
		sessions: memstore.New(),
		audit:    &recordingAudit{},
	}
	f.jobs = erasurejob.NewMemory(erasurejob.WithRestrictionWriter(f.users))
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{userEraser("sessions", 1)}})
	runner := newFixtureRunner(t, f.jobs, orch, f.audit).
		WithDeadlineResolver(func(context.Context, string) time.Duration { return time.Hour })
	f.router = admin.NewRouter(tenantstore.NewMemory(), admin.Options{Clock: erasureClock, Audit: f.audit}).
		WithUsers(f.users).WithErasure(runner, f.jobs).WithSessions(f.sessions)
	seedUser(t, f.users, "acme", "alice@acme.com")
	seedUser(t, f.users, "acme", "bob@acme.com")
	return f
}

// post sends a POST with body as the platform admin.
func (f *entryFixture) post(t *testing.T, path string, body any, as func(*http.Request) *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	f.router.Handler().ServeHTTP(rr, as(httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))))
	return rr
}

// get sends a GET as the given principal.
func (f *entryFixture) get(t *testing.T, path string, as func(*http.Request) *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	f.router.Handler().ServeHTTP(rr, as(httptest.NewRequest(http.MethodGet, path, nil)))
	return rr
}

// erase admits an erase for subject and returns the job id.
func (f *entryFixture) erase(t *testing.T, subject string) string {
	t.Helper()
	rr := f.post(t, "/v1/admin/users/"+subject+"/erase", admin.EraseUserRequest{TenantID: "acme"}, withAdminPrincipal)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("erase %s: %d %s", subject, rr.Code, rr.Body.String())
	}
	return decodeBody(t, rr)["jobId"].(string)
}

// failJob moves a job to failed, as a runner failure does.
func (f *entryFixture) failJob(t *testing.T, id string) {
	t.Helper()
	if _, err := f.jobs.Update(context.Background(), id, func(j *erasurejob.Job) error {
		j.Phase = erasurejob.PhaseFailed
		j.PhaseLog = append(j.PhaseLog, erasurejob.PhaseTransition{Phase: erasurejob.PhaseFailed, At: erasureClock()})
		j.Failure = "injected"
		return nil
	}); err != nil {
		t.Fatalf("fail job: %v", err)
	}
}

// hold puts a legal hold on a session of subject.
func (f *entryFixture) hold(t *testing.T, subject string) {
	t.Helper()
	seedSession(t, f.sessions, sessionstore.Session{
		ID: "sess_held_" + subject, TenantID: "acme", UserID: subject, LegalHold: true,
	})
}

// events returns the recorded audit events of type typ.
func (f *entryFixture) events(typ string) []admin.AuditEvent {
	var out []admin.AuditEvent
	for _, ev := range f.audit.snapshot() {
		if ev.Type == typ {
			out = append(out, ev)
		}
	}
	return out
}

func (f *entryFixture) flag(t *testing.T, subject string) (bool, string) {
	t.Helper()
	u, err := f.users.Get(context.Background(), "acme", subject)
	if err != nil {
		t.Fatalf("Get user: %v", err)
	}
	return u.ProcessingRestricted, u.ErasureJobID
}

func decodeBody(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode %s: %v", rr.Body.String(), err)
	}
	return m
}

// TestErasureEntryPoints_spec_12_8 is the table over the erase, retry, and
// clear entry points: for each case it asserts the job write or its
// absence, the job's status, the audit event and its jobId, the
// processing_restricted flag, and the HTTP response.
// spec: 12.8 (User-level erasure invocation), 12.8 (DeleteByUser dependency-ordered deletion sequence), 12.8 (Processing restriction during erasure (GDPR Article 18)), 15.1 (REST API)
func TestErasureEntryPoints_spec_12_8(t *testing.T) {
	override := admin.EraseUserRequest{TenantID: "acme", AcknowledgeHoldOverride: true, Justification: "court order"}

	t.Run("admitted erase persists the job", func(t *testing.T) {
		f := newEntryFixture(t)
		id := f.erase(t, "alice@acme.com")
		j, err := f.jobs.Get(context.Background(), erasurejob.AllTenants, id)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if j.Phase != erasurejob.PhaseInitiated || j.Deadline != time.Hour || j.InitiatedBy != "admin@acme.com" || j.OverrideReceipt != nil {
			t.Errorf("job = %+v, want initiated, 1h deadline, initiated by the admin, no override", j)
		}
		if r, jid := f.flag(t, "alice@acme.com"); !r || jid != id {
			t.Errorf("flag = %v naming %q, want set naming %s", r, jid, id)
		}
		if evs := f.events("admin.user.erasure_initiated"); len(evs) != 1 || evs[0].Detail["jobId"] != id {
			t.Errorf("erasure_initiated events = %+v", evs)
		}
	})
	t.Run("duplicate erase answers the in-progress job", func(t *testing.T) {
		f := newEntryFixture(t)
		id := f.erase(t, "alice@acme.com")
		rr := f.post(t, "/v1/admin/users/alice@acme.com/erase", admin.EraseUserRequest{TenantID: "acme"}, withAdminPrincipal)
		if rr.Code != http.StatusAccepted || decodeBody(t, rr)["jobId"] != id {
			t.Fatalf("duplicate erase = %d %s, want 202 with %s", rr.Code, rr.Body.String(), id)
		}
		if active, _ := f.jobs.ListActive(context.Background()); len(active) != 1 {
			t.Errorf("in-progress jobs = %d, want 1", len(active))
		}
		if evs := f.events("admin.user.erasure_initiated"); len(evs) != 1 {
			t.Errorf("erasure_initiated events = %d, want 1 (none for the duplicate)", len(evs))
		}
	})
	t.Run("duplicate erase with a hold is blocked", func(t *testing.T) {
		f := newEntryFixture(t)
		f.erase(t, "alice@acme.com")
		f.hold(t, "alice@acme.com")
		rr := f.post(t, "/v1/admin/users/alice@acme.com/erase", admin.EraseUserRequest{TenantID: "acme"}, withAdminPrincipal)
		if rr.Code != http.StatusConflict || errorCode(t, rr.Body.Bytes()) != "ERASURE_BLOCKED_BY_LEGAL_HOLD" {
			t.Fatalf("duplicate held erase = %d %s, want 409 ERASURE_BLOCKED_BY_LEGAL_HOLD", rr.Code, rr.Body.String())
		}
		evs := f.events("gdpr.erasure_blocked_by_hold")
		if len(evs) != 1 {
			t.Fatalf("blocked events = %d, want 1", len(evs))
		}
		if _, ok := evs[0].Detail["jobId"]; ok {
			t.Error("the erase path's blocked event carries a jobId")
		}
	})
	t.Run("duplicate erase admitted only by override is refused", func(t *testing.T) {
		f := newEntryFixture(t)
		f.erase(t, "alice@acme.com")
		f.hold(t, "alice@acme.com")
		rr := f.post(t, "/v1/admin/users/alice@acme.com/erase", override, withAdminPrincipal)
		if rr.Code != http.StatusConflict || errorCode(t, rr.Body.Bytes()) != "INVALID_STATE_TRANSITION" {
			t.Fatalf("duplicate override erase = %d %s, want 409 INVALID_STATE_TRANSITION", rr.Code, rr.Body.String())
		}
		if evs := f.events("gdpr.legal_hold_overridden"); len(evs) != 0 {
			t.Errorf("override events = %d, want 0", len(evs))
		}
	})
	t.Run("override erase persists the override", func(t *testing.T) {
		f := newEntryFixture(t)
		f.hold(t, "alice@acme.com")
		rr := f.post(t, "/v1/admin/users/alice@acme.com/erase", override, withAdminPrincipal)
		if rr.Code != http.StatusAccepted {
			t.Fatalf("override erase = %d %s", rr.Code, rr.Body.String())
		}
		id := decodeBody(t, rr)["jobId"].(string)
		j, _ := f.jobs.Get(context.Background(), erasurejob.AllTenants, id)
		if j.OverrideReceipt["legalHoldOverride"] != true || j.OverrideReceipt["holds"] == nil {
			t.Errorf("OverrideReceipt = %v, want the override fields and holds", j.OverrideReceipt)
		}
		if evs := f.events("gdpr.legal_hold_overridden"); len(evs) != 1 || evs[0].Detail["jobId"] != id {
			t.Errorf("override events = %+v, want one naming %s", evs, id)
		}
	})
	t.Run("unregistered user", func(t *testing.T) {
		f := newEntryFixture(t)
		rr := f.post(t, "/v1/admin/users/dave@acme.com/erase", admin.EraseUserRequest{TenantID: "acme"}, withAdminPrincipal)
		if rr.Code != http.StatusNotFound {
			t.Errorf("erase of an unregistered user = %d, want 404", rr.Code)
		}
	})
	t.Run("retry blocked by a hold", func(t *testing.T) {
		f := newEntryFixture(t)
		id := f.erase(t, "alice@acme.com")
		f.failJob(t, id)
		if _, err := f.jobs.ClearRestriction(context.Background(), id); err != nil {
			t.Fatalf("ClearRestriction: %v", err)
		}
		f.hold(t, "alice@acme.com")
		rr := f.post(t, "/v1/admin/erasure-jobs/"+id+"/retry", nil, withAdminPrincipal)
		if rr.Code != http.StatusConflict || errorCode(t, rr.Body.Bytes()) != "ERASURE_BLOCKED_BY_LEGAL_HOLD" {
			t.Fatalf("held retry = %d %s, want 409 ERASURE_BLOCKED_BY_LEGAL_HOLD", rr.Code, rr.Body.String())
		}
		if evs := f.events("gdpr.erasure_blocked_by_hold"); len(evs) != 1 || evs[0].Detail["jobId"] != id {
			t.Errorf("blocked events = %+v, want one naming %s", evs, id)
		}
		if j, _ := f.jobs.Get(context.Background(), erasurejob.AllTenants, id); j.Phase != erasurejob.PhaseFailed {
			t.Errorf("phase = %s, want failed", j.Phase)
		}
		if r, _ := f.flag(t, "alice@acme.com"); r {
			t.Error("a blocked retry set the cleared flag")
		}
	})
	t.Run("retry ignores the job's own override", func(t *testing.T) {
		f := newEntryFixture(t)
		f.hold(t, "alice@acme.com")
		rr := f.post(t, "/v1/admin/users/alice@acme.com/erase", override, withAdminPrincipal)
		id := decodeBody(t, rr)["jobId"].(string)
		f.failJob(t, id)
		rr = f.post(t, "/v1/admin/erasure-jobs/"+id+"/retry", nil, withAdminPrincipal)
		if rr.Code != http.StatusConflict || errorCode(t, rr.Body.Bytes()) != "ERASURE_BLOCKED_BY_LEGAL_HOLD" {
			t.Fatalf("retry with a still-active overridden hold = %d %s, want blocked", rr.Code, rr.Body.String())
		}
	})
	t.Run("retry while another job is in progress", func(t *testing.T) {
		f := newEntryFixture(t)
		id := f.erase(t, "alice@acme.com")
		f.failJob(t, id)
		f.erase(t, "alice@acme.com")
		rr := f.post(t, "/v1/admin/erasure-jobs/"+id+"/retry", nil, withAdminPrincipal)
		if rr.Code != http.StatusConflict || errorCode(t, rr.Body.Bytes()) != "INVALID_STATE_TRANSITION" {
			t.Fatalf("retry = %d %s, want 409 INVALID_STATE_TRANSITION", rr.Code, rr.Body.String())
		}
	})
	t.Run("admitted retry", func(t *testing.T) {
		f := newEntryFixture(t)
		id := f.erase(t, "alice@acme.com")
		f.failJob(t, id)
		if _, err := f.jobs.ClearRestriction(context.Background(), id); err != nil {
			t.Fatalf("ClearRestriction: %v", err)
		}
		rr := f.post(t, "/v1/admin/erasure-jobs/"+id+"/retry", nil, withAdminPrincipal)
		if rr.Code != http.StatusAccepted || decodeBody(t, rr)["phase"] != "initiated" {
			t.Fatalf("retry = %d %s, want 202 initiated", rr.Code, rr.Body.String())
		}
		if r, jid := f.flag(t, "alice@acme.com"); !r || jid != id {
			t.Errorf("flag = %v naming %q, want set naming %s", r, jid, id)
		}
		if evs := f.events("gdpr.erasure_job_retried"); len(evs) != 1 {
			t.Errorf("retried events = %d, want 1", len(evs))
		}
	})
	t.Run("clear of a job that is not failed", func(t *testing.T) {
		f := newEntryFixture(t)
		id := f.erase(t, "alice@acme.com")
		rr := f.post(t, "/v1/admin/erasure-jobs/"+id+"/clear-processing-restriction",
			map[string]string{"justification": "operator"}, withAdminPrincipal)
		if rr.Code != http.StatusConflict || errorCode(t, rr.Body.Bytes()) != "ERASURE_JOB_NOT_FAILED" {
			t.Fatalf("clear = %d %s, want 409 ERASURE_JOB_NOT_FAILED", rr.Code, rr.Body.String())
		}
		if r, _ := f.flag(t, "alice@acme.com"); !r {
			t.Error("a refused clear lifted the flag")
		}
	})
}

// TestErasureHandlersCrossTenantRead_spec_12_3 pins the cross_tenant_read
// events of the erasure-job handlers: the platform-admin status query, the
// retry, and the clear each record exactly one event with the route
// pattern, the caller, and a row count of 1 (0 for an unknown job); the
// tenant-admin status query records none; an emit error answers 500 before
// any write; a nil recorder emits nothing.
// spec: 12.3 (Postgres HA Requirements), 12.8 (User-level erasure invocation)
func TestErasureHandlersCrossTenantRead_spec_12_3(t *testing.T) {
	type event struct {
		endpoint, caller string
		rows             int
	}
	setup := func(t *testing.T, emitErr error) (*entryFixture, *[]event) {
		f := newEntryFixture(t)
		var events []event
		f.router.WithErasureCrossTenantReadAudit(func(_ context.Context, endpoint, caller string, rows int) error {
			events = append(events, event{endpoint, caller, rows})
			return emitErr
		})
		return f, &events
	}
	t.Run("one event per sentinel read", func(t *testing.T) {
		f, events := setup(t, nil)
		id := f.erase(t, "alice@acme.com")
		f.get(t, "/v1/admin/erasure-jobs/"+id, withAdminPrincipal)
		f.get(t, "/v1/admin/erasure-jobs/erasure_unknown", withAdminPrincipal)
		f.get(t, "/v1/admin/erasure-jobs/"+id, withTenantAdminPrincipal)
		f.failJob(t, id)
		f.post(t, "/v1/admin/erasure-jobs/"+id+"/clear-processing-restriction",
			map[string]string{"justification": "operator"}, withAdminPrincipal)
		f.post(t, "/v1/admin/erasure-jobs/"+id+"/retry", nil, withAdminPrincipal)
		want := []event{
			{"GET /v1/admin/erasure-jobs/{jobId}", "admin@acme.com", 1},
			{"GET /v1/admin/erasure-jobs/{jobId}", "admin@acme.com", 0},
			{"POST /v1/admin/erasure-jobs/{jobId}/clear-processing-restriction", "admin@acme.com", 1},
			{"POST /v1/admin/erasure-jobs/{jobId}/retry", "admin@acme.com", 1},
		}
		if len(*events) != len(want) {
			t.Fatalf("events = %+v, want %+v", *events, want)
		}
		for i := range want {
			if (*events)[i] != want[i] {
				t.Errorf("event %d = %+v, want %+v", i, (*events)[i], want[i])
			}
		}
	})
	t.Run("emit error fails closed", func(t *testing.T) {
		f, _ := setup(t, errors.New("audit down"))
		id := f.erase(t, "alice@acme.com")
		if rr := f.get(t, "/v1/admin/erasure-jobs/"+id, withAdminPrincipal); rr.Code != http.StatusInternalServerError {
			t.Errorf("status query = %d, want 500", rr.Code)
		}
		f.failJob(t, id)
		if rr := f.post(t, "/v1/admin/erasure-jobs/"+id+"/clear-processing-restriction",
			map[string]string{"justification": "operator"}, withAdminPrincipal); rr.Code != http.StatusInternalServerError {
			t.Errorf("clear = %d, want 500", rr.Code)
		}
		if r, _ := f.flag(t, "alice@acme.com"); !r {
			t.Error("a clear whose audit failed lifted the flag")
		}
		if rr := f.post(t, "/v1/admin/erasure-jobs/"+id+"/retry", nil, withAdminPrincipal); rr.Code != http.StatusInternalServerError {
			t.Errorf("retry = %d, want 500", rr.Code)
		}
		if j, _ := f.jobs.Get(context.Background(), erasurejob.AllTenants, id); j.Phase != erasurejob.PhaseFailed {
			t.Errorf("phase = %s, want failed (no write)", j.Phase)
		}
		if rr := f.get(t, "/v1/admin/erasure-jobs/"+id, withTenantAdminPrincipal); rr.Code != http.StatusOK {
			t.Errorf("tenant-admin status query = %d, want 200 (no sentinel read)", rr.Code)
		}
	})
	t.Run("nil recorder", func(t *testing.T) {
		f := newEntryFixture(t)
		id := f.erase(t, "alice@acme.com")
		if rr := f.get(t, "/v1/admin/erasure-jobs/"+id, withAdminPrincipal); rr.Code != http.StatusOK {
			t.Errorf("status query = %d, want 200", rr.Code)
		}
	})
}

// recordingLister records the filter of every List call.
type recordingLister struct {
	filters []sessionstore.ListFilter
	err     error
}

func (l *recordingLister) List(_ context.Context, _ string, f sessionstore.ListFilter) ([]sessionstore.Session, error) {
	l.filters = append(l.filters, f)
	return nil, l.err
}

// TestHeldResourcesForUserListsPrimary_spec_12_8 pins that the legal-hold
// enumeration lists only the user's sessions, from the primary, and
// returns a listing error.
// spec: 12.8 (DeleteByUser dependency-ordered deletion sequence), 12.8 (Lifecycle fence and drain)
func TestHeldResourcesForUserListsPrimary_spec_12_8(t *testing.T) {
	l := &recordingLister{}
	if _, err := admin.HeldResourcesForUser(context.Background(), l, nil, "acme", "alice"); err != nil {
		t.Fatalf("HeldResourcesForUser: %v", err)
	}
	if len(l.filters) != 1 || !reflect.DeepEqual(l.filters[0], sessionstore.ListFilter{UserID: "alice", Primary: true}) {
		t.Errorf("filters = %+v, want one {UserID: alice, Primary: true}", l.filters)
	}
	l.err = errors.New("primary down")
	if _, err := admin.HeldResourcesForUser(context.Background(), l, nil, "acme", "alice"); !errors.Is(err, l.err) {
		t.Errorf("listing error = %v, want the lister's error", err)
	}
}
