// SPDX-License-Identifier: MIT

package tokenexchange

import (
	"reflect"
	"testing"
	"time"
)

// spec: 13.3 (Credential Flow) "RFC 8693 parameter mapping to Lenny claims" actor_token, 13.3 (Credential Flow) "Lenny JWT claim structure" act
//
// Validate sets Issued.Act on an exchange that carries an actor_token, and
// only then. The four fields come from the actor token, never the subject:
// a child whose subject is alice and whose actor is bob names bob, with bob's
// session_id and bob's delegation_depth (the child's own depth is bob's + 1).
// Rotation and scope narrowing, which carry no actor, leave Act nil even when
// the subject is itself an a2a_delegation token.
func TestValidateSetsActOnlyWithActorToken_spec_13_3(t *testing.T) {
	bob := Token{
		TenantID: "acme", Subject: "bob@acme.com", SessionID: "sess_bob",
		CallerType: CallerAgent, DelegationDepth: 2,
		Scope: []string{"sessions:read"}, Audience: []string{"lenny-gateway"},
		Typ: TypeSessionCapability, Exp: now.Add(time.Hour),
	}
	delegated := validSubject()
	delegated.Typ = TypeA2ADelegation
	delegated.DelegationDepth = 1

	cases := []struct {
		name      string
		subject   Token
		actor     *Token
		wantAct   *Actor
		wantDepth int
	}{
		{
			name: "delegation mint with a distinct actor", subject: validSubject(), actor: &bob,
			wantAct:   &Actor{Subject: "bob@acme.com", TenantID: "acme", SessionID: "sess_bob", DelegationDepth: 2},
			wantDepth: 3,
		},
		{
			name: "delegation mint whose actor is the subject", subject: validSubject(), actor: ptr(validSubject()),
			wantAct:   &Actor{Subject: "alice@acme.com", TenantID: "acme", SessionID: "sess_root", DelegationDepth: 0},
			wantDepth: 1,
		},
		{name: "rotation", subject: validSubject(), wantDepth: 0},
		{name: "exchange of an a2a_delegation subject without an actor", subject: delegated, wantDepth: 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Validate(Request{
				Subject:      c.subject,
				Caller:       c.subject,
				Actor:        c.actor,
				Requested:    Token{Scope: []string{"sessions:read"}, Audience: []string{"lenny-gateway"}},
				RequestedExp: now.Add(30 * time.Minute),
				Now:          now,
			})
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if !reflect.DeepEqual(got.Act, c.wantAct) {
				t.Errorf("Act = %+v, want %+v", got.Act, c.wantAct)
			}
			if got.DelegationDepth != c.wantDepth {
				t.Errorf("DelegationDepth = %d, want %d", got.DelegationDepth, c.wantDepth)
			}
			if got.Subject != c.subject.Subject {
				t.Errorf("Subject = %q, want the subject %q", got.Subject, c.subject.Subject)
			}
		})
	}
}

// spec: 13.3 (Credential Flow) "Lenny JWT claim structure" act
//
// A rejected actor-bearing exchange returns the zero Issued, so no caller
// can read an actor off a refused mint.
func TestValidateRejectedActorExchangeCarriesNoAct_spec_13_3(t *testing.T) {
	actor := validSubject()
	actor.TenantID = "globex"
	got, err := Validate(Request{
		Subject: validSubject(), Caller: validSubject(), Actor: &actor,
		Requested:    Token{Scope: []string{"sessions:read"}, Audience: []string{"lenny-gateway"}},
		RequestedExp: now.Add(30 * time.Minute), Now: now,
	})
	if err == nil {
		t.Fatal("cross-tenant actor admitted, want tenant_mismatch")
	}
	if got.Act != nil {
		t.Errorf("rejected exchange carries Act %+v, want nil", got.Act)
	}
}

func ptr[T any](v T) *T { return &v }
