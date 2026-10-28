// SPDX-License-Identifier: MIT

package callercheck_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/callercheck"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
)

// fakeTokens is an issued-token reader over a fixed row or error. It
// counts reads so a test can assert the pod path makes none.
type fakeTokens struct {
	row   issuedtokenstore.IssuedToken
	err   error
	reads int
	// gotTenant and gotJTI record the last lookup key.
	gotTenant, gotJTI string
}

func (f *fakeTokens) Get(_ context.Context, tenantID, jti string) (issuedtokenstore.IssuedToken, error) {
	f.reads++
	f.gotTenant, f.gotJTI = tenantID, jti
	return f.row, f.err
}

// appended is one row a fakeAudit received.
type appended struct {
	tenantID, eventType string
	payload             map[string]any
	at                  time.Time
}

// fakeAudit records appended rows, or fails every append with err.
type fakeAudit struct {
	err  error
	rows []appended
}

func (f *fakeAudit) Append(_ context.Context, tenantID, eventType string, payload json.RawMessage, at time.Time) (audit.Row, error) {
	if f.err != nil {
		return audit.Row{}, f.err
	}
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		return audit.Row{}, err
	}
	f.rows = append(f.rows, appended{tenantID: tenantID, eventType: eventType, payload: m, at: at})
	return audit.Row{}, nil
}

var checkAt = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)

func bearer() authmw.Principal {
	return authmw.Principal{Subject: "alice@acme.com", TenantID: "acme", JTI: "jti-alice"}
}

func impersonationRow() issuedtokenstore.IssuedToken {
	return issuedtokenstore.IssuedToken{JTI: "jti-alice", TenantID: "acme", Subject: "alice@acme.com", ActSubject: "bob@platform"}
}

func revokedRow() issuedtokenstore.IssuedToken {
	return issuedtokenstore.IssuedToken{JTI: "jti-alice", TenantID: "acme", Subject: "alice@acme.com", RevokedAt: checkAt.Add(-time.Minute)}
}

// spec: §10.2 (Signing topology), §13.3 (Credential Flow: Write-before-issue
// ordering) — every rule of the session-creation caller check, in order.
// The passing rules write no audit row; a refusal returns *Refused only
// once the token.exchanged rejection row was written, and every failure
// to read or to audit fails closed with an internal error that is not a
// *Refused.
func TestCheckRules_spec_10_2(t *testing.T) {
	readErr := errors.New("pool exhausted")
	auditErr := errors.New("audit lock timeout")
	cases := []struct {
		name        string
		tokens      *fakeTokens
		noReader    bool
		audit       *fakeAudit
		noAudit     bool
		principal   authmw.Principal
		wantRefused string
		wantErr     bool
		wantReads   int
		wantRows    int
	}{
		{name: "no reader passes", noReader: true, principal: bearer(), audit: &fakeAudit{}},
		{name: "pod path with no jti passes without a read", tokens: &fakeTokens{row: impersonationRow()}, principal: authmw.Principal{Subject: "sess_parent", TenantID: "acme"}, audit: &fakeAudit{}},
		{name: "unrecorded bearer passes", tokens: &fakeTokens{err: issuedtokenstore.ErrNotFound}, principal: bearer(), audit: &fakeAudit{}, wantReads: 1},
		{name: "wrapped not-found passes", tokens: &fakeTokens{err: errors.Join(errors.New("ctx"), issuedtokenstore.ErrNotFound)}, principal: bearer(), audit: &fakeAudit{}, wantReads: 1},
		{name: "read error fails closed", tokens: &fakeTokens{err: readErr}, principal: bearer(), audit: &fakeAudit{}, wantErr: true, wantReads: 1},
		{name: "live ordinary bearer passes", tokens: &fakeTokens{row: issuedtokenstore.IssuedToken{JTI: "jti-alice", TenantID: "acme"}}, principal: bearer(), audit: &fakeAudit{}, wantReads: 1},
		{name: "delegation child row is not impersonation", tokens: &fakeTokens{row: issuedtokenstore.IssuedToken{JTI: "jti-alice", ActSubject: "carol", ParentJTI: "jti-root"}}, principal: bearer(), audit: &fakeAudit{}, wantReads: 1},
		{name: "impersonation bearer is refused", tokens: &fakeTokens{row: impersonationRow()}, principal: bearer(), audit: &fakeAudit{}, wantRefused: "subject_token_impersonation", wantReads: 1, wantRows: 1},
		{name: "revoked bearer is refused", tokens: &fakeTokens{row: revokedRow()}, principal: bearer(), audit: &fakeAudit{}, wantRefused: "subject_token_revoked", wantReads: 1, wantRows: 1},
		{name: "refusal with failed audit fails closed", tokens: &fakeTokens{row: impersonationRow()}, principal: bearer(), audit: &fakeAudit{err: auditErr}, wantErr: true, wantReads: 1},
		{name: "refusal with no auditor fails closed", tokens: &fakeTokens{row: revokedRow()}, principal: bearer(), noAudit: true, wantErr: true, wantReads: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var deps callercheck.Deps
			if !tc.noReader {
				deps.IssuedTokens = tc.tokens
			}
			if !tc.noAudit {
				deps.Audit = tc.audit
			}
			err := callercheck.Check(context.Background(), deps, tc.principal, checkAt)
			assertOutcome(t, err, tc.wantRefused, tc.wantErr)
			if tc.tokens != nil && tc.tokens.reads != tc.wantReads {
				t.Errorf("issued_tokens reads = %d, want %d", tc.tokens.reads, tc.wantReads)
			}
			if tc.audit != nil && len(tc.audit.rows) != tc.wantRows {
				t.Errorf("audit rows = %d, want %d", len(tc.audit.rows), tc.wantRows)
			}
		})
	}
}

// assertOutcome checks err against the expected refusal reason or
// internal-error expectation; neither expected means err must be nil.
func assertOutcome(t *testing.T, err error, wantRefused string, wantErr bool) {
	t.Helper()
	var refused *callercheck.Refused
	isRefused := errors.As(err, &refused)
	switch {
	case wantRefused != "":
		if !isRefused {
			t.Fatalf("err = %v, want *Refused{%s}", err, wantRefused)
		}
		if refused.Reason != wantRefused {
			t.Errorf("Reason = %q, want %q", refused.Reason, wantRefused)
		}
	case wantErr:
		if err == nil || isRefused {
			t.Fatalf("err = %v, want a wrapped internal error that is not *Refused", err)
		}
	default:
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
	}
}

// spec: §16.7 (token.exchanged), §13.3 (Credential Flow: Write-before-issue
// ordering) — the rejection row is a token.exchanged row in the bearer's
// tenant with exchange_type session_mint, the bearer's sub and jti,
// policy_result rejected:<reason>, and no issued_token_jti, and the lookup
// keys on the bearer's tenant and jti.
func TestCheckRejectionRowPayload_spec_16_7(t *testing.T) {
	tokens := &fakeTokens{row: impersonationRow()}
	rec := &fakeAudit{}
	err := callercheck.Check(context.Background(), callercheck.Deps{IssuedTokens: tokens, Audit: rec}, bearer(), checkAt)
	var refused *callercheck.Refused
	if !errors.As(err, &refused) {
		t.Fatalf("err = %v, want *Refused", err)
	}
	if tokens.gotTenant != "acme" || tokens.gotJTI != "jti-alice" {
		t.Errorf("lookup key = (%q, %q), want (acme, jti-alice)", tokens.gotTenant, tokens.gotJTI)
	}
	if len(rec.rows) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(rec.rows))
	}
	row := rec.rows[0]
	if row.tenantID != "acme" || row.eventType != "token.exchanged" || !row.at.Equal(checkAt) {
		t.Errorf("row = (%q, %q, %v), want (acme, token.exchanged, %v)", row.tenantID, row.eventType, row.at, checkAt)
	}
	want := map[string]string{
		"exchange_type":     callercheck.ExchangeTypeSessionMint,
		"subject_token_sub": "alice@acme.com",
		"subject_token_jti": "jti-alice",
		"policy_result":     "rejected:subject_token_impersonation",
	}
	for k, v := range want {
		if row.payload[k] != v {
			t.Errorf("payload[%s] = %v, want %q", k, row.payload[k], v)
		}
	}
	if _, ok := row.payload["issued_token_jti"]; ok {
		t.Error("a session_mint rejection row must carry no issued_token_jti")
	}
}

// spec: §13.3 (Credential Flow: Write-before-issue ordering, the
// rejected-exchange discipline) — a failed rejection write names the
// intended reason in the internal error and keeps the cause inspectable.
func TestCheckAuditFailureNamesIntendedReason_spec_13_3(t *testing.T) {
	cause := errors.New("audit unavailable")
	err := callercheck.Check(context.Background(), callercheck.Deps{
		IssuedTokens: &fakeTokens{row: revokedRow()},
		Audit:        &fakeAudit{err: cause},
	}, bearer(), checkAt)
	if !errors.Is(err, cause) {
		t.Errorf("err = %v, want it to wrap the audit cause", err)
	}
	if err == nil || !strings.Contains(err.Error(), "subject_token_revoked") {
		t.Errorf("err = %v, want it to name the intended reason", err)
	}
}

// spec: §10.2 (Signing topology) — Refused renders its reason.
func TestRefusedError_spec_10_2(t *testing.T) {
	err := &callercheck.Refused{Reason: "subject_token_revoked"}
	if !strings.Contains(err.Error(), "subject_token_revoked") {
		t.Errorf("Error() = %q, want it to carry the reason", err.Error())
	}
}
