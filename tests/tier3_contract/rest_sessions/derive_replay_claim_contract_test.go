// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 HTTP contract tests for the create-time warm-pod claim behind POST
// /v1/sessions/{id}/derive and POST /v1/sessions/{id}/replay in both replay
// modes. Derive and replay claim a pod before inserting the new row, as POST
// /v1/sessions does, so a claim failure answers the create path's
// claim-failure envelope: 503 RUNTIME_UNAVAILABLE with Retry-After for a
// warming pool, and 503 SESSION_CREATION_FAILED with Retry-After when no
// pool can place the session. Every error body is validated against the
// response the served OpenAPI document declares for the operation.
//
// spec: §7.1 (Normal Flow: Atomicity of session creation (steps 2–8), Derive
// session semantics), §15.1 (REST API: Session Replay Semantics, Error code
// catalog), §5.2 (pool warming)
package rest_sessions_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// claimContractNS is the agent namespace the claim contract's pools live in.
const claimContractNS = "lenny-agents"

// claimContractCluster returns a fake cluster holding objs. Pool resolution
// reads pools and templates with List and Get only, so the fake client
// serves it.
func claimContractCluster(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	s := runtime.NewScheme()
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).Build()
}

// echoPool returns the `echo` runtime's pool and template. A warming
// template carries the PoolWarmingUp condition the gateway reads.
func echoPool(warming bool) []client.Object {
	tmpl := &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "echo-tmpl", Namespace: claimContractNS},
		Spec:       lennyv1.SandboxTemplateSpec{RuntimeRef: "echo", IsolationProfile: string(isolation.ProfileSandboxed)},
	}
	if warming {
		tmpl.Status.Conditions = []metav1.Condition{{
			Type: "PoolWarmingUp", Status: metav1.ConditionTrue, Reason: "Warming", LastTransitionTime: metav1.Now(),
		}}
	}
	pool := &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "echo-pool", Namespace: claimContractNS},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "echo-tmpl", MinWarm: 1, MaxWarm: 5},
	}
	return []client.Object{pool, tmpl}
}

// spec: §7.1 (Normal Flow: Atomicity of session creation (steps 2–8), Derive session semantics), §15.1 (REST API: Session Replay Semantics, Error code catalog), §5.2 (Pool Configuration and Execution Modes)
// diagnosis: a failure means derive or replay inserted its row without
// running the create-time claim, answered a claim failure with a code other
// than the create path's, left a row behind after the claim failed, or wrote
// a body the served OpenAPI document does not declare. A 201 means the
// handler skipped claimForNewRow; check claimDerivedRow in
// pkg/gateway/sessionserver/derive.go and the 503 responses of the derive
// and replay operations in pkg/gateway/externalapi/openapi/openapi.json.
func TestDeriveAndReplayClaimFailureContract_spec_7_1(t *testing.T) {
	v := newSnapCopyValidator(t)
	cases := []struct {
		name    string
		cluster func(t *testing.T) client.Client
		code    string
	}{
		{
			name:    "pool warming up",
			cluster: func(t *testing.T) client.Client { return claimContractCluster(t, echoPool(true)...) },
			code:    "RUNTIME_UNAVAILABLE",
		},
		{
			name:    "no pool serves the runtime",
			cluster: func(t *testing.T) client.Client { return claimContractCluster(t) },
			code:    "SESSION_CREATION_FAILED",
		},
	}
	for _, ep := range snapCopyEndpoints {
		for _, tc := range cases {
			t.Run(ep.name+"/"+tc.name, func(t *testing.T) {
				f := newSnapCopyFixture(t)
				h := f.handler(func(o *sessionserver.Options) {
					o.DefaultIsolationProfile = isolation.ProfileSandboxed
					o.PodBinder = &podsession.Binder{Client: tc.cluster(t), Namespace: claimContractNS}
					o.PodRegistry = podsession.NewRegistry()
					o.AgentNamespace = claimContractNS
				})
				rr := ep.call(t, h)
				if rr.Code != http.StatusServiceUnavailable {
					t.Fatalf("status = %d, want 503; body %s", rr.Code, rr.Body.String())
				}
				if rr.Header().Get("Retry-After") == "" {
					t.Error("Retry-After missing on the retryable claim failure")
				}
				v.validate(t, ep.path, strconv.Itoa(rr.Code), rr.Body.Bytes())
				var env struct {
					Error struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
					t.Fatalf("decode error envelope: %v", err)
				}
				if env.Error.Code != tc.code {
					t.Errorf("code = %s, want %s", env.Error.Code, tc.code)
				}
				if _, err := f.store.Get(context.Background(), "acme", "sess_snap_new"); !errors.Is(err, sessionstore.ErrNotFound) {
					t.Errorf("the failed claim left a row behind (err %v)", err)
				}
			})
		}
	}
}
