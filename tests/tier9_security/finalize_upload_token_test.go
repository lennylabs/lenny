// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 finalize upload-token probe. Every finalize request carries the
// uploadToken issued at session creation, and the gateway verifies it against
// the session row before it writes the session. This suite drives REST create and
// finalize through sessionserver.New with the production token issuer and
// verifier, and asserts that a caller holding the session bearer but no
// valid token cannot finalize the session, bind a plan into it, or reach the
// pod. A second server over the same store carries a pod binder on a fake
// cluster that counts its calls, because a session's finalize prepare phase
// starts with a cluster read.
package tier9_security_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	ctrlinterceptor "sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
	"github.com/lennylabs/lenny/tests/testinfra/ownerprincipal"
)

// finalizeTokenPodNamespace is the agent namespace of the fake cluster.
const finalizeTokenPodNamespace = "lenny-agents"

type finalizeTokenProbe struct {
	ts    *httptest.Server
	store *memstore.Store
	// podTS serves the same store through a server with a pod binder, so a
	// finalize it admits on a row with a pod assignment reaches the cluster.
	podTS *httptest.Server
	// clusterCalls counts every call the binder's cluster client receives.
	clusterCalls *atomic.Int64
}

// countingCluster returns a fake cluster client that counts each read and
// delete a finalize prepare phase or its release path issues.
func countingCluster(t *testing.T, calls *atomic.Int64) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := lennyv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add lenny scheme: %v", err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(ctrlinterceptor.Funcs{
		Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			calls.Add(1)
			return cl.Get(ctx, key, obj, opts...)
		},
		List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			calls.Add(1)
			return cl.List(ctx, list, opts...)
		},
		Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			calls.Add(1)
			return cl.Delete(ctx, obj, opts...)
		},
	}).Build()
}

func newFinalizeTokenProbe(t *testing.T) *finalizeTokenProbe {
	t.Helper()
	store := memstore.New()
	issuer := uploadtoken.NewIssuer(nil)
	srv := sessionserver.New(store, sessionserver.Options{
		DevTenantHeader:     true,
		UploadTokenIssuer:   issuer,
		UploadTokenVerifier: uploadtoken.NewVerifier(nil),
	})
	ts := httptest.NewServer(ownerprincipal.Wrap(srv.Handler(), ownerprincipal.Alice))
	t.Cleanup(ts.Close)

	calls := &atomic.Int64{}
	podSrv := sessionserver.New(store, sessionserver.Options{
		DevTenantHeader:     true,
		UploadTokenVerifier: uploadtoken.NewVerifier(nil),
		PodBinder:           &podsession.Binder{Client: countingCluster(t, calls), Namespace: finalizeTokenPodNamespace},
		AgentNamespace:      finalizeTokenPodNamespace,
	})
	t.Cleanup(podSrv.Close)
	podTS := httptest.NewServer(ownerprincipal.Wrap(podSrv.Handler(), ownerprincipal.Alice))
	t.Cleanup(podTS.Close)
	return &finalizeTokenProbe{ts: ts, store: store, podTS: podTS, clusterCalls: calls}
}

func (p *finalizeTokenProbe) do(t *testing.T, path, tenant, token, body string) (int, string) {
	t.Helper()
	return p.doOn(t, p.ts, path, tenant, token, body)
}

func (p *finalizeTokenProbe) doOn(t *testing.T, ts *httptest.Server, path, tenant, token, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+path, strings.NewReader(body))
	req.Header.Set("X-Lenny-Tenant-ID", tenant)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("X-Lenny-Upload-Token", token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	var raw json.RawMessage
	_ = json.NewDecoder(resp.Body).Decode(&raw)
	return resp.StatusCode, string(raw)
}

func (p *finalizeTokenProbe) create(t *testing.T, tenant string) (string, string) {
	t.Helper()
	status, body := p.do(t, "/v1/sessions", tenant, "", `{"runtimeRef":"claude-code","userId":"alice@acme.com"}`)
	if status != http.StatusCreated {
		t.Fatalf("create: status %d body %s", status, body)
	}
	var created struct {
		ID          string `json:"id"`
		UploadToken string `json:"uploadToken"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil || created.UploadToken == "" {
		t.Fatalf("create decode: %v body %s", err, body)
	}
	return created.ID, created.UploadToken
}

// assignPod records a create-time pod claim on the row, so an admitted
// finalize on podTS runs the prepare phase against the cluster.
func (p *finalizeTokenProbe) assignPod(t *testing.T, id string) sessionstore.Session {
	t.Helper()
	row, err := p.store.Update(context.Background(), "acme", id, func(row *sessionstore.Session) error {
		row.PodAssignment = "sbx-" + id
		return nil
	})
	if err != nil {
		t.Fatalf("assign pod: %v", err)
	}
	return row
}

// spec: 7.4 (Upload Safety, Upload authorization), 7.1 (Normal Flow, uploadToken format and security properties), 15.1 (finalize)
// diagnosis: a failure means a caller without the session's own valid
// uploadToken finalized the session, bound a plan into it, or reached the pod:
// the token from another session, a well-formed token whose digest the session
// row does not hold, no token, or a token replayed after the session finalized
// was accepted. A nonzero cluster call count on a refusal means pod work runs
// ahead of the token check. A zero count on the admitted control case means
// the fixture no longer reaches the prepare phase, so the refusal cases prove
// nothing. Inspect the verifyUploadToken call at the top of handleFinalize in
// pkg/gateway/sessionserver/finalize_handler.go.
func TestFinalizeRefusesForeignAndReplayedUploadTokens_spec_7_4(t *testing.T) {
	p := newFinalizeTokenProbe(t)
	victim, victimToken := p.create(t, "acme")
	_, attackerToken := p.create(t, "acme")
	forged, err := uploadtoken.NewIssuer(nil).Issue(victim, uploadtoken.DefaultTTL)
	if err != nil {
		t.Fatalf("forge: %v", err)
	}
	p.assignPod(t, victim)
	plan := `{"workspacePlan":{"schemaVersion":1,"sources":[{"type":"inlineFile","path":"evil.sh","content":"x"}]}}`

	refusals := []struct {
		name       string
		token      string
		wantStatus int
		wantCode   string
	}{
		{"no token", "", http.StatusUnauthorized, "UPLOAD_TOKEN_INVALID"},
		{"another session's token", attackerToken, http.StatusForbidden, "UPLOAD_TOKEN_MISMATCH"},
		{"token forged with a guessed secret", forged, http.StatusUnauthorized, "UPLOAD_TOKEN_INVALID"},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			status, body := p.doOn(t, p.podTS, "/v1/sessions/"+victim+"/finalize", "acme", tc.token, plan)
			if status != tc.wantStatus || !strings.Contains(body, tc.wantCode) {
				t.Fatalf("status %d body %s, want %d %s", status, body, tc.wantStatus, tc.wantCode)
			}
			row, err := p.store.Get(context.Background(), "acme", victim)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if row.State != session.StateCreated || len(row.WorkspacePlan) != 0 {
				t.Fatalf("refused finalize changed the session: state %q plan %s", row.State, row.WorkspacePlan)
			}
			if n := p.clusterCalls.Load(); n != 0 {
				t.Fatalf("refused finalize made %d cluster calls, want none", n)
			}
		})
	}

	if status, body := p.do(t, "/v1/sessions/"+victim+"/finalize", "acme", victimToken, ""); status != http.StatusOK {
		t.Fatalf("owner finalize: status %d body %s", status, body)
	}
	finalized, err := p.store.Get(context.Background(), "acme", victim)
	if err != nil {
		t.Fatalf("get finalized row: %v", err)
	}
	if status, body := p.doOn(t, p.podTS, "/v1/sessions/"+victim+"/finalize", "acme", victimToken, plan); status != http.StatusGone || !strings.Contains(body, "UPLOAD_TOKEN_CONSUMED") {
		t.Fatalf("replayed token: status %d body %s, want 410 UPLOAD_TOKEN_CONSUMED", status, body)
	}
	replayed, err := p.store.Get(context.Background(), "acme", victim)
	if err != nil {
		t.Fatalf("get replayed row: %v", err)
	}
	if !reflect.DeepEqual(finalized, replayed) {
		t.Errorf("replayed finalize changed the row:\nbefore %+v\nafter  %+v", finalized, replayed)
	}
	if n := p.clusterCalls.Load(); n != 0 {
		t.Errorf("replayed finalize made %d cluster calls, want none", n)
	}

	// The control: the same server, row layout, and body with a valid token
	// reach the cluster, which is what gives the zero counts above meaning.
	control, controlToken := p.create(t, "acme")
	p.assignPod(t, control)
	p.doOn(t, p.podTS, "/v1/sessions/"+control+"/finalize", "acme", controlToken, plan)
	if p.clusterCalls.Load() == 0 {
		t.Error("an admitted finalize made no cluster call, so the refusal cases observe nothing")
	}
}
