// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore/replication"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
)

// spec: §25.11 lines 3898-3899 — POST/GET
// /v1/admin/artifact-replication/{region}/{resume,status}. F-25.11.1.

// fakeArtifactReplication is an in-memory admin.ArtifactReplicationController.
// resumeErr, when set, is returned from Resume so the error mapping is
// exercised; otherwise Resume flips the region to active.
type fakeArtifactReplication struct {
	states     map[string]replication.RegionState
	resumeErr  error
	resumedArg struct {
		region, operator, justification string
		called                          bool
	}
}

func (f *fakeArtifactReplication) Resume(_ context.Context, region, operatorSub, justification string) error {
	f.resumedArg.region = region
	f.resumedArg.operator = operatorSub
	f.resumedArg.justification = justification
	f.resumedArg.called = true
	if f.resumeErr != nil {
		return f.resumeErr
	}
	st := f.states[region]
	st.Region = region
	st.State = replication.StateActive
	st.DestinationJurisdictionTag = "eu-west-1"
	f.states[region] = st
	return nil
}

func (f *fakeArtifactReplication) GetState(_ context.Context, region string) (replication.RegionState, bool, error) {
	st, ok := f.states[region]
	return st, ok, nil
}

func newReplicationRouter(repl admin.ArtifactReplicationController) *admin.Router {
	r := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC) },
	})
	if repl != nil {
		r = r.WithArtifactReplication(repl)
	}
	return r
}

func TestResumeArtifactReplication_Success_spec_25_11(t *testing.T) {
	repl := &fakeArtifactReplication{states: map[string]replication.RegionState{
		"eu-west-1": {Region: "eu-west-1", State: replication.StateSuspendedResidencyViolation},
	}}
	h := newReplicationRouter(repl).Handler()

	body, _ := json.Marshal(map[string]string{"justification": "destination tag corrected"})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost,
		"/v1/admin/artifact-replication/eu-west-1/resume", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
	if !repl.resumedArg.called {
		t.Fatal("Resume was not invoked")
	}
	if repl.resumedArg.operator != "admin@acme.com" {
		t.Errorf("operator = %q, want admin@acme.com", repl.resumedArg.operator)
	}
	if repl.resumedArg.justification != "destination tag corrected" {
		t.Errorf("justification = %q", repl.resumedArg.justification)
	}
	var out replication.RegionState
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.State != replication.StateActive {
		t.Errorf("post-resume status = %q, want active", out.State)
	}
}

func TestResumeArtifactReplication_PersistingMismatch_spec_25_11(t *testing.T) {
	repl := &fakeArtifactReplication{
		states:    map[string]replication.RegionState{"eu-west-1": {Region: "eu-west-1"}},
		resumeErr: fmt.Errorf("%w: region eu-west-1: jurisdiction tag mismatch", replication.ErrRegionUnresolvable),
	}
	h := newReplicationRouter(repl).Handler()

	body, _ := json.Marshal(map[string]string{"justification": "tried to fix"})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost,
		"/v1/admin/artifact-replication/eu-west-1/resume", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	if code := errorCode(t, rr.Body.Bytes()); code != "ARTIFACT_REPLICATION_REGION_UNRESOLVABLE" {
		t.Errorf("code = %q, want ARTIFACT_REPLICATION_REGION_UNRESOLVABLE", code)
	}
}

func TestResumeArtifactReplication_MissingJustification_spec_25_11(t *testing.T) {
	repl := &fakeArtifactReplication{states: map[string]replication.RegionState{"eu-west-1": {}}}
	h := newReplicationRouter(repl).Handler()

	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost,
		"/v1/admin/artifact-replication/eu-west-1/resume", bytes.NewReader([]byte(`{}`))))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400; body %s", rr.Code, rr.Body.String())
	}
	if code := errorCode(t, rr.Body.Bytes()); code != "JUSTIFICATION_REQUIRED" {
		t.Errorf("code = %q, want JUSTIFICATION_REQUIRED", code)
	}
	if repl.resumedArg.called {
		t.Error("Resume must not run when justification is missing")
	}
}

func TestResumeArtifactReplication_Unwired_503_spec_25_11(t *testing.T) {
	h := newReplicationRouter(nil).Handler()

	body, _ := json.Marshal(map[string]string{"justification": "x"})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost,
		"/v1/admin/artifact-replication/eu-west-1/resume", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503; body %s", rr.Code, rr.Body.String())
	}
	if code := errorCode(t, rr.Body.Bytes()); code != "ARTIFACT_REPLICATION_UNAVAILABLE" {
		t.Errorf("code = %q, want ARTIFACT_REPLICATION_UNAVAILABLE", code)
	}
}

func TestResumeArtifactReplication_NonPlatformAdmin_403_spec_25_11(t *testing.T) {
	repl := &fakeArtifactReplication{states: map[string]replication.RegionState{"eu-west-1": {}}}
	h := newReplicationRouter(repl).Handler()

	body, _ := json.Marshal(map[string]string{"justification": "x"})
	req := withTenantAdminPrincipal(httptest.NewRequest(http.MethodPost,
		"/v1/admin/artifact-replication/eu-west-1/resume", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403 (platform-admin only); body %s", rr.Code, rr.Body.String())
	}
	if repl.resumedArg.called {
		t.Error("Resume must not run for a non-platform-admin caller")
	}
}

func TestArtifactReplicationStatus_Success_spec_25_11(t *testing.T) {
	repl := &fakeArtifactReplication{states: map[string]replication.RegionState{
		"eu-west-1": {
			Region:                     "eu-west-1",
			State:                      replication.StateSuspendedResidencyViolation,
			LastPreflightResult:        "jurisdiction tag mismatch",
			DestinationEndpoint:        "https://backup.example.eu",
			DestinationBucket:          "lenny-artifacts-dr",
			DestinationJurisdictionTag: "us-east-1",
			ReplicationLagSeconds:      42,
		},
	}}
	h := newReplicationRouter(repl).Handler()

	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet,
		"/v1/admin/artifact-replication/eu-west-1/status", nil))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
	var out replication.RegionState
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.State != replication.StateSuspendedResidencyViolation {
		t.Errorf("status = %q, want suspended_residency_violation", out.State)
	}
	if out.ReplicationLagSeconds != 42 {
		t.Errorf("replicationLagSeconds = %d, want 42", out.ReplicationLagSeconds)
	}
	if out.DestinationJurisdictionTag != "us-east-1" {
		t.Errorf("destinationJurisdictionTag = %q", out.DestinationJurisdictionTag)
	}
}

func TestArtifactReplicationStatus_UnknownRegion_404_spec_25_11(t *testing.T) {
	repl := &fakeArtifactReplication{states: map[string]replication.RegionState{}}
	h := newReplicationRouter(repl).Handler()

	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet,
		"/v1/admin/artifact-replication/ap-south-1/status", nil))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404; body %s", rr.Code, rr.Body.String())
	}
}

// errorCode extracts error.code from the canonical admin error envelope.
func errorCode(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode error envelope: %v (body %s)", err, body)
	}
	return env.Error.Code
}
