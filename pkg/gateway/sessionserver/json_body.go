// SPDX-License-Identifier: MIT

package sessionserver

import (
	"encoding/json"
	"net/http"
)

// MaxJSONBodyBytes is the platform cap on JSON request bodies for
// every endpoint that decodes JSON (create, derive, extend-retention,
// admin mutations). Spec §13.4 fixes the per-archive ceilings; this
// constant covers the smaller per-request control plane and
// matches the typical CRD admission body size. 1 MiB is well above
// realistic envelopes (a populous workspacePlan is ~32 KiB) while
// preventing memory-exhaustion DoS on the gateway.
const MaxJSONBodyBytes int64 = 1024 * 1024

// jsonReader returns r.Body wrapped in http.MaxBytesReader so JSON
// decoders see io.EOF / *http.MaxBytesError on oversize inputs
// before any allocation. Handlers using json.Decoder must wrap their
// body with this helper.
func jsonReader(w http.ResponseWriter, r *http.Request) interface {
	Read(p []byte) (int, error)
	Close() error
} {
	return http.MaxBytesReader(w, r.Body, MaxJSONBodyBytes)
}

// isJSONNull reports whether the supplied raw JSON is the literal
// `null` token (RFC 8259 §3) ignoring leading / trailing whitespace.
// Used to distinguish `{"workspacePlan": null}` from an omitted
// field.
func isJSONNull(raw json.RawMessage) bool {
	return string(raw) == "null"
}
