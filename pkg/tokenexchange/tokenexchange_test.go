// SPDX-License-Identifier: MIT

package tokenexchange

import (
	"errors"
	"testing"
	"time"
)

var now = time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)

func validSubject() Token {
	return Token{
		TenantID:        "acme",
		Subject:         "alice@acme.com",
		SessionID:       "sess_root",
		CallerType:      CallerHuman,
		DelegationDepth: 0,
		Scope:           []string{"sessions:read", "tools:sessions:write"},
		Audience:        []string{"lenny-gateway"},
		Typ:             TypeUserBearer,
		Exp:             now.Add(time.Hour),
	}
}

// Rotation: same subject + same caller; scope ⊆ subject; typ preserved.
func TestValidateRotationHappyPath(t *testing.T) {
	subject := validSubject()
	got, err := Validate(Request{
		Subject: subject,
		Caller:  subject,
		Requested: Token{
			Scope:    []string{"sessions:read"}, // narrower
			Audience: []string{"lenny-gateway"},
		},
		RequestedExp: now.Add(30 * time.Minute),
		Now:          now,
	})
	if err != nil {
		t.Fatalf("rotation should succeed, got %v", err)
	}
	if got.Typ != TypeUserBearer {
		t.Errorf("rotation preserves typ; want user_bearer, got %q", got.Typ)
	}
	if got.DelegationDepth != 0 {
		t.Errorf("rotation preserves depth; want 0, got %d", got.DelegationDepth)
	}
	if !got.Exp.Equal(now.Add(30 * time.Minute)) {
		t.Errorf("Exp: want %v, got %v", now.Add(30*time.Minute), got.Exp)
	}
}

// Cross-tenant: caller.tenant ≠ subject.tenant.
func TestValidateRejectsCrossTenantCaller(t *testing.T) {
	subject := validSubject()
	caller := validSubject()
	caller.TenantID = "globex" // different tenant
	_, err := Validate(Request{
		Subject:      subject,
		Caller:       caller,
		Requested:    Token{Scope: []string{"sessions:read"}, Audience: []string{"lenny-gateway"}},
		RequestedExp: now.Add(time.Minute),
		Now:          now,
	})
	var ee *ExchangeError
	if !errors.As(err, &ee) || ee.Reason != "tenant_mismatch" {
		t.Errorf("expected tenant_mismatch, got %v", err)
	}
}

// Requested.tenant_id ≠ subject.tenant_id.
func TestValidateRejectsCrossTenantIssued(t *testing.T) {
	subject := validSubject()
	_, err := Validate(Request{
		Subject:      subject,
		Caller:       subject,
		Requested:    Token{TenantID: "globex", Scope: []string{"sessions:read"}, Audience: []string{"lenny-gateway"}},
		RequestedExp: now.Add(time.Minute),
		Now:          now,
	})
	var ee *ExchangeError
	if !errors.As(err, &ee) || ee.Reason != "tenant_mismatch" {
		t.Errorf("expected tenant_mismatch on issued, got %v", err)
	}
}

// Scope narrowing.
func TestValidateRejectsScopeBroadening(t *testing.T) {
	subject := validSubject()
	_, err := Validate(Request{
		Subject:      subject,
		Caller:       subject,
		Requested:    Token{Scope: []string{"sessions:read", "tools:admin:write"}, Audience: []string{"lenny-gateway"}}, // tools:admin:write not in subject
		RequestedExp: now.Add(time.Minute),
		Now:          now,
	})
	var ee *ExchangeError
	if !errors.As(err, &ee) || ee.Code != "invalid_scope" {
		t.Errorf("expected invalid_scope, got %v", err)
	}
}

// Audience cannot broaden.
func TestValidateRejectsAudienceBroadening(t *testing.T) {
	subject := validSubject()
	_, err := Validate(Request{
		Subject:      subject,
		Caller:       subject,
		Requested:    Token{Scope: []string{"sessions:read"}, Audience: []string{"lenny-gateway", "lenny-ops"}},
		RequestedExp: now.Add(time.Minute),
		Now:          now,
	})
	var ee *ExchangeError
	if !errors.As(err, &ee) || ee.Reason != "audience_broadened" {
		t.Errorf("expected audience_broadened, got %v", err)
	}
}

// caller_type cannot elevate.
func TestValidateRejectsCallerTypeElevation(t *testing.T) {
	subject := validSubject()
	subject.CallerType = CallerAgent
	_, err := Validate(Request{
		Subject:      subject,
		Caller:       subject,
		Requested:    Token{CallerType: CallerHuman, Scope: []string{"sessions:read"}, Audience: []string{"lenny-gateway"}},
		RequestedExp: now.Add(time.Minute),
		Now:          now,
	})
	var ee *ExchangeError
	if !errors.As(err, &ee) || ee.Reason != "caller_type_elevated" {
		t.Errorf("expected caller_type_elevated, got %v", err)
	}
}

// Subject expired: rejected.
func TestValidateRejectsExpiredSubject(t *testing.T) {
	subject := validSubject()
	subject.Exp = now.Add(-10 * time.Second) // well past skew
	_, err := Validate(Request{
		Subject:      subject,
		Caller:       subject,
		Requested:    Token{Scope: []string{"sessions:read"}, Audience: []string{"lenny-gateway"}},
		RequestedExp: now.Add(time.Minute),
		Now:          now,
	})
	var ee *ExchangeError
	if !errors.As(err, &ee) || ee.Reason != "subject_token_expired" {
		t.Errorf("expected subject_token_expired, got %v", err)
	}
}

// §13.3 ±1s skew: a subject 500ms past exp must still admit.
func TestValidateSkewAllowsBoundaryExpiry(t *testing.T) {
	subject := validSubject()
	subject.Exp = now.Add(-500 * time.Millisecond) // within skew
	_, err := Validate(Request{
		Subject:      subject,
		Caller:       subject,
		Requested:    Token{Scope: []string{"sessions:read"}, Audience: []string{"lenny-gateway"}},
		RequestedExp: now.Add(time.Minute),
		Now:          now,
	})
	// Subject is past exp but within ±1s skew; not yet 'expired'.
	// The issued exp lands in the past, however, so the result is
	// rejected for exp_in_past, NOT subject_token_expired.
	var ee *ExchangeError
	if !errors.As(err, &ee) {
		t.Fatalf("expected error: %v", err)
	}
	if ee.Reason == "subject_token_expired" {
		t.Errorf("§13.3 ±1s skew should not flag this subject as expired")
	}
}

// Child-token minting (delegation): typ becomes a2a_delegation, depth
// = actor.depth + 1.
func TestValidateChildMintingProducesA2ADelegation(t *testing.T) {
	subject := validSubject()
	actor := validSubject()
	actor.SessionID = "sess_parent"
	actor.Subject = "alice@acme.com"
	actor.DelegationDepth = 1
	actor.Typ = TypeSessionCapability

	got, err := Validate(Request{
		Subject: subject,
		Actor:   &actor,
		Caller:  subject,
		Requested: Token{
			Scope:    []string{"sessions:read"},
			Audience: []string{"lenny-gateway"},
		},
		RequestedExp: now.Add(time.Minute),
		Now:          now,
	})
	if err != nil {
		t.Fatalf("child minting should succeed, got %v", err)
	}
	if got.Typ != TypeA2ADelegation {
		t.Errorf("child typ: want a2a_delegation, got %q", got.Typ)
	}
	if got.DelegationDepth != 2 {
		t.Errorf("child depth: want actor+1=2, got %d", got.DelegationDepth)
	}
}

// Actor expired: rejected with actor_token_expired (distinct from
// subject_token_expired).
func TestValidateRejectsExpiredActor(t *testing.T) {
	subject := validSubject()
	actor := validSubject()
	actor.Exp = now.Add(-10 * time.Second)
	_, err := Validate(Request{
		Subject:      subject,
		Actor:        &actor,
		Caller:       subject,
		Requested:    Token{Scope: []string{"sessions:read"}, Audience: []string{"lenny-gateway"}},
		RequestedExp: now.Add(time.Minute),
		Now:          now,
	})
	var ee *ExchangeError
	if !errors.As(err, &ee) || ee.Reason != "actor_token_expired" {
		t.Errorf("expected actor_token_expired, got %v", err)
	}
}

// typ mutation (rotation): rejected.
func TestValidateRejectsTypMutationOnRotation(t *testing.T) {
	subject := validSubject()
	subject.Typ = TypeUserBearer
	_, err := Validate(Request{
		Subject:      subject,
		Caller:       subject,
		Requested:    Token{Typ: TypeServiceToken, Scope: []string{"sessions:read"}, Audience: []string{"lenny-gateway"}},
		RequestedExp: now.Add(time.Minute),
		Now:          now,
	})
	var ee *ExchangeError
	if !errors.As(err, &ee) || ee.Reason != "typ_mutation" {
		t.Errorf("expected typ_mutation, got %v", err)
	}
}

// Exp = min(requested, subject, perDialectCap). When cap is tightest,
// cap wins.
func TestValidateExpRespectsPerDialectCap(t *testing.T) {
	subject := validSubject()
	subject.Exp = now.Add(time.Hour) // 1h
	got, err := Validate(Request{
		Subject:       subject,
		Caller:        subject,
		Requested:     Token{Scope: []string{"sessions:read"}, Audience: []string{"lenny-gateway"}},
		RequestedExp:  now.Add(time.Hour), // requested 1h
		PerDialectCap: 5 * time.Minute,    // cap = 5min
		Now:           now,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := now.Add(5 * time.Minute).Truncate(time.Second)
	if !got.Exp.Equal(want) {
		t.Errorf("Exp with per-dialect cap: want %v, got %v", want, got.Exp)
	}
}

// Exp is Unix seconds integer per RFC 7519: must be whole seconds.
func TestValidateExpTruncatesToWholeSeconds(t *testing.T) {
	subject := validSubject()
	subject.Exp = now.Add(2 * time.Hour) // beyond requested
	requested := now.Add(time.Minute + 750*time.Millisecond)
	got, err := Validate(Request{
		Subject:      subject,
		Caller:       subject,
		Requested:    Token{Scope: []string{"sessions:read"}, Audience: []string{"lenny-gateway"}},
		RequestedExp: requested,
		Now:          now,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Exp.Nanosecond() != 0 {
		t.Errorf("Exp must be whole seconds (Unix integer), got %v ns", got.Exp.Nanosecond())
	}
}

// exp_in_past: caller asked for an expiry already past.
func TestValidateRejectsExpInPast(t *testing.T) {
	subject := validSubject()
	_, err := Validate(Request{
		Subject:      subject,
		Caller:       subject,
		Requested:    Token{Scope: []string{"sessions:read"}, Audience: []string{"lenny-gateway"}},
		RequestedExp: now.Add(-time.Minute),
		Now:          now,
	})
	var ee *ExchangeError
	if !errors.As(err, &ee) || ee.Reason != "exp_in_past" {
		t.Errorf("expected exp_in_past, got %v", err)
	}
}

// Subject invalid (missing tenant_id, bad typ): rejected.
func TestValidateRejectsInvalidSubject(t *testing.T) {
	subject := validSubject()
	subject.TenantID = ""
	_, err := Validate(Request{
		Subject:      subject,
		Caller:       validSubject(),
		Requested:    Token{Scope: []string{"sessions:read"}, Audience: []string{"lenny-gateway"}},
		RequestedExp: now.Add(time.Minute),
		Now:          now,
	})
	var ee *ExchangeError
	if !errors.As(err, &ee) || ee.Reason != "subject_invalid" {
		t.Errorf("expected subject_invalid, got %v", err)
	}
}

// caller_type rank ordering: agent < service < human.
func TestCallerRankOrder(t *testing.T) {
	if callerRank(CallerAgent) >= callerRank(CallerService) {
		t.Errorf("agent must rank below service")
	}
	if callerRank(CallerService) >= callerRank(CallerHuman) {
		t.Errorf("service must rank below human")
	}
}
