// SPDX-License-Identifier: MIT

package admin_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/api/admin"
	"github.com/lennylabs/lenny/pkg/tenancy/tenantstore"
)

type receiptKeysBody struct {
	Keys []struct {
		KeyID string `json:"keyId"`
		Alg   string `json:"alg"`
		JWK   struct {
			Kty, Crv, X, Y, Kid, Alg, Use string
		} `json:"jwk"`
	} `json:"keys"`
}

func getReceiptKeys(t *testing.T, router *admin.Router, wrap func(*http.Request) *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, wrap(httptest.NewRequest(http.MethodGet, "/v1/admin/audit-receipt-keys", nil)))
	return rr
}

// TestReceiptKeysRouteServesKeysAnExternalVerifierCanUse pins that the
// route lists every trusted receipt key under the KMS key identifier a
// receipt records, and that a verifier holding only the served JWK checks
// a receipt signature with it.
//
// spec: §12.8 (Compliance Interfaces), §15.1 (REST API)
func TestReceiptKeysRouteServesKeysAnExternalVerifierCanUse(t *testing.T) {
	signing, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	previous, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := audit.NewReceiptKeySet(
		audit.ReceiptKey{KeyID: "audit-key-v2", PublicKey: &signing.PublicKey},
		audit.ReceiptKey{KeyID: "audit-key-v1", PublicKey: &previous.PublicKey},
	)
	if err != nil {
		t.Fatal(err)
	}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{}).WithReceiptKeys(keys)

	rr := getReceiptKeys(t, router, withTenantAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var body receiptKeysBody
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Keys) != 2 || body.Keys[0].KeyID != "audit-key-v1" || body.Keys[1].KeyID != "audit-key-v2" {
		t.Fatalf("keys = %+v, want audit-key-v1 then audit-key-v2", body.Keys)
	}
	served := body.Keys[1]
	if served.Alg != "ES256" || served.JWK.Kty != "EC" || served.JWK.Crv != "P-256" ||
		served.JWK.Kid != "audit-key-v2" || served.JWK.Alg != "ES256" || served.JWK.Use != "sig" {
		t.Fatalf("served entry = %+v", served)
	}

	// An external verifier rebuilds the key from the JWK alone and
	// checks a receipt the signing key signed.
	x, _ := base64.RawURLEncoding.DecodeString(served.JWK.X)
	y, _ := base64.RawURLEncoding.DecodeString(served.JWK.Y)
	pub := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
	input, err := audit.ReceiptSigningInput(audit.RedactionReceipt{
		ReceiptID: "r", AuditEventID: "a", TenantID: "acme", RedactedSeq: 1,
		OriginalHash: "0a", NewHash: "0b", ErasureJobID: "j", LegalBasis: "gdpr_art17",
		RedactorIdentity: "erasure-job:j", Timestamp: time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(input)
	r, s, err := ecdsa.Sign(rand.Reader, signing, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	if !ecdsa.Verify(pub, digest[:], r, s) {
		t.Fatal("the served JWK does not verify a signature of the signing key")
	}
}

// TestReceiptKeysRouteRequiresAnAuditReader pins the role gate: a caller
// with neither platform-admin nor tenant-admin is refused, and a router
// with no key set serves an empty list.
//
// spec: §15.1 (REST API)
func TestReceiptKeysRouteRequiresAnAuditReader(t *testing.T) {
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{})
	if rr := getReceiptKeys(t, router, withUserPrincipal); rr.Code != http.StatusForbidden {
		t.Errorf("user principal: status %d, want 403", rr.Code)
	}
	rr := getReceiptKeys(t, router, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("platform-admin: status %d: %s", rr.Code, rr.Body.String())
	}
	var body receiptKeysBody
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Keys == nil || len(body.Keys) != 0 {
		t.Errorf("keys = %+v, want an empty list", body.Keys)
	}
}
