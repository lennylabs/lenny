// SPDX-License-Identifier: MIT

package toolops

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/workspaceplan/vcscred"
)

// stubResolver answers every resolution with cred and err, and records the
// last request.
type stubResolver struct {
	cred                       vcscred.Credential
	err                        error
	tenant, gitURL, leaseScope string
}

func (s *stubResolver) Resolve(_ context.Context, tenantID, gitURL, leaseScope string) (vcscred.Credential, error) {
	s.tenant, s.gitURL, s.leaseScope = tenantID, gitURL, leaseScope
	return s.cred, s.err
}

// TestIssueVCSTokenMapsTheResolver checks that a resolved credential is the
// token, that an ambiguous host, an unsupported host, and a pool with no
// usable credential come back as a VCSTokenError with the resolver's
// message and the host details, and that any other failure is returned as
// it came; no resolver issues no token operation.
// spec: GWSEAM-IssueVCSToken
func TestIssueVCSTokenMapsTheResolver(t *testing.T) {
	ctx := context.Background()
	if NewVCSTokens(nil) != nil {
		t.Fatal("NewVCSTokens(nil) is not a nil interface")
	}
	r := &stubResolver{cred: vcscred.Credential{Username: "x-access-token", Token: "ghs_x"}}
	tok, err := NewVCSTokens(r).IssueVCSToken(ctx, "acme", "https://github.com", "vcs.github.read")
	if err != nil || tok.Username != "x-access-token" || tok.Token != "ghs_x" {
		t.Fatalf("token = %+v, %v", tok, err)
	}
	if r.tenant != "acme" || r.gitURL != "https://github.com" || r.leaseScope != "vcs.github.read" {
		t.Errorf("resolver saw %q %q %q", r.tenant, r.gitURL, r.leaseScope)
	}

	ambiguous := &credentialpoolstore.VCSResolveError{Host: "github.com", Provider: "github", Reason: credentialpoolstore.VCSHostAmbiguous, MatchingPools: []string{"a", "b"}}
	unsupported := &credentialpoolstore.VCSResolveError{Host: "gitlab.com", Provider: "github", Reason: credentialpoolstore.VCSHostUnsupported}
	other := errors.New("pool store down")
	for _, tc := range []struct {
		name  string
		err   error
		want  seam.VCSTokenFailure
		pools int
		host  string
	}{
		{"ambiguous host", fmt.Errorf("resolve: %w", ambiguous), seam.VCSHostAmbiguous, 2, "github.com"},
		{"unsupported host", unsupported, seam.VCSHostUnsupported, 0, "gitlab.com"},
		{"no usable credential", vcscred.ErrNoUsableCredential, seam.VCSNoUsableCredential, 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewVCSTokens(&stubResolver{err: tc.err}).IssueVCSToken(ctx, "acme", "https://x", "vcs.github.read")
			var te *seam.VCSTokenError
			if !errors.As(err, &te) || te.Failure != tc.want || len(te.MatchingPools) != tc.pools || te.Host != tc.host {
				t.Fatalf("err = %#v, want failure %d", err, tc.want)
			}
			if err.Error() != tc.err.Error() {
				t.Errorf("message = %q, want the resolver's %q", err.Error(), tc.err.Error())
			}
		})
	}
	if _, err := NewVCSTokens(&stubResolver{err: other}).IssueVCSToken(ctx, "acme", "https://x", ""); !errors.Is(err, other) {
		t.Errorf("other failure = %v, want %v", err, other)
	}
}
