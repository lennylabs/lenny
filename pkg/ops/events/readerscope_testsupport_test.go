// SPDX-License-Identifier: MIT

package events

import (
	"net/http"
)

// platformAdminReq returns r with the §25.5 read-caller scope of a
// platform-admin on its context, mirroring what the opsserver route boundary
// threads onto every authenticated read request. The read handlers fail closed
// on a context that carries no resolved scope, so a test that means to observe
// the whole event window states that grant explicitly rather than relying on an
// absent authorization decision. spec: §25.5 (read-endpoint tenant filter).
func platformAdminReq(r *http.Request) *http.Request {
	return r.WithContext(WithReaderScope(r.Context(), "alice@acme.com", "", true))
}
