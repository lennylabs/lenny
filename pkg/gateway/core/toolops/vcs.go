// SPDX-License-Identifier: MIT

package toolops

import (
	"context"
	"errors"
	"slices"

	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/workspaceplan/vcscred"
)

// VCSTokens serves seam.VCSTokens over the VCS credential resolver the
// gateway's gitClone path also uses.
type VCSTokens struct {
	resolver vcscred.Resolver
}

var _ seam.VCSTokens = (*VCSTokens)(nil)

// NewVCSTokens returns the VCS token operation over resolver. It returns a
// nil interface for a nil resolver, so the platform tools seam registers no
// VCS token tool, as it does with no resolver wired.
func NewVCSTokens(resolver vcscred.Resolver) seam.VCSTokens {
	if resolver == nil {
		return nil
	}
	return &VCSTokens{resolver: resolver}
}

// IssueVCSToken resolves the tenant's VCS credential for gitURL under
// leaseScope. A host no pool serves, a host several pools serve, and a pool
// with no usable credential come back as a *seam.VCSTokenError carrying the
// resolver's message; any other failure is returned as it came.
// spec: GWSEAM-IssueVCSToken
func (v *VCSTokens) IssueVCSToken(ctx context.Context, tenantID, gitURL, leaseScope string) (seam.VCSToken, error) {
	cred, err := v.resolver.Resolve(ctx, tenantID, gitURL, leaseScope)
	if err != nil {
		return seam.VCSToken{}, vcsTokenError(err)
	}
	return seam.VCSToken{Username: cred.Username, Token: cred.Token}, nil
}

// vcsTokenError maps a resolver failure onto the seam's VCS token error.
func vcsTokenError(err error) error {
	var resolveErr *credentialpoolstore.VCSResolveError
	if errors.As(err, &resolveErr) {
		failure := seam.VCSHostUnsupported
		var pools []string
		if resolveErr.Reason == credentialpoolstore.VCSHostAmbiguous {
			failure = seam.VCSHostAmbiguous
			pools = slices.Clone(resolveErr.MatchingPools)
		}
		return &seam.VCSTokenError{
			Failure:       failure,
			Host:          resolveErr.Host,
			Provider:      resolveErr.Provider,
			MatchingPools: pools,
			Message:       err.Error(),
		}
	}
	if errors.Is(err, vcscred.ErrNoUsableCredential) {
		return &seam.VCSTokenError{Failure: seam.VCSNoUsableCredential, Message: err.Error()}
	}
	return err
}
