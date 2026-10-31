// SPDX-License-Identifier: MIT

package sessionserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	ctrlinterceptor "sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/experiment"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/experimentstore"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/sdkwarm"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

const (
	// wdRuntime declares two default files and one default setup command.
	wdRuntime = "wd-rt"
	// wdPlainRuntime declares no workspaceDefaults.
	wdPlainRuntime = "wd-plain"
	wdTenant       = "default"
)

// wdDefaults is the workspaceDefaults block of wdRuntime.
func wdDefaults() *runtimestore.WorkspaceDefaults {
	return &runtimestore.WorkspaceDefaults{
		Files: []runtimestore.WorkspaceFile{
			{Path: "CLAUDE.md", Content: "default instructions"},
			{Path: "defaults.txt", Content: "default content"},
		},
		SetupCommands: []runtimestore.WorkspaceSetupCommand{{Cmd: "echo defaults", TimeoutSeconds: 30}},
	}
}

// wdFixture is a minimal session server over an in-memory runtime store
// whose Get can be failed per runtime.
type wdFixture struct {
	srv      *Server
	store    sessionstore.Store
	runtimes *faultRuntimes
	nextID   string
	// cluster and writes are set when wdOpts.claims wires a pod binder.
	cluster client.Client
	writes  *kubeWrites
}

type wdOpts struct {
	experiments experimentstore.Store
	chain       *interceptor.Chain
	// claims wires a pod binder over a fake cluster holding one warm pool
	// per runtime, so a create route that reaches the §7.1 claim writes a
	// SandboxClaim the test can count.
	claims bool
}

func newWDFixture(t *testing.T, extra []runtimestore.Runtime, opts wdOpts) *wdFixture {
	t.Helper()
	mem := runtimestore.NewMemory()
	rts := append([]runtimestore.Runtime{
		{Name: wdRuntime, Type: runtimestore.TypeAgent, WorkspaceDefaults: wdDefaults()},
		{Name: wdPlainRuntime, Type: runtimestore.TypeAgent},
	}, extra...)
	for _, rt := range rts {
		if err := mem.Create(context.Background(), rt); err != nil {
			t.Fatalf("seed runtime %s: %v", rt.Name, err)
		}
	}
	f := &wdFixture{store: memstore.New(), runtimes: &faultRuntimes{Store: mem}}
	clock := func() time.Time { return time.Date(2026, 6, 8, 9, 0, 0, 0, time.UTC) }
	srvOpts := Options{
		DevTenantHeader: true,
		Clock:           clock,
		Runtimes:        f.runtimes,
		Experiments:     opts.experiments,
		Interceptors:    opts.chain,
		IDFunc:          func() string { return f.nextID },
	}
	if opts.claims {
		f.writes = &kubeWrites{}
		f.cluster = wdCluster(t, rts, f.writes)
		srvOpts.PodBinder = &podsession.Binder{Client: f.cluster, Namespace: lookupNS}
		srvOpts.AgentNamespace = lookupNS
		srvOpts.DefaultIsolationProfile = isolation.ProfileSandboxed
	}
	f.srv = New(f.store, srvOpts)
	t.Cleanup(f.srv.Close)
	return f
}

// wdCluster is a fake cluster holding a warm pool, a template, and one idle
// sandbox with its pod for each runtime in rts, recording every create,
// update, and patch in writes.
func wdCluster(t *testing.T, rts []runtimestore.Runtime, writes *kubeWrites) client.Client {
	t.Helper()
	var objs []client.Object
	for _, rt := range rts {
		pool, tmpl := rt.Name+"-pool", rt.Name+"-tmpl"
		objs = append(
			objs,
			&lennyv1.SandboxWarmPool{
				ObjectMeta: metav1.ObjectMeta{Name: pool, Namespace: lookupNS},
				Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: tmpl, MinWarm: 1, MaxWarm: 5},
			},
			&lennyv1.SandboxTemplate{
				ObjectMeta: metav1.ObjectMeta{Name: tmpl, Namespace: lookupNS},
				Spec:       lennyv1.SandboxTemplateSpec{RuntimeRef: rt.Name, IsolationProfile: string(isolation.ProfileSandboxed)},
			},
			&lennyv1.Sandbox{
				ObjectMeta: metav1.ObjectMeta{Name: rt.Name + "-sbx", Namespace: lookupNS, Labels: map[string]string{warmpool.LabelPool: pool}},
				Status:     lennyv1.SandboxStatus{Phase: "idle", PodIP: "10.244.0.9"},
			},
			&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: rt.Name + "-sbx", Namespace: lookupNS}},
		)
	}
	scheme := claimAtCreateScheme(t)
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	record := func(verb string) { writes.add(verb) }
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).
		WithInterceptorFuncs(ctrlinterceptor.Funcs{
			Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				record("create")
				return cl.Create(ctx, obj, opts...)
			},
			Update: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				record("update")
				return cl.Update(ctx, obj, opts...)
			},
			Patch: func(ctx context.Context, cl client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				record("patch")
				return cl.Patch(ctx, obj, patch, opts...)
			},
		}).Build()
}

// claimCount returns the number of SandboxClaims in the fixture's cluster.
func (f *wdFixture) claimCount(t *testing.T) int {
	t.Helper()
	var claims lennyv1.SandboxClaimList
	if err := f.cluster.List(context.Background(), &claims, client.InNamespace(lookupNS)); err != nil {
		t.Fatalf("list claims: %v", err)
	}
	return len(claims.Items)
}

// post sends a create or create-and-start request for runtime with the
// given plan (omitted when empty) under session ID id.
func (f *wdFixture) post(t *testing.T, path, id, runtime, plan string) *httptest.ResponseRecorder {
	t.Helper()
	f.nextID = id
	body := map[string]any{"runtimeRef": runtime, "userId": "alice"}
	if plan != "" {
		body["workspacePlan"] = json.RawMessage(plan)
	}
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Lenny-Tenant-ID", wdTenant)
	rr := httptest.NewRecorder()
	asOwner(f.srv.Handler()).ServeHTTP(rr, r)
	return rr
}

// seed stores a created row for runtime with no plan.
func (f *wdFixture) seed(t *testing.T, id, runtime string) {
	t.Helper()
	at := time.Date(2026, 6, 8, 9, 0, 0, 0, time.UTC)
	if err := f.store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: wdTenant, UserID: testOwner, RuntimeRef: runtime,
		State: session.StateCreated, CreatedAt: at, UpdatedAt: at,
	}); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

func (f *wdFixture) row(t *testing.T, id string) sessionstore.Session {
	t.Helper()
	row, err := f.store.Get(context.Background(), wdTenant, id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return row
}

func (f *wdFixture) exists(id string) bool {
	_, err := f.store.Get(context.Background(), wdTenant, id)
	return err == nil
}

// getPlan returns the workspacePlan GET /v1/sessions/{id} reports.
func (f *wdFixture) getPlan(t *testing.T, id string) workspaceplan.Plan {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+id, nil)
	r.Header.Set("X-Lenny-Tenant-ID", wdTenant)
	rr := httptest.NewRecorder()
	asOwner(f.srv.Handler()).ServeHTTP(rr, r)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET %s: status %d, body=%s", id, rr.Code, rr.Body.String())
	}
	var resp struct {
		WorkspacePlan json.RawMessage `json:"workspacePlan"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode GET: %v", err)
	}
	if len(resp.WorkspacePlan) == 0 {
		return workspaceplan.Plan{}
	}
	p, _, err := workspaceplan.ParseStored(resp.WorkspacePlan)
	if err != nil {
		t.Fatalf("parse GET plan: %v", err)
	}
	return p
}

// storedPlan parses the plan stored on the row.
func storedPlan(t *testing.T, row sessionstore.Session) workspaceplan.Plan {
	t.Helper()
	p, err := storedWorkspacePlan(row)
	if err != nil {
		t.Fatalf("parse stored plan: %v", err)
	}
	return p
}

// planPaths lists each source's path, in order.
func planPaths(p workspaceplan.Plan) []string {
	out := make([]string, 0, len(p.Sources))
	for _, s := range p.Sources {
		out = append(out, s.Variant.Path())
	}
	return out
}

func planCmds(p workspaceplan.Plan) []string {
	out := make([]string, 0, len(p.SetupCommands))
	for _, c := range p.SetupCommands {
		out = append(out, c.Cmd)
	}
	return out
}

func assertList(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func wdErrorCode(t *testing.T, rr *httptest.ResponseRecorder) (string, map[string]any) {
	t.Helper()
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v; body=%s", err, rr.Body.String())
	}
	return env.Error.Code, env.Error.Details
}

const wdClientPlan = `{"schemaVersion":1,"sources":[{"type":"inlineFile","path":"src/main.go","content":"package main"}],"setupCommands":[{"cmd":"go build ./..."}]}`

var (
	wdDefaultPaths = []string{"CLAUDE.md", "defaults.txt"}
	wdDefaultCmds  = []string{"echo defaults"}
)

// TestCreateWithPlanStoresComposedPlan_spec_14_1 pins that a plan bound at
// POST /v1/sessions is stored with the runtime's defaults ahead of the
// client's entries, and that GET returns the composed plan.
//
// spec: 14.1 (WorkspacePlan Schema Versioning), 5.1 (Runtime)
func TestCreateWithPlanStoresComposedPlan_spec_14_1(t *testing.T) {
	f := newWDFixture(t, nil, wdOpts{})
	if rr := f.post(t, "/v1/sessions", "sess_c", wdRuntime, wdClientPlan); rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body=%s", rr.Code, rr.Body.String())
	}
	got := f.getPlan(t, "sess_c")
	assertList(t, "sources", planPaths(got), append(wdDefaultPaths, "src/main.go"))
	assertList(t, "setupCommands", planCmds(got), append(wdDefaultCmds, "go build ./..."))
	if got.SetupCommands[0].TimeoutSeconds != 30 {
		t.Errorf("default command timeout = %d, want 30", got.SetupCommands[0].TimeoutSeconds)
	}
}

// TestCreateWithoutPlanComposesAtFinalize_spec_14_1 pins that a create
// without a plan binds nothing, that a body-less finalize binds the
// defaults-only plan, and that a finalize body plan is stored behind the
// defaults.
//
// spec: 14.1 (WorkspacePlan Schema Versioning), 5.1 (Runtime)
func TestCreateWithoutPlanComposesAtFinalize_spec_14_1(t *testing.T) {
	f := newWDFixture(t, nil, wdOpts{})
	if rr := f.post(t, "/v1/sessions", "sess_np", wdRuntime, ""); rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body=%s", rr.Code, rr.Body.String())
	}
	if row := f.row(t, "sess_np"); len(row.WorkspacePlan) != 0 {
		t.Fatalf("create without a plan stored %s, want none", row.WorkspacePlan)
	}
	if rr := postFinalize(t, f.srv, "sess_np", ""); rr.Code != http.StatusOK {
		t.Fatalf("finalize: status %d, body=%s", rr.Code, rr.Body.String())
	}
	got := storedPlan(t, f.row(t, "sess_np"))
	assertList(t, "no-body finalize sources", planPaths(got), wdDefaultPaths)
	assertList(t, "no-body finalize setupCommands", planCmds(got), wdDefaultCmds)

	f.seed(t, "sess_fb", wdRuntime)
	if rr := postFinalize(t, f.srv, "sess_fb", `{"workspacePlan":`+wdClientPlan+`}`); rr.Code != http.StatusOK {
		t.Fatalf("finalize with body: status %d, body=%s", rr.Code, rr.Body.String())
	}
	got = storedPlan(t, f.row(t, "sess_fb"))
	assertList(t, "body finalize sources", planPaths(got), append(wdDefaultPaths, "src/main.go"))
	assertList(t, "body finalize setupCommands", planCmds(got), append(wdDefaultCmds, "go build ./..."))
}

// TestCreateAndStartComposesDefaults_spec_14_1 pins that create-and-start
// without a plan binds the defaults-only plan, and that the build the claim
// and start stages read carries the same plan the row stores.
//
// spec: 14.1 (WorkspacePlan Schema Versioning), 5.1 (Runtime)
func TestCreateAndStartComposesDefaults_spec_14_1(t *testing.T) {
	f := newWDFixture(t, nil, wdOpts{})
	if rr := f.post(t, "/v1/sessions/start", "sess_cs", wdRuntime, ""); rr.Code/100 != 2 {
		t.Fatalf("create-and-start: status %d, body=%s", rr.Code, rr.Body.String())
	}
	got := storedPlan(t, f.row(t, "sess_cs"))
	assertList(t, "create-and-start sources", planPaths(got), wdDefaultPaths)
	assertList(t, "create-and-start setupCommands", planCmds(got), wdDefaultCmds)

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/start", nil)
	rec := httptest.NewRecorder()
	f.nextID = "sess_direct_start"
	row, build, ok := f.srv.buildCreateAndStartRow(rec, req, wdTenant, "alice", CreateAndStartRequest{RuntimeRef: wdRuntime})
	if !ok {
		t.Fatalf("buildCreateAndStartRow rejected: %s", rec.Body.String())
	}
	assertSamePlan(t, "create-and-start build", build.parsedPlan, storedPlan(t, row))
	assertList(t, "create-and-start build sources", planPaths(build.parsedPlan), wdDefaultPaths)

	req = httptest.NewRequest(http.MethodPost, "/v1/sessions", nil)
	rec = httptest.NewRecorder()
	f.nextID = "sess_direct_create"
	row, cbuild, ok := f.srv.validateAndBuildCreateRow(rec, req, CreateSessionRequest{
		RuntimeRef: wdRuntime, WorkspacePlan: json.RawMessage(wdClientPlan),
	}, wdTenant, "alice", trace.SpanFromContext(req.Context()))
	if !ok {
		t.Fatalf("validateAndBuildCreateRow rejected: %s", rec.Body.String())
	}
	assertSamePlan(t, "create build", cbuild.parsedPlan, storedPlan(t, row))
	assertList(t, "create build sources", planPaths(cbuild.parsedPlan), append(wdDefaultPaths, "src/main.go"))
}

func assertSamePlan(t *testing.T, what string, a, b workspaceplan.Plan) {
	t.Helper()
	ra, err := workspaceplan.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	rb, err := workspaceplan.Marshal(b)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Equal(ra, rb) {
		t.Errorf("%s plan = %s, stored plan = %s", what, ra, rb)
	}
}

// TestBoundDefaultsArePinned_spec_14_1 pins that the defaults are fixed when
// the plan binds: replacing the runtime's defaults afterwards leaves the
// stored plan, and the bind request built from it, unchanged.
//
// spec: 14.1 (WorkspacePlan Schema Versioning), 5.1 (Runtime)
func TestBoundDefaultsArePinned_spec_14_1(t *testing.T) {
	f := newWDFixture(t, nil, wdOpts{})
	if rr := f.post(t, "/v1/sessions", "sess_pin", wdRuntime, wdClientPlan); rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body=%s", rr.Code, rr.Body.String())
	}
	before := append([]byte(nil), f.row(t, "sess_pin").WorkspacePlan...)
	if _, err := f.runtimes.Update(context.Background(), wdRuntime, func(rt *runtimestore.Runtime) error {
		rt.WorkspaceDefaults = &runtimestore.WorkspaceDefaults{
			Files: []runtimestore.WorkspaceFile{{Path: "CLAUDE.md", Content: "replaced"}},
		}
		return nil
	}); err != nil {
		t.Fatalf("replace defaults: %v", err)
	}
	if rr := postFinalize(t, f.srv, "sess_pin", ""); rr.Code != http.StatusOK {
		t.Fatalf("finalize: status %d, body=%s", rr.Code, rr.Body.String())
	}
	row := f.row(t, "sess_pin")
	if !bytes.Equal(row.WorkspacePlan, before) {
		t.Fatalf("stored plan changed at finalize:\n got %s\nwant %s", row.WorkspacePlan, before)
	}
	req := f.srv.exclusiveBindRequest(context.Background(), row, podsession.PoolMatch{}, storedPlan(t, row), nil, nil, nil, "")
	var original, replaced int
	for _, src := range req.Plan.GetSources() {
		switch src.GetContent() {
		case "default instructions":
			original++
		case "replaced":
			replaced++
		}
	}
	if original != 1 || replaced != 0 {
		t.Errorf("bind request carries the original default %d times and the replacement %d times, want 1 and 0", original, replaced)
	}
}

// TestDefaultCommandsExemptFromSetupPolicy_spec_7_5 pins that
// setupCommandPolicy checks and counts only the client's commands: default
// commands outside the allowlist do not reject the plan or consume the
// maxCommands budget, while a client plan over the cap is still rejected and
// a rejected finalize binds nothing.
//
// spec: 14.1 (WorkspacePlan Schema Versioning), 5.1 (Runtime), 7.5 (Setup Commands)
func TestDefaultCommandsExemptFromSetupPolicy_spec_7_5(t *testing.T) {
	policyRT := runtimestore.Runtime{
		Name: "wd-policy", Type: runtimestore.TypeAgent,
		SetupCommandPolicy: &runtimestore.SetupCommandPolicy{
			Mode: runtimestore.SetupCommandModeAllowlist, Allowlist: []string{"make"}, MaxCommands: 1,
		},
		WorkspaceDefaults: &runtimestore.WorkspaceDefaults{SetupCommands: []runtimestore.WorkspaceSetupCommand{
			{Cmd: "pip install -r requirements.txt"}, {Cmd: "npm ci"},
		}},
	}
	oneMake := `{"schemaVersion":1,"sources":[],"setupCommands":[{"cmd":"make build"}]}`
	twoMake := `{"schemaVersion":1,"sources":[],"setupCommands":[{"cmd":"make build"},{"cmd":"make test"}]}`
	routes := []string{"/v1/sessions", "/v1/sessions/start", "finalize"}
	for _, route := range routes {
		t.Run(route+"/accepted", func(t *testing.T) {
			f := newWDFixture(t, []runtimestore.Runtime{policyRT}, wdOpts{})
			rr := f.bind(t, route, "sess_ok", "wd-policy", oneMake)
			if rr.Code/100 != 2 {
				t.Fatalf("status %d, body=%s", rr.Code, rr.Body.String())
			}
			assertList(t, "setupCommands", planCmds(storedPlan(t, f.row(t, "sess_ok"))),
				[]string{"pip install -r requirements.txt", "npm ci", "make build"})
		})
		t.Run(route+"/over-cap", func(t *testing.T) {
			f := newWDFixture(t, []runtimestore.Runtime{policyRT}, wdOpts{})
			rr := f.bind(t, route, "sess_cap", "wd-policy", twoMake)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400; body=%s", rr.Code, rr.Body.String())
			}
			_, details := wdErrorCode(t, rr)
			if details["reason"] != "setup_commands_max_exceeded" {
				t.Errorf("reason = %v, want setup_commands_max_exceeded", details["reason"])
			}
			if got, _ := details["count"].(float64); int(got) != 2 {
				t.Errorf("count = %v, want 2", details["count"])
			}
			f.assertUnbound(t, route, "sess_cap")
		})
	}
}

// bind drives one binding route with plan: POST /v1/sessions,
// POST /v1/sessions/start, or the finalize body of a session created
// without a plan. An empty plan sends no plan.
func (f *wdFixture) bind(t *testing.T, route, id, runtime, plan string) *httptest.ResponseRecorder {
	t.Helper()
	if route != "finalize" {
		return f.post(t, route, id, runtime, plan)
	}
	f.seed(t, id, runtime)
	body := ""
	if plan != "" {
		body = `{"workspacePlan":` + plan + `}`
	}
	return postFinalize(t, f.srv, id, body)
}

// assertUnbound checks that a rejected binding request left no state: no row
// for a create route, and a created row with no plan for finalize.
func (f *wdFixture) assertUnbound(t *testing.T, route, id string) {
	t.Helper()
	if route != "finalize" {
		if f.exists(id) {
			t.Errorf("%s persisted a row after the rejection", route)
		}
		return
	}
	row := f.row(t, id)
	if row.State != session.StateCreated || len(row.WorkspacePlan) != 0 {
		t.Errorf("finalize left the row %s with plan %q, want created with no plan", row.State, row.WorkspacePlan)
	}
}

// TestDefaultsResolveFailureLeavesStateUnchanged_spec_14_1 pins the
// fail-closed composition: a transient read of the runtime or of a derived
// runtime's base fails every binding request with 503 SERVICE_UNAVAILABLE
// and Retry-After, and a derived runtime whose base is missing fails it with
// 500 INTERNAL_ERROR. Each leaves no session state behind, and a
// rejected create route takes no SandboxClaim.
//
// spec: 14.1 (WorkspacePlan Schema Versioning), 5.1 (Runtime)
func TestDefaultsResolveFailureLeavesStateUnchanged_spec_14_1(t *testing.T) {
	extra := []runtimestore.Runtime{
		{Name: "wd-base", Type: runtimestore.TypeAgent, WorkspaceDefaults: wdDefaults()},
		{Name: "wd-derived", BaseRuntime: "wd-base"},
		{Name: "wd-orphan", BaseRuntime: "wd-gone"},
	}
	type binding struct{ route, plan string }
	bindings := []binding{
		{"/v1/sessions", wdClientPlan},
		{"/v1/sessions/start", ""},
		{"finalize", wdClientPlan},
		{"finalize", ""},
	}
	faults := []struct {
		name     string
		runtime  string
		arm      string
		wantCode string
		status   int
	}{
		{"runtime-transient", wdRuntime, wdRuntime, "SERVICE_UNAVAILABLE", http.StatusServiceUnavailable},
		{"base-transient", "wd-derived", "wd-base", "SERVICE_UNAVAILABLE", http.StatusServiceUnavailable},
		{"base-missing", "wd-orphan", "", "INTERNAL_ERROR", http.StatusInternalServerError},
	}
	for _, fault := range faults {
		for _, b := range bindings {
			name := fault.name + b.route
			if b.plan == "" {
				name += "/no-plan"
			}
			t.Run(name, func(t *testing.T) {
				f := newWDFixture(t, extra, wdOpts{claims: b.route != "finalize"})
				if fault.arm != "" {
					// The finalize row is seeded straight into the session
					// store, so the fault hits only the binding request's
					// runtime read.
					f.runtimes.arm(fault.arm, errors.New("injected: connection reset"))
				}
				rr := f.bind(t, b.route, "sess_fail", fault.runtime, b.plan)
				if rr.Code != fault.status {
					t.Fatalf("status %d, want %d; body=%s", rr.Code, fault.status, rr.Body.String())
				}
				code, _ := wdErrorCode(t, rr)
				if code != fault.wantCode {
					t.Errorf("code = %q, want %q", code, fault.wantCode)
				}
				if fault.status == http.StatusServiceUnavailable && rr.Header().Get("Retry-After") == "" {
					t.Error("SERVICE_UNAVAILABLE carries no Retry-After")
				}
				f.assertUnbound(t, b.route, "sess_fail")
				if b.route != "finalize" {
					f.assertNoClaim(t, b.route)
				}
			})
		}
	}
	// The no-claim assertion above discriminates only if the fixture's
	// cluster takes a claim when composition succeeds.
	t.Run("control/claim-reached", func(t *testing.T) {
		f := newWDFixture(t, extra, wdOpts{claims: true})
		rr := f.post(t, "/v1/sessions/start", "sess_ok", wdRuntime, "")
		if n := f.writes.nonDelete(); n == 0 {
			t.Fatalf("a composing create-and-start wrote nothing to the cluster (status %d, body=%s); the no-claim assertion is vacuous", rr.Code, rr.Body.String())
		}
	})
}

// assertNoClaim checks that a rejected create route reached no §7.1 pod
// claim: the cluster holds no SandboxClaim and took no write.
func (f *wdFixture) assertNoClaim(t *testing.T, route string) {
	t.Helper()
	if n := f.claimCount(t); n != 0 {
		t.Errorf("%s left %d SandboxClaims after the rejection, want 0", route, n)
	}
	if n := f.writes.nonDelete(); n != 0 {
		t.Errorf("%s wrote to the cluster after the rejection: %v", route, f.writes.counts)
	}
}

// TestRuntimeWithoutDefaultsStoresSubmittedBytes_spec_14_1 pins that a
// runtime with no defaults, or a runtimeRef with no registry entry, stores
// the submitted plan bytes unchanged.
//
// spec: 14.1 (WorkspacePlan Schema Versioning), 5.1 (Runtime)
func TestRuntimeWithoutDefaultsStoresSubmittedBytes_spec_14_1(t *testing.T) {
	for _, runtime := range []string{wdPlainRuntime, "wd-unregistered"} {
		t.Run(runtime, func(t *testing.T) {
			f := newWDFixture(t, nil, wdOpts{})
			if rr := f.post(t, "/v1/sessions", "sess_raw", runtime, wdClientPlan); rr.Code != http.StatusCreated {
				t.Fatalf("create: status %d, body=%s", rr.Code, rr.Body.String())
			}
			if got := f.row(t, "sess_raw").WorkspacePlan; string(got) != wdClientPlan {
				t.Errorf("stored plan = %s, want the submitted bytes %s", got, wdClientPlan)
			}
		})
	}
}

// TestRoutedRuntimeDefaultsAreComposed_spec_14_1 pins that the defaults come
// from the runtime routing selected: an experiment variant's runtime at
// create and at create-and-start, and a priority-350 PreRoute MODIFY at
// create-and-start. The requested runtime's defaults are absent.
//
// spec: 14.1 (WorkspacePlan Schema Versioning), 5.1 (Runtime), 10.7 (Experiment Primitives), 4.8 (Gateway Policy Engine)
func TestRoutedRuntimeDefaultsAreComposed_spec_14_1(t *testing.T) {
	variant := runtimestore.Runtime{Name: "wd-variant", Type: runtimestore.TypeAgent, WorkspaceDefaults: &runtimestore.WorkspaceDefaults{
		Files: []runtimestore.WorkspaceFile{{Path: "variant.txt", Content: "v"}},
	}}
	exps := func(t *testing.T) experimentstore.Store {
		s := experimentstore.NewMemory()
		if err := s.Create(context.Background(), experimentstore.Experiment{
			ID: "exp_wd", TenantID: wdTenant, Status: experiment.StatusActive, BaseRuntime: wdRuntime,
			Variants:      []experimentstore.Variant{{ID: "treatment", Runtime: "wd-variant", Pool: "wd-variant-pool", Weight: 0.999999}},
			TargetingMode: experiment.TargetingPercentage, Sticky: experiment.StickySession,
			Propagation: experiment.PropagationInherit,
		}); err != nil {
			t.Fatalf("seed experiment: %v", err)
		}
		return s
	}
	assertVariant := func(t *testing.T, f *wdFixture, id string) {
		t.Helper()
		row := f.row(t, id)
		if row.RuntimeRef != "wd-variant" {
			t.Fatalf("routed runtime = %q, want wd-variant", row.RuntimeRef)
		}
		paths := planPaths(storedPlan(t, row))
		if len(paths) == 0 || paths[0] != "variant.txt" {
			t.Errorf("sources = %v, want the variant default first", paths)
		}
		for _, p := range paths {
			if p == "defaults.txt" {
				t.Errorf("sources = %v carry the requested runtime's default", paths)
			}
		}
	}
	t.Run("experiment/create", func(t *testing.T) {
		f := newWDFixture(t, []runtimestore.Runtime{variant}, wdOpts{experiments: exps(t)})
		if rr := f.post(t, "/v1/sessions", "sess_exp_c", wdRuntime, wdClientPlan); rr.Code != http.StatusCreated {
			t.Fatalf("create: status %d, body=%s", rr.Code, rr.Body.String())
		}
		assertVariant(t, f, "sess_exp_c")
	})
	t.Run("experiment/create-and-start", func(t *testing.T) {
		f := newWDFixture(t, []runtimestore.Runtime{variant}, wdOpts{experiments: exps(t)})
		if rr := f.post(t, "/v1/sessions/start", "sess_exp_s", wdRuntime, ""); rr.Code/100 != 2 {
			t.Fatalf("create-and-start: status %d, body=%s", rr.Code, rr.Body.String())
		}
		assertVariant(t, f, "sess_exp_s")
	})
	t.Run("preroute-modify/create-and-start", func(t *testing.T) {
		modified, _ := json.Marshal(routeTaskSpec{TenantID: wdTenant, UserID: testOwner, RequestedRuntime: "wd-variant"})
		chain := interceptor.NewChain()
		if err := chain.Register(interceptor.PhasePreRoute, routeModifyInterceptor{priority: 350, out: modified}); err != nil {
			t.Fatalf("register: %v", err)
		}
		f := newWDFixture(t, []runtimestore.Runtime{variant}, wdOpts{chain: chain})
		if rr := f.post(t, "/v1/sessions/start", "sess_mod", wdRuntime, ""); rr.Code/100 != 2 {
			t.Fatalf("create-and-start: status %d, body=%s", rr.Code, rr.Body.String())
		}
		assertVariant(t, f, "sess_mod")
	})
}

// TestDefaultsOnlyPlanTriggersSDKWarmDemotion_spec_6_1 pins that a default
// file at an sdkWarmBlockingPaths path demotes an SDK-warm pod even when the
// client sent no plan, because the bind request reads the stored plan.
//
// spec: 14.1 (WorkspacePlan Schema Versioning), 5.1 (Runtime), 6.1 (What a Pre-Warmed Pod Looks Like)
func TestDefaultsOnlyPlanTriggersSDKWarmDemotion_spec_6_1(t *testing.T) {
	warm := runtimestore.Runtime{
		Name: "wd-warm", Type: runtimestore.TypeAgent,
		Capabilities:         &runtimestore.RuntimeCapabilities{PreConnect: true},
		SDKWarmBlockingPaths: sdkwarm.DefaultBlockingPaths,
		WorkspaceDefaults:    &runtimestore.WorkspaceDefaults{Files: []runtimestore.WorkspaceFile{{Path: "CLAUDE.md", Content: "x"}}},
	}
	for _, route := range []string{"finalize", "/v1/sessions/start"} {
		t.Run(route, func(t *testing.T) {
			f := newWDFixture(t, []runtimestore.Runtime{warm}, wdOpts{})
			if rr := f.bind(t, route, "sess_warm", "wd-warm", ""); rr.Code/100 != 2 {
				t.Fatalf("status %d, body=%s", rr.Code, rr.Body.String())
			}
			row := f.row(t, "sess_warm")
			req := f.srv.exclusiveBindRequest(context.Background(), row, podsession.PoolMatch{}, storedPlan(t, row), nil, nil, nil, "")
			if !podsession.RequiresDemotion(req) {
				t.Errorf("RequiresDemotion = false for a defaults-only plan placing CLAUDE.md (plan %s)", row.WorkspacePlan)
			}
		})
	}
}
