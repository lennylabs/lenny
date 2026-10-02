// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
)

// keptRuntimeRecycle is a recycling block that keeps the pod's runtime
// process across sessions (maxSessionsPerPod above 1, standard scrub).
func keptRuntimeRecycle() *runtimestore.RecyclePolicy {
	return &runtimestore.RecyclePolicy{Enabled: true, AcknowledgeBestEffortScrub: true, MaxSessionsPerPod: 5}
}

// putPoolQuery issues a PUT on path (which may carry a query string) with
// the pool's current ETag as the If-Match precondition.
func putPoolQuery(t *testing.T, h http.Handler, name, query string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPut, "/v1/admin/pools/"+name+query, bytes.NewReader(b)))
	if etag := currentPoolETag(h, "/v1/admin/pools/"+name); etag != "" {
		req.Header.Set("If-Match", etag)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// spec: 5.2 (Deployer acknowledgment (runtime process kept across sessions))
func TestCreatePoolRefusesUnacknowledgedRuntimeReuse_spec_5_2(t *testing.T) {
	router, store, runtimes, _ := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "claude-code"})
	for _, path := range []string{"/v1/admin/pools", "/v1/admin/pools?dryRun=true"} {
		rr := poolReq(t, router.Handler(), http.MethodPost, path, admin.PoolPayload{
			Name: "reuse", RuntimeRef: "claude-code", ExecutionMode: "session",
			SessionPolicy: &runtimestore.SessionPolicy{Recycle: keptRuntimeRecycle()},
		})
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400; body=%s", path, rr.Code, rr.Body.String())
		}
		assertErrorCode(t, rr, "VALIDATION_ERROR")
		if !bytes.Contains(rr.Body.Bytes(), []byte("acknowledgeProcessLevelIsolation")) {
			t.Errorf("%s: body does not name acknowledgeProcessLevelIsolation: %s", path, rr.Body.String())
		}
		if _, err := store.Get(context.Background(), "reuse"); !errors.Is(err, poolstore.ErrNotFound) {
			t.Errorf("%s: a row was stored (err=%v)", path, err)
		}
	}
}

// spec: 5.2 (Deployer acknowledgment (runtime process kept across sessions))
func TestUpdatePoolRefusesRemovingRuntimeReuseAcknowledgment_spec_5_2(t *testing.T) {
	router, store, runtimes, _ := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "claude-code"})
	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name: "reuse", RuntimeRef: "claude-code", ExecutionMode: "session",
		SessionPolicy: &runtimestore.SessionPolicy{AcknowledgeProcessLevelIsolation: true, Recycle: keptRuntimeRecycle()},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}
	before, _ := store.Get(context.Background(), "reuse")

	for _, query := range []string{"", "?dryRun=true"} {
		rr = putPoolQuery(t, router.Handler(), "reuse", query, admin.UpdatePoolRequest{
			SessionPolicy: &runtimestore.SessionPolicy{Recycle: keptRuntimeRecycle()},
		})
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("PUT%s: status %d, want 400; body=%s", query, rr.Code, rr.Body.String())
		}
		assertErrorCode(t, rr, "VALIDATION_ERROR")
		after, _ := store.Get(context.Background(), "reuse")
		if !after.SessionPolicy.AcknowledgeProcessLevelIsolation || after.Generation != before.Generation {
			t.Errorf("PUT%s changed the stored row: ack=%v generation %d → %d", query,
				after.SessionPolicy.AcknowledgeProcessLevelIsolation, before.Generation, after.Generation)
		}
	}

	oneSession := keptRuntimeRecycle()
	oneSession.MaxSessionsPerPod = 1
	rr = putPoolQuery(t, router.Handler(), "reuse", "", admin.UpdatePoolRequest{
		SessionPolicy: &runtimestore.SessionPolicy{Recycle: oneSession},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT to maxSessionsPerPod 1: status %d, body=%s", rr.Code, rr.Body.String())
	}
	rr = putPoolQuery(t, router.Handler(), "reuse", "", admin.UpdatePoolRequest{ClearSessionPolicy: true})
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT clearSessionPolicy: status %d, body=%s", rr.Code, rr.Body.String())
	}
}
