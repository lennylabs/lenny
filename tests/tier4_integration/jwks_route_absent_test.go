// SPDX-License-Identifier: MIT

//go:build integration

package tier4_integration_test

import (
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// spec: §10.2 (Authentication, "Verifier key set and rotation")
// diagnosis: the live cmd/lenny-gateway binary still mounted a JWK Set
// at /.well-known/jwks.json. No spec section defines that endpoint and
// every verifier loads its key set from the KMS at startup, so the
// gateway serves no route there and the request falls through to 404.
// Dev mode admits unauthenticated requests, so the status is the mux's
// answer rather than the auth middleware's 401.
func TestWellKnownJWKSPathNotServedByLiveGateway(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
	gw := gateway.StartWith(t, "--no-environment-policy", "allow-all", "--dev-mode")
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		req, err := http.NewRequest(method, gw.BaseURL()+"/.well-known/jwks.json", nil)
		if err != nil {
			t.Fatalf("build %s request: %v", method, err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s /.well-known/jwks.json: %v", method, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s /.well-known/jwks.json status = %d, want 404", method, resp.StatusCode)
		}
	}
}
