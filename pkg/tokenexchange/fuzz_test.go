// SPDX-License-Identifier: MIT

package tokenexchange

import (
	"testing"
	"time"
)

// FuzzValidateDoesNotPanic exercises the §13.3 RFC 8693 validator on
// arbitrary inputs. Invariant: Validate never panics. The
// success/error verdict is up to the implementation; fuzz only
// guards against crashes that would let a malformed exchange request
// take down the Token Service.
//
// The fuzz seed covers cross-tenant, empty, oversize, and
// scope-broadening shapes.
func FuzzValidateDoesNotPanic(f *testing.F) {
	f.Add("acme", "alice", "alice@acme.com", "alice", "alice@acme.com", "sessions:read", "lenny-gateway")
	f.Add("", "", "", "", "", "", "")
	f.Add("acme", "alice", "alice", "alice", "alice", string(make([]byte, 4096)), "aud")
	f.Add("acme/bad", "alice", "alice", "alice", "alice", "scope", "aud")

	f.Fuzz(func(t *testing.T,
		tenant, callerSub, callerEmail, subjectSub, subjectEmail, scope, audience string,
	) {
		caller := Token{
			TenantID: tenant,
			Subject:  callerSub,
			Scope:    []string{scope},
			Audience: []string{audience},
			Typ:      TypeUserBearer,
		}
		subject := Token{
			TenantID: tenant,
			Subject:  subjectSub,
			Scope:    []string{scope},
			Audience: []string{audience},
			Typ:      TypeUserBearer,
		}
		req := Request{
			Caller:        caller,
			Subject:       subject,
			Requested:     Token{TenantID: tenant, Subject: subjectSub, Typ: TypeUserBearer},
			RequestedExp:  time.Now().Add(time.Hour),
			PerDialectCap: time.Hour,
			Now:           time.Now(),
		}
		_, _ = Validate(req)
	})
}
