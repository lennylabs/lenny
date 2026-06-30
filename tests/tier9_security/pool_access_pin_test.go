// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 §4 pool_tenant_access enforcement on the create-body pool
// selector (F-CS2, proposal 0018 C6a). A pool is a platform-global
// resource with no tenant_id; per-tenant visibility and use are granted
// through the §4 pool_tenant_access join table. A client may pin a target
// pool on session creation (the §14.1 CreateSessionRequest.pool selector),
// so the gateway must enforce the grant: a tenant that pins a pool it was
// never granted is rejected fail-closed with 403 FORBIDDEN, even when the
// pool exists and is backed by the requested runtime. Without the gate a
// client could schedule onto any platform-global pool by naming it,
// crossing the §4 tenant-isolation boundary the join table exists to
// enforce.
//
// This drives the gateway REST POST /v1/sessions/start handler through
// sessionserver.New (the same wiring the gateway binary uses) against an
// envtest-backed warm pool, so the security tier exercises the
// authorization boundary the way an external client reaches it. It
// complements the in-package component cases in
// pkg/gateway/sessionserver/pool_selection_component_test.go, asserting
// the security-tier fail-closed posture on the unauthorized-pin path.
package tier9_security_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantaccessstore"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

const poolAccessNS = "lenny-agents"

// poolAccessCluster brings up an envtest kube-apiserver holding one warm
// pool backed by the echo runtime at sandboxed isolation, so ResolvePool
// confirms the pinned pool is satisfiable and the create reaches the §4
// authorization gate.
func poolAccessCluster(t *testing.T) client.Client {
	t.Helper()
	env := envtest.Start(t)
	s := runtime.NewScheme()
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme lenny: %v", err)
	}
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme corev1: %v", err)
	}
	c, err := client.New(env.RESTConfig(), client.Options{Scheme: s})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	ctx := context.Background()
	if err := c.Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: poolAccessNS},
	}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace: %v", err)
	}
	objs := []client.Object{
		&lennyv1.SandboxWarmPool{
			ObjectMeta: metav1.ObjectMeta{Name: "echo-pool", Namespace: poolAccessNS},
			Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "echo-tmpl", MinWarm: 1, MaxWarm: 5},
		},
		&lennyv1.SandboxTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "echo-tmpl", Namespace: poolAccessNS},
			Spec: lennyv1.SandboxTemplateSpec{
				RuntimeRef: "echo", IsolationProfile: string(isolation.ProfileSandboxed),
			},
		},
	}
	for _, o := range objs {
		if err := c.Create(ctx, o); err != nil {
			t.Fatalf("create %T %s: %v", o, o.GetName(), err)
		}
	}
	return c
}

// diagnosis: the §4 pool_tenant_access grant is not enforced on a
// create-body pool pin. The test seeds an envtest warm pool backed by the
// echo runtime, then pins it on POST /v1/sessions/start from a tenant that
// holds no pool grant. An admitted session (any status other than 403)
// means a client can pin and schedule onto any platform-global pool by
// naming it, crossing the §4 tenant-isolation boundary. A grant for the
// pool admits the same request, ruling out a blanket-rejection false
// positive.
// spec: 7.1 (pool selector), 14.1 (CreateSessionRequest.pool), 4 (pool_tenant_access), 15.1 (FORBIDDEN)
func TestCreateBodyPoolEnforcesTenantAccess_spec_4(t *testing.T) {
	cluster := poolAccessCluster(t)
	binder := &podsession.Binder{Client: cluster, Namespace: poolAccessNS}

	// Grant some-other-tenant access to echo-pool but never acme: acme's
	// pin is satisfiable (the pool is backed) yet unauthorized.
	access := tenantaccessstore.NewMemory()
	if _, err := access.Grant(context.Background(), tenantaccessstore.KindPool, "echo-pool",
		"globex", "platform-admin@globex.com", time.Time{}); err != nil {
		t.Fatalf("seed grant: %v", err)
	}

	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		IDFunc:                  func() string { return "sess-pool-403" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             podsession.NewRegistry(),
		AgentNamespace:          poolAccessNS,
		TenantAccess:            access,
	})

	post := func(tenant string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(sessionserver.CreateAndStartRequest{
			RuntimeRef: "echo", UserID: "alice@" + tenant + ".com", Pool: "echo-pool",
		})
		req := httptest.NewRequest(http.MethodPost, "/v1/sessions/start", bytes.NewReader(body))
		req.Header.Set("X-Lenny-Tenant-ID", tenant)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		return rr
	}

	// acme holds no grant for echo-pool: the pin must fail closed with 403.
	rr := post("acme")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("§4: an ungranted tenant pinned echo-pool and got status %d, want 403 FORBIDDEN; "+
			"the pool_tenant_access boundary did not fail closed (body=%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "FORBIDDEN") {
		t.Errorf("§4: rejection body must carry FORBIDDEN: %s", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "pool_access_denied") {
		t.Errorf("§4: rejection must record reason pool_access_denied: %s", rr.Body.String())
	}

	// globex holds the grant: the satisfiable, authorized pin must clear the
	// §4 gate (it will not 403). A 403 here would be an over-broad gate that
	// rejects a granted tenant.
	rr = post("globex")
	if rr.Code == http.StatusForbidden && strings.Contains(rr.Body.String(), "pool_access_denied") {
		t.Errorf("§4: a granted tenant was rejected with pool_access_denied; the gate is over-broad (body=%s)",
			rr.Body.String())
	}
}
