// SPDX-License-Identifier: MIT

package impersonation_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/impersonation"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/impersonation/impersonationtest"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
)

const (
	callAppendStarted = "audit.append:admin.impersonation_started"
	callAppendEnded   = "audit.append:admin.impersonation_ended"
	callSign          = "signer.sign"
)

// recordedAppend is one audit row a fakeAppender captured.
type recordedAppend struct {
	tenantID  string
	eventType string
	payload   map[string]any
}

// fakeAppender captures audit writes and returns errFor[eventType] when
// set, which exercises the §11.7 CMP-058 fail-closed path per event.
type fakeAppender struct {
	log    *impersonationtest.CallLog
	rows   []recordedAppend
	errFor map[string]error
}

func (a *fakeAppender) Append(_ context.Context, tenantID, eventType string, payload json.RawMessage, _ time.Time) (audit.Row, error) {
	a.log.Add("audit.append:" + eventType)
	if err := a.errFor[eventType]; err != nil {
		return audit.Row{}, err
	}
	var p map[string]any
	_ = json.Unmarshal(payload, &p)
	a.rows = append(a.rows, recordedAppend{tenantID: tenantID, eventType: eventType, payload: p})
	return audit.Row{ID: "row-" + eventType}, nil
}

func (a *fakeAppender) byType(t string) (recordedAppend, bool) {
	for _, r := range a.rows {
		if r.eventType == t {
			return r, true
		}
	}
	return recordedAppend{}, false
}

// fakeSigner records the claims it signed and returns a fixed token, or
// err when set.
type fakeSigner struct {
	log    *impersonationtest.CallLog
	calls  int
	claims jwt.Claims
	err    error
}

func (s *fakeSigner) Sign(_ context.Context, c jwt.Claims) (string, error) {
	s.log.Add(callSign)
	s.calls++
	s.claims = c
	if s.err != nil {
		return "", s.err
	}
	return "signed-token", nil
}

// regionUnresolvable is a stand-in for the §11.7 auditstore CMP-058
// fail-closed error implementing the Code()/HTTPStatus() surface.
type regionUnresolvable struct{}

func (regionUnresolvable) Error() string   { return "platform audit region unresolvable" }
func (regionUnresolvable) Code() string    { return "PLATFORM_AUDIT_REGION_UNRESOLVABLE" }
func (regionUnresolvable) HTTPStatus() int { return 422 }

// harness bundles a Service with its fakes and one shared call log.
type harness struct {
	log    *impersonationtest.CallLog
	tokens *impersonationtest.IssuedTokens
	cache  *impersonationtest.RevocationCache
	reads  *impersonationtest.CrossTenantReads
	app    *fakeAppender
	signer *fakeSigner
	svc    *impersonation.Service
}

func newHarness(t *testing.T, clock func() time.Time, maxDuration time.Duration) *harness {
	t.Helper()
	log := &impersonationtest.CallLog{}
	h := &harness{
		log:    log,
		tokens: &impersonationtest.IssuedTokens{Log: log},
		cache:  &impersonationtest.RevocationCache{Log: log},
		reads:  &impersonationtest.CrossTenantReads{Log: log},
		app:    &fakeAppender{log: log},
		signer: &fakeSigner{log: log},
	}
	ids := 0
	h.svc = impersonation.New(h.tokens, h.cache, h.reads, h.app, h.signer, impersonation.Config{
		PlatformTenantID: "platform",
		MaxDuration:      maxDuration,
		Issuer:           "https://lenny.test",
		Audience:         []string{"lenny-gateway"},
		Clock:            clock,
		NewID:            func() string { ids++; return "imp-" + string(rune('0'+ids)) },
	})
	return h
}

func fixedClock(at time.Time) func() time.Time { return func() time.Time { return at } }

var t0 = time.Unix(1700000000, 0).UTC()

func issueReq() impersonation.IssueRequest {
	return impersonation.IssueRequest{
		AdminSub:       "admin@acme.com",
		TargetTenantID: "acme",
		TargetUserID:   "alice@acme.com",
		Reason:         "support escalation",
		TicketRef:      "SUP-42",
		Duration:       30 * time.Minute,
		TargetRoles:    []auth.Role{auth.RoleUser},
		CallerTenantID: "platform",
		CallerJTI:      "caller-jti",
	}
}

// TestIssue_RecordsImpersonationRowAfterStartedAndSign_spec_13_3 covers
// the rule 1 happy path: the caller check reads the caller's bearer in the
// caller's tenant, then the started event, the sign, and the record run in
// that order, and the recorded row carries the session jti, the target,
// act_sub set to the admin, no parent, the bearer hash, and the exp the
// started event's time fixes.
//
// spec: 13.3 (Credential Flow: Platform-admin impersonation), 16.7 (Section 25 audit events)
func TestIssue_RecordsImpersonationRowAfterStartedAndSign_spec_13_3(t *testing.T) {
	// The clock advances on every read, so an exp taken from a later
	// reading than the started event's would differ.
	now := t0
	h := newHarness(t, func() time.Time { now = now.Add(time.Second); return now }, time.Hour)

	ticket, bearer, err := h.svc.Issue(context.Background(), issueReq())
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if bearer != "signed-token" || h.signer.calls != 1 {
		t.Fatalf("expected one mint, got bearer=%q calls=%d", bearer, h.signer.calls)
	}
	wantOrder := []string{impersonationtest.CallGet, callAppendStarted, callSign, impersonationtest.CallRecord}
	if got := h.log.Entries(); !reflect.DeepEqual(got, wantOrder) {
		t.Fatalf("call order = %v, want %v", got, wantOrder)
	}
	if got := h.tokens.Gets(); len(got) != 1 || got[0] != [2]string{"platform", "caller-jti"} {
		t.Fatalf("caller check read %v, want the caller's tenant and jti", got)
	}
	recs := h.tokens.Records()
	if len(recs) != 1 || recs[0].CallerTenantID != "platform" {
		t.Fatalf("RecordImpersonation calls = %+v, want one with the caller's tenant", recs)
	}
	row := recs[0].Token
	sum := sha256.Sum256([]byte("signed-token"))
	if row.JTI != ticket.ID || row.TenantID != "acme" || row.Subject != "alice@acme.com" ||
		row.ActSubject != "admin@acme.com" || row.ParentJTI != "" || string(row.TokenHash) != string(sum[:]) {
		t.Fatalf("recorded row = %+v", row)
	}
	if !row.ExpiresAt.Equal(ticket.IssuedAt.Add(30*time.Minute)) || !row.IssuedAt.Equal(ticket.IssuedAt) {
		t.Fatalf("row window %s..%s, want issued %s + 30m", row.IssuedAt, row.ExpiresAt, ticket.IssuedAt)
	}
	if h.signer.claims.Expiry != row.ExpiresAt.Unix() || h.signer.claims.JWTID != ticket.ID ||
		h.signer.claims.Typ != auth.TokenUserBearer || h.signer.claims.TenantID != "acme" {
		t.Fatalf("minted claims = %+v", h.signer.claims)
	}
	started, ok := h.app.byType("admin.impersonation_started")
	if !ok || started.tenantID != "platform" || started.payload["target_tenant_id"] != "acme" ||
		started.payload["impersonation_session_id"] != ticket.ID || started.payload["ticket_id"] != "SUP-42" {
		t.Fatalf("started event = %+v", started)
	}
}

// TestIssue_FailureStopsLaterSteps_spec_13_3 covers the rule 1 order on
// each failure: a started-event failure signs and records nothing, a sign
// failure records nothing, and a record failure returns no bearer.
//
// spec: 13.3 (Credential Flow: Platform-admin impersonation), 11.7 (Audit Logging)
func TestIssue_FailureStopsLaterSteps_spec_13_3(t *testing.T) {
	t.Run("started append fails closed", func(t *testing.T) {
		h := newHarness(t, fixedClock(t0), time.Hour)
		h.app.errFor = map[string]error{"admin.impersonation_started": regionUnresolvable{}}
		_, bearer, err := h.svc.Issue(context.Background(), issueReq())
		var unresolvable interface{ Code() string }
		if !errors.As(err, &unresolvable) || bearer != "" {
			t.Fatalf("got bearer=%q err=%v, want the CMP-058 error", bearer, err)
		}
		if h.signer.calls != 0 || len(h.tokens.Records()) != 0 {
			t.Fatalf("sign calls=%d records=%d, want none", h.signer.calls, len(h.tokens.Records()))
		}
	})
	t.Run("sign fails", func(t *testing.T) {
		h := newHarness(t, fixedClock(t0), time.Hour)
		h.signer.err = errors.New("kms down")
		if _, bearer, err := h.svc.Issue(context.Background(), issueReq()); err == nil || bearer != "" {
			t.Fatalf("got bearer=%q err=%v, want error", bearer, err)
		}
		if len(h.tokens.Records()) != 0 {
			t.Fatalf("a sign failure must record no row")
		}
	})
	t.Run("record fails", func(t *testing.T) {
		h := newHarness(t, fixedClock(t0), time.Hour)
		h.tokens.RecordErr = errors.New("postgres down")
		_, bearer, err := h.svc.Issue(context.Background(), issueReq())
		if err == nil || bearer != "" || errors.Is(err, impersonation.ErrIssuanceRefused) {
			t.Fatalf("got bearer=%q err=%v, want a non-refusal error and no bearer", bearer, err)
		}
	})
	t.Run("record fence refuses an invalidated user", func(t *testing.T) {
		h := newHarness(t, fixedClock(t0), time.Hour)
		h.tokens.RecordErr = &issuedtokenstore.UserInvalidatedError{TenantID: "acme", Subject: "alice@acme.com"}
		_, bearer, err := h.svc.Issue(context.Background(), issueReq())
		var inv *issuedtokenstore.UserInvalidatedError
		if !errors.Is(err, impersonation.ErrIssuanceRefused) || !errors.As(err, &inv) || bearer != "" {
			t.Fatalf("got bearer=%q err=%v, want ErrIssuanceRefused wrapping UserInvalidatedError", bearer, err)
		}
	})
}

// TestIssue_CallerCheckRefusesImpersonationOrRevokedCaller_spec_13_3 covers
// the rule 1 caller check: a caller bearer that step (1a) would reject as a
// subject_token is refused before any audit write, sign, or record, while
// a caller with no row or no jti passes and a read error aborts.
//
// spec: 13.3 (Credential Flow: Platform-admin impersonation; Write-before-issue ordering)
func TestIssue_CallerCheckRefusesImpersonationOrRevokedCaller_spec_13_3(t *testing.T) {
	impersonationRow := issuedtokenstore.IssuedToken{JTI: "caller-jti", TenantID: "platform", Subject: "admin@acme.com", ActSubject: "root@acme.com", ExpiresAt: t0.Add(time.Hour)}
	revokedRow := issuedtokenstore.IssuedToken{JTI: "caller-jti", TenantID: "platform", Subject: "admin@acme.com", RevokedAt: t0, ExpiresAt: t0.Add(time.Hour)}
	cases := []struct {
		name       string
		row        *issuedtokenstore.IssuedToken
		getErr     error
		callerJTI  string
		wantRefuse bool
		wantErr    bool
	}{
		{name: "impersonation caller bearer", row: &impersonationRow, callerJTI: "caller-jti", wantRefuse: true},
		{name: "revoked caller bearer", row: &revokedRow, callerJTI: "caller-jti", wantRefuse: true},
		{name: "caller bearer without a row", callerJTI: "caller-jti"},
		{name: "caller without a jti", callerJTI: ""},
		{name: "read error", getErr: errors.New("postgres down"), callerJTI: "caller-jti", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, fixedClock(t0), time.Hour)
			if tc.row != nil {
				h.tokens.Put(*tc.row)
			}
			h.tokens.GetErr = tc.getErr
			req := issueReq()
			req.CallerJTI = tc.callerJTI
			_, bearer, err := h.svc.Issue(context.Background(), req)
			if tc.wantRefuse != errors.Is(err, impersonation.ErrIssuanceRefused) {
				t.Fatalf("err = %v, want refusal=%v", err, tc.wantRefuse)
			}
			if tc.wantRefuse || tc.wantErr {
				if err == nil || bearer != "" || len(h.app.rows) != 0 || h.signer.calls != 0 || len(h.tokens.Records()) != 0 {
					t.Fatalf("a refused or failed caller check must write nothing: err=%v appends=%d signs=%d records=%d",
						err, len(h.app.rows), h.signer.calls, len(h.tokens.Records()))
				}
				return
			}
			if err != nil || bearer == "" {
				t.Fatalf("got bearer=%q err=%v, want issuance", bearer, err)
			}
			if tc.callerJTI == "" && len(h.tokens.Gets()) != 0 {
				t.Fatalf("a caller without a jti must not be read")
			}
		})
	}
}

// TestIssue_DurationBoundsAndClamp_spec_15_1 covers the §15.1 range: 59
// seconds is rejected, 60 is accepted, and 3601 is rejected even when the
// configured maximum is two hours, because New clamps it to one hour.
//
// spec: 15.1 (REST API), 13.3 (Credential Flow: Platform-admin impersonation)
func TestIssue_DurationBoundsAndClamp_spec_15_1(t *testing.T) {
	cases := []struct {
		seconds int
		wantErr bool
	}{{59, true}, {60, false}, {3600, false}, {3601, true}}
	for _, tc := range cases {
		h := newHarness(t, fixedClock(t0), 2*time.Hour)
		req := issueReq()
		req.Duration = time.Duration(tc.seconds) * time.Second
		_, _, err := h.svc.Issue(context.Background(), req)
		if tc.wantErr != errors.Is(err, impersonation.ErrInvalidDuration) {
			t.Fatalf("durationSeconds %d: err = %v, want invalid=%v", tc.seconds, err, tc.wantErr)
		}
	}
}

// TestIssue_Validation_spec_13_3 covers the required-field guards.
//
// spec: 13.3 (Credential Flow: Platform-admin impersonation)
func TestIssue_Validation_spec_13_3(t *testing.T) {
	h := newHarness(t, fixedClock(t0), time.Hour)
	for name, mutate := range map[string]func(*impersonation.IssueRequest){
		"missing admin":         func(r *impersonation.IssueRequest) { r.AdminSub = "" },
		"missing target tenant": func(r *impersonation.IssueRequest) { r.TargetTenantID = "" },
		"missing target user":   func(r *impersonation.IssueRequest) { r.TargetUserID = "" },
		"missing caller tenant": func(r *impersonation.IssueRequest) { r.CallerTenantID = "" },
	} {
		req := issueReq()
		mutate(&req)
		if _, _, err := h.svc.Issue(context.Background(), req); !errors.Is(err, impersonation.ErrMissingField) {
			t.Fatalf("%s: got %v, want ErrMissingField", name, err)
		}
	}
}

// issueOne establishes a session through the service and resets the log.
func issueOne(t *testing.T, h *harness) impersonation.Ticket {
	t.Helper()
	ticket, _, err := h.svc.Issue(context.Background(), issueReq())
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	*h.log = impersonationtest.CallLog{}
	h.app.rows = nil
	return ticket
}

// TestEnd_RevokesThenCachesThenAuditsThenEmits_spec_13_3 covers the rule 3
// order and the §16.7 ended payload: the store end, the cache push, the
// ended append, and cross_tenant_read run in that order, and the payload
// has exactly the §16.7 fields with no ticket_id or end_reason.
//
// spec: 13.3 (Credential Flow: Platform-admin impersonation), 16.7 (Section 25 audit events), 12.3 (Postgres HA Requirements)
func TestEnd_RevokesThenCachesThenAuditsThenEmits_spec_13_3(t *testing.T) {
	h := newHarness(t, fixedClock(t0), time.Hour)
	ticket := issueOne(t, h)

	res, err := h.svc.End(context.Background(), ticket.ID, "admin2@acme.com")
	if err != nil {
		t.Fatalf("End: %v", err)
	}
	if res.SessionID != ticket.ID || !res.EndedAt.Equal(t0) {
		t.Fatalf("EndResult = %+v", res)
	}
	want := []string{impersonationtest.CallEnd, impersonationtest.CallRevoke, callAppendEnded, impersonationtest.CallEmit}
	if got := h.log.Entries(); !reflect.DeepEqual(got, want) {
		t.Fatalf("call order = %v, want %v", got, want)
	}
	if got := h.cache.Revoked(); !reflect.DeepEqual(got, []string{ticket.ID}) {
		t.Fatalf("cache pushes = %v", got)
	}
	ended, _ := h.app.byType("admin.impersonation_ended")
	keys := make([]string, 0, len(ended.payload))
	for k := range ended.payload {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	wantKeys := []string{"admin_sub", "admin_tenant_id", "ended_at", "ended_by", "impersonation_session_id", "started_at", "target_tenant_id", "target_user_id"}
	if !reflect.DeepEqual(keys, wantKeys) {
		t.Fatalf("ended payload fields = %v, want %v", keys, wantKeys)
	}
	if ended.tenantID != "platform" || ended.payload["admin_sub"] != "admin@acme.com" ||
		ended.payload["target_tenant_id"] != "acme" || ended.payload["ended_by"] != "admin2@acme.com" {
		t.Fatalf("ended event = %+v", ended)
	}
	ev := h.reads.Events()
	if len(ev) != 1 || ev[0].Category != "impersonation_session" || ev[0].RowCount != 1 ||
		ev[0].Endpoint != "DELETE /v1/admin/impersonation/{id}" || ev[0].CallerSubject != "admin2@acme.com" {
		t.Fatalf("cross_tenant_read events = %+v", ev)
	}
}

// TestEnd_AppendAndEmitFailuresAreIndependent_spec_13_3 covers rules 3 and
// 7: an ended-append failure after the revoke still pushes the cache and
// emits cross_tenant_read and returns the append error, and a
// cross_tenant_read failure never suppresses the ended event.
//
// spec: 13.3 (Credential Flow: Platform-admin impersonation), 11.7 (Audit Logging)
func TestEnd_AppendAndEmitFailuresAreIndependent_spec_13_3(t *testing.T) {
	t.Run("ended append fails", func(t *testing.T) {
		h := newHarness(t, fixedClock(t0), time.Hour)
		ticket := issueOne(t, h)
		h.app.errFor = map[string]error{"admin.impersonation_ended": regionUnresolvable{}}
		res, err := h.svc.End(context.Background(), ticket.ID, "admin@acme.com")
		var unresolvable interface{ Code() string }
		if !errors.As(err, &unresolvable) || res.SessionID != ticket.ID {
			t.Fatalf("got res=%+v err=%v, want the append error with the result", res, err)
		}
		if len(h.cache.Revoked()) != 1 || len(h.reads.Events()) != 1 {
			t.Fatalf("cache=%v emits=%d, want the cache push and the emit", h.cache.Revoked(), len(h.reads.Events()))
		}
		if row, _ := h.tokens.Row(ticket.ID); row.RevokedAt.IsZero() {
			t.Fatalf("the bearer must stay revoked after an ended-append failure")
		}
	})
	t.Run("cross_tenant_read fails", func(t *testing.T) {
		h := newHarness(t, fixedClock(t0), time.Hour)
		ticket := issueOne(t, h)
		emitErr := errors.New("audit chain down")
		h.reads.Err = emitErr
		_, err := h.svc.End(context.Background(), ticket.ID, "admin@acme.com")
		if !errors.Is(err, emitErr) {
			t.Fatalf("err = %v, want the emit error", err)
		}
		if _, ok := h.app.byType("admin.impersonation_ended"); !ok {
			t.Fatalf("a cross_tenant_read failure must not suppress the ended event")
		}
	})
}

// TestEnd_MissesEmitZeroRowRead_spec_13_3 covers the rule 3 misses: an
// unknown id returns ErrNotFound, a revoked or expired row returns
// ErrAlreadyEnded with the state recoverable, each emits cross_tenant_read
// with a row count of zero, and none pushes the cache or writes the ended
// event.
//
// spec: 13.3 (Credential Flow: Platform-admin impersonation), 15.1 (REST API), 12.3 (Postgres HA Requirements)
func TestEnd_MissesEmitZeroRowRead_spec_13_3(t *testing.T) {
	clock := t0
	h := newHarness(t, func() time.Time { return clock }, time.Hour)
	ended := issueOne(t, h)
	if _, err := h.svc.End(context.Background(), ended.ID, "x"); err != nil {
		t.Fatalf("first End: %v", err)
	}
	expiring := issueOne(t, h)
	clock = t0.Add(2 * time.Hour)

	cases := []struct {
		name      string
		id        string
		wantState string
	}{
		{"unknown id", "imp-missing", ""},
		{"revoked", ended.ID, issuedtokenstore.StateRevoked},
		{"expired", expiring.ID, issuedtokenstore.StateExpired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			*h.log = impersonationtest.CallLog{}
			before := len(h.reads.Events())
			cachedBefore := len(h.cache.Revoked())
			h.app.rows = nil
			_, err := h.svc.End(context.Background(), tc.id, "admin@acme.com")
			if tc.wantState == "" {
				if !errors.Is(err, impersonation.ErrNotFound) {
					t.Fatalf("err = %v, want ErrNotFound", err)
				}
			} else {
				var na *issuedtokenstore.NotActiveError
				if !errors.Is(err, impersonation.ErrAlreadyEnded) || !errors.As(err, &na) || na.State != tc.wantState {
					t.Fatalf("err = %v, want ErrAlreadyEnded with state %q", err, tc.wantState)
				}
			}
			ev := h.reads.Events()
			if len(ev) != before+1 || ev[len(ev)-1].RowCount != 0 {
				t.Fatalf("want one cross_tenant_read with RowCount 0, got %+v", ev[before:])
			}
			if len(h.cache.Revoked()) != cachedBefore || len(h.app.rows) != 0 {
				t.Fatalf("a miss must not push the cache or append the ended event")
			}
		})
	}
}

// TestEnd_StoreErrorReturned_spec_13_3 covers a store failure on the end:
// the error is returned and nothing is pushed or appended.
//
// spec: 13.3 (Credential Flow: Platform-admin impersonation)
func TestEnd_StoreErrorReturned_spec_13_3(t *testing.T) {
	h := newHarness(t, fixedClock(t0), time.Hour)
	storeErr := errors.New("postgres down")
	h.tokens.EndErr = storeErr
	if _, err := h.svc.End(context.Background(), "imp-1", "x"); !errors.Is(err, storeErr) {
		t.Fatalf("err = %v, want the store error", err)
	}
	if len(h.cache.Revoked()) != 0 || len(h.app.rows) != 0 {
		t.Fatalf("a store failure must not push the cache or append")
	}
}

// TestListActive_EmitsBeforeReading_spec_13_3 covers rule 7 for the
// listing: cross_tenant_read is emitted before the store is read, an emit
// failure returns no list without reading, and the sessions carry the
// issued_tokens fields.
//
// spec: 13.3 (Credential Flow: Platform-admin impersonation), 12.3 (Postgres HA Requirements), 15.1 (REST API)
func TestListActive_EmitsBeforeReading_spec_13_3(t *testing.T) {
	h := newHarness(t, fixedClock(t0), time.Hour)
	ticket := issueOne(t, h)

	sessions, err := h.svc.ListActive(context.Background(), "admin@acme.com")
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	if got := h.log.Entries(); !reflect.DeepEqual(got, []string{impersonationtest.CallEmit, impersonationtest.CallList}) {
		t.Fatalf("call order = %v, want emit then list", got)
	}
	want := impersonation.Session{
		ID: ticket.ID, AdminSub: "admin@acme.com", TargetTenantID: "acme", TargetUserID: "alice@acme.com",
		IssuedAt: ticket.IssuedAt, ExpiresAt: ticket.ExpiresAt,
	}
	if len(sessions) != 1 || sessions[0] != want {
		t.Fatalf("sessions = %+v, want %+v", sessions, want)
	}
	ev := h.reads.Events()
	if len(ev) != 1 || ev[0].Endpoint != "GET /v1/admin/impersonation" || ev[0].CallerSubject != "admin@acme.com" {
		t.Fatalf("cross_tenant_read = %+v", ev)
	}

	*h.log = impersonationtest.CallLog{}
	h.reads.Err = errors.New("audit chain down")
	if sessions, err := h.svc.ListActive(context.Background(), "admin@acme.com"); err == nil || sessions != nil {
		t.Fatalf("got %v, %v; want no list on an emit failure", sessions, err)
	}
	if got := h.log.Entries(); !reflect.DeepEqual(got, []string{impersonationtest.CallEmit}) {
		t.Fatalf("the store must not be read after an emit failure, calls = %v", got)
	}

	h.reads.Err = nil
	h.tokens.ListErr = errors.New("postgres down")
	if _, err := h.svc.ListActive(context.Background(), "admin@acme.com"); err == nil {
		t.Fatalf("want the store error")
	}
}

// TestNew_PanicsOnMissingCollaborator_spec_13_3 covers the wiring guard:
// a missing collaborator is a wiring error.
//
// spec: 13.3 (Credential Flow: Platform-admin impersonation)
func TestNew_PanicsOnMissingCollaborator_spec_13_3(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatalf("New with a nil collaborator must panic")
		}
	}()
	impersonation.New(nil, &impersonationtest.RevocationCache{}, &impersonationtest.CrossTenantReads{}, &fakeAppender{}, &fakeSigner{},
		impersonation.Config{NewID: func() string { return "x" }})
}
