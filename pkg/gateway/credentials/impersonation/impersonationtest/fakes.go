// SPDX-License-Identifier: MIT

// Package impersonationtest provides in-memory collaborators for tests of
// the §13.3 platform-admin impersonation service and of the handlers and
// fixtures built over it. IssuedTokens models the issued_tokens semantics
// the service relies on (the impersonation predicate, the conditional
// end, and the active listing) without Postgres; the single-winner
// guarantee of the conditional end is a Postgres property that only the
// Postgres-tier tests prove.
//
// spec: §13.3 (Platform-admin impersonation).
package impersonationtest

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
)

// CallLog records the order in which the fakes are called, so a test can
// assert the cross-collaborator order a spec rule fixes.
type CallLog struct {
	mu      sync.Mutex
	entries []string
}

// Add appends one entry. A nil CallLog records nothing.
func (l *CallLog) Add(entry string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, entry)
}

// Entries returns a copy of the recorded entries.
func (l *CallLog) Entries() []string {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.entries...)
}

// Call-log entries the fakes write.
const (
	CallGet    = "issued_tokens.get"
	CallRecord = "issued_tokens.record_impersonation"
	CallEnd    = "issued_tokens.end_impersonation"
	CallList   = "issued_tokens.list_active_impersonations"
	CallRevoke = "revocation_cache.revoke"
	CallEmit   = "cross_tenant_read.emit"
)

// RecordCall is one RecordImpersonation invocation.
type RecordCall struct {
	Token          issuedtokenstore.IssuedToken
	CallerTenantID string
}

// IssuedTokens is an in-memory impersonation.IssuedTokens. Rows are keyed
// by jti, the table's primary key. Each *Err field, when set, is returned
// by the matching method in place of its normal behaviour.
type IssuedTokens struct {
	Log *CallLog

	GetErr    error
	RecordErr error
	EndErr    error
	ListErr   error

	mu      sync.Mutex
	rows    map[string]issuedtokenstore.IssuedToken
	records []RecordCall
	gets    [][2]string
}

// Put stores a row directly, as another writer would.
func (f *IssuedTokens) Put(tok issuedtokenstore.IssuedToken) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rows == nil {
		f.rows = map[string]issuedtokenstore.IssuedToken{}
	}
	f.rows[tok.JTI] = tok
}

// Row returns the stored row for jti.
func (f *IssuedTokens) Row(jti string) (issuedtokenstore.IssuedToken, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[jti]
	return r, ok
}

// Records returns every RecordImpersonation call.
func (f *IssuedTokens) Records() []RecordCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]RecordCall(nil), f.records...)
}

// Gets returns the (tenantID, jti) pair of every Get call.
func (f *IssuedTokens) Gets() [][2]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][2]string(nil), f.gets...)
}

// Get returns the row whose jti and tenant match, or ErrNotFound.
func (f *IssuedTokens) Get(_ context.Context, tenantID, jti string) (issuedtokenstore.IssuedToken, error) {
	f.Log.Add(CallGet)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets = append(f.gets, [2]string{tenantID, jti})
	if f.GetErr != nil {
		return issuedtokenstore.IssuedToken{}, f.GetErr
	}
	r, ok := f.rows[jti]
	if !ok || r.TenantID != tenantID {
		return issuedtokenstore.IssuedToken{}, issuedtokenstore.ErrNotFound
	}
	return r, nil
}

// RecordImpersonation stores tok unless RecordErr is set or the jti exists.
func (f *IssuedTokens) RecordImpersonation(_ context.Context, tok issuedtokenstore.IssuedToken, callerTenantID string) error {
	f.Log.Add(CallRecord)
	f.mu.Lock()
	f.records = append(f.records, RecordCall{Token: tok, CallerTenantID: callerTenantID})
	f.mu.Unlock()
	if f.RecordErr != nil {
		return f.RecordErr
	}
	if _, exists := f.Row(tok.JTI); exists {
		return issuedtokenstore.ErrAlreadyExists
	}
	f.Put(tok)
	return nil
}

// EndImpersonation revokes an active impersonation row and classifies a
// miss as the store does.
func (f *IssuedTokens) EndImpersonation(_ context.Context, jti string, at time.Time) (issuedtokenstore.IssuedToken, error) {
	f.Log.Add(CallEnd)
	if f.EndErr != nil {
		return issuedtokenstore.IssuedToken{}, f.EndErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[jti]
	if !ok || !isImpersonation(r) {
		return issuedtokenstore.IssuedToken{}, issuedtokenstore.ErrNotFound
	}
	if !r.RevokedAt.IsZero() {
		return issuedtokenstore.IssuedToken{}, &issuedtokenstore.NotActiveError{State: issuedtokenstore.StateRevoked}
	}
	if !r.ExpiresAt.After(at) {
		return issuedtokenstore.IssuedToken{}, &issuedtokenstore.NotActiveError{State: issuedtokenstore.StateExpired}
	}
	r.RevokedAt = at
	r.RevokedReason = "impersonation_ended"
	f.rows[jti] = r
	return r, nil
}

// ListActiveImpersonations returns the active impersonation rows ordered
// by issued_at.
func (f *IssuedTokens) ListActiveImpersonations(_ context.Context, now time.Time) ([]issuedtokenstore.IssuedToken, error) {
	f.Log.Add(CallList)
	if f.ListErr != nil {
		return nil, f.ListErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []issuedtokenstore.IssuedToken
	for _, r := range f.rows {
		if isImpersonation(r) && r.RevokedAt.IsZero() && r.ExpiresAt.After(now) {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IssuedAt.Before(out[j].IssuedAt) })
	return out, nil
}

// isImpersonation applies the §13.3 rule 2 predicate.
func isImpersonation(r issuedtokenstore.IssuedToken) bool {
	return r.ActSubject != "" && r.ParentJTI == ""
}

// RevocationCache records every revoked jti.
type RevocationCache struct {
	Log *CallLog

	mu   sync.Mutex
	jtis []string
}

// Revoke records jti.
func (c *RevocationCache) Revoke(jti string) {
	c.Log.Add(CallRevoke)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.jtis = append(c.jtis, jti)
}

// Revoked returns every recorded jti.
func (c *RevocationCache) Revoked() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.jtis...)
}

// CrossTenantReads records every cross_tenant_read event and returns Err
// when it is set.
type CrossTenantReads struct {
	Log *CallLog
	Err error

	mu     sync.Mutex
	events []auditstore.CrossTenantRead
}

// EmitCrossTenantRead records r and returns Err.
func (c *CrossTenantReads) EmitCrossTenantRead(_ context.Context, r auditstore.CrossTenantRead) error {
	c.Log.Add(CallEmit)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, r)
	return c.Err
}

// Events returns every recorded event.
func (c *CrossTenantReads) Events() []auditstore.CrossTenantRead {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]auditstore.CrossTenantRead(nil), c.events...)
}
