// SPDX-License-Identifier: MIT

package pipeline

import (
	"context"
	"slices"

	"github.com/lennylabs/lenny/pkg/auth/authmw"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
)

// Callers serves seam.CallerIdentity, the caller identity the API front
// answers for the platform tools seam, from the principal the
// authentication middleware, or the platform tools bridge, attached to the
// context. The
// zero value is ready for use.
type Callers struct{}

var _ seam.CallerIdentity = Callers{}

// RequestCaller returns the caller the context's principal names.
// spec: GWSEAM-RequestCaller
func (Callers) RequestCaller(ctx context.Context) (seam.Caller, bool) {
	p, ok := authmw.FromContext(ctx)
	if !ok {
		return seam.Caller{}, false
	}
	return seam.Caller{
		Subject:    p.Subject,
		TenantID:   p.TenantID,
		SessionID:  p.SessionID,
		CallerType: p.CallerType,
		Groups:     slices.Clone(p.Groups),
		JTI:        p.JTI,
		Origin:     p.Origin,
	}, true
}
