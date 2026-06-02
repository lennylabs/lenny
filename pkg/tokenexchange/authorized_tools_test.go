// SPDX-License-Identifier: MIT

package tokenexchange

import (
	"errors"
	"testing"
	"time"
)

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// §13.3 line 580: a rotation that requests no authorized_tools narrowing
// preserves the subject's allowlist onto the issued token rather than
// dropping it (the F-13.3.11 defect).
func TestValidatePreservesAuthorizedTools_spec_13_3_580(t *testing.T) {
	subject := validSubject()
	subject.AuthorizedTools = []string{"tools:sessions:read", "tools:sessions:write"}
	got, err := Validate(Request{
		Subject:      subject,
		Caller:       subject,
		Requested:    Token{Scope: []string{"sessions:read"}, Audience: []string{"lenny-gateway"}},
		RequestedExp: now.Add(30 * time.Minute),
		Now:          now,
	})
	if err != nil {
		t.Fatalf("exchange should succeed: %v", err)
	}
	if !equalStrings(got.AuthorizedTools, subject.AuthorizedTools) {
		t.Errorf("authorized_tools preserved; want %v, got %v", subject.AuthorizedTools, got.AuthorizedTools)
	}
}

// §13.3 line 583(e): an exchange may further narrow authorized_tools to a
// subset of the subject's.
func TestValidateNarrowsAuthorizedTools_spec_13_3_583(t *testing.T) {
	subject := validSubject()
	subject.AuthorizedTools = []string{"tools:sessions:read", "tools:sessions:write"}
	got, err := Validate(Request{
		Subject: subject,
		Caller:  subject,
		Requested: Token{
			Scope:           []string{"sessions:read"},
			Audience:        []string{"lenny-gateway"},
			AuthorizedTools: []string{"tools:sessions:read"}, // narrower
		},
		RequestedExp: now.Add(30 * time.Minute),
		Now:          now,
	})
	if err != nil {
		t.Fatalf("exchange should succeed: %v", err)
	}
	if !equalStrings(got.AuthorizedTools, []string{"tools:sessions:read"}) {
		t.Errorf("authorized_tools narrowed; want [tools:sessions:read], got %v", got.AuthorizedTools)
	}
}

// §13.3 line 580: broadening authorized_tools (requesting a tool the
// subject did not hold) is rejected.
func TestValidateRejectsAuthorizedToolsBroadening_spec_13_3_580(t *testing.T) {
	subject := validSubject()
	subject.AuthorizedTools = []string{"tools:sessions:read"}
	_, err := Validate(Request{
		Subject: subject,
		Caller:  subject,
		Requested: Token{
			Scope:           []string{"sessions:read"},
			Audience:        []string{"lenny-gateway"},
			AuthorizedTools: []string{"tools:sessions:read", "tools:sessions:delete"}, // broadened
		},
		RequestedExp: now.Add(30 * time.Minute),
		Now:          now,
	})
	var ee *ExchangeError
	if !errors.As(err, &ee) || ee.Reason != "authorized_tools_broadened" {
		t.Fatalf("want authorized_tools_broadened rejection, got %v", err)
	}
}

// §13.3 line 583(e): a child-minting exchange (actor_token present) copies
// the parent's authorized_tools intersected with the issued scope, so a
// tool whose scope the child did not inherit is dropped from the allowlist.
func TestValidateChildMintingIntersectsAuthorizedToolsWithScope_spec_13_3_583(t *testing.T) {
	subject := validSubject()
	subject.Scope = []string{"tools:sessions:read", "tools:sessions:write", "tools:sessions:admin"}
	subject.AuthorizedTools = []string{"tools:sessions:read", "tools:sessions:write", "tools:sessions:admin"}
	actor := validSubject()
	actor.Typ = TypeSessionCapability
	actor.DelegationDepth = 1
	got, err := Validate(Request{
		Subject: subject,
		Actor:   &actor,
		Caller:  subject,
		Requested: Token{
			// Child scope drops the admin tool — it is not in the issued
			// scope, so it must be dropped from authorized_tools too.
			Scope:    []string{"tools:sessions:read", "tools:sessions:write"},
			Audience: []string{"lenny-gateway"},
		},
		RequestedExp: now.Add(30 * time.Minute),
		Now:          now,
	})
	if err != nil {
		t.Fatalf("child-minting should succeed: %v", err)
	}
	want := []string{"tools:sessions:read", "tools:sessions:write"}
	if !equalStrings(got.AuthorizedTools, want) {
		t.Errorf("child authorized_tools ∩ scope; want %v, got %v", want, got.AuthorizedTools)
	}
}

// An empty subject allowlist with no request stays empty (operability
// surface has nothing to enforce, fail-closed).
func TestValidateEmptyAuthorizedToolsStaysEmpty_spec_13_3_580(t *testing.T) {
	subject := validSubject()
	got, err := Validate(Request{
		Subject:      subject,
		Caller:       subject,
		Requested:    Token{Scope: []string{"sessions:read"}, Audience: []string{"lenny-gateway"}},
		RequestedExp: now.Add(30 * time.Minute),
		Now:          now,
	})
	if err != nil {
		t.Fatalf("exchange should succeed: %v", err)
	}
	if len(got.AuthorizedTools) != 0 {
		t.Errorf("empty subject allowlist preserved empty; got %v", got.AuthorizedTools)
	}
}
