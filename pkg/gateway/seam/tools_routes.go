// SPDX-License-Identifier: MIT

package seam

import "context"

// ActiveCallers is served by the session core to the platform tools seam's
// client routes: the check that the user a request runs as may act, which
// the session core's user lifecycle owns.
type ActiveCallers interface {
	// AdmitActiveCaller is GWSEAM-AdmitActiveCaller. action is the clause
	// the refusal's message names, such as "cannot resolve interactions".
	// It returns nil for a caller that may act, or that carries no user;
	// a *Refusal with the code USER_INVALIDATED or ERASURE_IN_PROGRESS for
	// one that may not; or a transient error when the user cannot be read.
	AdmitActiveCaller(ctx context.Context, action string) error
}
