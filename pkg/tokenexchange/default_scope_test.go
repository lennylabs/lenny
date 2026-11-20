// SPDX-License-Identifier: MIT

package tokenexchange

import (
	"errors"
	"testing"
	"time"
)

// exchangeScope runs Validate for a rotation-style exchange from a subject
// carrying subjectScope with requested as the request's scope.
func exchangeScope(t *testing.T, subjectScope, requested []string) (Issued, error) {
	t.Helper()
	subject := validSubject()
	subject.Scope = subjectScope
	return Validate(Request{
		Subject:      subject,
		Caller:       subject,
		Requested:    Token{Scope: requested, Audience: []string{"lenny-gateway"}},
		RequestedExp: now.Add(30 * time.Minute),
		Now:          now,
	})
}

// spec: §13.3 (Credential Flow), §25.1 (Design Philosophy and Agent Model)
func TestValidateIssuesTheSubjectScopeWhenTheRequestNamesNone(t *testing.T) {
	cases := []struct {
		name      string
		subject   []string
		requested []string
		want      []string
	}{
		{
			name:    "narrowed subject without requested scope issues the subject scope",
			subject: []string{"tools:health:read", "sessions:read"},
			want:    []string{"tools:health:read", "sessions:read"},
		},
		{
			name:      "empty requested scope list issues the subject scope",
			subject:   []string{"tools:health:read"},
			requested: []string{},
			want:      []string{"tools:health:read"},
		},
		{
			name:    "subject without a scope claim issues no scope",
			subject: nil,
			want:    nil,
		},
		{
			name:      "explicit subset is issued unchanged",
			subject:   []string{"tools:health:read", "sessions:read"},
			requested: []string{"sessions:read"},
			want:      []string{"sessions:read"},
		},
		{
			name:      "explicit scope equal to the subject is issued unchanged",
			subject:   []string{"tools:health:read", "sessions:read"},
			requested: []string{"sessions:read", "tools:health:read"},
			want:      []string{"sessions:read", "tools:health:read"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := exchangeScope(t, tc.subject, tc.requested)
			if err != nil {
				t.Fatalf("exchange should succeed, got %v", err)
			}
			if !equalStrings(got.Scope, tc.want) {
				t.Errorf("issued scope: want %v, got %v", tc.want, got.Scope)
			}
		})
	}
}

// spec: §13.3 (Credential Flow)
func TestValidateRefusesARequestedScopeOutsideTheSubject(t *testing.T) {
	cases := []struct {
		name      string
		subject   []string
		requested []string
	}{
		{"one value outside a narrowed subject", []string{"tools:health:read"}, []string{"tools:health:read", "tools:pool:write"}},
		{"disjoint from a narrowed subject", []string{"tools:health:read"}, []string{"tools:pool:write"}},
		{"any value from a subject without a scope claim", nil, []string{"tools:health:read"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := exchangeScope(t, tc.subject, tc.requested)
			var ee *ExchangeError
			if !errors.As(err, &ee) || ee.Code != "invalid_scope" || ee.Reason != "scope_not_subset" {
				t.Fatalf("want invalid_scope scope_not_subset, got %v (issued %+v)", err, got)
			}
		})
	}
}

// spec: §13.3 (Credential Flow)
func TestValidateDefaultScopeIsACopyOfTheSubjectScope(t *testing.T) {
	subjectScope := []string{"tools:health:read", "sessions:read"}
	got, err := exchangeScope(t, subjectScope, nil)
	if err != nil {
		t.Fatalf("exchange should succeed, got %v", err)
	}
	got.Scope[0] = "tools:*"
	if subjectScope[0] != "tools:health:read" {
		t.Errorf("issued scope aliases the subject's: subject now %v", subjectScope)
	}
}

// spec: §13.3 (Credential Flow)
func TestValidateChildMintWithoutScopeIntersectsAuthorizedToolsWithTheSubjectScope(t *testing.T) {
	subject := validSubject()
	subject.Scope = []string{"tools:health:read", "tools:pool:read"}
	subject.AuthorizedTools = []string{"tools:health:read", "tools:pool:write"}
	actor := validSubject()
	actor.DelegationDepth = 1
	got, err := Validate(Request{
		Subject:      subject,
		Actor:        &actor,
		Caller:       subject,
		Requested:    Token{Audience: []string{"lenny-gateway"}},
		RequestedExp: now.Add(time.Minute),
		Now:          now,
	})
	if err != nil {
		t.Fatalf("child minting should succeed, got %v", err)
	}
	if want := []string{"tools:health:read", "tools:pool:read"}; !equalStrings(got.Scope, want) {
		t.Errorf("child scope: want %v, got %v", want, got.Scope)
	}
	if want := []string{"tools:health:read"}; !equalStrings(got.AuthorizedTools, want) {
		t.Errorf("child authorized_tools: want %v, got %v", want, got.AuthorizedTools)
	}
}
