//go:build chaos

// SPDX-License-Identifier: MIT

// Tier-8 chaos coverage for composing a runtime's workspaceDefaults into a
// binding plan under a real Postgres outage. The session servers and the
// lenny/delegate_task tool read the runtime registry from a real Postgres
// through a fault proxy. With the proxy cut, a request that binds a plan
// (create, create-and-start, finalize, or a delegated child's creation)
// cannot read the runtime's defaults, so it fails closed with the retryable
// SERVICE_UNAVAILABLE and writes nothing, rather than binding the plan
// without the defaults. Once Postgres returns, the same request binds the
// plan with the defaults composed ahead of the client's sources.
package tier8_chaos_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	ctrlinterceptor "sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation/export"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	runtimepg "github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/ownerprincipal"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
	"github.com/lennylabs/lenny/tests/testinfra/tcpfault"
)

const (
	wsdOutageTenant  = "acme"
	wsdOutageRuntime = "echo-defaults"
	wsdOutageNS      = "lenny-agents"
	wsdOutageParent  = "sess-wsd-parent"
)

// wsdOutageEnv holds the binding surfaces whose runtime registry is a real
// Postgres behind a fault proxy, over one in-memory session store, so the
// runtime read is the only store dependency the outage reaches:
//
//   - handler is a session server with no pod binder, so a create-and-start
//     that composes its plan persists the row the test reads back.
//   - claimHandler is a session server whose pod binder writes to a fake
//     cluster holding one idle warm sandbox for the runtime, so a
//     create-and-start that reaches the §7.1 claim leaves a recorded write.
//   - mcp serves lenny/delegate_task over a delegation service whose child
//     export is a recording fake, under a running parent session.
type wsdOutageEnv struct {
	proxy        *tcpfault.Proxy
	sessions     *memstore.Store
	handler      http.Handler
	claimHandler http.Handler
	cluster      client.Client
	writes       *wsdClusterWrites
	mcp          http.Handler
	exports      *wsdExportRecorder
	ids          []string
	childIDs     []string
}

func newWsdOutageEnv(t *testing.T) *wsdOutageEnv {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	ctx := context.Background()
	if err := runtimepg.New(pg.Pool).Create(ctx, runtimestore.Runtime{
		Name:             wsdOutageRuntime,
		Type:             runtimestore.TypeAgent,
		Image:            "ghcr.io/acme/echo@sha256:" + strings.Repeat("a", 64),
		ExecutionMode:    runtimestore.ExecutionModeSession,
		IsolationProfile: isolation.ProfileSandboxed,
		IntegrationLevel: runtimestore.IntegrationLevelBasic,
		WorkspaceDefaults: &runtimestore.WorkspaceDefaults{
			Files: []runtimestore.WorkspaceFile{{Path: "defaults.txt", Content: "default content"}},
		},
	}); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	proxy, dsn := tcpfault.StartURL(t, pg.DSN)
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse proxied dsn: %v", err)
	}
	cfg.ConnConfig.ConnectTimeout = 2 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("proxied pool: %v", err)
	}
	t.Cleanup(pool.Close)

	runtimes := runtimepg.New(pool)
	e := &wsdOutageEnv{proxy: proxy, sessions: memstore.New(), writes: &wsdClusterWrites{}, exports: &wsdExportRecorder{}}
	e.handler = e.sessionServer(t, runtimes, nil)
	e.cluster = wsdOutageCluster(t, e.writes)
	e.claimHandler = e.sessionServer(t, runtimes, &podsession.Binder{Client: e.cluster, Namespace: wsdOutageNS})
	e.mcp = e.delegateServer(t, runtimes)
	return e
}

// sessionServer builds a session server over the env's session store and
// the proxied runtime registry, claiming pods through binder when it is set.
func (e *wsdOutageEnv) sessionServer(t *testing.T, runtimes runtimestore.Store, binder *podsession.Binder) http.Handler {
	t.Helper()
	opts := sessionserver.Options{
		DevTenantHeader:         true,
		IDFunc:                  e.nextID,
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		Runtimes:                runtimes,
	}
	if binder != nil {
		opts.PodBinder = binder
		opts.AgentNamespace = wsdOutageNS
	}
	srv := sessionserver.New(e.sessions, opts)
	t.Cleanup(srv.Close)
	return ownerprincipal.Wrap(srv.Handler(), ownerprincipal.Alice)
}

// delegateServer registers lenny/delegate_task over a delegation service
// that reads the proxied runtime registry, and seeds the running parent the
// delegations hang from. The parent runs a different runtime and pool than
// the child so the §8.2 cycle check admits the delegation.
func (e *wsdOutageEnv) delegateServer(t *testing.T, runtimes runtimestore.Store) http.Handler {
	t.Helper()
	at := time.Date(2026, 6, 8, 9, 0, 0, 0, time.UTC)
	if err := e.sessions.Create(context.Background(), sessionstore.Session{
		ID: wsdOutageParent, TenantID: wsdOutageTenant, UserID: "alice@acme.com", State: session.StateRunning,
		RuntimeRef: "parent-agent", PoolRef: "pool-parent", IsolationProfile: isolation.ProfileSandboxed,
		CreatedAt: at, UpdatedAt: at,
	}); err != nil {
		t.Fatalf("seed parent session: %v", err)
	}
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		SessionService:    ownerprincipal.SessionAccess{},
		DevTenantFallback: true,
		Store:             e.sessions,
		Executor:          executor.NewEchoExecutor(),
		Runtimes:          runtimes,
		Delegation: delegation.NewService(e.sessions, delegation.Options{
			IDFunc:             e.nextChildID,
			Runtimes:           runtimes,
			ExportMaterializer: e.exports,
			SessionObjects:     e.exports,
		}),
		IDFunc:   func() string { return "sess-wsd-mcp" },
		TenantID: wsdOutageTenant,
	})
	return ownerprincipal.Wrap(srv.Handler(), ownerprincipal.Alice)
}

// wsdOutageCluster is a fake cluster holding a warm pool, a template, and
// one idle sandbox with its pod for the runtime, recording every create,
// update, and patch in writes.
func wsdOutageCluster(t *testing.T, writes *wsdClusterWrites) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := lennyv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add lenny scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	pool, tmpl, sbx := wsdOutageRuntime+"-pool", wsdOutageRuntime+"-tmpl", wsdOutageRuntime+"-sbx"
	objs := []client.Object{
		&lennyv1.SandboxWarmPool{
			ObjectMeta: metav1.ObjectMeta{Name: pool, Namespace: wsdOutageNS},
			Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: tmpl, MinWarm: 1, MaxWarm: 5},
		},
		&lennyv1.SandboxTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: tmpl, Namespace: wsdOutageNS},
			Spec:       lennyv1.SandboxTemplateSpec{RuntimeRef: wsdOutageRuntime, IsolationProfile: string(isolation.ProfileSandboxed)},
		},
		&lennyv1.Sandbox{
			ObjectMeta: metav1.ObjectMeta{Name: sbx, Namespace: wsdOutageNS, Labels: map[string]string{warmpool.LabelPool: pool}},
			Status:     lennyv1.SandboxStatus{Phase: "idle", PodIP: "10.244.0.9"},
		},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: sbx, Namespace: wsdOutageNS}},
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).
		WithInterceptorFuncs(ctrlinterceptor.Funcs{
			Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				writes.add("create")
				return cl.Create(ctx, obj, opts...)
			},
			Update: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				writes.add("update")
				return cl.Update(ctx, obj, opts...)
			},
			Patch: func(ctx context.Context, cl client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				writes.add("patch")
				return cl.Patch(ctx, obj, patch, opts...)
			},
		}).Build()
}

// wsdClusterWrites counts the writes the fake cluster took, by verb.
type wsdClusterWrites struct {
	mu     sync.Mutex
	counts map[string]int
}

func (w *wsdClusterWrites) add(verb string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.counts == nil {
		w.counts = map[string]int{}
	}
	w.counts[verb]++
}

func (w *wsdClusterWrites) total() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := 0
	for _, c := range w.counts {
		n += c
	}
	return n
}

// claimCount returns the number of SandboxClaims in the fake cluster.
func (e *wsdOutageEnv) claimCount(t *testing.T) int {
	t.Helper()
	var claims lennyv1.SandboxClaimList
	if err := e.cluster.List(context.Background(), &claims, client.InNamespace(wsdOutageNS)); err != nil {
		t.Fatalf("list sandbox claims: %v", err)
	}
	return len(claims.Items)
}

// wsdExportRecorder stands in for the §8.7 file export of a delegated
// child: Materialize returns one uploadFile source in the child's export
// prefix, and DeleteBySession records the child whose export objects the
// delegation undid.
type wsdExportRecorder struct {
	mu      sync.Mutex
	deleted []string
}

func (r *wsdExportRecorder) Materialize(_ context.Context, p export.Params) (export.Result, error) {
	ref := "lenny-blob://" + p.TenantID + "/export/" + p.ChildSessionID + "/docs/a.txt?ttl=3600"
	return export.Result{
		Sources: []workspaceplan.Source{{
			Type:    workspaceplan.TypeUploadFile,
			Variant: workspaceplan.UploadFile{PathField: "docs/a.txt", UploadRef: ref},
			Raw:     map[string]any{"type": workspaceplan.TypeUploadFile, "path": "docs/a.txt", "uploadRef": ref},
		}},
		FileCount: 1,
	}, nil
}

func (r *wsdExportRecorder) DeleteBySession(_ context.Context, _, sessionID string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deleted = append(r.deleted, sessionID)
	return 1, nil
}

func (r *wsdExportRecorder) undone(sessionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range r.deleted {
		if id == sessionID {
			return true
		}
	}
	return false
}

// nextID hands out the session IDs the test queued with expectID.
func (e *wsdOutageEnv) nextID() string {
	if len(e.ids) == 0 {
		return "sess-wsd-unexpected"
	}
	id := e.ids[0]
	e.ids = e.ids[1:]
	return id
}

func (e *wsdOutageEnv) expectID(id string) { e.ids = append(e.ids, id) }

// nextChildID hands out the delegated child IDs the test queued with
// expectChildID.
func (e *wsdOutageEnv) nextChildID() string {
	if len(e.childIDs) == 0 {
		return "sess-wsd-child-unexpected"
	}
	id := e.childIDs[0]
	e.childIDs = e.childIDs[1:]
	return id
}

func (e *wsdOutageEnv) expectChildID(id string) { e.childIDs = append(e.childIDs, id) }

func (e *wsdOutageEnv) post(t *testing.T, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return e.postTo(t, e.handler, path, body)
}

func (e *wsdOutageEnv) postTo(t *testing.T, h http.Handler, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode %s body: %v", path, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req := httptest.NewRequest(http.MethodPost, path, &buf).WithContext(ctx)
	req.Header.Set("X-Lenny-Tenant-ID", wsdOutageTenant)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// clientPlan is the plan each binding request submits: one inline file the
// runtime's defaults do not declare.
func clientPlan(t *testing.T) json.RawMessage {
	t.Helper()
	plan, err := json.Marshal(map[string]any{
		"schemaVersion": 1,
		"sources":       []map[string]any{{"type": "inlineFile", "path": "client.txt", "content": "client content"}},
	})
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	return plan
}

func (e *wsdOutageEnv) createWithPlan(t *testing.T, id string) *httptest.ResponseRecorder {
	t.Helper()
	e.expectID(id)
	return e.post(t, "/v1/sessions", sessionserver.CreateSessionRequest{
		RuntimeRef: wsdOutageRuntime, WorkspacePlan: clientPlan(t),
	})
}

// createAndStartWithPlan sends POST /v1/sessions/start with the client plan
// to h under session ID id.
func (e *wsdOutageEnv) createAndStartWithPlan(t *testing.T, h http.Handler, id string) *httptest.ResponseRecorder {
	t.Helper()
	e.expectID(id)
	return e.postTo(t, h, "/v1/sessions/start", sessionserver.CreateAndStartRequest{
		RuntimeRef: wsdOutageRuntime, WorkspacePlan: clientPlan(t),
	})
}

// delegate calls lenny/delegate_task from the running parent to the
// defaults-declaring runtime with one file export, under child ID childID,
// and returns the tool result.
func (e *wsdOutageEnv) delegate(t *testing.T, childID string) map[string]any {
	t.Helper()
	e.expectChildID(childID)
	args := `{"parentSessionId":"` + wsdOutageParent + `","target":"` + wsdOutageRuntime +
		`","poolRef":"pool-child","task":{"workspaceFiles":{"export":[{"source":"docs/*.txt"}]}}}`
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"lenny/delegate_task","arguments":` + args + `}}`
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body)).WithContext(ctx)
	rr := httptest.NewRecorder()
	e.mcp.ServeHTTP(rr, req)
	var resp struct {
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode delegate_task response: %v; body=%s", err, rr.Body.String())
	}
	if resp.Result == nil {
		t.Fatalf("delegate_task returned no result: %s", rr.Body.String())
	}
	return resp.Result
}

func (e *wsdOutageEnv) row(t *testing.T, id string) (sessionstore.Session, error) {
	t.Helper()
	return e.sessions.Get(context.Background(), wsdOutageTenant, id)
}

// assertFailsClosed checks the 503 SERVICE_UNAVAILABLE response the
// composition writes for a transient runtime read failure.
func assertFailsClosed(t *testing.T, step string, rr *httptest.ResponseRecorder) {
	t.Helper()
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("%s during the outage: status %d, want 503; body=%s", step, rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "SERVICE_UNAVAILABLE") {
		t.Fatalf("%s during the outage: body %s, want code SERVICE_UNAVAILABLE", step, rr.Body.String())
	}
	if secs, err := strconv.Atoi(rr.Header().Get("Retry-After")); err != nil || secs <= 0 {
		t.Fatalf("%s during the outage: Retry-After %q, want a positive number of seconds", step, rr.Header().Get("Retry-After"))
	}
}

// storedSourcePaths returns the paths of the sources in a stored plan, in
// order.
func storedSourcePaths(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	var plan struct {
		Sources []struct {
			Type string `json:"type"`
			Path string `json:"path"`
		} `json:"sources"`
	}
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatalf("decode stored plan %s: %v", raw, err)
	}
	paths := make([]string, 0, len(plan.Sources))
	for _, s := range plan.Sources {
		paths = append(paths, s.Type+":"+s.Path)
	}
	return paths
}

// assertDefaultsFirst checks that a stored plan carries the runtime's
// default file ahead of the client's source.
func assertDefaultsFirst(t *testing.T, step string, raw json.RawMessage, clientSource string) {
	t.Helper()
	got := storedSourcePaths(t, raw)
	want := []string{"inlineFile:defaults.txt", clientSource}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("%s: stored plan sources = %v, want %v (defaults ahead of the client's source)", step, got, want)
	}
}

// assertNoClaim checks that a rejected create-and-start reached no §7.1
// pod claim: the fake cluster holds no SandboxClaim and took no write.
func (e *wsdOutageEnv) assertNoClaim(t *testing.T, step string) {
	t.Helper()
	if n := e.claimCount(t); n != 0 {
		t.Fatalf("%s during the outage left %d SandboxClaims, want 0", step, n)
	}
	if n := e.writes.total(); n != 0 {
		t.Fatalf("%s during the outage wrote to the cluster: %v", step, e.writes.counts)
	}
}

// spec: 14.1 (WorkspacePlan Schema Versioning, Runtime workspace defaults),
// 12.8, 15.1 (REST API), 7.5 (Setup Commands)
//
// diagnosis: a Postgres outage while a plan was binding did not fail closed,
// or the plan did not bind with the defaults once Postgres returned. One of:
// a create carrying a plan, a finalize of a session created without one, or
// a POST /v1/sessions/start carrying a plan bound the plan without the
// runtime's workspaceDefaults (201 or a finalizing row), returned a status
// other than 503 SERVICE_UNAVAILABLE with Retry-After, wrote session state
// before rejecting, or (create-and-start) took a SandboxClaim or wrote to the
// cluster; a lenny/delegate_task to the defaults-declaring runtime did not
// return the retryable SERVICE_UNAVAILABLE tool error, created a child row,
// or left the child's file export in place; or, after recovery, the create,
// create-and-start, or delegated child's stored plan did not carry the
// defaults ahead of the client's or the export's source, or a
// create-and-start never reached the claim (making the no-claim assertion
// vacuous).
func TestWorkspaceDefaultsPostgresOutageFailsClosed_spec_14_1(t *testing.T) {
	e := newWsdOutageEnv(t)

	// A session created without a plan binds its plan at finalize.
	e.expectID("sess-wsd-finalize")
	if rr := e.post(t, "/v1/sessions", sessionserver.CreateSessionRequest{
		RuntimeRef: wsdOutageRuntime,
	}); rr.Code != http.StatusCreated {
		t.Fatalf("create without a plan: status %d, body=%s", rr.Code, rr.Body.String())
	}

	e.proxy.Cut()

	assertFailsClosed(t, "create with a plan", e.createWithPlan(t, "sess-wsd-cut"))
	if _, err := e.row(t, "sess-wsd-cut"); !errors.Is(err, sessionstore.ErrNotFound) {
		t.Fatalf("session row after the rejected create: err = %v, want ErrNotFound", err)
	}

	assertFailsClosed(t, "finalize", e.post(t, "/v1/sessions/sess-wsd-finalize/finalize", nil))
	row, err := e.row(t, "sess-wsd-finalize")
	if err != nil {
		t.Fatalf("session row after the rejected finalize: %v", err)
	}
	if row.State != session.StateCreated {
		t.Fatalf("session state after the rejected finalize = %q, want %q", row.State, session.StateCreated)
	}
	if len(row.WorkspacePlan) != 0 {
		t.Fatalf("session plan after the rejected finalize = %s, want none bound", row.WorkspacePlan)
	}

	assertFailsClosed(t, "create-and-start with a plan", e.createAndStartWithPlan(t, e.claimHandler, "sess-wsd-start-cut"))
	if _, err := e.row(t, "sess-wsd-start-cut"); !errors.Is(err, sessionstore.ErrNotFound) {
		t.Fatalf("session row after the rejected create-and-start: err = %v, want ErrNotFound", err)
	}
	e.assertNoClaim(t, "create-and-start with a plan")

	env := toolErrorEnvelope(t, e.delegate(t, "sess-wsd-child-cut"))
	if env["code"] != "SERVICE_UNAVAILABLE" || env["retryable"] != true {
		t.Fatalf("delegate_task during the outage: envelope %v, want retryable SERVICE_UNAVAILABLE", env)
	}
	if _, err := e.row(t, "sess-wsd-child-cut"); !errors.Is(err, sessionstore.ErrNotFound) {
		t.Fatalf("child row after the rejected delegation: err = %v, want ErrNotFound", err)
	}
	if !e.exports.undone("sess-wsd-child-cut") {
		t.Fatal("the rejected delegation left the child's file export in place")
	}

	e.proxy.Restore(t)

	if rr := e.createWithPlan(t, "sess-wsd-ok"); rr.Code != http.StatusCreated {
		t.Fatalf("create with a plan after Postgres returned: status %d, body=%s", rr.Code, rr.Body.String())
	}
	row, err = e.row(t, "sess-wsd-ok")
	if err != nil {
		t.Fatalf("session row after recovery: %v", err)
	}
	assertDefaultsFirst(t, "create after recovery", row.WorkspacePlan, "inlineFile:client.txt")

	if rr := e.createAndStartWithPlan(t, e.handler, "sess-wsd-start-ok"); rr.Code/100 != 2 {
		t.Fatalf("create-and-start with a plan after Postgres returned: status %d, body=%s", rr.Code, rr.Body.String())
	}
	row, err = e.row(t, "sess-wsd-start-ok")
	if err != nil {
		t.Fatalf("create-and-start row after recovery: %v", err)
	}
	assertDefaultsFirst(t, "create-and-start after recovery", row.WorkspacePlan, "inlineFile:client.txt")

	// The no-claim assertion above discriminates only if a composing
	// create-and-start on the claiming server reaches the claim. The fake
	// pod runs no adapter, so the start itself fails after the claim.
	rr := e.createAndStartWithPlan(t, e.claimHandler, "sess-wsd-start-claim")
	if e.writes.total() == 0 {
		t.Fatalf("a composing create-and-start after recovery wrote nothing to the cluster (status %d, body=%s); the no-claim assertion is vacuous", rr.Code, rr.Body.String())
	}

	result := e.delegate(t, "sess-wsd-child-ok")
	if result["isError"] == true {
		t.Fatalf("delegate_task after Postgres returned failed: %v", result)
	}
	row, err = e.row(t, "sess-wsd-child-ok")
	if err != nil {
		t.Fatalf("child row after recovery: %v", err)
	}
	assertDefaultsFirst(t, "delegated child after recovery", row.WorkspacePlan, "uploadFile:docs/a.txt")
}
