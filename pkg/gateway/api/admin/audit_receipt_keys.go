// SPDX-License-Identifier: MIT

package admin

import (
	"encoding/base64"
	"net/http"

	"github.com/lennylabs/lenny/pkg/audit"
)

// WithReceiptKeys wires the redaction receipt signing keys a chain verifier
// trusts. The audit-events chain walk verifies receipt signatures with
// them, and GET /v1/admin/audit-receipt-keys serves their public keys.
func (r *Router) WithReceiptKeys(keys *audit.ReceiptKeySet) *Router {
	r.receiptKeys = keys
	return r
}

// receiptVerifier returns the receipt verifier of the chain walk, or nil,
// which trusts no receipt, when no key set is wired. A nil
// *audit.ReceiptKeySet is not returned as a non-nil interface, so the
// chain's fail-closed nil check holds.
func (r *Router) receiptVerifier() audit.ReceiptVerifier {
	if r.receiptKeys == nil {
		return nil
	}
	return r.receiptKeys
}

// receiptKeyJWK is the RFC 7517 JSON Web Key form of a P-256 public key.
type receiptKeyJWK struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	Use string `json:"use"`
}

// receiptKeyEntry is one trusted receipt signing key: the KMS key
// identifier a receipt records in signature_kms_key_id, the algorithm, and
// the public key.
type receiptKeyEntry struct {
	KeyID string        `json:"keyId"`
	Alg   string        `json:"alg"`
	JWK   receiptKeyJWK `json:"jwk"`
}

// registerReceiptKeyRoutes registers GET /v1/admin/audit-receipt-keys
// behind the audit reader gate.
func (r *Router) registerReceiptKeyRoutes(mux *http.ServeMux) {
	mux.Handle("GET /v1/admin/audit-receipt-keys", r.requireAuditReader(http.HandlerFunc(r.handleListReceiptKeys)))
}

// handleListReceiptKeys serves the public keys that verify RedactionReceipt
// signatures, one entry per key the platform:audit-signing and
// platform:audit-verify aliases name, sorted by key identifier. The JWK's
// kid is the same KMS key identifier, so an external verifier selects the
// key a receipt names by its signature_kms_key_id. A router with no key set
// serves an empty list, under which no receipt verifies.
//
// spec: §12.8 (Compliance Interfaces), §15.1 (REST API)
func (r *Router) handleListReceiptKeys(w http.ResponseWriter, _ *http.Request) {
	keys := r.receiptKeys.Keys()
	out := make([]receiptKeyEntry, 0, len(keys))
	for _, k := range keys {
		raw, err := k.PublicKey.Bytes()
		if err != nil || len(raw) != 65 {
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "receipt public key is not an uncompressed P-256 point", nil)
			return
		}
		out = append(out, receiptKeyEntry{
			KeyID: k.KeyID,
			Alg:   "ES256",
			JWK: receiptKeyJWK{
				Kty: "EC",
				Crv: "P-256",
				X:   base64.RawURLEncoding.EncodeToString(raw[1:33]),
				Y:   base64.RawURLEncoding.EncodeToString(raw[33:65]),
				Kid: k.KeyID,
				Alg: "ES256",
				Use: "sig",
			},
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": out})
}
