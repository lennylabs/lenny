// SPDX-License-Identifier: MIT

package sessionserver

import (
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	idemmw "github.com/lennylabs/lenny/pkg/gateway/middleware/idempotency"
)

// spec: §10.3 (mTLS PKI) NET-072
//
// TestVerifyGatewayPeerRefusesAConnectionWithoutAPeerCertificate_spec_10_3
// asserts that the CH-HOLDERFORWARD listener's peer check fails closed when
// the connection presents no peer certificate at all.
func TestVerifyGatewayPeerRefusesAConnectionWithoutAPeerCertificate_spec_10_3(t *testing.T) {
	if err := verifyGatewayPeer(tls.ConnectionState{}); !errors.Is(err, errPeerNotGateway) {
		t.Fatalf("verifyGatewayPeer(no peer certificate) = %v, want errPeerNotGateway", err)
	}
}

// spec: §11.5 (Idempotency), §29.3 (Interactive message send) Forwarding
// rule 7
//
// TestForwardedMarkerFlushCarriesTheNoStoreMark_spec_29_3 asserts that a
// forwarded streamed response whose first byte reaches the forwarding
// replica through a flush, before any explicit header write, still carries
// the no-store mark when the handler set the gate, that the flush reaches the
// underlying writer, and that http.ResponseController reaches the underlying
// writer through Unwrap.
func TestForwardedMarkerFlushCarriesTheNoStoreMark_spec_29_3(t *testing.T) {
	for _, tc := range []struct {
		name     string
		gate     bool
		wantMark bool
	}{
		{"a set gate marks the flushed response", true, true},
		{"an unset gate leaves the flushed response unmarked", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var ctlErr error
			h := forwardedMarker{inner: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.gate {
					idemmw.DoNotStore(r.Context())
				}
				w.(http.Flusher).Flush()
				ctlErr = http.NewResponseController(w).Flush()
			})}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/sessions/s/messages", nil))
			if !rec.Flushed || rec.Code != http.StatusOK {
				t.Errorf("flushed = %v status = %d, want a flushed 200", rec.Flushed, rec.Code)
			}
			if got := rec.Header().Get(forwardNoStoreHeader) != ""; got != tc.wantMark {
				t.Errorf("no-store mark present = %v, want %v", got, tc.wantMark)
			}
			if ctlErr != nil {
				t.Errorf("ResponseController flush through Unwrap: %v", ctlErr)
			}
		})
	}
}
