// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

// spec: §7.1 line 6 — CreateSession(runtime, pool, retryPolicy,
// metadata). The metadata payload is preserved verbatim across the
// session lifetime and echoed on GET /v1/sessions/{id}. F-7.3.20.

func newCreateServerWithMetadata(t *testing.T) (*sessionserver.Server, func() string) {
	t.Helper()
	clock := func() time.Time { return time.Date(2026, 5, 26, 9, 0, 0, 0, time.UTC) }
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("test-secret")})
	idFn := func() string { return "sess_md" }
	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		Clock:             clock,
		IDFunc:            idFn,
		UploadTokenIssuer: uploadtoken.NewIssuer(ring, clock),
	})
	return srv, idFn
}

func TestCreateSessionRoundTripsClientMetadata_spec_7_1_F_7_3_20(t *testing.T) {
	srv, _ := newCreateServerWithMetadata(t)
	meta := map[string]string{
		"trace_id":     "abc-123",
		"caller_team":  "platform",
		"workflow_run": "run/9183",
	}

	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code",
		UserID:     "alice@acme.com",
		Metadata:   meta,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("F-7.3.20: status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var resp sessionserver.CreateSessionResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(resp.Metadata, meta) {
		t.Errorf("F-7.3.20: response.metadata = %v, want %v", resp.Metadata, meta)
	}

	// Defensive copy: mutating the create response's map MUST NOT
	// mutate the stored row's payload echoed by GET.
	resp.Metadata["trace_id"] = "MUTATED"

	getReq := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+resp.ID, nil)
	getReq.Header.Set("X-Lenny-Tenant-ID", "acme")
	getRR := httptest.NewRecorder()
	srv.Handler().ServeHTTP(getRR, getReq)
	if getRR.Code != http.StatusOK {
		t.Fatalf("F-7.3.20: GET status = %d, body=%s", getRR.Code, getRR.Body.String())
	}
	var got sessionserver.SessionResponse
	if err := json.Unmarshal(getRR.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode GET: %v", err)
	}
	if !reflect.DeepEqual(got.Metadata, meta) {
		t.Errorf("F-7.3.20: GET.metadata = %v, want preserved %v", got.Metadata, meta)
	}
}

// spec: §7.1 line 6 — absent metadata payload is preserved as nil so
// the wire envelope omits the field per the omitempty contract.
func TestCreateSessionMetadataAbsentOmitsField_spec_F_7_3_20(t *testing.T) {
	srv, _ := newCreateServerWithMetadata(t)
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	// The raw body must not contain the metadata key when absent.
	if bytes.Contains(rr.Body.Bytes(), []byte(`"metadata"`)) {
		t.Errorf("F-7.3.20: absent metadata should be omitted; got body=%s", rr.Body.String())
	}
	var resp sessionserver.CreateSessionResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Metadata != nil {
		t.Errorf("F-7.3.20: absent metadata resp.Metadata = %v, want nil", resp.Metadata)
	}
}

// spec: a non-string metadata value rejects with 400 VALIDATION_ERROR so
// the on-row shape stays a flat string→string map. The decoder enforces
// this via the typed CreateSessionRequest.Metadata field.
func TestCreateSessionMetadataNonStringRejects_spec_F_7_3_20(t *testing.T) {
	srv, _ := newCreateServerWithMetadata(t)
	body := []byte(`{"runtimeRef":"claude-code","metadata":{"k":42}}`)
	rr := createRequestRaw(t, srv.Handler(), body)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("F-7.3.20: non-string metadata value, status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}
