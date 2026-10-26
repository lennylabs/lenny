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
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// spec: §15.1 — platform-admin tenant suspend/resume.
// F-15.1.3.

var suspendClock = time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)

func newSuspendRouter(store *tenantstore.Memory, sink admin.AuditSink) *admin.Router {
	return admin.NewRouter(store, admin.Options{
		Clock: func() time.Time { return suspendClock },
		Audit: sink,
	})
}

func seedSuspendTenant(t *testing.T, store *tenantstore.Memory, tn tenantstore.Tenant) {
	t.Helper()
	if tn.DisplayName == "" {
		tn.DisplayName = tn.ID
	}
	if err := store.Create(context.Background(), tn); err != nil {
		t.Fatalf("seed tenant %q: %v", tn.ID, err)
	}
}

func suspendErrorCode(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v (body %s)", err, rr.Body.String())
	}
	return env.Error.Code
}

func TestSuspendTenant_Success_spec_15_1_818(t *testing.T) {
	store := tenantstore.NewMemory()
	seedSuspendTenant(t, store, tenantstore.Tenant{ID: "acme"})
	sink := &recordingSink{}
	h := newSuspendRouter(store, sink).Handler()

	body := bytes.NewBufferString(`{"reason":"abuse investigation"}`)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants/acme/suspend", body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
	var out tenantSuspendView
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.Suspended || out.Reason != "abuse investigation" {
		t.Errorf("payload = %+v, want suspended with reason", out)
	}
	if out.SuspendedBy != "admin@acme.com" {
		t.Errorf("suspendedBy = %q, want admin@acme.com", out.SuspendedBy)
	}
	if out.SuspendedAt == "" {
		t.Error("suspendedAt is empty")
	}

	got, _ := store.Get(context.Background(), "acme")
	if !got.Suspended || got.SuspendedReason != "abuse investigation" || got.SuspendedBy != "admin@acme.com" {
		t.Errorf("stored row = %+v, want suspended with reason and operator", got)
	}
	if got.SuspendedAt != suspendClock {
		t.Errorf("stored suspendedAt = %v, want %v", got.SuspendedAt, suspendClock)
	}
	if !sink.has("tenant.suspended") {
		t.Errorf("no tenant.suspended audit event; got %+v", sink.events)
	}
}

// A repeat suspend of an already-suspended tenant leaves the stored
// marker untouched and still writes one audit row recording the repeat.
// spec: §15.1 (REST API), §11.8
func TestSuspendTenant_RepeatKeepsMarkerAndAudits_spec_15_1(t *testing.T) {
	store := tenantstore.NewMemory()
	seedSuspendTenant(t, store, tenantstore.Tenant{ID: "acme", Suspended: true, SuspendedReason: "prior"})
	sink := &recordingSink{}
	h := newSuspendRouter(store, sink).Handler()

	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants/acme/suspend",
		bytes.NewBufferString(`{"reason":"second"}`)))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
	if len(sink.events) != 1 || sink.events[0].Type != "tenant.suspended" {
		t.Fatalf("repeat suspend emitted %+v, want one tenant.suspended", sink.events)
	}
	if got := sink.events[0].Detail["already_suspended"]; got != true {
		t.Errorf("already_suspended = %v, want true", got)
	}
	got, _ := store.Get(context.Background(), "acme")
	if got.SuspendedReason != "prior" {
		t.Errorf("stored reason = %q, want the first suspend's reason kept", got.SuspendedReason)
	}
}

func TestSuspendTenant_NotFound_spec_15_1_818(t *testing.T) {
	store := tenantstore.NewMemory()
	h := newSuspendRouter(store, &recordingSink{}).Handler()

	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants/ghost/suspend", nil))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rr.Code)
	}
	if code := suspendErrorCode(t, rr); code != "RESOURCE_NOT_FOUND" {
		t.Errorf("code = %q, want RESOURCE_NOT_FOUND", code)
	}
}

func TestSuspendTenant_DeletedConflict_spec_15_1_818(t *testing.T) {
	store := tenantstore.NewMemory()
	seedSuspendTenant(t, store, tenantstore.Tenant{ID: "acme", State: tenantstore.TenantStateDeleted})
	h := newSuspendRouter(store, &recordingSink{}).Handler()

	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants/acme/suspend", nil))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409", rr.Code)
	}
	if code := suspendErrorCode(t, rr); code != "INVALID_STATE_TRANSITION" {
		t.Errorf("code = %q, want INVALID_STATE_TRANSITION", code)
	}
}

func TestSuspendTenant_MalformedBody_spec_15_1_818(t *testing.T) {
	store := tenantstore.NewMemory()
	seedSuspendTenant(t, store, tenantstore.Tenant{ID: "acme"})
	h := newSuspendRouter(store, &recordingSink{}).Handler()

	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants/acme/suspend",
		bytes.NewBufferString("{not json")))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rr.Code)
	}
	got, _ := store.Get(context.Background(), "acme")
	if got.Suspended {
		t.Error("malformed body must not suspend the tenant")
	}
}

func TestSuspendTenant_RequiresPlatformAdmin_spec_15_1_818(t *testing.T) {
	store := tenantstore.NewMemory()
	seedSuspendTenant(t, store, tenantstore.Tenant{ID: "acme"})
	h := newSuspendRouter(store, &recordingSink{}).Handler()

	req := withTenantAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants/acme/suspend", nil))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", rr.Code)
	}
}

func TestSuspendTenant_DrainsActiveSessions_spec_15_1_818(t *testing.T) {
	store := tenantstore.NewMemory()
	seedSuspendTenant(t, store, tenantstore.Tenant{ID: "acme"})

	sessions := memstore.New()
	rows := map[string]sessionstore.Session{
		"sess_run1": {ID: "sess_run1", TenantID: "acme", State: session.StateRunning},
		"sess_run2": {ID: "sess_run2", TenantID: "acme", State: session.StateInputRequired},
		"sess_term": {ID: "sess_term", TenantID: "acme", State: session.StateCompleted},
		"sess_glob": {ID: "sess_glob", TenantID: "globex", State: session.StateRunning},
	}
	for _, s := range rows {
		if err := sessions.Create(context.Background(), s); err != nil {
			t.Fatalf("seed session %q: %v", s.ID, err)
		}
	}
	sa := &fakeSessionAdmin{rows: rows}
	sink := &recordingSink{}
	h := newSuspendRouter(store, sink).WithSessions(sessions).WithSessionAdmin(sa).Handler()

	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants/acme/suspend", nil))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
	var out tenantSuspendView
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out.DrainedSessions != 2 {
		t.Errorf("drainedSessions = %d, want 2 (terminal skipped, foreign untouched)", out.DrainedSessions)
	}
	// The foreign-tenant session must be untouched.
	for _, id := range sa.terminated {
		if id == "sess_glob" {
			t.Error("drained a session belonging to another tenant")
		}
	}
	if g := sa.rows["sess_glob"]; g.State != session.StateRunning {
		t.Errorf("globex session state = %q, want running", g.State)
	}
}

func TestResumeTenant_Success_spec_15_1_819(t *testing.T) {
	store := tenantstore.NewMemory()
	seedSuspendTenant(t, store, tenantstore.Tenant{
		ID: "acme", Suspended: true, SuspendedReason: "abuse", SuspendedBy: "admin@acme.com", SuspendedAt: suspendClock,
	})
	sink := &recordingSink{}
	h := newSuspendRouter(store, sink).Handler()

	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants/acme/resume", nil))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
	got, _ := store.Get(context.Background(), "acme")
	if got.Suspended || got.SuspendedReason != "" || got.SuspendedBy != "" || !got.SuspendedAt.IsZero() {
		t.Errorf("resumed row still carries suspension metadata: %+v", got)
	}
	if !sink.has("tenant.resumed") {
		t.Errorf("no tenant.resumed audit event; got %+v", sink.events)
	}
}

func TestResumeTenant_IdempotentNotSuspended_spec_15_1_819(t *testing.T) {
	store := tenantstore.NewMemory()
	seedSuspendTenant(t, store, tenantstore.Tenant{ID: "acme"})
	sink := &recordingSink{}
	h := newSuspendRouter(store, sink).Handler()

	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants/acme/resume", nil))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
	if len(sink.events) != 0 {
		t.Errorf("idempotent resume emitted %d events, want 0", len(sink.events))
	}
}

func TestResumeTenant_NotFound_spec_15_1_819(t *testing.T) {
	store := tenantstore.NewMemory()
	h := newSuspendRouter(store, &recordingSink{}).Handler()

	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants/ghost/resume", nil))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rr.Code)
	}
}

// Resume restores normal operation but does not un-terminate the
// sessions drained by the suspension. spec: §15.1.
func TestResumeTenant_DoesNotTouchSessions_spec_15_1_819(t *testing.T) {
	store := tenantstore.NewMemory()
	seedSuspendTenant(t, store, tenantstore.Tenant{ID: "acme", Suspended: true})

	sessions := memstore.New()
	_ = sessions.Create(context.Background(), sessionstore.Session{
		ID: "sess_run", TenantID: "acme", State: session.StateRunning,
	})
	sa := &fakeSessionAdmin{rows: map[string]sessionstore.Session{
		"sess_run": {ID: "sess_run", TenantID: "acme", State: session.StateRunning},
	}}
	h := newSuspendRouter(store, &recordingSink{}).WithSessions(sessions).WithSessionAdmin(sa).Handler()

	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants/acme/resume", nil))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	if len(sa.terminated) != 0 {
		t.Errorf("resume force-terminated %v, want none", sa.terminated)
	}
}

// tenantSuspendView mirrors the suspend response payload for decoding.
type tenantSuspendView struct {
	TenantID        string `json:"tenantId"`
	Suspended       bool   `json:"suspended"`
	Reason          string `json:"reason"`
	SuspendedAt     string `json:"suspendedAt"`
	SuspendedBy     string `json:"suspendedBy"`
	DrainedSessions int    `json:"drainedSessions"`
	// FailedSessions is a pointer so a test can tell an absent or null
	// field from an empty array.
	FailedSessions *[]string `json:"failedSessions"`
}

// suspendTrace is the ordered log one suspend request leaves across the
// tenant store, the audit sink, and the drain's seams, so a test can
// assert that the marker commits, then the audit row is written, then
// the drain runs.
type suspendTrace struct {
	mu    sync.Mutex
	steps []string
}

func (s *suspendTrace) add(step string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.steps = append(s.steps, step)
}

func (s *suspendTrace) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.steps...)
}

// writeCountingTenants counts the Update calls that wrote a row. A
// mutate that returns an error writes nothing and is not counted.
// afterWrite, when set, runs once the write has committed.
type writeCountingTenants struct {
	*tenantstore.Memory
	trace      *suspendTrace
	afterWrite func()
	mu         sync.Mutex
	writes     int
}

func (c *writeCountingTenants) Update(ctx context.Context, id string, mutate func(*tenantstore.Tenant) error) (tenantstore.Tenant, error) {
	row, err := c.Memory.Update(ctx, id, mutate)
	if err != nil {
		return row, err
	}
	c.mu.Lock()
	c.writes++
	c.mu.Unlock()
	c.trace.add("marker")
	if c.afterWrite != nil {
		c.afterWrite()
	}
	return row, nil
}

func (c *writeCountingTenants) writeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writes
}

// tracingSink records each audit event, the trace step, and whether the
// emit context had already ended.
type tracingSink struct {
	trace    *suspendTrace
	mu       sync.Mutex
	events   []admin.AuditEvent
	ctxEnded []bool
}

func (s *tracingSink) EmitAdminEvent(ctx context.Context, e admin.AuditEvent) {
	s.mu.Lock()
	s.events = append(s.events, e)
	s.ctxEnded = append(s.ctxEnded, ctx.Err() != nil)
	s.mu.Unlock()
	s.trace.add("audit")
}

// scriptedSessions answers the drain's listings from a script: answer i
// serves the i-th List call, and errs[i] fails it. Only List is used by
// the suspend handler, so the embedded Store stays nil.
type scriptedSessions struct {
	sessionstore.Store
	trace   *suspendTrace
	answers [][]sessionstore.Session
	errs    []error
	mu      sync.Mutex
	filters []sessionstore.ListFilter
}

func (l *scriptedSessions) List(_ context.Context, _ string, f sessionstore.ListFilter) ([]sessionstore.Session, error) {
	l.mu.Lock()
	i := len(l.filters)
	l.filters = append(l.filters, f)
	l.mu.Unlock()
	l.trace.add("list")
	if i < len(l.errs) && l.errs[i] != nil {
		return nil, l.errs[i]
	}
	if i < len(l.answers) {
		return l.answers[i], nil
	}
	return nil, nil
}

// scriptedTerminator applies the forced transition by answering from a
// per-id error table, records each call in the trace, and runs onCall
// with the context the drain passed.
type scriptedTerminator struct {
	trace  *suspendTrace
	errs   map[string]error
	onCall func(ctx context.Context, id string)
	mu     sync.Mutex
	ctxs   []bool
}

func (d *scriptedTerminator) GetByID(context.Context, string) (sessionstore.Session, error) {
	return sessionstore.Session{}, sessionstore.ErrNotFound
}

func (d *scriptedTerminator) ForceTerminate(ctx context.Context, tenantID, id string) (sessionstore.Session, session.State, bool, error) {
	d.trace.add("terminate:" + id)
	if d.onCall != nil {
		d.onCall(ctx, id)
	}
	d.mu.Lock()
	d.ctxs = append(d.ctxs, ctx.Err() != nil)
	d.mu.Unlock()
	if err := d.errs[id]; err != nil {
		return sessionstore.Session{}, "", false, err
	}
	return sessionstore.Session{ID: id, TenantID: tenantID, State: session.StateFailed}, session.StateRunning, true, nil
}

func liveRow(id string) sessionstore.Session {
	return sessionstore.Session{ID: id, TenantID: "acme", State: session.StateRunning}
}

// suspendFixture wires one router over the tracing seams.
type suspendFixture struct {
	trace    *suspendTrace
	tenants  *writeCountingTenants
	sink     *tracingSink
	sessions *scriptedSessions
	term     *scriptedTerminator
	handler  http.Handler
}

func newSuspendFixture(t *testing.T, seed tenantstore.Tenant, answers [][]sessionstore.Session, listErrs []error) *suspendFixture {
	t.Helper()
	tr := &suspendTrace{}
	mem := tenantstore.NewMemory()
	seedSuspendTenant(t, mem, seed)
	f := &suspendFixture{
		trace:    tr,
		tenants:  &writeCountingTenants{Memory: mem, trace: tr},
		sink:     &tracingSink{trace: tr},
		sessions: &scriptedSessions{trace: tr, answers: answers, errs: listErrs},
		term:     &scriptedTerminator{trace: tr, errs: map[string]error{}},
	}
	f.handler = admin.NewRouter(f.tenants, admin.Options{
		Clock: func() time.Time { return suspendClock },
		Audit: f.sink,
	}).WithSessions(f.sessions).WithSessionAdmin(f.term).Handler()
	return f
}

func (f *suspendFixture) suspend(ctx context.Context) *httptest.ResponseRecorder {
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants/acme/suspend",
		bytes.NewBufferString(`{"reason":"abuse"}`)))
	req = req.WithContext(withPrincipalFrom(req.Context(), ctx))
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req)
	return rr
}

// withPrincipalFrom moves the principal of src onto base, so a test can
// hand the handler a cancellable context that still carries the admin
// principal.
func withPrincipalFrom(src, base context.Context) context.Context {
	p, _ := authmw.FromContext(src)
	return authmw.WithPrincipal(base, p)
}

func decodeSuspendView(t *testing.T, rr *httptest.ResponseRecorder) tenantSuspendView {
	t.Helper()
	var out tenantSuspendView
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rr.Body.String())
	}
	return out
}

func assertTrace(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("trace = %v, want %v", got, want)
	}
}

// A first suspend commits the marker once, writes one tenant.suspended
// row with already_suspended=false before the drain, and answers 200
// with the drain's count and an empty failedSessions.
// spec: §15.1 (REST API), §12.8 (Lifecycle fence and drain), §11.8
func TestSuspendTenant_FirstSuspendMarkerAuditThenDrain_spec_15_1(t *testing.T) {
	f := newSuspendFixture(t, tenantstore.Tenant{ID: "acme"},
		[][]sessionstore.Session{{liveRow("s1"), liveRow("s2")}, nil}, nil)

	rr := f.suspend(context.Background())
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
	if n := f.tenants.writeCount(); n != 1 {
		t.Errorf("marker writes = %d, want 1", n)
	}
	assertTrace(t, f.trace.snapshot(), []string{"marker", "audit", "list", "terminate:s1", "terminate:s2", "list"})
	if len(f.sink.events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(f.sink.events))
	}
	d := f.sink.events[0].Detail
	if d["already_suspended"] != false || d["tenant_id"] != "acme" || d["reason"] != "abuse" {
		t.Errorf("detail = %v, want tenant_id acme, reason abuse, already_suspended false", d)
	}
	if _, ok := d["drained_sessions"]; ok {
		t.Errorf("detail carries a session count: %v", d)
	}
	out := decodeSuspendView(t, rr)
	if !out.Suspended || out.DrainedSessions != 2 {
		t.Errorf("payload = %+v, want suspended with drainedSessions 2", out)
	}
	if out.FailedSessions == nil || len(*out.FailedSessions) != 0 {
		t.Errorf("failedSessions = %v, want []", out.FailedSessions)
	}
}

// A repeat suspend writes no marker, audits already_suspended=true
// before the drain, runs the drain again, and reports its count. The
// repeat is the retry path for an incomplete earlier drain.
// spec: §15.1 (REST API), §12.8 (Lifecycle fence and drain), §11.8
func TestSuspendTenant_RepeatRedrainsAndAudits_spec_15_1(t *testing.T) {
	f := newSuspendFixture(t,
		tenantstore.Tenant{ID: "acme", Suspended: true, SuspendedReason: "prior", SuspendedBy: "bob@acme.com", SuspendedAt: suspendClock},
		[][]sessionstore.Session{{liveRow("s_late")}, nil}, nil)

	rr := f.suspend(context.Background())
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
	if n := f.tenants.writeCount(); n != 0 {
		t.Errorf("marker writes = %d, want 0", n)
	}
	assertTrace(t, f.trace.snapshot(), []string{"audit", "list", "terminate:s_late", "list"})
	if len(f.sink.events) != 1 || f.sink.events[0].Detail["already_suspended"] != true {
		t.Fatalf("audit = %+v, want one row with already_suspended true", f.sink.events)
	}
	out := decodeSuspendView(t, rr)
	if out.DrainedSessions != 1 {
		t.Errorf("drainedSessions = %d, want 1", out.DrainedSessions)
	}
	if !out.Suspended || out.Reason != "prior" || out.SuspendedBy != "bob@acme.com" {
		t.Errorf("payload = %+v, want the locked row's marker", out)
	}
}

// An incomplete drain answers 200 and lists the failed transitions
// followed by the sessions the verifying listing still found live.
// spec: §15.1 (REST API), §12.8 (Lifecycle fence and drain)
func TestSuspendTenant_IncompleteDrainReportsFailedSessions_spec_15_1(t *testing.T) {
	f := newSuspendFixture(t, tenantstore.Tenant{ID: "acme"},
		[][]sessionstore.Session{
			{liveRow("s_ok"), liveRow("s_bad")},
			{liveRow("s_bad"), liveRow("s_new")},
		}, nil)
	f.term.errs["s_bad"] = errors.New("pod release refused")

	rr := f.suspend(context.Background())
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
	out := decodeSuspendView(t, rr)
	if out.DrainedSessions != 1 {
		t.Errorf("drainedSessions = %d, want 1", out.DrainedSessions)
	}
	if out.FailedSessions == nil || strings.Join(*out.FailedSessions, ",") != "s_bad,s_new" {
		t.Errorf("failedSessions = %v, want [s_bad s_new]", out.FailedSessions)
	}
}

// A drain that cannot list the tenant's sessions answers 500
// INTERNAL_ERROR with the drained count, and the marker stays set.
// spec: §15.1 (REST API), §12.8 (Lifecycle fence and drain)
func TestSuspendTenant_ListingFailureAnswers500MarkerSet_spec_15_1(t *testing.T) {
	cases := map[string][]error{
		"first listing":     {errors.New("primary unreachable")},
		"verifying listing": {nil, errors.New("primary unreachable")},
	}
	for name, errs := range cases {
		t.Run(name, func(t *testing.T) {
			f := newSuspendFixture(t, tenantstore.Tenant{ID: "acme"},
				[][]sessionstore.Session{{liveRow("s1")}}, errs)

			rr := f.suspend(context.Background())
			if rr.Code != http.StatusInternalServerError {
				t.Fatalf("status %d, want 500 (body %s)", rr.Code, rr.Body.String())
			}
			if code := suspendErrorCode(t, rr); code != "INTERNAL_ERROR" {
				t.Errorf("code = %q, want INTERNAL_ERROR", code)
			}
			var env struct {
				Error struct {
					Details map[string]any `json:"details"`
				} `json:"error"`
			}
			_ = json.Unmarshal(rr.Body.Bytes(), &env)
			wantDrained := float64(0)
			if name == "verifying listing" {
				wantDrained = 1
			}
			if got := env.Error.Details["drainedSessions"]; got != wantDrained {
				t.Errorf("details.drainedSessions = %v, want %v", got, wantDrained)
			}
			got, _ := f.tenants.Get(context.Background(), "acme")
			if !got.Suspended {
				t.Error("listing failure cleared the suspension marker")
			}
			if len(f.sink.events) != 1 {
				t.Errorf("audit events = %d, want 1", len(f.sink.events))
			}
		})
	}
}

// A request context that ends after the marker commits, or during the
// drain, still yields the audit row on a live context and a drain over
// every listed session: both run on a context detached from request
// cancellation.
// spec: §15.1 (REST API), §12.8 (Lifecycle fence and drain), §11.8
func TestSuspendTenant_CancelledRequestStillAuditsAndDrains_spec_15_1(t *testing.T) {
	t.Run("cancelled after the marker commits", func(t *testing.T) {
		f := newSuspendFixture(t, tenantstore.Tenant{ID: "acme"},
			[][]sessionstore.Session{{liveRow("s1"), liveRow("s2")}, nil}, nil)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		f.tenants.afterWrite = cancel

		rr := f.suspend(ctx)
		if rr.Code != http.StatusOK {
			t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
		}
		if len(f.sink.ctxEnded) != 1 || f.sink.ctxEnded[0] {
			t.Errorf("audit emit context ended = %v, want one emit on a live context", f.sink.ctxEnded)
		}
		if out := decodeSuspendView(t, rr); out.DrainedSessions != 2 {
			t.Errorf("drainedSessions = %d, want 2", out.DrainedSessions)
		}
	})
	t.Run("cancelled during the first transition", func(t *testing.T) {
		f := newSuspendFixture(t, tenantstore.Tenant{ID: "acme"},
			[][]sessionstore.Session{{liveRow("s1"), liveRow("s2"), liveRow("s3")}, nil}, nil)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		f.term.onCall = func(context.Context, string) { cancel() }

		rr := f.suspend(ctx)
		if rr.Code != http.StatusOK {
			t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
		}
		assertTrace(t, f.trace.snapshot(),
			[]string{"marker", "audit", "list", "terminate:s1", "terminate:s2", "terminate:s3", "list"})
		out := decodeSuspendView(t, rr)
		if out.DrainedSessions != 3 || out.FailedSessions == nil || len(*out.FailedSessions) != 0 {
			t.Errorf("payload = %+v, want 3 drained and no failures", out)
		}
		for i, ended := range f.term.ctxs {
			if ended {
				t.Errorf("transition %d ran on an ended context", i)
			}
		}
	})
}

// With no session seams wired, a suspend answers 200 with no drained
// sessions and an empty failedSessions.
// spec: §15.1 (REST API)
func TestSuspendTenant_UnwiredSeamsEmptyFailedSessions_spec_15_1(t *testing.T) {
	store := tenantstore.NewMemory()
	seedSuspendTenant(t, store, tenantstore.Tenant{ID: "acme"})
	h := newSuspendRouter(store, &recordingSink{}).Handler()

	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants/acme/suspend", nil))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"failedSessions":[]`) {
		t.Errorf("body %s, want failedSessions encoded as []", rr.Body.String())
	}
}

// A marker write that fails answers 500 and writes no audit row.
// spec: §15.1 (REST API)
func TestSuspendTenant_MarkerWriteFailure_spec_15_1(t *testing.T) {
	sink := &recordingSink{}
	h := admin.NewRouter(failingUpdateTenants{tenantstore.NewMemory()}, admin.Options{
		Clock: func() time.Time { return suspendClock }, Audit: sink,
	}).Handler()
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants/acme/suspend", nil))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rr.Code)
	}
	if len(sink.events) != 0 {
		t.Errorf("audit events = %d, want 0", len(sink.events))
	}
}

// failingUpdateTenants returns a seeded active row from Get and fails
// every Update.
type failingUpdateTenants struct{ *tenantstore.Memory }

func (failingUpdateTenants) Get(context.Context, string) (tenantstore.Tenant, error) {
	return tenantstore.Tenant{ID: "acme"}, nil
}

func (failingUpdateTenants) Update(context.Context, string, func(*tenantstore.Tenant) error) (tenantstore.Tenant, error) {
	return tenantstore.Tenant{}, errors.New("primary unreachable")
}

// TestSuspendTenantDoesNotWaitForThePostTerminalWindow_spec_12_8 pins
// tenant suspension to the drain alone: no step after its drain acts on a
// session's resources, so a session still in its §7.1 post-terminal window
// does not hold the suspension, which answers 200.
//
// spec: §12.8 (Lifecycle fence and drain), §7.1 (Post-terminal window)
func TestSuspendTenantDoesNotWaitForThePostTerminalWindow_spec_12_8(t *testing.T) {
	store := tenantstore.NewMemory()
	seedSuspendTenant(t, store, tenantstore.Tenant{ID: "acme"})
	sessions := memstore.New(memstore.WithPostTerminalCap(time.Hour))
	row := sessionstore.Session{
		ID: "sess_sealing", TenantID: "acme", State: session.StateCompleted, PodAssignment: "pod-a", TerminatedAt: time.Now(),
	}
	if err := sessions.Create(context.Background(), row); err != nil {
		t.Fatalf("seed: %v", err)
	}
	sa := &fakeSessionAdmin{rows: map[string]sessionstore.Session{row.ID: row}}
	h := newSuspendRouter(store, &recordingSink{}).WithSessions(sessions).WithSessionAdmin(sa).Handler()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants/acme/suspend", nil)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, want 200; body %s", rr.Code, rr.Body.String())
	}
}
