// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/audit/auditscope"
	"github.com/lennylabs/lenny/pkg/gateway/callercheck"
	"github.com/lennylabs/lenny/pkg/gateway/environment/translator"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
)

// spec: §10.2 (Signing topology) — without a Postgres pool the caller
// check carries no collaborator, so it passes every caller; with a pool it
// reads issued_tokens and audits through the validated durable chain.
func TestSessionCallerCheckWiring_spec_10_2(t *testing.T) {
	if deps := sessionCallerCheck(nil, auditscope.New(nopChain{}, nil)); deps.IssuedTokens != nil || deps.Audit != nil {
		t.Errorf("no pool: deps = %+v, want zero", deps)
	}
	pool := &pgxpool.Pool{}
	withAudit := sessionCallerCheck(pool, auditscope.New(nopChain{}, nil))
	if withAudit.IssuedTokens == nil || withAudit.Audit == nil {
		t.Errorf("pool and auditor: deps = %+v, want both set", withAudit)
	}
	// A nil auditor stays an untyped nil, so the check fails closed on a
	// refusal rather than calling a typed-nil validator.
	if noAudit := sessionCallerCheck(pool, nil); noAudit.IssuedTokens == nil || noAudit.Audit != nil {
		t.Errorf("pool without auditor: deps = %+v, want reader only", noAudit)
	}
}

// nopChain is an auditscope.Chain that accepts every row.
type nopChain struct{}

func (nopChain) Append(context.Context, string, string, json.RawMessage, time.Time) (audit.Row, error) {
	return audit.Row{}, nil
}

func (nopChain) Rows(context.Context, string) ([]audit.Row, error) { return nil, nil }

func (nopChain) Verify(context.Context, string) (audit.VerifyResult, error) {
	return audit.VerifyResult{}, nil
}

// impersonationReader reads every jti as an impersonation row.
type impersonationReader struct{}

func (impersonationReader) Get(_ context.Context, tenantID, jti string) (issuedtokenstore.IssuedToken, error) {
	return issuedtokenstore.IssuedToken{JTI: jti, TenantID: tenantID, ActSubject: "bob@platform"}, nil
}

// spec: §10.2 (Signing topology), §15.2.1 (rule 1) — the translator
// single-shot path reaches requireActiveUser through
// CreateAndStartService, so an impersonation bearer is refused with
// FORBIDDEN before any pod claim.
func TestSingleShotBinderRefusesImpersonationBearer_spec_10_2(t *testing.T) {
	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		DevTenantHeader: true,
		CallerCheck:     callercheck.Deps{IssuedTokens: impersonationReader{}, Audit: nopChain{}},
	})
	ctx := authmw.WithPrincipal(context.Background(),
		authmw.Principal{Subject: "alice@acme.com", TenantID: "acme", JTI: "jti-impersonation"})
	_, err := sessionSingleShotBinder{srv: srv}.BindSingleShot(ctx, translator.SingleShotSpec{TenantID: "acme", RuntimeRef: "claude-code"})
	var ssErr *translator.SingleShotError
	if !errors.As(err, &ssErr) {
		t.Fatalf("err = %v, want *translator.SingleShotError", err)
	}
	if ssErr.HTTPStatus != http.StatusForbidden || ssErr.Code != "FORBIDDEN" {
		t.Errorf("mapped error = (%d, %q), want (403, FORBIDDEN)", ssErr.HTTPStatus, ssErr.Code)
	}
}
