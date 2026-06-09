// SPDX-License-Identifier: MIT

package impersonation_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/impersonation"
)

// recordedAppend is one audit row a fakeAppender captured.
type recordedAppend struct {
	tenantID  string
	eventType string
	payload   map[string]any
}

// fakeAppender captures audit writes and can inject a per-call error to
// exercise the §11.7 CMP-058 fail-closed path.
type fakeAppender struct {
	rows []recordedAppend
	err  error
}

func (a *fakeAppender) Append(_ context.Context, tenantID, eventType string, payload json.RawMessage, _ time.Time) (audit.Row, error) {
	if a.err != nil {
		return audit.Row{}, a.err
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

// fakeSigner records the claims it signed and returns a fixed token.
type fakeSigner struct {
	calls  int
	claims jwt.Claims
}

func (s *fakeSigner) Sign(c jwt.Claims) (string, error) {
	s.calls++
	s.claims = c
	return "signed-token", nil
}

// regionUnresolvable is a stand-in for the §11.7 auditstore CMP-058
// fail-closed error implementing the Code()/HTTPStatus() surface.
type regionUnresolvable struct{}

func (regionUnresolvable) Error() string   { return "platform audit region unresolvable" }
func (regionUnresolvable) Code() string    { return "PLATFORM_AUDIT_REGION_UNRESOLVABLE" }
func (regionUnresolvable) HTTPStatus() int { return 422 }

func newService(t *testing.T, app *fakeAppender, signer *fakeSigner, clock func() time.Time) *impersonation.Service {
	t.Helper()
	ids := 0
	return impersonation.New(impersonation.NewMemStore(), app, signer, impersonation.Config{
		PlatformTenantID: "platform",
		MaxDuration:      time.Hour,
		Issuer:           "https://lenny.test",
		Audience:         []string{"lenny-gateway"},
		Clock:            clock,
		NewID:            func() string { ids++; return "imp-" + string(rune('0'+ids)) },
	})
}

func fixedClock(at time.Time) func() time.Time { return func() time.Time { return at } }

// TestIssue_EmitsStartedAndMints_spec_16_7 covers the happy path: a
// confirmed issue writes admin.impersonation_started under the platform
// tenant carrying target_tenant_id, mints the bearer, and records the
// session.
func TestIssue_EmitsStartedAndMints_spec_16_7(t *testing.T) {
	app := &fakeAppender{}
	signer := &fakeSigner{}
	at := time.Unix(1700000000, 0).UTC()
	svc := newService(t, app, signer, fixedClock(at))

	ticket, bearer, err := svc.Issue(context.Background(), impersonation.IssueRequest{
		AdminSub:       "admin@acme.com",
		TargetTenantID: "acme",
		TargetUserID:   "alice@acme.com",
		Reason:         "support escalation",
		TicketRef:      "SUP-42",
		Duration:       30 * time.Minute,
		TargetRoles:    []auth.Role{auth.RoleUser},
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if bearer != "signed-token" || signer.calls != 1 {
		t.Fatalf("expected one mint, got bearer=%q calls=%d", bearer, signer.calls)
	}
	row, ok := app.byType("admin.impersonation_started")
	if !ok {
		t.Fatalf("admin.impersonation_started not emitted")
	}
	if row.tenantID != "platform" {
		t.Fatalf("started event tenant = %q, want platform", row.tenantID)
	}
	// §11.7 CMP-058 routing keys on a top-level target_tenant_id field.
	if row.payload["target_tenant_id"] != "acme" {
		t.Fatalf("started payload target_tenant_id = %v, want acme", row.payload["target_tenant_id"])
	}
	for _, f := range []string{"admin_sub", "admin_tenant_id", "target_user_id", "impersonation_reason", "impersonation_duration_seconds", "ticket_id"} {
		if _, present := row.payload[f]; !present {
			t.Fatalf("started payload missing §16.7 field %q: %v", f, row.payload)
		}
	}
	if row.payload["ticket_id"] != "SUP-42" {
		t.Fatalf("ticket_id = %v, want SUP-42 (external ref)", row.payload["ticket_id"])
	}
	// The minted bearer is a user_bearer for the target user, bounded by
	// the requested duration.
	if signer.claims.Subject != "alice@acme.com" || signer.claims.TenantID != "acme" {
		t.Fatalf("minted claims subject/tenant = %q/%q", signer.claims.Subject, signer.claims.TenantID)
	}
	if signer.claims.Typ != auth.TokenUserBearer {
		t.Fatalf("minted typ = %v, want user_bearer", signer.claims.Typ)
	}
	if signer.claims.Expiry != at.Add(30*time.Minute).Unix() {
		t.Fatalf("minted exp = %d, want %d", signer.claims.Expiry, at.Add(30*time.Minute).Unix())
	}
	if !ticket.Active() || ticket.ExpiresAt != at.Add(30*time.Minute) {
		t.Fatalf("unexpected ticket: %+v", ticket)
	}
}

// TestIssue_AuditBeforeMint_FailClosed_spec_11_7 covers the §16.7
// audit-must-be-durable-first contract: when the CMP-058 gate fails the
// started write closed, NO bearer is minted and NO session is recorded.
func TestIssue_AuditBeforeMint_FailClosed_spec_11_7(t *testing.T) {
	app := &fakeAppender{err: regionUnresolvable{}}
	signer := &fakeSigner{}
	svc := newService(t, app, signer, fixedClock(time.Unix(1700000000, 0).UTC()))

	_, _, err := svc.Issue(context.Background(), impersonation.IssueRequest{
		AdminSub: "admin@acme.com", TargetTenantID: "eu-tenant", TargetUserID: "bob@eu", Duration: time.Minute,
	})
	var unresolvable interface{ Code() string }
	if !errors.As(err, &unresolvable) || unresolvable.Code() != "PLATFORM_AUDIT_REGION_UNRESOLVABLE" {
		t.Fatalf("expected CMP-058 fail-closed, got %v", err)
	}
	if signer.calls != 0 {
		t.Fatalf("bearer must not be minted when the started audit fails closed (calls=%d)", signer.calls)
	}
	active, _ := svc.ListActive(context.Background())
	if len(active) != 0 {
		t.Fatalf("no session must be recorded on fail-closed, got %d", len(active))
	}
}

// TestIssue_Validation_spec_13_3 covers the issue input guards.
func TestIssue_Validation_spec_13_3(t *testing.T) {
	svc := newService(t, &fakeAppender{}, &fakeSigner{}, fixedClock(time.Unix(1700000000, 0).UTC()))
	cases := []struct {
		name string
		req  impersonation.IssueRequest
		want error
	}{
		{"missing admin", impersonation.IssueRequest{TargetTenantID: "acme", TargetUserID: "a", Duration: time.Minute}, impersonation.ErrMissingField},
		{"missing target tenant", impersonation.IssueRequest{AdminSub: "x", TargetUserID: "a", Duration: time.Minute}, impersonation.ErrMissingField},
		{"zero duration", impersonation.IssueRequest{AdminSub: "x", TargetTenantID: "acme", TargetUserID: "a", Duration: 0}, impersonation.ErrInvalidDuration},
		{"over max duration", impersonation.IssueRequest{AdminSub: "x", TargetTenantID: "acme", TargetUserID: "a", Duration: 2 * time.Hour}, impersonation.ErrInvalidDuration},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := svc.Issue(context.Background(), tc.req); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

// TestEnd_EmitsEnded_spec_16_7 covers the explicit end: it emits
// admin.impersonation_ended (reason=explicit) and marks the session.
func TestEnd_EmitsEnded_spec_16_7(t *testing.T) {
	app := &fakeAppender{}
	at := time.Unix(1700000000, 0).UTC()
	svc := newService(t, app, &fakeSigner{}, fixedClock(at))
	ticket, _, err := svc.Issue(context.Background(), impersonation.IssueRequest{
		AdminSub: "admin@acme.com", TargetTenantID: "acme", TargetUserID: "alice@acme.com",
		Reason: "r", TicketRef: "SUP-1", Duration: 10 * time.Minute,
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	ended, err := svc.End(context.Background(), ticket.ID, "admin2@acme.com")
	if err != nil {
		t.Fatalf("End: %v", err)
	}
	if ended.Active() || ended.EndReason != impersonation.EndReasonExplicit || ended.EndedBy != "admin2@acme.com" {
		t.Fatalf("unexpected ended ticket: %+v", ended)
	}
	row, ok := app.byType("admin.impersonation_ended")
	if !ok {
		t.Fatalf("admin.impersonation_ended not emitted")
	}
	if row.tenantID != "platform" || row.payload["target_tenant_id"] != "acme" {
		t.Fatalf("ended event mis-routed: tenant=%q target=%v", row.tenantID, row.payload["target_tenant_id"])
	}
	if row.payload["end_reason"] != "explicit" {
		t.Fatalf("end_reason = %v, want explicit", row.payload["end_reason"])
	}
	// A second end is rejected — the audit pair never duplicates.
	if _, err := svc.End(context.Background(), ticket.ID, "x"); !errors.Is(err, impersonation.ErrAlreadyEnded) {
		t.Fatalf("double-end got %v, want ErrAlreadyEnded", err)
	}
}

// TestEnd_NotFound_spec_13_3 covers ending an unknown session.
func TestEnd_NotFound_spec_13_3(t *testing.T) {
	svc := newService(t, &fakeAppender{}, &fakeSigner{}, fixedClock(time.Unix(1700000000, 0).UTC()))
	if _, err := svc.End(context.Background(), "nope", "x"); !errors.Is(err, impersonation.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

// TestSweepExpired_EndsExpiredOnly_spec_16_7 covers the expiry sweep:
// only sessions at/past their bearer expiry are ended, each emitting
// admin.impersonation_ended (reason=expired).
func TestSweepExpired_EndsExpiredOnly_spec_16_7(t *testing.T) {
	app := &fakeAppender{}
	at := time.Unix(1700000000, 0).UTC()
	clk := at
	svc := newService(t, app, &fakeSigner{}, func() time.Time { return clk })

	short, _, _ := svc.Issue(context.Background(), impersonation.IssueRequest{
		AdminSub: "admin", TargetTenantID: "acme", TargetUserID: "a", Reason: "r", TicketRef: "T1", Duration: 1 * time.Minute,
	})
	_, _, _ = svc.Issue(context.Background(), impersonation.IssueRequest{
		AdminSub: "admin", TargetTenantID: "acme", TargetUserID: "b", Reason: "r", TicketRef: "T2", Duration: 30 * time.Minute,
	})

	// Advance past the short session's expiry but not the long one's.
	n, err := svc.SweepExpired(context.Background(), at.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("SweepExpired: %v", err)
	}
	if n != 1 {
		t.Fatalf("swept %d, want 1", n)
	}
	active, _ := svc.ListActive(context.Background())
	if len(active) != 1 || active[0].TargetUserID != "b" {
		t.Fatalf("expected only the long session active, got %+v", active)
	}
	// The expired session's ended event is reason=expired, ended_by=system.
	var expiredEnd bool
	for _, r := range app.rows {
		if r.eventType == "admin.impersonation_ended" && r.payload["impersonation_session_id"] == short.ID {
			expiredEnd = r.payload["end_reason"] == "expired" && r.payload["ended_by"] == "system"
		}
	}
	if !expiredEnd {
		t.Fatalf("expired session must end with reason=expired ended_by=system")
	}
}

// TestSweepExpired_FailClosedRetries_spec_11_7 covers that a CMP-058
// fail-closed on the ended write leaves the expired session due for the
// next sweep rather than silently dropping the terminal record.
func TestSweepExpired_FailClosedRetries_spec_11_7(t *testing.T) {
	app := &fakeAppender{}
	at := time.Unix(1700000000, 0).UTC()
	svc := newService(t, app, &fakeSigner{}, fixedClock(at))
	_, _, _ = svc.Issue(context.Background(), impersonation.IssueRequest{
		AdminSub: "admin", TargetTenantID: "acme", TargetUserID: "a", Reason: "r", TicketRef: "T1", Duration: 1 * time.Minute,
	})
	app.err = regionUnresolvable{} // ended write now fails closed
	if n, _ := svc.SweepExpired(context.Background(), at.Add(5*time.Minute)); n != 0 {
		t.Fatalf("fail-closed sweep ended %d, want 0", n)
	}
	if active, _ := svc.ListActive(context.Background()); len(active) != 1 {
		t.Fatalf("session must remain due for retry, got %d active", len(active))
	}
	app.err = nil // the next sweep succeeds
	if n, _ := svc.SweepExpired(context.Background(), at.Add(6*time.Minute)); n != 1 {
		t.Fatalf("retry sweep ended %d, want 1", n)
	}
}
