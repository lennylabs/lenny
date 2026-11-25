// SPDX-License-Identifier: MIT

package tokenexchange

import (
	"reflect"
	"testing"
	"time"
)

// spec: §13.3 (Credential Flow)
//
// A child minted with a narrow actor token is bounded by the actor's scope
// as well as the subject's (the scenario CX-6 of change 0165's model): alice
// holds an unrestricted subject and an actor narrowed to tools:me:read, so a
// child that would carry no scope, or a value outside the actor's scope, is
// refused with scope_exceeds_actor, and a child inside the actor's scope is
// issued. An actor with no scope claim or tools:* grants every child scope.
func TestValidateRefusesAChildBroaderThanItsActor(t *testing.T) {
	cases := []struct {
		name       string
		actorScope []string
		requested  []string
		wantErr    bool
	}{
		{"child without scope from a narrow actor", []string{"tools:me:read"}, nil, true},
		{"child naming a value outside the actor", []string{"tools:me:read"}, []string{"tools:sessions:scale"}, true},
		{"child naming a domain wildcard the actor holds one action of", []string{"tools:me:read"}, []string{"tools:me:*"}, true},
		{"child naming tools:* from a domain-wildcard actor", []string{"tools:me:*"}, []string{"tools:*"}, true},
		{"child inside the actor", []string{"tools:me:read"}, []string{"tools:me:read"}, false},
		{"child inside a domain-wildcard actor", []string{"tools:me:*"}, []string{"tools:me:read"}, false},
		{"actor without a scope claim", nil, nil, false},
		{"actor with tools:*", []string{"tools:*"}, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			subject := validSubject()
			subject.Scope = nil
			actor := validSubject()
			actor.Scope = c.actorScope
			got, err := exchangeOf(subject, actor, &actor, c.requested)
			if c.wantErr {
				wantRefusal(t, err, "invalid_scope", "scope_exceeds_actor")
				return
			}
			if err != nil {
				t.Fatalf("child mint should succeed, got %v", err)
			}
			if !reflect.DeepEqual(got.Scope, c.requested) {
				t.Errorf("child scope = %v, want %v", got.Scope, c.requested)
			}
		})
	}
}

// spec: §13.3 (Credential Flow)
//
// An exchange without an actor_token copies the subject's act claim, so a
// narrowed or rotated delegation child keeps the name of its delegator (the
// scenario CX-9 of change 0165's model). The copy shares no memory with the
// subject's claim. A subject without act issues none, and a child mint names
// its actor rather than the subject's delegator.
func TestValidateCopiesTheSubjectActOnExchangesWithoutAnActor(t *testing.T) {
	delegator := &Actor{Subject: "alice@acme.com", TenantID: "acme", SessionID: "sess_root", DelegationDepth: 0}
	child := validSubject()
	child.Typ = TypeA2ADelegation
	child.DelegationDepth = 1
	child.Act = delegator

	narrowed, err := exchangeOf(child, child, nil, []string{"tools:sessions:read"})
	if err != nil {
		t.Fatalf("narrowing a delegation child should succeed, got %v", err)
	}
	if !reflect.DeepEqual(narrowed.Act, delegator) {
		t.Errorf("narrowed act = %+v, want %+v", narrowed.Act, delegator)
	}
	if narrowed.Act == delegator {
		t.Error("narrowed act aliases the subject's act claim")
	}
	if narrowed.DelegationDepth != 1 || narrowed.Typ != TypeA2ADelegation {
		t.Errorf("narrowed depth %d typ %s, want 1 a2a_delegation", narrowed.DelegationDepth, narrowed.Typ)
	}

	rotated, err := exchangeOf(child, child, nil, nil)
	if err != nil {
		t.Fatalf("rotating a delegation child should succeed, got %v", err)
	}
	if !reflect.DeepEqual(rotated.Act, delegator) {
		t.Errorf("rotated act = %+v, want %+v", rotated.Act, delegator)
	}

	plain, err := exchangeOf(validSubject(), validSubject(), nil, nil)
	if err != nil {
		t.Fatalf("rotation should succeed, got %v", err)
	}
	if plain.Act != nil {
		t.Errorf("act = %+v on an exchange of a subject without act, want none", plain.Act)
	}

	actor := validSubject()
	actor.Subject = "carol@acme.com"
	actor.Scope = nil
	minted, err := exchangeOf(child, actor, &actor, []string{"tools:sessions:read"})
	if err != nil {
		t.Fatalf("child mint should succeed, got %v", err)
	}
	if minted.Act == nil || minted.Act.Subject != "carol@acme.com" {
		t.Errorf("child act = %+v, want the actor carol", minted.Act)
	}
}

// spec: §13.3 (Credential Flow)
//
// A child's delegation_depth is its actor's plus one whatever the subject's
// depth (the scenario CX-8 of change 0165's model): a child minted from a
// depth-2 subject with a depth-0 actor has depth 1, and rule (b) does not
// refuse it, because it compares a child with its actor.
func TestValidateChildDepthFollowsTheActor(t *testing.T) {
	subject := validSubject()
	subject.Typ = TypeA2ADelegation
	subject.DelegationDepth = 2
	actor := validSubject()
	actor.Scope = nil
	got, err := exchangeOf(subject, actor, &actor, nil)
	if err != nil {
		t.Fatalf("child mint should succeed, got %v", err)
	}
	if got.DelegationDepth != 1 {
		t.Errorf("child depth = %d, want the actor's depth plus one, 1", got.DelegationDepth)
	}
}

// spec: §13.3 (Credential Flow)
//
// A tools:<domain>:* value grants more than the list of its domain's current
// actions, so another caller's request that lists every current action of a
// domain the subject holds by wildcard is strictly narrower and accepted (the
// scenario CX-7 of change 0165's model).
func TestValidateTheListedActionsOfAWildcardAreStrictlyNarrower(t *testing.T) {
	subject := validSubject()
	subject.Scope = []string{"tools:sessions:*", "tools:me:write"}
	caller := validSubject()
	caller.CallerType = CallerAgent
	requested := []string{"tools:sessions:read", "tools:sessions:write", "tools:sessions:scale", "tools:me:write"}
	got, err := exchangeOf(subject, caller, nil, requested)
	if err != nil {
		t.Fatalf("listing a wildcard's actions should be strictly narrower, got %v", err)
	}
	if !reflect.DeepEqual(got.Scope, requested) {
		t.Errorf("issued scope = %v, want %v", got.Scope, requested)
	}
}

// spec: §13.3 (Credential Flow)
//
// A subject without an aud claim grants the default audience lenny-gateway
// and no other, so it can be exchanged for a lenny-gateway token and an
// exchange naming another audience is refused with audience_broadened.
func TestValidateASubjectWithoutAudGrantsTheDefaultAudience(t *testing.T) {
	subject := validSubject()
	subject.Audience = nil
	for _, c := range []struct {
		audience []string
		wantErr  bool
	}{
		{[]string{DefaultAudience}, false},
		{[]string{"lenny-ops"}, true},
	} {
		_, err := Validate(Request{
			Subject: subject, Caller: subject,
			Requested:    Token{Audience: c.audience},
			RequestedExp: now.Add(time.Minute), Now: now,
		})
		if c.wantErr {
			wantRefusal(t, err, "invalid_request", "audience_broadened")
			continue
		}
		if err != nil {
			t.Errorf("audience %v: err = %v, want nil", c.audience, err)
		}
	}
	if got := SubjectAudience([]string{"lenny-ops"}); !reflect.DeepEqual(got, []string{"lenny-ops"}) {
		t.Errorf("SubjectAudience of a subject with aud = %v, want its own aud", got)
	}
}
