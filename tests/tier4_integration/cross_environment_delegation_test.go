// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §8.3 cross-environment credential
// compatibility check on lenny/delegate_task. When a delegation crosses
// a §10.6 environment boundary with credentialPropagation: inherit, the
// gateway intersects the providers represented in the parent's origin
// credential pool with the child runtime's supportedProviders. A
// non-empty intersection admits the delegation (the gateway assigns a
// credential whose provider is in the intersection); an empty
// intersection is rejected deterministically with
// CREDENTIAL_PROVIDER_MISMATCH before any warm pod is claimed.
//
// The flow is driven end to end through the real lenny/delegate_task MCP
// handler over a real delegation.Service and the real §10.6 environment
// registry, session store, runtime registry, and tenant registry
// (delegation-time gate), and the constrained finalize-time credential
// assignment is observed through the real sessionserver finalize barrier,
// its pod binder, and the real credential-pool minting path over an
// envtest-backed warm pool (assignment-time constraint).

package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/admission/ownership"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/environment"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/environmentstore"
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
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"

	"github.com/lennylabs/lenny/pkg/adapter"
)

// The provider set the tenant credentialPolicy spans. Every provider used
// in the fixture has a pool, so the origin pool's provider set is bounded
// only by the origin and child runtime supportedProviders, isolating the
// §8.3 cross-environment compatibility check from the tenant policy.
const (
	provAnthropic = "anthropic_direct"
	provBedrock   = "aws_bedrock"
	provOpenAI    = "openai_direct"

	poolAnthropic = "claude-prod"
	poolBedrock   = "bedrock-prod"
	poolOpenAI    = "openai-prod"
)

// crossEnvFixture wires the real lenny/delegate_task MCP handler over a
// real delegation.Service and the real §10.6 stores, modelling a
// bilateral cross-environment topology:
//
//	team-a (origin) --outbound--> team-b (child) --outbound--> team-c (grandchild)
//
// The origin runtime team-a-agent supports {anthropic_direct, aws_bedrock}
// so the origin credential pool's provider set is that pair. team-b holds
// two cross-environment-reachable child runtimes: shared-tool
// ({anthropic_direct, openai_direct}) whose intersection with the origin
// pool is {anthropic_direct} (admit), and isolated-tool ({openai_direct})
// whose intersection is empty (reject). team-c holds leaf-tool
// ({openai_direct}) for the multi-hop origin re-check.
type crossEnvFixture struct {
	srv   *mcp.Server
	store sessionstore.Store
}

func newCrossEnvFixture(t *testing.T) *crossEnvFixture {
	t.Helper()
	store := memstore.New()
	runtimes := runtimestore.NewMemory()
	envs := environmentstore.NewMemory()
	tenants := tenantstore.NewMemory()

	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:        store,
		Executor:     executor.NewEchoExecutor(),
		Runtimes:     runtimes,
		Environments: envs,
		Tenants:      tenants,
		Delegation: delegation.NewService(store, delegation.Options{
			IDFunc:   childIDCounter(),
			Runtimes: runtimes,
		}),
		Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:   func() string { return "sess_mcp" },
		TenantID: "acme",
	})

	ctx := context.Background()
	sharedSel := environment.Selector{MatchLabels: map[string]string{"shared": "true"}}
	leafSel := environment.Selector{MatchLabels: map[string]string{"leaf": "true"}}

	// The origin runtime, resolved live at each inherit hop, supports the
	// origin pool's provider pair.
	mustCreateRuntime(t, runtimes, runtimestore.Runtime{
		Name: "team-a-agent", Type: runtimestore.TypeAgent,
		Labels:             map[string]string{"team": "a"},
		SupportedProviders: []string{provAnthropic, provBedrock},
	})
	// team-b child runtimes: shared-tool overlaps the origin pool; isolated-tool is disjoint.
	mustCreateRuntime(t, runtimes, runtimestore.Runtime{
		Name: "shared-tool", Type: runtimestore.TypeAgent,
		Labels:             map[string]string{"shared": "true"},
		SupportedProviders: []string{provAnthropic, provOpenAI},
	})
	mustCreateRuntime(t, runtimes, runtimestore.Runtime{
		Name: "isolated-tool", Type: runtimestore.TypeAgent,
		Labels:             map[string]string{"shared": "true"},
		SupportedProviders: []string{provOpenAI},
	})
	// team-c grandchild runtime: disjoint from the origin pool, but
	// overlapping the intermediate child shared-tool, so the multi-hop
	// re-check distinguishes an origin-pool comparison from an
	// intermediate-parent comparison.
	mustCreateRuntime(t, runtimes, runtimestore.Runtime{
		Name: "leaf-tool", Type: runtimestore.TypeAgent,
		Labels:             map[string]string{"leaf": "true"},
		SupportedProviders: []string{provOpenAI},
	})

	// The tenant credentialPolicy spans every provider so the origin pool's
	// provider set equals the origin runtime's supportedProviders.
	if err := tenants.Create(ctx, tenantstore.Tenant{
		ID: "acme",
		CredentialPolicy: credential.CredentialPolicy{
			ProviderPools: map[string]credential.ProviderPool{
				provAnthropic: {DefaultPool: poolAnthropic},
				provBedrock:   {DefaultPool: poolBedrock},
				provOpenAI:    {DefaultPool: poolOpenAI},
			},
		},
	}); err != nil {
		t.Fatalf("create tenant: %v", err)
	}

	// team-a admits its own team=a runtimes and declares outbound
	// delegation to team-b; team-b admits shared:true runtimes, accepts
	// inbound from team-a, and declares outbound to team-c; team-c admits
	// leaf:true runtimes and accepts inbound from team-b.
	mustCreateEnv(t, envs, environmentstore.Environment{
		Name: "team-a", TenantID: "acme",
		Members:         []environmentstore.Member{memberGroup("team-a-members")},
		RuntimeSelector: environment.Selector{MatchLabels: map[string]string{"team": "a"}},
		CrossEnvOutbound: []environmentstore.CrossEnvRule{
			{Environment: "team-b", Runtimes: sharedSel},
		},
	})
	mustCreateEnv(t, envs, environmentstore.Environment{
		Name: "team-b", TenantID: "acme",
		Members:         []environmentstore.Member{memberGroup("team-b-members")},
		RuntimeSelector: sharedSel,
		CrossEnvInbound: []environmentstore.CrossEnvRule{
			{Environment: "team-a", Runtimes: sharedSel},
		},
		CrossEnvOutbound: []environmentstore.CrossEnvRule{
			{Environment: "team-c", Runtimes: leafSel},
		},
	})
	mustCreateEnv(t, envs, environmentstore.Environment{
		Name: "team-c", TenantID: "acme",
		Members:         []environmentstore.Member{memberGroup("team-c-members")},
		RuntimeSelector: leafSel,
		CrossEnvInbound: []environmentstore.CrossEnvRule{
			{Environment: "team-b", Runtimes: leafSel},
		},
	})

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(ctx, sessionstore.Session{
		ID: "sess_parent", TenantID: "acme", UserID: "user_alice",
		State:      session.StateRunning,
		RuntimeRef: "team-a-agent", PoolRef: "pool-a", IsolationProfile: isolation.ProfileSandboxed,
		Environment: "team-a", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create parent session: %v", err)
	}
	return &crossEnvFixture{srv: srv, store: store}
}

// A cross-environment hop is only a cross-environment hop when the target
// runtime is outside the caller's own environment scope. The caller must
// therefore be a member of the delegating session's environment but not of
// the target's, so each hop uses the principal scoped to that hop's source
// environment: team-a for the team-a -> team-b hop, team-b for the
// team-b -> team-c hop. A caller that also belonged to the target
// environment would see the target as directly in scope and the delegation
// would not be cross-environment at all.
var (
	crossEnvCallerTeamA = authmw.Principal{Subject: "alice", TenantID: "acme", Groups: []string{"team-a-members"}}
	crossEnvCallerTeamB = authmw.Principal{Subject: "alice", TenantID: "acme", Groups: []string{"team-b-members"}}
)

// spec: 8.3 (cross-environment inherit provider-compatibility check,
// CREDENTIAL_PROVIDER_MISMATCH; multi-hop origin re-check)
// diagnosis: the §8.3 cross-environment inherit compatibility check
// diverged. A cross-environment delegate_task with
// credentialPropagation: inherit either admitted an incompatible child
// (empty provider intersection, which must reject with
// CREDENTIAL_PROVIDER_MISMATCH before pod allocation and before the
// child row is committed) or rejected a compatible one (non-empty
// intersection, which must proceed and assign a credential from the
// origin pool), or a multi-hop inherit chain compared the immediate
// parent's providers rather than the forwarded origin pool at the
// environment boundary.
func TestCrossEnvironmentDelegationCredentialCompatibility(t *testing.T) {
	// Reject: a cross-environment inherit delegation whose child shares no
	// provider with the origin pool is rejected with
	// CREDENTIAL_PROVIDER_MISMATCH before any warm pod is claimed and
	// before a child row is committed.
	//
	// The §8.3 line 470 "before any pod allocation" invariant is observed
	// here through the session store: delegation.Service.Delegate runs the
	// admission gates (this credential-compatibility gate included) and
	// commits the child row via insertChildSession, but it never claims a
	// pod. A pod is claimed later at the sessionserver finalize barrier
	// (exercised by admit_credential_assignment_constrained_to_origin_pool),
	// strictly downstream of a committed running child. The gate firing
	// before the child row commits (childrenOf == 0) therefore proves it
	// fires before pod allocation: no committed child means no session that
	// could ever reach finalize and claim a pod. Warm-pool counters only
	// move at the finalize barrier, so at the delegate-time tier the
	// pre-commit session-store check (childrenOf == 0) is the observable
	// pre-claim signal.
	t.Run("reject_disjoint_provider_intersection", func(t *testing.T) {
		fx := newCrossEnvFixture(t)
		resp := crossEnvCall(t, fx.srv, crossEnvCallerTeamA, "sess_parent", "isolated-tool", "inherit")
		result, _ := resp["result"].(map[string]any)
		if result["isError"] != true {
			t.Fatalf("a disjoint cross-environment inherit delegation must be a tool error: %+v", resp)
		}
		env := crossEnvErrorEnvelope(t, result)
		if env["code"] != "CREDENTIAL_PROVIDER_MISMATCH" {
			t.Errorf("code = %v, want CREDENTIAL_PROVIDER_MISMATCH", env["code"])
		}
		// spec: §15 — CREDENTIAL_PROVIDER_MISMATCH is POLICY / 422, non-retryable.
		if env["category"] != "POLICY" {
			t.Errorf("category = %v, want POLICY", env["category"])
		}
		if env["retryable"] != false {
			t.Errorf("retryable = %v, want false", env["retryable"])
		}
		if msg, _ := env["message"].(string); !strings.Contains(msg,
			"parent credential pool providers do not intersect with child runtime supportedProviders") {
			t.Errorf("message = %q, want the verbatim §8.3 mismatch message", msg)
		}
		// The rejection is pre-claim: the gate fires before
		// delegation.Service.Delegate (the pod-claiming call), so no child
		// session row is committed and no warm pod is claimed.
		if childrenOf(t, fx.store, "sess_parent") != 0 {
			t.Error("a CREDENTIAL_PROVIDER_MISMATCH rejection must commit no child session (pre-claim)")
		}
	})

	// Admit: a cross-environment inherit delegation whose child shares a
	// provider with the origin pool proceeds, commits a child, and threads
	// the origin credential pool onto the child row so the finalize-time
	// assignment (asserted separately) draws from the origin pool.
	t.Run("admit_overlapping_provider_intersection", func(t *testing.T) {
		fx := newCrossEnvFixture(t)
		resp := crossEnvCall(t, fx.srv, crossEnvCallerTeamA, "sess_parent", "shared-tool", "inherit")
		result, _ := resp["result"].(map[string]any)
		if result["isError"] == true {
			t.Fatalf("a compatible cross-environment inherit delegation must proceed: %+v", resp)
		}
		childID := crossEnvChildID(t, result)
		child, err := fx.store.Get(context.Background(), "acme", childID)
		if err != nil {
			t.Fatalf("a compatible inherit delegation must commit a child session: %v", err)
		}
		// The child appears in the parent's task tree (§8.2 lineage).
		if child.ParentSessionID != "sess_parent" {
			t.Errorf("child parent = %q, want sess_parent", child.ParentSessionID)
		}
		if child.RuntimeRef != "shared-tool" {
			t.Errorf("child runtime = %q, want shared-tool", child.RuntimeRef)
		}
		// §8.3 lines 472/488: the inherit hop threads the origin credential
		// pool onto the child. The parent carries no origin id, so the
		// child's origin is the parent itself (the env team-a origin pool).
		if child.CredentialOriginSessionID != "sess_parent" {
			t.Errorf("child CredentialOriginSessionID = %q, want sess_parent (the origin credential pool)",
				child.CredentialOriginSessionID)
		}
	})

	// Multi-hop: a grandchild inherit hop at the team-b -> team-c boundary
	// re-checks the forwarded origin pool (team-a, {anthropic_direct,
	// aws_bedrock}) against the grandchild runtime, not the intermediate
	// parent shared-tool ({anthropic_direct, openai_direct}). leaf-tool
	// supports only openai_direct: it overlaps the intermediate parent but
	// is disjoint from the origin pool, so the hop must reject. An
	// implementation that compared the intermediate parent's providers
	// would admit here.
	t.Run("multi_hop_re_checks_origin_pool_not_intermediate", func(t *testing.T) {
		fx := newCrossEnvFixture(t)

		// First hop: admit shared-tool into team-b.
		resp := crossEnvCall(t, fx.srv, crossEnvCallerTeamA, "sess_parent", "shared-tool", "inherit")
		result, _ := resp["result"].(map[string]any)
		if result["isError"] == true {
			t.Fatalf("first inherit hop must admit: %+v", resp)
		}
		intermediate := crossEnvChildID(t, result)

		// The intermediate child must be running in team-b to delegate
		// onward. delegate_task commits a child in the created state; move
		// it to running and tag its environment, mirroring the lifecycle a
		// delegated pod walks before it delegates.
		if _, err := fx.store.Update(context.Background(), "acme", intermediate, func(s *sessionstore.Session) error {
			s.State = session.StateRunning
			s.Environment = "team-b"
			return nil
		}); err != nil {
			t.Fatalf("promote intermediate child to running in team-b: %v", err)
		}

		// Second hop: inherit from the intermediate child to leaf-tool in
		// team-c. The forwarded origin pool is still team-a's, so the
		// disjoint origin∩grandchild intersection rejects.
		resp = crossEnvCall(t, fx.srv, crossEnvCallerTeamB, intermediate, "leaf-tool", "inherit")
		result, _ = resp["result"].(map[string]any)
		if result["isError"] != true {
			t.Fatalf("the grandchild hop must re-check the origin pool and reject leaf-tool: %+v", resp)
		}
		env := crossEnvErrorEnvelope(t, result)
		if env["code"] != "CREDENTIAL_PROVIDER_MISMATCH" {
			t.Errorf("grandchild code = %v, want CREDENTIAL_PROVIDER_MISMATCH (origin pool re-checked against grandchild)",
				env["code"])
		}
		details, _ := env["details"].(map[string]any)
		if details["originRuntime"] != "team-a-agent" {
			t.Errorf("grandchild rejection originRuntime = %v, want team-a-agent; the origin pool must be the team-a root, not the intermediate parent",
				details["originRuntime"])
		}
		// The rejected grandchild hop commits no child under the intermediate.
		if childrenOf(t, fx.store, intermediate) != 0 {
			t.Error("the rejected grandchild hop must commit no child session")
		}
	})

	// Admit assignment: an admitted inherit child draws its finalize-time
	// credential from the origin pool (spec/08 §8.3 line 470). Driven through the
	// real sessionserver finalize barrier, its pod binder, and the real
	// credential-pool minting path over an envtest-backed warm pool, the
	// child's assigned credential provider is constrained to the
	// origin∩child intersection ({anthropic_direct}), not the child's own
	// wider eligible set ({anthropic_direct, openai_direct}).
	t.Run("admit_credential_assignment_constrained_to_origin_pool", func(t *testing.T) {
		assertInheritChildDrawsFromOriginPool(t)
	})
}

// crossEnvCall invokes lenny/delegate_task with an authenticated principal
// on the request context so §10.6 environment membership resolves, and the
// declared credentialPropagation mode. It returns the decoded JSON-RPC
// response.
func crossEnvCall(t *testing.T, srv *mcp.Server, principal authmw.Principal, parentID, target, propagation string) map[string]any {
	t.Helper()
	args := `{"parentSessionId":"` + parentID + `","target":"` + target +
		`","poolRef":"pool-b","credentialPropagation":"` + propagation +
		`","task":{"input":[{"type":"text","inline":"do work"}]}}`
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"lenny/delegate_task","arguments":` + args + `}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte(body)))
	req = req.WithContext(authmw.WithPrincipal(req.Context(), principal))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode delegate_task response: %v; body=%s", err, rr.Body.String())
	}
	return resp
}

// crossEnvErrorEnvelope extracts the §15.2.1 lenny error envelope from an
// isError tool result.
func crossEnvErrorEnvelope(t *testing.T, result map[string]any) map[string]any {
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

// crossEnvChildID reads the childSessionId from a successful delegate_task
// tool result.
func crossEnvChildID(t *testing.T, result map[string]any) string {
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

// childrenOf counts the committed session rows whose parent is parentID.
func childrenOf(t *testing.T, store sessionstore.Store, parentID string) int {
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

// childIDCounter returns a monotonic child-session id generator so a
// multi-hop tree commits distinct child rows.
func childIDCounter() func() string {
	var mu sync.Mutex
	n := 0
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		n++
		return "sess_child_" + string(rune('0'+n))
	}
}

func mustCreateRuntime(t *testing.T, s runtimestore.Store, rt runtimestore.Runtime) {
	t.Helper()
	if err := s.Create(context.Background(), rt); err != nil {
		t.Fatalf("create runtime %s: %v", rt.Name, err)
	}
}

func mustCreateEnv(t *testing.T, s environmentstore.Store, e environmentstore.Environment) {
	t.Helper()
	if err := s.Create(context.Background(), e); err != nil {
		t.Fatalf("create environment %s: %v", e.Name, err)
	}
}

func memberGroup(group string) environmentstore.Member {
	return environmentstore.Member{
		Identity: environmentstore.Identity{Type: "oidc-group", Value: group},
		Role:     environment.RoleCreator,
	}
}

// poolRecordingAssigner is a podsession.CredentialAssigner that records
// every pool name AssignProto is called with, so the finalize-time
// credential assignment can be asserted against the origin∩child
// intersection. It returns a fixed proxy-mode lease per pool.
type poolRecordingAssigner struct {
	mu    sync.Mutex
	pools []string
}

func (a *poolRecordingAssigner) AssignProto(pool, _, _, _ string) (*adapterv1.CredentialLease, error) {
	a.mu.Lock()
	a.pools = append(a.pools, pool)
	a.mu.Unlock()
	return &adapterv1.CredentialLease{
		LeaseId:  "cl-" + pool,
		Provider: pool,
		Payload: []byte(`{"deliveryMode":"proxy",` +
			`"materializedConfig":{"proxyUrl":"https://p/v1","leaseToken":"lt-` + pool + `"}}`),
	}, nil
}

func (a *poolRecordingAssigner) ReleaseSession(string) {}

func (a *poolRecordingAssigner) assignedPools() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := append([]string(nil), a.pools...)
	sort.Strings(out)
	return out
}

// assertInheritChildDrawsFromOriginPool drives an inherit child through the
// real sessionserver create → finalize barrier over an envtest-backed warm
// pool and asserts the pod binder minted a credential only from the origin
// pool's provider (anthropic_direct → claude-prod), not the child's own
// wider eligible set. Before the origin-pool constraint applies, the child
// would additionally draw openai-prod (its own openai_direct provider).
//
// spec: §8.3 line 470 (assign a credential from the parent's pool whose
// provider appears in the intersection), line 440 (inherit draws from the
// origin pool).
func assertInheritChildDrawsFromOriginPool(t *testing.T) {
	envtest.SkipUnlessAvailable(t)
	ctx := context.Background()

	cluster := sharedToolCluster(t)
	assigner := &poolRecordingAssigner{}
	binder := &podsession.Binder{
		Client:           cluster,
		Namespace:        crossEnvNS,
		AdapterPort:      50051,
		AcceptedVersions: []string{adapter.ProtocolVersionV1},
		DialAdapter:      eagerAdapterDialer(t, newRecordingAdapter(t)),
		Blobs:            blobstore.NewMemoryStore(nil),
		Credentials:      assigner,
	}

	store := memstore.New()
	tenants := tenantstore.NewMemory()
	if err := tenants.Create(ctx, tenantstore.Tenant{
		ID: "acme",
		CredentialPolicy: credential.CredentialPolicy{
			PreferredSource: credential.PreferredSourcePool,
			ProviderPools: map[string]credential.ProviderPool{
				provAnthropic: {DefaultPool: poolAnthropic},
				provBedrock:   {DefaultPool: poolBedrock},
				provOpenAI:    {DefaultPool: poolOpenAI},
			},
		},
	}); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	runtimes := runtimestore.NewMemory()
	// The origin runtime supports {anthropic_direct, aws_bedrock}; the
	// child shared-tool supports {anthropic_direct, openai_direct}. The
	// origin∩child intersection is {anthropic_direct}.
	mustCreateRuntime(t, runtimes, runtimestore.Runtime{
		Name: "team-a-agent", SupportedProviders: []string{provAnthropic, provBedrock},
	})
	mustCreateRuntime(t, runtimes, runtimestore.Runtime{
		Name: "shared-tool", SupportedProviders: []string{provAnthropic, provOpenAI},
	})

	credPools := credentialpoolstore.NewMemory()
	for _, p := range []struct{ name, provider string }{
		{poolAnthropic, provAnthropic},
		{poolOpenAI, provOpenAI},
		{poolBedrock, provBedrock},
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

	// The origin session row the child inherits its credential pool from.
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(ctx, sessionstore.Session{
		ID: "sess-origin", TenantID: "acme", UserID: "alice@acme.com",
		RuntimeRef: "team-a-agent", State: session.StateRunning,
		IsolationProfile: isolation.ProfileSandboxed, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed origin session: %v", err)
	}

	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  func() string { return "sess-inherit-child" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             podsession.NewRegistry(),
		AgentNamespace:          crossEnvNS,
		Tenants:                 tenants,
		Runtimes:                runtimes,
		CredentialPools:         credPools,
		CredentialRouter:        credrouter.NewDefault(),
		Blobs:                   binder.Blobs,
	})
	h := srv.Handler()

	post := func(path string, body []byte) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}

	// Create the child (eager pod claim). The create-time pre-check uses
	// the child's own eligible set ({anthropic_direct, openai_direct}); the
	// inherit constraint applies at finalize.
	createBody, _ := json.Marshal(sessionserver.CreateSessionRequest{RuntimeRef: "shared-tool", UserID: "alice@acme.com"})
	if rr := post("/v1/sessions", createBody); rr.Code != http.StatusCreated {
		t.Fatalf("create inherit child: status %d, body=%s", rr.Code, rr.Body.String())
	}
	// Thread the origin credential pool onto the child, as the §8.3 inherit
	// hop (spec/08 §8.3 lines 472, 488) does when the delegation commits the row.
	if _, err := store.Update(ctx, "acme", "sess-inherit-child", func(s *sessionstore.Session) error {
		s.CredentialOriginSessionID = "sess-origin"
		return nil
	}); err != nil {
		t.Fatalf("thread origin onto inherit child: %v", err)
	}

	// Finalize: the credential lease is assigned here, constrained to the
	// origin∩child intersection.
	rr := post("/v1/sessions/sess-inherit-child/finalize", []byte(`{}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("finalize inherit child: status %d, body=%s", rr.Code, rr.Body.String())
	}

	got := assigner.assignedPools()
	want := []string{poolAnthropic}
	if len(got) != 1 || got[0] != poolAnthropic {
		t.Fatalf("inherit child assigned pools = %v, want %v (only the origin∩child provider); openai-prod present means the child drew from its own set, not the origin pool",
			got, want)
	}
}

// crossEnvNS is the envtest namespace the finalize sub-test claims pods in.
const crossEnvNS = "lenny-agents"

// sharedToolCluster returns an envtest-backed cluster seeded with a warm
// pool, its template, and one idle Sandbox for the shared-tool runtime, so
// the create handler can claim a pod for the inherit child.
func sharedToolCluster(t *testing.T) client.Client {
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
	if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: crossEnvNS}}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace: %v", err)
	}
	if err := c.Create(ctx, &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "shared-pool", Namespace: crossEnvNS},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "shared-tmpl", MinWarm: 1, MaxWarm: 5},
	}); err != nil {
		t.Fatalf("create warm pool: %v", err)
	}
	if err := c.Create(ctx, &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "shared-tmpl", Namespace: crossEnvNS},
		Spec:       lennyv1.SandboxTemplateSpec{RuntimeRef: "shared-tool", IsolationProfile: string(isolation.ProfileSandboxed)},
	}); err != nil {
		t.Fatalf("create template: %v", err)
	}
	if err := c.Create(ctx, &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: "sbx-share", Namespace: crossEnvNS,
			Labels: map[string]string{warmpool.LabelPool: "shared-pool"},
		},
	}); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	u := &unstructured.Unstructured{}
	u.SetAPIVersion(lennyv1.GroupVersion.String())
	u.SetKind("Sandbox")
	u.SetName("sbx-share")
	u.SetNamespace(crossEnvNS)
	_ = unstructured.SetNestedField(u.Object, map[string]interface{}{"phase": "idle", "podIP": "10.244.2.9"}, "status")
	if err := c.Status().Patch(ctx, u, client.Apply, client.FieldOwner(string(ownership.WarmPoolController))); err != nil {
		t.Fatalf("seed WPC sandbox status: %v", err)
	}
	return c
}

// newRecordingAdapter builds an in-process adapter.Server the finalize
// binder streams the workspace and credential lease into.
func newRecordingAdapter(t *testing.T) *adapter.Server {
	t.Helper()
	srv := adapter.New("cross-env-adapter")
	srv.WorkspaceRoot = t.TempDir()
	srv.StagingDir = t.TempDir()
	srv.CredentialsDir = t.TempDir()
	srv.Runtime = &noopRuntime{}
	return srv
}

// noopRuntime satisfies adapter.RuntimeProcess for the finalize path, which
// prepares the pod but does not launch the runtime.
type noopRuntime struct{}

func (noopRuntime) Start(context.Context, string) error           { return nil }
func (noopRuntime) WriteEnvelope(string, []byte) error            { return nil }
func (noopRuntime) Interrupt(context.Context, string, bool) error { return nil }
func (noopRuntime) Close(context.Context, string) error           { return nil }
func (noopRuntime) Output(context.Context, string) (<-chan []byte, error) {
	ch := make(chan []byte)
	close(ch)
	return ch, nil
}
