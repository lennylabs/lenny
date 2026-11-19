// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"net/http"
)

// The methods below are the session core's side of the operations the
// platform tools seam's client routes call: the memory, interaction, and
// tree routes, which register through GWSEAM-HandleRoute and run behind the
// session routes' access checks.

// AdmitActiveCaller is GWSEAM-AdmitActiveCaller: the caller check the
// session creation route applies, for the user the request runs as.
//
// spec: GWSEAM-AdmitActiveCaller
func (t ToolsSeam) AdmitActiveCaller(ctx context.Context, action string) error {
	return t.s.activeUserRefusal(ctx, action)
}

// RequestTenant is GWSEAM-RequestTenant as the session core resolves the
// tenant, which the test harnesses hand the platform tools seam; the
// composition root hands it the API front's pipeline.Identity.
//
// spec: GWSEAM-RequestTenant
func (t ToolsSeam) RequestTenant(r *http.Request) string {
	return t.s.requestSeamTenant(r)
}
