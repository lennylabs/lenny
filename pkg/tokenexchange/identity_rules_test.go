// SPDX-License-Identifier: MIT

package tokenexchange

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/common/scopes"
)

// exchangeOf validates an exchange of subject by caller requesting scope.
func exchangeOf(subject, caller Token, actor *Token, scope []string) (Issued, error) {
	return Validate(Request{
		Subject:      subject,
		Actor:        actor,
		Caller:       caller,
		Requested:    Token{Scope: scope, Audience: []string{"lenny-gateway"}},
		RequestedExp: now.Add(30 * time.Minute),
		Now:          now,
	})
}

// wantRefusal fails unless err is an ExchangeError with code and reason.
func wantRefusal(t *testing.T, err error, code, reason string) {
	t.Helper()
	var ee *ExchangeError
	if !errors.As(err, &ee) || ee.Code != code || ee.Reason != reason {
		t.Fatalf("err = %v, want %s %s", err, code, reason)
	}
}

// spec: §13.3 (Credential Flow), §25.1 (Design Philosophy and Agent Model)
//
// The subject's scope grants a requested value under the scope grammar: an
// absent claim and tools:* grant every canonical value, tools:<domain>:*
// grants every action of its domain, and any other value grants itself. A
// requested value outside the grant, a non-canonical value even when the
// subject carries it literally, and any explicit request from a subject
// whose scope does not parse are refused with invalid_scope.
func TestValidateNarrowsByTheScopeGrammar(t *testing.T) {
	cases := []struct {
		name      string
		subject   []string
		requested []string
		wantOK    bool
	}{
		{"absent claim to a specific scope", nil, []string{"tools:health:read"}, true},
		{"absent claim to a domain wildcard and a scope", nil, []string{"tools:health:*", "tools:diagnostics:read"}, true},
		{"absent claim to tools:*", nil, []string{"tools:*"}, true},
		{"absent claim to a non-canonical value", nil, []string{"sessions:read"}, false},
		{"absent claim to an unknown domain", nil, []string{"tools:nope:read"}, false},
		{"tools:* to a specific scope", []string{"tools:*"}, []string{"tools:pool:scale"}, true},
		{"domain wildcard to an action of the domain", []string{"tools:pool:*"}, []string{"tools:pool:read", "tools:pool:write"}, true},
		{"domain wildcard to itself", []string{"tools:pool:*"}, []string{"tools:pool:*"}, true},
		{"domain wildcard to another domain", []string{"tools:pool:*"}, []string{"tools:health:read"}, false},
		{"domain wildcard to tools:*", []string{"tools:pool:*"}, []string{"tools:*"}, false},
		{"specific scope to its domain wildcard", []string{"tools:pool:read"}, []string{"tools:pool:*"}, false},
		{"specific scope to another action", []string{"tools:pool:read"}, []string{"tools:pool:write"}, false},
		{"literal non-canonical value", []string{"sessions:read"}, []string{"sessions:read"}, false},
		{"unparseable subject to a canonical value", []string{"sessions:read"}, []string{"tools:health:read"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			subject := validSubject()
			subject.Scope = c.subject
			got, err := exchangeOf(subject, subject, nil, c.requested)
			if !c.wantOK {
				wantRefusal(t, err, "invalid_scope", "scope_not_subset")
				return
			}
			if err != nil {
				t.Fatalf("exchange should succeed, got %v", err)
			}
			if !reflect.DeepEqual(got.Scope, c.requested) {
				t.Errorf("issued scope = %v, want %v", got.Scope, c.requested)
			}
		})
	}
}

// spec: §13.3 (Credential Flow)
//
// A request without scope from a subject whose scope does not parse still
// issues the subject's scope, so the default never broadens the subject.
func TestValidateDefaultScopeOfAnUnparseableSubject(t *testing.T) {
	subject := validSubject()
	subject.Scope = []string{"sessions:read"}
	got, err := exchangeOf(subject, subject, nil, nil)
	if err != nil {
		t.Fatalf("exchange should succeed, got %v", err)
	}
	if !reflect.DeepEqual(got.Scope, subject.Scope) {
		t.Errorf("issued scope = %v, want %v", got.Scope, subject.Scope)
	}
}

// spec: §13.3 (Credential Flow)
//
// A playground-origin subject or actor is refused with invalid_grant
// before any other narrowing rule, whatever the caller and the scope.
func TestValidateRefusesPlaygroundSources(t *testing.T) {
	playground := validSubject()
	playground.Origin = OriginPlayground
	plain := validSubject()

	_, err := exchangeOf(playground, plain, nil, []string{"tools:sessions:read"})
	wantRefusal(t, err, "invalid_grant", "subject_token_playground")

	actor := playground
	actor.Typ = TypeSessionCapability
	_, err = exchangeOf(plain, plain, &actor, []string{"tools:sessions:read"})
	wantRefusal(t, err, "invalid_grant", "actor_token_playground")

	other := validSubject()
	other.Origin = "web"
	if _, err := exchangeOf(other, plain, nil, []string{"tools:sessions:read"}); err != nil {
		t.Errorf("non-playground origin refused: %v", err)
	}
}

// narrowCase is one exchange of TestValidateADifferentCallerMustNarrow.
type narrowCase struct {
	name         string
	caller       Token
	subjectScope []string
	requested    []string
	wantOK       bool
}

// spec: §13.3 (Credential Flow)
//
// A caller whose sub differs from the subject's, or whose caller_type
// differs when both carry one, must request a strictly narrower scope.
// An equal scope, a broader-reading equal set, no scope, and tools:* from
// an unscoped subject are refused with invalid_scope scope_not_narrowed;
// a strictly narrower set is accepted. A request that lists every domain
// wildcard grants what tools:* grants and is not narrower than an unscoped
// or tools:* subject. A caller without caller_type and
// with the subject's sub is the subject's principal.
func TestValidateADifferentCallerMustNarrow(t *testing.T) {
	bob := validSubject()
	bob.Subject = "bob@acme.com"
	agent := validSubject()
	agent.CallerType = CallerAgent
	noType := validSubject()
	noType.CallerType = ""
	full := []string{"tools:sessions:read", "tools:sessions:write"}

	cases := []narrowCase{
		{"other sub, equal scope", bob, full, full, false},
		{"other sub, equal scope reordered", bob, full, []string{"tools:sessions:write", "tools:sessions:read"}, false},
		{"other sub, no scope", bob, full, nil, false},
		{"other sub, narrower scope", bob, full, []string{"tools:sessions:read"}, true},
		{"other sub, domain wildcard subject to the wildcard", bob, []string{"tools:pool:*"}, []string{"tools:pool:*"}, false},
		{"other sub, domain wildcard subject to one action", bob, []string{"tools:pool:*"}, []string{"tools:pool:read"}, true},
		{"other sub, unscoped subject to tools:*", bob, nil, []string{"tools:*"}, false},
		{"other sub, unscoped subject to a scope", bob, nil, []string{"tools:health:read"}, true},
		{"other caller_type, equal scope", agent, full, full, false},
		{"other caller_type, narrower scope", agent, full, []string{"tools:sessions:write"}, true},
		{"no caller_type, same sub, equal scope", noType, full, full, true},
		{"no caller_type, same sub, no scope", noType, full, nil, true},
	}
	everyDomain := make([]string, 0, len(scopes.Domains))
	for domain := range scopes.Domains {
		everyDomain = append(everyDomain, "tools:"+domain+":*")
	}
	allButOne := everyDomain[1:]
	cases = append(
		cases,
		narrowCase{"other sub, unscoped subject to every domain wildcard", bob, nil, everyDomain, false},
		narrowCase{"other sub, tools:* subject to every domain wildcard", bob, []string{"tools:*"}, everyDomain, false},
		narrowCase{"other sub, unscoped subject to all but one domain wildcard", bob, nil, allButOne, true},
	)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			subject := validSubject()
			subject.Scope = c.subjectScope
			_, err := exchangeOf(subject, c.caller, nil, c.requested)
			if !c.wantOK {
				wantRefusal(t, err, "invalid_scope", "scope_not_narrowed")
				return
			}
			if err != nil {
				t.Fatalf("exchange should succeed, got %v", err)
			}
		})
	}
}

// spec: §13.3 (Credential Flow)
//
// A child mint whose subject is another user's token must name a scope
// strictly narrower than the subject's: the subject's own scope and an
// absent scope are refused with invalid_scope, and a narrower scope is
// accepted and carries the subject's identity at that scope.
func TestValidateChildMintForAnotherUserMustNarrow(t *testing.T) {
	subject := validSubject()
	bob := validSubject()
	bob.Subject = "bob@acme.com"
	bob.CallerType = CallerAgent
	bob.Typ = TypeSessionCapability
	for _, c := range []struct {
		name      string
		requested []string
	}{
		{"no scope", nil},
		{"the subject scope", subject.Scope},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := exchangeOf(subject, bob, &bob, c.requested)
			wantRefusal(t, err, "invalid_scope", "scope_not_narrowed")
		})
	}
	got, err := exchangeOf(subject, bob, &bob, []string{"tools:sessions:read"})
	if err != nil {
		t.Fatalf("narrower child mint should succeed, got %v", err)
	}
	if got.Subject != subject.Subject || !reflect.DeepEqual(got.Scope, []string{"tools:sessions:read"}) {
		t.Errorf("child = sub %q scope %v, want alice at tools:sessions:read", got.Subject, got.Scope)
	}
}

// spec: §13.3 (Credential Flow)
//
// The actor token of a child mint must belong to the caller: a caller that
// presents another principal's token as the actor is refused with
// invalid_grant, whatever scope it requests and whoever the subject is.
func TestValidateChildMintRefusesAnActorThatIsNotTheCaller(t *testing.T) {
	subject := validSubject()
	bob := validSubject()
	bob.Subject = "bob@acme.com"
	bob.Typ = TypeSessionCapability
	carol := validSubject()
	carol.Subject = "carol@acme.com"
	for _, c := range []struct {
		name      string
		caller    Token
		actor     Token
		requested []string
	}{
		{"another user's actor", carol, bob, []string{"tools:sessions:read"}},
		{"the subject's actor presented by another user", carol, subject, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := exchangeOf(subject, c.caller, &c.actor, c.requested)
			wantRefusal(t, err, "invalid_grant", "actor_token_not_caller")
		})
	}
}

// spec: §13.3 (Credential Flow)
//
// A parent session minting a child for its own user is unaffected: when
// the caller, the actor, and the subject share a sub, a request without
// scope mints the child at the subject's scope.
func TestValidateChildMintForTheActorsOwnUserIsUnaffected(t *testing.T) {
	subject := validSubject()
	session := validSubject()
	session.Typ = TypeSessionCapability
	got, err := exchangeOf(subject, session, &session, nil)
	if err != nil {
		t.Fatalf("child mint should succeed, got %v", err)
	}
	if got.Typ != TypeA2ADelegation || !reflect.DeepEqual(got.Scope, subject.Scope) {
		t.Errorf("child = typ %q scope %v, want a2a_delegation at the subject scope", got.Typ, got.Scope)
	}
}

// spec: §13.3 (Credential Flow)
//
// A child mint keeps the authorized_tools entries the issued scope grants:
// every entry when the issued scope is absent, the entries of a domain
// wildcard, and none when the issued scope does not parse.
func TestValidateChildAllowlistFollowsTheIssuedScopeGrant(t *testing.T) {
	tools := []string{"tools:pool:read", "tools:pool:scale", "tools:health:read"}
	cases := []struct {
		name         string
		subjectScope []string
		requested    []string
		want         []string
	}{
		{"absent issued scope keeps every entry", nil, nil, tools},
		{"domain wildcard keeps its domain", nil, []string{"tools:pool:*"}, []string{"tools:pool:read", "tools:pool:scale"}},
		{"tools:* keeps every entry", []string{"tools:*"}, nil, tools},
		{"specific scope keeps itself", nil, []string{"tools:health:read"}, []string{"tools:health:read"}},
		{"unparseable issued scope keeps none", []string{"sessions:read"}, nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			subject := validSubject()
			subject.Scope = c.subjectScope
			subject.AuthorizedTools = tools
			actor := validSubject()
			actor.Typ = TypeSessionCapability
			got, err := exchangeOf(subject, subject, &actor, c.requested)
			if err != nil {
				t.Fatalf("child mint should succeed, got %v", err)
			}
			if !reflect.DeepEqual(got.AuthorizedTools, c.want) {
				t.Errorf("authorized_tools = %v, want %v", got.AuthorizedTools, c.want)
			}
		})
	}
}
