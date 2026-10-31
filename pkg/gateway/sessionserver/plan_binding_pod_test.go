// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// planBindPodEnv is one envtest cluster holding the idle pod sbx-1, a
// session server whose binder claims from it, and a blob store holding one
// upload staged for the session sess-upload-owner.
type planBindPodEnv struct {
	cluster   client.Client
	store     *memstore.Store
	handler   http.Handler
	stagedRef string
}

func newPlanBindPodEnv(t *testing.T, sessionID string) *planBindPodEnv {
	t.Helper()
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceBase = t.TempDir()
	adapterSrv.Runtime = &podBindRuntime{}
	cluster := podBindClient(
		t,
		podBindWarmPool("echo-pool", "echo-tmpl"),
		podBindTemplate("echo-tmpl", "echo", string(isolation.ProfileSandboxed)),
		podBindIdleSandbox("sbx-1", "echo-pool", "10.244.2.5"),
	)
	binder := podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv))
	blobs := blobstore.NewMemoryStore(nil)
	binder.Blobs = blobs
	staged := blobstore.URI{
		TenantID:   "acme",
		ObjectType: blobstore.ObjectTypeUpload,
		SessionID:  "sess-upload-owner",
		PartID:     "p1",
		TTL:        time.Hour,
	}
	if _, err := blobs.Put(staged, "text/plain", bytes.NewReader([]byte("owned by another session"))); err != nil {
		t.Fatalf("stage blob: %v", err)
	}
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		DevTenantHeader:         true,
		IDFunc:                  func() string { return sessionID },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             podsession.NewRegistry(),
		AgentNamespace:          podTestNS,
		Blobs:                   blobs,
	})
	return &planBindPodEnv{cluster: cluster, store: store, handler: asOwner(srv.Handler()), stagedRef: staged.String()}
}

// claimPresent reports whether the per-pod claim on sbx-1 exists.
func (e *planBindPodEnv) claimPresent(t *testing.T) bool {
	t.Helper()
	var claim lennyv1.SandboxClaim
	err := e.cluster.Get(context.Background(), client.ObjectKey{Namespace: podTestNS, Name: "claim-sbx-1"}, &claim)
	return err == nil
}

// uploadFilePlanNaming returns a plan whose one uploadFile source names ref.
func uploadFilePlanNaming(ref string) json.RawMessage {
	return json.RawMessage(`{"schemaVersion":1,"sources":[{"type":"uploadFile","path":"a.txt","uploadRef":"` + ref + `"}]}`)
}

// spec: 14.1 (WorkspacePlan Schema Versioning), 14 (Workspace Plan Schema)
// diagnosis: a POST /v1/sessions or POST /v1/sessions/start plan naming an
// upload staged for another session of the same tenant must fail with 400
// VALIDATION_ERROR (upload_ref_foreign_session) before any pod is claimed.
// A claim on sbx-1 or a 2xx means the create path admitted a foreign
// uploadRef, which lets one session materialize another session's staged
// content, or that the scope check moved after the pod claim.
func TestCreateTimeUploadRefRejectedBeforePodClaim_spec_14_1(t *testing.T) {
	for _, path := range []string{"/v1/sessions", "/v1/sessions/start"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			env := newPlanBindPodEnv(t, "sess-create-foreign")
			var body []byte
			if path == "/v1/sessions/start" {
				body, _ = json.Marshal(sessionserver.CreateAndStartRequest{
					RuntimeRef: "echo", WorkspacePlan: uploadFilePlanNaming(env.stagedRef),
				})
			} else {
				body, _ = json.Marshal(sessionserver.CreateSessionRequest{
					RuntimeRef: "echo", WorkspacePlan: uploadFilePlanNaming(env.stagedRef),
				})
			}
			rr := postSessionStep(t, env.handler, path, body)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), `"upload_ref_foreign_session"`) {
				t.Errorf("body = %s, want reason upload_ref_foreign_session", rr.Body.String())
			}
			if env.claimPresent(t) {
				t.Errorf("claim-sbx-1 exists after the rejected create; the pod was claimed for a rejected plan")
			}
			if _, err := env.store.Get(context.Background(), "acme", "sess-create-foreign"); err == nil {
				t.Errorf("a row was persisted for the rejected create")
			}
		})
	}
}

// spec: 14.1 (WorkspacePlan Schema Versioning), 14 (Workspace Plan Schema), 7.1 (Normal Flow)
// diagnosis: a finalize plan naming an upload staged for another session
// must fail with 400 VALIDATION_ERROR (upload_ref_foreign_session), leave
// the session `created` with no plan, and keep the pod claimed at create.
// A failed row, a released claim, or a stored plan means the rejection ran
// after the created → finalizing write or engaged the prepare barrier.
func TestFinalizeForeignUploadRefKeepsClaimedPod_spec_14_1(t *testing.T) {
	t.Parallel()
	env := newPlanBindPodEnv(t, "sess-fin-foreign")
	createBody, _ := json.Marshal(sessionserver.CreateSessionRequest{RuntimeRef: "echo"})
	if rr := postSessionStep(t, env.handler, "/v1/sessions", createBody); rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body=%s", rr.Code, rr.Body.String())
	}
	if !env.claimPresent(t) {
		t.Fatalf("claim-sbx-1 missing after create")
	}
	finalizeBody, _ := json.Marshal(map[string]json.RawMessage{"workspacePlan": uploadFilePlanNaming(env.stagedRef)})
	rr := postSessionStep(t, env.handler, "/v1/sessions/sess-fin-foreign/finalize", finalizeBody)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("finalize: status %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"upload_ref_foreign_session"`) {
		t.Errorf("body = %s, want reason upload_ref_foreign_session", rr.Body.String())
	}
	row, err := env.store.Get(context.Background(), "acme", "sess-fin-foreign")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if row.State != session.StateCreated {
		t.Errorf("state = %q, want created", row.State)
	}
	if len(row.WorkspacePlan) != 0 {
		t.Errorf("stored plan = %s, want none", row.WorkspacePlan)
	}
	if !env.claimPresent(t) {
		t.Errorf("claim-sbx-1 released by a rejected finalize")
	}
}
