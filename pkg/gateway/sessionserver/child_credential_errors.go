// SPDX-License-Identifier: MIT

package sessionserver

import (
	"errors"

	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/credrouter"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
)

// seamMatched is a failure of the session core that also matches the seam
// interface's sentinel for it, so the platform tools seam branches on the
// sentinel without importing the credential packages GW-R2 confines. Its
// message and chain are the core's error unchanged.
type seamMatched struct {
	err      error
	sentinel error
}

func (e *seamMatched) Error() string { return e.err.Error() }

// Unwrap returns the core's error and the seam sentinel.
func (e *seamMatched) Unwrap() []error { return []error{e.err, e.sentinel} }

// childCredentialError adds to a delegated child's materialization failure
// the seam sentinel of its credential cause: credrouter's exhausted pool and
// missing user credential, and credassign's Token Service outage. Any other
// error, nil included, is returned unchanged.
// spec: GWSEAM-MaterializeChild
func childCredentialError(err error) error {
	var sentinel error
	switch {
	case err == nil:
		return nil
	case errors.Is(err, credrouter.ErrNoCredentialAvailable):
		sentinel = seam.ErrChildCredentialUnavailable
	case errors.Is(err, credrouter.ErrUserCredentialNotFound):
		sentinel = seam.ErrChildUserCredentialNotFound
	case errors.Is(err, credassign.ErrTokenServiceUnavailable):
		sentinel = seam.ErrTokenServiceUnavailable
	default:
		return err
	}
	return &seamMatched{err: err, sentinel: sentinel}
}
