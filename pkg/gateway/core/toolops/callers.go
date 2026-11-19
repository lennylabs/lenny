// SPDX-License-Identifier: MIT

package toolops

import (
	"context"
	"errors"
	"time"

	"github.com/lennylabs/lenny/pkg/auth/authmw"
	"github.com/lennylabs/lenny/pkg/auth/callercheck"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
)

// CallerCheck serves seam.CallerChecks over the session-creation caller
// check. Its zero value checks nothing, as a zero callercheck.Deps does.
type CallerCheck struct {
	deps callercheck.Deps
}

var _ seam.CallerChecks = CallerCheck{}

// NewCallerCheck returns the caller check over deps, the issued-token
// reader and the auditor of the session-creation caller check.
func NewCallerCheck(deps callercheck.Deps) CallerCheck {
	return CallerCheck{deps: deps}
}

// CheckCreatingCaller checks the bearer of the context's principal. A
// context with no principal passes, and a refusal comes back as a
// *seam.CallerRefusal once its rejection row is durable.
// spec: GWSEAM-CheckCreatingCaller
func (c CallerCheck) CheckCreatingCaller(ctx context.Context, at time.Time) error {
	principal, ok := authmw.FromContext(ctx)
	if !ok {
		return nil
	}
	err := callercheck.Check(ctx, c.deps, principal, at)
	if err == nil {
		return nil
	}
	var refused *callercheck.Refused
	if errors.As(err, &refused) {
		return &seam.CallerRefusal{Reason: refused.Reason}
	}
	return err
}
