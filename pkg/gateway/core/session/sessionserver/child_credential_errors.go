// SPDX-License-Identifier: MIT

package sessionserver

import (
	"errors"

	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/credrouter"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
)

// childCredentialError adds to a delegated child's materialization failure
// the seam sentinel of its credential cause, so the platform tools seam
// branches on the sentinel without importing the credential packages GW-R2
// confines: credrouter's exhausted pool and missing user credential, and
// credassign's Token Service outage. The message and the chain are the
// core's error unchanged (withSentinel). Any other error, nil included, is
// returned unchanged.
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
	return withSentinel(sentinel, err.Error(), err)
}
