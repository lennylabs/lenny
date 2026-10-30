// SPDX-License-Identifier: MIT

// Package callercheck applies step (1a) of the §13.3 write-before-issue
// ordering to the bearer that creates a session. Session creation mints no
// JWT, so the check reads the caller bearer's issued_tokens row as the
// subject_token and refuses a revoked or impersonation bearer before any
// session work runs. Every root-creating handler and the client-facing
// lenny/delegate_task tool call Check.
//
// spec: §10.2 (Signing topology), §13.3 (Credential Flow: Write-before-issue
// ordering), §16.7.
package callercheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
	auditcatalog "github.com/lennylabs/lenny/pkg/observability/audit"
)

// ExchangeTypeSessionMint is the §16.7 token.exchanged exchange_type of a
// session-creation caller-check rejection. The row is always a rejection
// row, because session creation mints no JWT.
const ExchangeTypeSessionMint = "session_mint"

// IssuedTokenReader reads one issued_tokens row by tenant and jti.
// *issuedtokenstore.Store satisfies it.
type IssuedTokenReader interface {
	Get(ctx context.Context, tenantID, jti string) (issuedtokenstore.IssuedToken, error)
}

// Auditor writes one audit row into a tenant's chain and returns once the
// row is durable. The production implementation appends under the §11.7
// per-tenant audit advisory lock and commits before it returns.
type Auditor interface {
	Append(ctx context.Context, tenantID, eventType string, payload json.RawMessage, at time.Time) (audit.Row, error)
}

// Deps carries the check's collaborators. A zero Deps passes every
// caller: a gateway without the Postgres pool registers no impersonation
// route and records no issued_tokens row to refuse.
type Deps struct {
	// IssuedTokens reads the caller bearer's issued_tokens row.
	IssuedTokens IssuedTokenReader
	// Audit writes the token.exchanged rejection row.
	Audit Auditor
}

// Refused reports that step (1a) refused the caller bearer and that the
// rejection audit row is durable. Reason is the §13.3 invalid_grant reason
// (subject_token_revoked or subject_token_impersonation). The caller maps
// it to 403 FORBIDDEN.
type Refused struct {
	Reason string
}

// Error implements error.
func (e *Refused) Error() string {
	return "callercheck: session creation refused: " + e.Reason
}

// rejectionPayload is the §16.7 token.exchanged payload of a session-mint
// rejection. It names the bearer by sub and jti only, never the token, and
// carries no issued_token_jti because nothing was issued.
type rejectionPayload struct {
	ExchangeType    string    `json:"exchange_type"`
	SubjectTokenSub string    `json:"subject_token_sub"`
	SubjectTokenJTI string    `json:"subject_token_jti"`
	PolicyResult    string    `json:"policy_result"`
	Timestamp       time.Time `json:"timestamp"`
}

// Check applies §13.3 step (1a) to the session-creating bearer p at the
// instant at. It returns nil when the bearer may create a session, a
// *Refused once the rejection row is durable, and a wrapped internal error
// when the issued_tokens read or the rejection write fails. The rules run
// in order:
//
//  1. Without an IssuedTokens reader the check passes.
//  2. A principal with no jti passes. The pod path over LNK-GWCONTROL
//     carries no Lenny-issued JWT.
//  3. A bearer with no issued_tokens row passes; any other read error
//     fails closed.
//  4. A revoked or impersonation row writes the rejection row and refuses.
//
// spec: §10.2 (Signing topology), §13.3 (Credential Flow: Write-before-issue
// ordering, step (1a)), §16.7.
func Check(ctx context.Context, deps Deps, p authmw.Principal, at time.Time) error {
	if deps.IssuedTokens == nil || p.JTI == "" {
		return nil
	}
	row, err := deps.IssuedTokens.Get(ctx, p.TenantID, p.JTI)
	if errors.Is(err, issuedtokenstore.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("callercheck: read issued token %q: %w", p.JTI, err)
	}
	refusal := row.SourceRefusal(issuedtokenstore.SourceSubjectToken)
	if refusal == nil {
		return nil
	}
	return reject(ctx, deps.Audit, p, refusalReason(refusal), at)
}

// reject writes the token.exchanged rejection row for p and returns
// *Refused once it is durable. A nil Auditor or a failed write returns a
// wrapped internal error naming the intended reason, so no session is
// created on a refusal that left no audit row.
//
// spec: §13.3 (Credential Flow: Write-before-issue ordering, the
// rejected-exchange discipline), §16.7.
func reject(ctx context.Context, a Auditor, p authmw.Principal, reason string, at time.Time) error {
	if a == nil {
		return fmt.Errorf("callercheck: no rejection auditor; intended rejection: %s", reason)
	}
	payload, err := json.Marshal(rejectionPayload{
		ExchangeType:    ExchangeTypeSessionMint,
		SubjectTokenSub: p.Subject,
		SubjectTokenJTI: p.JTI,
		PolicyResult:    "rejected:" + reason,
		Timestamp:       at,
	})
	if err != nil {
		return fmt.Errorf("callercheck: encode rejection row; intended rejection: %s: %w", reason, err)
	}
	if _, err := a.Append(ctx, p.TenantID, string(auditcatalog.EventTokenExchanged), payload, at); err != nil {
		return fmt.Errorf("callercheck: write rejection row; intended rejection: %s: %w", reason, err)
	}
	return &Refused{Reason: reason}
}

// refusalReason returns the §13.3 invalid_grant reason SourceRefusal
// carries. Every error SourceRefusal returns has a Reason method.
func refusalReason(err error) string {
	var r interface{ Reason() string }
	if errors.As(err, &r) {
		return r.Reason()
	}
	return err.Error()
}
