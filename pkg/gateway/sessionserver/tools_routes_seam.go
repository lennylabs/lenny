// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
)

// The methods below are the session core's side of the operations the
// platform tools seam's client routes call: the memory, interaction, and
// tree routes, which register through GWSEAM-HandleRoute and run behind the
// access checks of the API front's request pipeline.

// AdmitActiveCaller is GWSEAM-AdmitActiveCaller: the caller check the
// session creation route applies, for the user the request runs as.
//
// spec: GWSEAM-AdmitActiveCaller
func (t ToolsSeam) AdmitActiveCaller(ctx context.Context, action string) error {
	return t.s.activeUserRefusal(ctx, action)
}
