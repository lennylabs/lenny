// SPDX-License-Identifier: MIT

package impersonation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/credential/impersonation"
)

// spec: §13.3 (Credential Flow)
//
// An impersonation bearer expires no later than the caller's own bearer
// (the scenario CX-4 of change 0165's model): with a caller bearer that
// expires before the requested duration ends, the bearer's exp claim, the
// ticket, and the recorded row carry the caller's exp, truncated to whole
// seconds; with a later caller exp or none, they carry the requested
// duration. A caller bearer that has expired by the issue instant is
// refused before the started event is written.
func TestIssueBearerExpiresNoLaterThanTheCallerBearer(t *testing.T) {
	cases := []struct {
		name      string
		callerExp time.Time
		want      time.Time
	}{
		{"caller expires first", t0.Add(10*time.Minute + 500*time.Millisecond), t0.Add(10 * time.Minute)},
		{"duration ends first", t0.Add(2 * time.Hour), t0.Add(30 * time.Minute)},
		{"caller without exp", time.Time{}, t0.Add(30 * time.Minute)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, fixedClock(t0), time.Hour)
			req := issueReq()
			req.CallerExpiry = c.callerExp
			ticket, _, err := h.svc.Issue(context.Background(), req)
			if err != nil {
				t.Fatalf("Issue: %v", err)
			}
			if !ticket.ExpiresAt.Equal(c.want) || h.signer.claims.Expiry != c.want.Unix() {
				t.Errorf("ticket exp %v claim exp %d, want %v", ticket.ExpiresAt, h.signer.claims.Expiry, c.want)
			}
			recs := h.tokens.Records()
			if len(recs) != 1 || !recs[0].Token.ExpiresAt.Equal(c.want) {
				t.Errorf("recorded rows = %+v, want one expiring at %v", recs, c.want)
			}
		})
	}
	t.Run("caller already expired", func(t *testing.T) {
		h := newHarness(t, fixedClock(t0), time.Hour)
		req := issueReq()
		req.CallerExpiry = t0
		_, _, err := h.svc.Issue(context.Background(), req)
		if !errors.Is(err, impersonation.ErrIssuanceRefused) {
			t.Fatalf("err = %v, want ErrIssuanceRefused", err)
		}
		if _, ok := h.app.byType("admin.impersonation_started"); ok {
			t.Error("a refused issuance wrote the started event")
		}
		if h.signer.calls != 0 || len(h.tokens.Records()) != 0 {
			t.Error("a refused issuance signed or recorded a bearer")
		}
	})
}

// spec: §13.3 (Credential Flow)
//
// The impersonation row records the caller's own bearer in its
// impersonator link, which a recursive revocation follows to end the
// session, and keeps parent_jti empty so the row predicate still
// identifies it. A caller without a jti records no link.
func TestIssueRecordsTheImpersonatorLink(t *testing.T) {
	h := newHarness(t, fixedClock(t0), time.Hour)
	if _, _, err := h.svc.Issue(context.Background(), issueReq()); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	row := h.tokens.Records()[0].Token
	if row.ImpersonatorTenantID != "platform" || row.ImpersonatorJTI != "caller-jti" {
		t.Errorf("impersonator link = (%q, %q), want (platform, caller-jti)", row.ImpersonatorTenantID, row.ImpersonatorJTI)
	}
	if row.ParentJTI != "" || row.ActSubject != "admin@acme.com" {
		t.Errorf("row parent %q act_sub %q, want no parent and the admin", row.ParentJTI, row.ActSubject)
	}

	h = newHarness(t, fixedClock(t0), time.Hour)
	req := issueReq()
	req.CallerJTI = ""
	if _, _, err := h.svc.Issue(context.Background(), req); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	row = h.tokens.Records()[0].Token
	if row.ImpersonatorTenantID != "" || row.ImpersonatorJTI != "" {
		t.Errorf("impersonator link = (%q, %q), want none for a caller without jti", row.ImpersonatorTenantID, row.ImpersonatorJTI)
	}
}
