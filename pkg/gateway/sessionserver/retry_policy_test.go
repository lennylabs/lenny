// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

// spec: §7.3 lines 377-393 — CreateSession accepts a client-supplied
// retryPolicy. The gateway clamps each field against the deployer caps
// and echoes the effective policy on the response. F-7.3.1.

func newCreateServerWithRetryCaps(t *testing.T, caps session.RetryPolicyCaps) *sessionserver.Server {
	t.Helper()
	clock := func() time.Time { return time.Date(2026, 5, 26, 10, 0, 0, 0, time.UTC) }
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("test-secret")})
	idFn := func() string { return "sess_rp" }
	return sessionserver.New(memstore.New(), sessionserver.Options{
		Clock:             clock,
		IDFunc:            idFn,
		UploadTokenIssuer: uploadtoken.NewIssuer(ring, clock),
		RetryPolicyCaps:   caps,
	})
}

func TestCreateSessionEchoesRetryPolicy_F_7_3_1(t *testing.T) {
	caps := session.RetryPolicyCaps{MaxRetries: 5, MaxSessionAgeSeconds: 7200, MaxResumeWindowSeconds: 900}
	srv := newCreateServerWithRetryCaps(t, caps)
	req := sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code",
		RetryPolicy: &session.RetryPolicy{
			Mode:                   session.RetryModeAutoThenClient,
			MaxRetries:             2,
			MaxSessionAgeSeconds:   3600,
			MaxResumeWindowSeconds: 600,
			RetryableFailures:      []string{"pod_evicted", "node_lost"},
		},
	}
	rr := createRequest(t, srv.Handler(), req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp sessionserver.CreateSessionResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.RetryPolicy == nil {
		t.Fatalf("retryPolicy missing from response")
	}
	if resp.RetryPolicy.MaxRetries != 2 {
		t.Errorf("MaxRetries = %d, want 2", resp.RetryPolicy.MaxRetries)
	}
	if resp.RetryPolicy.MaxSessionAgeSeconds != 3600 {
		t.Errorf("MaxSessionAgeSeconds = %d, want 3600", resp.RetryPolicy.MaxSessionAgeSeconds)
	}
	if len(resp.RetryPolicy.RetryableFailures) != 2 {
		t.Errorf("RetryableFailures = %v, want 2 entries", resp.RetryPolicy.RetryableFailures)
	}
}

// spec: §7.3 lines 377-393 — populated client fields clamp down to the
// deployer caps, and zero fields fall through to the cap.
func TestCreateSessionClampsAgainstDeployerCaps_F_7_3_1(t *testing.T) {
	caps := session.RetryPolicyCaps{MaxRetries: 2, MaxSessionAgeSeconds: 7200, MaxResumeWindowSeconds: 900}
	srv := newCreateServerWithRetryCaps(t, caps)
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code",
		RetryPolicy: &session.RetryPolicy{
			MaxRetries:             10,     // above cap, clamps to 2
			MaxSessionAgeSeconds:   100000, // above cap, clamps to 7200
			MaxResumeWindowSeconds: 0,      // unset, falls through to cap 900
		},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp sessionserver.CreateSessionResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.RetryPolicy == nil {
		t.Fatalf("retryPolicy missing")
	}
	if resp.RetryPolicy.MaxRetries != 2 {
		t.Errorf("MaxRetries = %d, want clamp to 2", resp.RetryPolicy.MaxRetries)
	}
	if resp.RetryPolicy.MaxSessionAgeSeconds != 7200 {
		t.Errorf("MaxSessionAgeSeconds = %d, want clamp to 7200", resp.RetryPolicy.MaxSessionAgeSeconds)
	}
	if resp.RetryPolicy.MaxResumeWindowSeconds != 900 {
		t.Errorf("MaxResumeWindowSeconds = %d, want fall-through to 900", resp.RetryPolicy.MaxResumeWindowSeconds)
	}
	// Mode should default to auto_then_client per §7.3 line 382.
	if resp.RetryPolicy.Mode != session.RetryModeAutoThenClient {
		t.Errorf("Mode = %q, want %q", resp.RetryPolicy.Mode, session.RetryModeAutoThenClient)
	}
}

// spec: §7.3 — negative or unknown values reject at the gateway decode
// boundary with 400 VALIDATION_ERROR and the offending field surfaces
// on details.
func TestCreateSessionRejectsInvalidRetryPolicy_F_7_3_1(t *testing.T) {
	caps := session.RetryPolicyCaps{MaxRetries: 2, MaxSessionAgeSeconds: 7200}
	srv := newCreateServerWithRetryCaps(t, caps)
	cases := []struct {
		name    string
		body    string
		wantSub string
	}{
		{
			name:    "negative_max_retries",
			body:    `{"runtimeRef":"claude-code","retryPolicy":{"maxRetries":-3}}`,
			wantSub: "retryPolicy.maxRetries",
		},
		{
			name:    "negative_max_session_age",
			body:    `{"runtimeRef":"claude-code","retryPolicy":{"maxSessionAgeSeconds":-1}}`,
			wantSub: "retryPolicy.maxSessionAgeSeconds",
		},
		{
			name:    "unknown_mode",
			body:    `{"runtimeRef":"claude-code","retryPolicy":{"mode":"bogus"}}`,
			wantSub: "retryPolicy.mode",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader([]byte(tc.body)))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Lenny-Tenant-ID", "acme")
			rr := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
			}
			if !bytes.Contains(rr.Body.Bytes(), []byte(tc.wantSub)) {
				t.Errorf("body missing %q: %s", tc.wantSub, rr.Body.String())
			}
		})
	}
}

// Absent retryPolicy omits the field on the response per omitempty.
func TestCreateSessionRetryPolicyOmitsWhenAbsent_F_7_3_1(t *testing.T) {
	srv := newCreateServerWithRetryCaps(t, session.RetryPolicyCaps{MaxRetries: 2})
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{RuntimeRef: "claude-code"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if bytes.Contains(rr.Body.Bytes(), []byte(`"retryPolicy"`)) {
		t.Errorf("absent retryPolicy should be omitted; body=%s", rr.Body.String())
	}
}
