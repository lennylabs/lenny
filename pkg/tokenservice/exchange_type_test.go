// SPDX-License-Identifier: MIT

package tokenservice

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	obsaudit "github.com/lennylabs/lenny/pkg/observability/audit"
	"github.com/lennylabs/lenny/pkg/tokenexchange"
)

// exchangePayloads extracts every token.exchanged payload from a
// recordingAuditor snapshot.
func exchangePayloads(t *testing.T, rows []recordedAudit) []exchangeAuditPayload {
	t.Helper()
	var out []exchangeAuditPayload
	for _, r := range rows {
		if r.EventType != string(obsaudit.EventTokenExchanged) {
			continue
		}
		var p exchangeAuditPayload
		if err := json.Unmarshal(r.Payload, &p); err != nil {
			t.Fatalf("unmarshal token.exchanged payload: %v", err)
		}
		out = append(out, p)
	}
	return out
}

// spec: §16.7 line 672 — every token.exchanged row carries the mandatory
// exchange_type classification. Before this change exchangeAuditPayload
// omitted the field on every row, so the §16.7 payload contract was
// violated. This asserts the field is present and correctly classified
// for the self/service-principal rotation grant (admin_rotation). It
// fails against the pre-fix struct, which had no exchange_type JSON key.
func TestExchangedRowCarriesExchangeType_Rotation_spec_16_7(t *testing.T) {
	signer := jwt.NewHMACSigner("dev-1", []byte("dev-secret"))
	auditor := &recordingAuditor{}
	store := &recordingIssuedTokenStore{}
	srv := NewServer(Options{
		Signer:       signer,
		Issuer:       "https://lenny.dev.test/token",
		IssuedTokens: store,
		Auditor:      auditor,
	})
	// A self-rotation: caller authenticates with — and rotates — its own
	// token, preserving scope and audience.
	tok := mintToken(t, signer, jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme", JWTID: "tok-1",
		Typ: auth.TokenUserBearer, Scope: "sessions:read", Audience: []string{"lenny-gateway"},
	})

	resp := doExchange(t, srv, tok, Request{
		GrantType: grantTypeExchange, SubjectToken: tok,
		Scope: "sessions:read", Audience: "lenny-gateway",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	payloads := exchangePayloads(t, auditor.snapshot())
	if len(payloads) != 1 {
		t.Fatalf("token.exchanged rows = %d, want 1", len(payloads))
	}
	if payloads[0].ExchangeType != exchangeTypeAdminRotation {
		t.Errorf("exchange_type = %q, want admin_rotation", payloads[0].ExchangeType)
	}

	// Belt-and-braces: the raw JSON must carry the exchange_type key so a
	// SIEM consumer sees the §16.7-mandated field, not just the Go struct.
	var raw map[string]any
	for _, r := range auditor.snapshot() {
		if r.EventType == string(obsaudit.EventTokenExchanged) {
			if err := json.Unmarshal(r.Payload, &raw); err != nil {
				t.Fatalf("unmarshal raw payload: %v", err)
			}
		}
	}
	if _, ok := raw["exchange_type"]; !ok {
		t.Errorf("token.exchanged payload is missing the exchange_type key: %v", raw)
	}
}

// spec: §16.7 line 672 — a delegation child mint (actor_token present)
// classifies as delegation_mint.
func TestExchangedRowCarriesExchangeType_DelegationMint_spec_16_7(t *testing.T) {
	signer := jwt.NewHMACSigner("dev-1", []byte("dev-secret"))
	auditor := &recordingAuditor{}
	store := &recordingIssuedTokenStore{}
	srv := NewServer(Options{
		Signer:       signer,
		Issuer:       "https://lenny.dev.test/token",
		IssuedTokens: store,
		Auditor:      auditor,
	})
	tok := mintToken(t, signer, jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme", JWTID: "tok-1",
		Typ: auth.TokenUserBearer, Scope: "sessions:read", Audience: []string{"lenny-gateway"},
	})

	resp := doExchange(t, srv, tok, Request{
		GrantType: grantTypeExchange, SubjectToken: tok,
		ActorToken: tok, Scope: "sessions:read", Audience: "lenny-gateway",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	payloads := exchangePayloads(t, auditor.snapshot())
	if len(payloads) != 1 {
		t.Fatalf("token.exchanged rows = %d, want 1", len(payloads))
	}
	if payloads[0].ExchangeType != exchangeTypeDelegationMint {
		t.Errorf("exchange_type = %q, want delegation_mint", payloads[0].ExchangeType)
	}
}

// spec: §16.7 line 672 — a self-exchange that narrows scope is a
// scope_narrow derivation.
func TestExchangedRowCarriesExchangeType_ScopeNarrow_spec_16_7(t *testing.T) {
	signer := jwt.NewHMACSigner("dev-1", []byte("dev-secret"))
	auditor := &recordingAuditor{}
	store := &recordingIssuedTokenStore{}
	srv := NewServer(Options{
		Signer:       signer,
		Issuer:       "https://lenny.dev.test/token",
		IssuedTokens: store,
		Auditor:      auditor,
	})
	tok := mintToken(t, signer, jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme", JWTID: "tok-1",
		Typ: auth.TokenUserBearer, Scope: "sessions:read sessions:write", Audience: []string{"lenny-gateway"},
	})

	resp := doExchange(t, srv, tok, Request{
		GrantType: grantTypeExchange, SubjectToken: tok,
		Scope: "sessions:read", Audience: "lenny-gateway",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	payloads := exchangePayloads(t, auditor.snapshot())
	if len(payloads) != 1 {
		t.Fatalf("token.exchanged rows = %d, want 1", len(payloads))
	}
	if payloads[0].ExchangeType != exchangeTypeScopeNarrow {
		t.Errorf("exchange_type = %q, want scope_narrow", payloads[0].ExchangeType)
	}
}

// spec: §16.7 line 672 — the classifier maps each accepted exchange to
// its exchange_type. A session-capability issued token (no actor, not a
// rotation) is a credential-lease issuance; a plain narrowing derivation
// is scope_narrow; an actor-bearing exchange is delegation_mint; a
// self-rotation is admin_rotation.
func TestClassifyExchangeType_spec_16_7(t *testing.T) {
	actor := &jwt.Claims{Subject: "alice@acme.com"}
	cases := []struct {
		name       string
		issued     tokenexchange.Issued
		isRotation bool
		actor      *jwt.Claims
		want       string
	}{
		{"delegation mint wins over rotation", tokenexchange.Issued{Typ: tokenexchange.TypeA2ADelegation}, true, actor, exchangeTypeDelegationMint},
		{"self rotation", tokenexchange.Issued{Typ: tokenexchange.TypeUserBearer}, true, nil, exchangeTypeAdminRotation},
		{"session capability lease", tokenexchange.Issued{Typ: tokenexchange.TypeSessionCapability}, false, nil, exchangeTypeLeaseIssue},
		{"plain narrow", tokenexchange.Issued{Typ: tokenexchange.TypeUserBearer}, false, nil, exchangeTypeScopeNarrow},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classifyExchangeType(c.issued, c.isRotation, c.actor); got != c.want {
				t.Errorf("classifyExchangeType = %q, want %q", got, c.want)
			}
		})
	}
}

// spec: §16.7 line 672 — a rejected exchange still carries an
// exchange_type on its token.exchanged row so the SIEM classifies the
// probe. A rejected delegation-mint attempt (actor present) classifies
// as delegation_mint.
func TestRejectedExchangeCarriesExchangeType_spec_16_7(t *testing.T) {
	signer := jwt.NewHMACSigner("dev-1", []byte("dev-secret"))
	auditor := &recordingAuditor{}
	srv := NewServer(Options{
		Signer:  signer,
		Issuer:  "https://lenny.dev.test/token",
		Auditor: auditor,
	})
	// Broaden scope beyond the subject's — the validator rejects it.
	tok := mintToken(t, signer, jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme", JWTID: "tok-1",
		Typ: auth.TokenUserBearer, Scope: "sessions:read", Audience: []string{"lenny-gateway"},
	})

	resp := doExchange(t, srv, tok, Request{
		GrantType: grantTypeExchange, SubjectToken: tok,
		ActorToken: tok, Scope: "sessions:read sessions:admin", Audience: "lenny-gateway",
	})
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("status=%d, want a rejection (scope broadening)", resp.StatusCode)
	}

	payloads := exchangePayloads(t, auditor.snapshot())
	if len(payloads) != 1 {
		t.Fatalf("token.exchanged rows = %d, want 1 (the rejection row)", len(payloads))
	}
	if payloads[0].ExchangeType != exchangeTypeDelegationMint {
		t.Errorf("rejected exchange_type = %q, want delegation_mint (actor present)", payloads[0].ExchangeType)
	}
	if payloads[0].PolicyResult == "" || payloads[0].PolicyResult == "accepted" {
		t.Errorf("policy_result = %q, want a rejected:<reason>", payloads[0].PolicyResult)
	}
}
