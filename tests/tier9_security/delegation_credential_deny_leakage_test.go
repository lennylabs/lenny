// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 credential-delivery/leakage boundary for credentialPropagation: deny.
// The functional tier-4 test observes which credential pools a deny child
// resolves; this suite pins the delivery boundary that gate exists to protect:
// no lease token is ever minted or delivered into a deny child's pod, and an
// inherit hop whose origin traces to a deny session is rejected before any
// lease mint. A deny hop grants the child no LLM credentials, so a pure
// file-processing tool delegated with deny must never receive an upstream
// credential lease, and a deny session holds no origin pool for a descendant
// inherit hop to draw from.
//
// The suite wires one lease-counting credential assigner (the podsession
// Binder.Credentials whose AssignProto increments a mutex-guarded counter)
// across the real delegate_task → delegation.Service → sessionserver finalize
// barrier over an envtest warm pool and the real credential-pool minting path.
// A non-deny control child finalized through the same fixture drives the
// counter above zero, so the deny assertions are not vacuous: the assigner is
// live and would mint for a child the deny policy does not forbid.
package tier9_security_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/admission/ownership"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/credrouter"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// The provider/pool identifiers the fixture spans. shared-tool supports both
// providers so a non-deny child on it draws two lease mints, and both
// file-tool and sub-tool support only anthropic_direct so the inherit hop's
// rejection is attributable to the deny origin terminator rather than a
// provider mismatch.
const (
	denyLeakageAnthropic = "anthropic_direct"
	denyLeakageOpenAI    = "openai_direct"
	denyLeakagePoolAnth  = "claude-prod"
	denyLeakagePoolOpen  = "openai-prod"
)

// denyLeakageCaller is the delegating principal. The fixture wires no §10.6
// environment store and the sessions carry no environment, so the caller's
// group is unused; the delegation resolves same-environment.
var denyLeakageCaller = authmw.Principal{Subject: "alice", TenantID: "acme", Groups: []string{"agents"}}

// spec: §8.3 (deny receives no LLM credentials; inherit-from-deny fails closed
// — no lease token reaches the deny pod)
// diagnosis: a nonzero AssignProto count for the deny leaf or the
// inherit-from-deny grandchild means the gateway minted and delivered an
// LLM-credential lease token to a pod the deny policy forbids, the
// credential-leakage exposure this gate exists to prevent. A deny child that
// mints any lease has received an upstream credential the spec grants only to
// inherit/independent hops (line 443); an inherit grandchild that mints or is
// admitted has drawn from a deny session's origin pool, which does not exist
// (line 490), instead of being rejected with CREDENTIAL_POOL_EXHAUSTED before
// any lease mint (line 474).
func TestDenyDelegationDeliversNoLeaseToken(t *testing.T) {
	envtest.SkipUnlessAvailable(t)
	ctx := context.Background()

	cluster := denyLeakageCluster(t)

	adapterSrv := adapter.New("deny-leakage-adapter")
	adapterSrv.WorkspaceRoot = t.TempDir()
	adapterSrv.StagingDir = t.TempDir()
	adapterSrv.CredentialsDir = t.TempDir()
	adapterSrv.Runtime = noopRuntime{}

	assigner := &leaseCountingAssigner{}
	blobs := blobstore.NewMemoryStore(nil)
	binder := &podsession.Binder{
		Client:           cluster,
		Namespace:        deliveryGateNS,
		AdapterPort:      50051,
		AcceptedVersions: []string{adapter.ProtocolVersionV1},
		DialAdapter:      deliveryGateDialer(t, adapterSrv),
		Blobs:            blobs,
		Credentials:      assigner,
	}

	store := memstore.New()
	tenants := tenantstore.NewMemory()
	if err := tenants.Create(ctx, tenantstore.Tenant{
		ID: "acme",
		CredentialPolicy: credential.CredentialPolicy{
			PreferredSource: credential.PreferredSourcePool,
			ProviderPools: map[string]credential.ProviderPool{
				denyLeakageAnthropic: {DefaultPool: denyLeakagePoolAnth},
				denyLeakageOpenAI:    {DefaultPool: denyLeakagePoolOpen},
			},
		},
	}); err != nil {
		t.Fatalf("create tenant: %v", err)
	}

	runtimes := runtimestore.NewMemory()
	// shared-tool (the finalized leaf runtime) supports both providers, so a
	// non-deny child on it draws two lease mints; the deny bit is the only
	// reason the deny child mints none.
	mustCreateRuntimeDeny(t, runtimes, runtimestore.Runtime{
		Name: "shared-tool", Type: runtimestore.TypeAgent,
		SupportedProviders: []string{denyLeakageAnthropic, denyLeakageOpenAI},
	})
	// The delegation chain runtimes: planner (parent), file-tool (the deny
	// child), and sub-tool (the inherit grandchild). Each declares
	// anthropic_direct with an active pool, so the inherit grandchild would be
	// credentialed if the origin-deny terminator did not fire.
	mustCreateRuntimeDeny(t, runtimes, runtimestore.Runtime{
		Name: "planner", Type: runtimestore.TypeAgent,
		SupportedProviders: []string{denyLeakageAnthropic},
	})
	mustCreateRuntimeDeny(t, runtimes, runtimestore.Runtime{
		Name: "file-tool", Type: runtimestore.TypeAgent,
		SupportedProviders: []string{denyLeakageAnthropic},
	})
	mustCreateRuntimeDeny(t, runtimes, runtimestore.Runtime{
		Name: "sub-tool", Type: runtimestore.TypeAgent,
		SupportedProviders: []string{denyLeakageAnthropic},
	})

	credPools := credentialpoolstore.NewMemory()
	for _, p := range []struct{ name, provider string }{
		{denyLeakagePoolAnth, denyLeakageAnthropic},
		{denyLeakagePoolOpen, denyLeakageOpenAI},
	} {
		if err := credPools.Create(ctx, credentialpoolstore.CredentialPool{
			TenantID: "acme", Name: p.name, Provider: p.provider, MaxConcurrentSessions: 10,
			Credentials: []credentialpoolstore.Credential{
				{ID: p.name + "-cred", SecretRef: "secret-" + p.name, Status: credentialpoolstore.CredentialActive},
			},
		}); err != nil {
			t.Fatalf("create pool %s: %v", p.name, err)
		}
	}

	// A monotonic id generator for the REST-created leaf children (the control
	// and deny leaves), distinct from the delegation Service's own ids.
	restIDs := denyLeakageIDCounter("sess-leaf-")
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  restIDs,
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             podsession.NewRegistry(),
		AgentNamespace:          deliveryGateNS,
		Tenants:                 tenants,
		Runtimes:                runtimes,
		CredentialPools:         credPools,
		CredentialRouter:        credrouter.NewDefault(),
		Blobs:                   blobs,
	})
	h := srv.Handler()

	// The same sessionserver.Server is the §8.3 delegation-time
	// credential-availability checker for the MCP delegate_task handler, so the
	// inherit-from-deny rejection runs the real §4.9 engine against the shared
	// store the finalize path also reads.
	mcpSrv := mcp.NewServer()
	mcptools.Register(mcpSrv, mcptools.Deps{
		Store:    store,
		Executor: executor.NewEchoExecutor(),
		Runtimes: runtimes,
		Delegation: delegation.NewService(store, delegation.Options{
			IDFunc:   denyLeakageIDCounter("sess-deleg-"),
			Runtimes: runtimes,
		}),
		CredAvailability: srv,
		Clock:            func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:           func() string { return "sess_mcp" },
		TenantID:         "acme",
	})

	// A running parent the delegation chain hangs off of.
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(ctx, sessionstore.Session{
		ID: "sess_parent", TenantID: "acme", UserID: "alice@acme.com",
		State: session.StateRunning, RuntimeRef: "planner",
		IsolationProfile: isolation.ProfileSandboxed, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create parent session: %v", err)
	}

	post := func(path string, body []byte) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}

	// createLeaf creates a session on shared-tool through the REST create
	// handler, which claims a warm pod and sets the row's PodAssignment so the
	// finalize barrier reaches the credential-assignment step. Absent a claimed
	// pod, finalize returns before any assignment and the delivery boundary
	// would be untested.
	createLeaf := func() string {
		body, _ := json.Marshal(sessionserver.CreateSessionRequest{RuntimeRef: "shared-tool", UserID: "alice@acme.com"})
		rr := post("/v1/sessions", body)
		if rr.Code != http.StatusCreated {
			t.Fatalf("create leaf: status %d, body=%s", rr.Code, rr.Body.String())
		}
		var out struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil || out.ID == "" {
			t.Fatalf("decode create response: %v; body=%s", err, rr.Body.String())
		}
		return out.ID
	}

	// Control non-deny leaf: an independent child on shared-tool draws both of
	// its providers' pools, driving the shared assigner above zero. This proves
	// the credential-pool minting path is live and would mint for the deny leaf
	// too, so the deny assertion below is not vacuous.
	t.Run("control_non_deny_child_mints_lease_tokens", func(t *testing.T) {
		before := assigner.count()
		id := createLeaf()
		if rr := post("/v1/sessions/"+id+"/finalize", []byte(`{}`)); rr.Code != http.StatusOK {
			t.Fatalf("finalize control child: status %d, body=%s", rr.Code, rr.Body.String())
		}
		if got := assigner.count() - before; got == 0 {
			t.Fatalf("control non-deny child minted %d lease tokens, want > 0 (the assigner and credential-pool minting path must be live for the deny assertions to be meaningful)", got)
		}
	})

	// Deny leaf: the delivery boundary. A deny child on the same shared-tool
	// runtime (which a non-deny child mints two leases for) is finalized through
	// the same barrier and must mint no lease token — no AssignProto call, so no
	// lease token is delivered into the deny pod. The deny marker is stamped
	// exactly as the delegation Service stamps it on a credentialPropagation:
	// deny hop.
	t.Run("deny_leaf_receives_no_lease_token_at_finalize", func(t *testing.T) {
		before := assigner.count()
		denyID := createLeaf()
		if _, err := store.Update(ctx, "acme", denyID, func(s *sessionstore.Session) error {
			s.CredentialDeny = true
			return nil
		}); err != nil {
			t.Fatalf("stamp deny marker onto leaf: %v", err)
		}
		if rr := post("/v1/sessions/"+denyID+"/finalize", []byte(`{}`)); rr.Code != http.StatusOK {
			t.Fatalf("finalize deny leaf: status %d, body=%s", rr.Code, rr.Body.String())
		}
		// A zero AssignProto delta is the delivery-boundary evidence: no lease
		// was minted, so the deny pod received an empty credential set (no
		// credential pool and no user provider). The finalize path assigns
		// credentials only by streaming a minted lease through AssignProto, and a
		// deny row resolves to zero eligible providers, so no AssignProto runs.
		if got := assigner.count() - before; got != 0 {
			t.Fatalf("deny leaf minted %d lease tokens, want 0 (a deny hop grants the child no LLM credentials; any mint delivers a forbidden lease token into the deny pod)", got)
		}
	})

	// Inherit-from-deny grandchild: a deny session holds no origin pool, so an
	// inherit hop whose origin resolves to the deny child is rejected with
	// CREDENTIAL_POOL_EXHAUSTED at delegation time, before any pod is claimed or
	// any lease is minted. The shared assigner (proven live above) must not
	// advance, confirming no lease token reaches the grandchild pod.
	t.Run("inherit_from_deny_grandchild_rejected_before_lease_mint", func(t *testing.T) {
		before := assigner.count()

		// A deny hop skips the delegation-time availability pre-check, so the
		// deny child commits; the delegation Service stamps the deny marker.
		resp := denyLeakageDelegate(t, mcpSrv, "sess_parent", "file-tool", "deny")
		result, _ := resp["result"].(map[string]any)
		if result["isError"] == true {
			t.Fatalf("a deny delegation must commit a child (no pre-check runs for deny): %+v", resp)
		}
		denyChild := denyLeakageChildID(t, result)
		child, err := store.Get(ctx, "acme", denyChild)
		if err != nil {
			t.Fatalf("deny child must be committed: %v", err)
		}
		if !child.CredentialDeny {
			t.Fatalf("delegated deny child CredentialDeny = false, want true (the deny marker must persist for the origin-chain terminator)")
		}
		if _, err := store.Update(ctx, "acme", denyChild, func(s *sessionstore.Session) error {
			s.State = session.StateRunning
			return nil
		}); err != nil {
			t.Fatalf("promote deny child to running: %v", err)
		}

		// Inherit grandchild from the deny child. sub-tool declares
		// anthropic_direct with an active pool, so absent the origin-deny check
		// the pre-check would derive eligibility from the deny runtime and admit.
		resp = denyLeakageDelegate(t, mcpSrv, denyChild, "sub-tool", "inherit")
		result, _ = resp["result"].(map[string]any)
		if result["isError"] != true {
			t.Fatalf("an inherit hop from a deny origin must be a tool error: %+v", resp)
		}
		env := denyLeakageErrorEnvelope(t, result)
		if env["code"] != "CREDENTIAL_POOL_EXHAUSTED" {
			t.Errorf("grandchild code = %v, want CREDENTIAL_POOL_EXHAUSTED (deny origin holds no pool)", env["code"])
		}
		if denyLeakageChildrenOf(t, store, denyChild) != 0 {
			t.Error("an inherit-from-deny rejection must commit no grandchild session (pre-claim)")
		}
		if got := assigner.count() - before; got != 0 {
			t.Fatalf("inherit-from-deny grandchild minted %d lease tokens, want 0 (the rejection must precede any lease mint)", got)
		}
	})
}

// denyLeakageDelegate invokes lenny/delegate_task with the delegating
// principal on the request context and the declared credentialPropagation
// mode, returning the decoded JSON-RPC response.
func denyLeakageDelegate(t *testing.T, srv *mcp.Server, parentID, target, propagation string) map[string]any {
	t.Helper()
	args := `{"parentSessionId":"` + parentID + `","target":"` + target +
		`","poolRef":"pool-child","credentialPropagation":"` + propagation +
		`","task":{"input":[{"type":"text","inline":"do work"}]}}`
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"lenny/delegate_task","arguments":` + args + `}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte(body)))
	req = req.WithContext(authmw.WithPrincipal(req.Context(), denyLeakageCaller))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode delegate_task response: %v; body=%s", err, rr.Body.String())
	}
	return resp
}

// denyLeakageErrorEnvelope extracts the §15.2.1 lenny error envelope from an
// isError tool result.
func denyLeakageErrorEnvelope(t *testing.T, result map[string]any) map[string]any {
	t.Helper()
	content, _ := result["content"].([]any)
	for _, raw := range content {
		block, _ := raw.(map[string]any)
		if block["type"] != "lenny/error" {
			continue
		}
		text, _ := block["text"].(string)
		var env map[string]any
		if err := json.Unmarshal([]byte(text), &env); err != nil {
			t.Fatalf("decode error envelope: %v", err)
		}
		return env
	}
	t.Fatalf("no lenny/error block in %+v", content)
	return nil
}

// denyLeakageChildID reads the childSessionId from a successful delegate_task
// tool result.
func denyLeakageChildID(t *testing.T, result map[string]any) string {
	t.Helper()
	content, _ := result["content"].([]any)
	for _, raw := range content {
		block, _ := raw.(map[string]any)
		text, _ := block["text"].(string)
		var out struct {
			ChildSessionID string `json:"childSessionId"`
		}
		if json.Unmarshal([]byte(text), &out) == nil && out.ChildSessionID != "" {
			return out.ChildSessionID
		}
	}
	t.Fatalf("delegate_task result carried no childSessionId: %+v", content)
	return ""
}

// denyLeakageChildrenOf counts the committed session rows whose parent is
// parentID.
func denyLeakageChildrenOf(t *testing.T, store sessionstore.Store, parentID string) int {
	t.Helper()
	rows, err := store.List(context.Background(), "acme", sessionstore.ListFilter{})
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	n := 0
	for _, r := range rows {
		if r.ParentSessionID == parentID {
			n++
		}
	}
	return n
}

// denyLeakageIDCounter returns a monotonic id generator with the given prefix.
func denyLeakageIDCounter(prefix string) func() string {
	n := 0
	return func() string {
		n++
		return prefix + string(rune('0'+n))
	}
}

func mustCreateRuntimeDeny(t *testing.T, s runtimestore.Store, rt runtimestore.Runtime) {
	t.Helper()
	if err := s.Create(context.Background(), rt); err != nil {
		t.Fatalf("create runtime %s: %v", rt.Name, err)
	}
}

// denyLeakageCluster returns an envtest-backed cluster seeded with a warm pool,
// its template (SPIFFE binding not disabled, so the credential-delivery
// isolation gate does not reject the control child), and two idle Sandboxes for
// the shared-tool runtime, one per REST-created leaf the test finalizes.
func denyLeakageCluster(t *testing.T) client.Client {
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
	if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: deliveryGateNS}}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace: %v", err)
	}
	if err := c.Create(ctx, &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "shared-pool", Namespace: deliveryGateNS},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "shared-tmpl", MinWarm: 2, MaxWarm: 5},
	}); err != nil {
		t.Fatalf("create warm pool: %v", err)
	}
	if err := c.Create(ctx, &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "shared-tmpl", Namespace: deliveryGateNS},
		Spec:       lennyv1.SandboxTemplateSpec{RuntimeRef: "shared-tool", IsolationProfile: string(isolation.ProfileSandboxed)},
	}); err != nil {
		t.Fatalf("create template: %v", err)
	}
	for i, ip := range []string{"10.244.2.21", "10.244.2.22"} {
		name := "sbx-leaf-" + string(rune('a'+i))
		if err := c.Create(ctx, &lennyv1.Sandbox{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: deliveryGateNS,
				Labels: map[string]string{warmpool.LabelPool: "shared-pool"},
			},
		}); err != nil {
			t.Fatalf("create sandbox %s: %v", name, err)
		}
		u := &unstructured.Unstructured{}
		u.SetAPIVersion(lennyv1.GroupVersion.String())
		u.SetKind("Sandbox")
		u.SetName(name)
		u.SetNamespace(deliveryGateNS)
		_ = unstructured.SetNestedField(u.Object, map[string]interface{}{"phase": "idle", "podIP": ip}, "status")
		if err := c.Status().Patch(ctx, u, client.Apply, client.FieldOwner(string(ownership.WarmPoolController))); err != nil {
			t.Fatalf("seed WPC sandbox status %s: %v", name, err)
		}
	}
	return c
}
