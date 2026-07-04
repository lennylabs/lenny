// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test for the §8.10 "Duplicate spawn across parent
// recovery" residual and its client-facing §11.5 mitigation boundary.
//
// The proposal defines an at-least-once guarantee for external side
// effects across an unplanned pod restore (§7.3): a restored parent
// re-derives its next actions from a possibly-stale checkpoint and can
// re-issue a lenny/delegate_task spawn, producing a duplicate child
// subtree. The §11.5 idempotency mechanism does not suppress that
// duplicate on the recovery path, because it is wired only on the
// client-facing edge /mcp surface; the intra-pod platform-tool dispatch
// (mcp.Server.DispatchTool) a restored parent re-issues delegate_task
// over omits the §11.5 hook (pkg/gateway/mcpfabric/mcp/mcp.go
// DispatchTool). This test pins both halves of that boundary:
//
//   - Intra-pod residual: two delegate_task calls through DispatchTool
//     (the intra-pod entry point), one with and one without a §11.5
//     idempotencyKey argument, each create a distinct child subtree for
//     the same parent, because DispatchTool never runs the §11.5 hook.
//     This is the unmitigated at-least-once residual §8.10 documents.
//
//   - Client-facing mitigation: the same delegate_task through the
//     client-facing edge /mcp HTTP surface with an Idempotency-Key header
//     collapses a same-body retry (replays the cached result) and returns
//     422 IDEMPOTENCY_KEY_REUSED on a changed body. This confirms the
//     mitigation is client-facing and does not reach the recovery path.
//
// The intra-pod case runs in-process against the real
// mcp.Server + delegation.Service wiring (mirroring
// pkg/gateway/mcpfabric/mcptools/delegate_integration_test.go), the same
// handler the §9.1 intra-pod platform MCP server reaches over
// GatewayControl.CallPlatformTool. The mitigation case runs against the
// real cmd/lenny-gateway binary on the compose stack with Postgres and
// the §11.5 idempotency_keys table so the durable key cache is exercised
// end-to-end (postWithKey/postRaw pattern from idempotency_test.go, the
// gateway.StartWith subprocess harness).

package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/delegation/cycle"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// spec: 8.10 (duplicate spawn across parent recovery), 11.5 (idempotency)
// diagnosis: the §8.10 duplicate-spawn residual boundary regressed. If
// the intra-pod sub-test fails, DispatchTool started deduplicating
// delegate_task (silently gaining a §11.5 hook the recovery path must
// not have, so the documented at-least-once residual is now
// mis-described) or the delegation service grew a (parent_session, call)
// dedup key the proposal states does not exist. If the client-facing
// sub-test fails, the §11.5 mitigation on the /mcp edge regressed: a
// same-body retry no longer replays, or a changed body no longer returns
// 422 IDEMPOTENCY_KEY_REUSED, so the mitigation boundary the residual
// contrasts against is broken.
func TestDelegateTaskRecoveryResidual(t *testing.T) {
	gateway.SkipUnlessAvailable(t)

	t.Run("intra_pod_dispatch_creates_duplicate_subtree_ignoring_idempotency_key", func(t *testing.T) {
		testIntraPodResidual(t)
	})

	t.Run("client_facing_edge_mcp_idempotency_key_collapses_retry", func(t *testing.T) {
		testClientFacingMitigation(t)
	})
}

// testIntraPodResidual drives two delegate_task re-issues through the
// intra-pod platform-tool dispatch path (mcp.Server.DispatchTool wired
// to the real delegation.Service), simulating a restored parent
// re-deriving the same spawn from a stale checkpoint. It asserts that a
// second child subtree is created for the same parent whether or not a
// §11.5 idempotencyKey argument is supplied, because the intra-pod
// dispatch omits the §11.5 hook. This pins the unmitigated
// at-least-once residual §8.10 documents.
//
// spec: 8.10 (duplicate spawn across parent recovery), 11.5 (idempotency)
func testIntraPodResidual(t *testing.T) {
	const tenant = "acme"
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := func() time.Time { return now }
	// A real routable §12.6 root id so the delegation service mints
	// valid, distinct child ids off its routing prefix (session.NewChildID).
	parentID := session.NewID()

	store := memstore.New()
	runtimes := runtimestore.NewMemory()
	// The parent runs `claude`; the delegation target `worker` is a
	// distinct runtime so re-issuing the same spawn twice creates two
	// sibling children under the parent without tripping the §8.2 cycle
	// detector (which keys on the ancestor chain, not siblings).
	for _, rt := range []runtimestore.Runtime{
		{Name: "claude", Image: "lenny/claude@sha256:abc"},
		{Name: "worker", Image: "lenny/worker@sha256:def"},
	} {
		if err := runtimes.Create(context.Background(), rt); err != nil {
			t.Fatalf("seed runtime %s: %v", rt.Name, err)
		}
	}

	// IDFunc left nil so the delegation service mints a fresh child id per
	// call (session.NewChildID): the second dispatch is a genuinely new
	// child, exactly as a restored parent's re-derived spawn is.
	svc := delegation.NewService(store, delegation.Options{
		Clock:     clk,
		Runtimes:  runtimes,
		CycleMode: cycle.ModeEnforce,
	})

	srv := mcp.NewServer()
	// Deliberately do NOT call srv.SetIdempotency: the intra-pod
	// DispatchTool entry point never runs the §11.5 hook even when it is
	// configured (pkg/gateway/mcpfabric/mcp/mcp.go DispatchTool), and the
	// production intra-pod platform MCP server reaches DispatchTool over
	// GatewayControl.CallPlatformTool. Leaving it unset mirrors that
	// intra-pod surface directly.
	mcptools.Register(srv, mcptools.Deps{
		Store:      store,
		Delegation: svc,
		Runtimes:   runtimes,
		Clock:      clk,
		TenantID:   tenant,
	})

	if err := store.Create(context.Background(), sessionstore.Session{
		ID: parentID, TenantID: tenant, UserID: "user_alice",
		RuntimeRef: "claude", PoolRef: "pool-a",
		State: session.StateRunning, IsolationProfile: isolation.ProfileSandboxed,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed parent: %v", err)
	}

	spawnArgs := func(withKey bool) string {
		key := ""
		if withKey {
			key = `"idempotencyKey":"recovery-key-1",`
		}
		return `{"parentSessionId":"` + parentID + `","target":"worker","poolRef":"pool-b",` +
			key + `"task":{"input":[{"type":"text","inline":"do work"}]}}`
	}

	// First spawn: the parent's original delegate_task, carrying a §11.5
	// idempotencyKey argument. On the client-facing /mcp edge that key
	// would be honoured; the intra-pod DispatchTool ignores it.
	firstChild := dispatchDelegate(t, srv, spawnArgs(true))

	// Second spawn: the restored parent re-derives and re-issues the same
	// delegate_task with the same idempotencyKey. Because DispatchTool
	// omits the §11.5 hook, the key does not deduplicate; a distinct
	// child subtree is created.
	secondChild := dispatchDelegate(t, srv, spawnArgs(true))

	if firstChild == secondChild {
		t.Fatalf("intra-pod re-issue with the same §11.5 idempotencyKey collapsed to one child (%q); "+
			"the recovery path must be unmitigated at-least-once, so DispatchTool must not deduplicate",
			firstChild)
	}

	// The parent's task tree now records two child subtrees for one
	// logical spawn: the duplicate-subtree residual §8.10 documents. Read
	// the tree through the same intra-pod platform-tool surface.
	children := intraPodTreeChildren(t, srv, parentID)
	if len(children) != 2 {
		t.Fatalf("parent tree children = %d, want 2 (the reattached original plus the re-derived duplicate); "+
			"a §11.5 argument key must not suppress the intra-pod re-issue", len(children))
	}
	got := map[string]bool{}
	for _, c := range children {
		got[c] = true
	}
	if !got[firstChild] || !got[secondChild] {
		t.Errorf("parent tree children = %v, want both %q and %q", children, firstChild, secondChild)
	}

	// Contrast within the intra-pod surface: a spawn with NO
	// idempotencyKey behaves identically. The residual does not depend on
	// whether a key is supplied; the hook never runs on this path.
	thirdChild := dispatchDelegate(t, srv, spawnArgs(false))
	if thirdChild == firstChild || thirdChild == secondChild {
		t.Fatalf("keyless intra-pod spawn reused an existing child id %q; each intra-pod spawn is a new subtree", thirdChild)
	}
	if n := len(intraPodTreeChildren(t, srv, parentID)); n != 3 {
		t.Fatalf("parent tree children after keyless re-issue = %d, want 3; the intra-pod dispatch never deduplicates", n)
	}
}

// dispatchDelegate invokes lenny/delegate_task through the intra-pod
// DispatchTool entry point and returns the created child session id. It
// fails the test if the dispatch reports a tool-level error.
func dispatchDelegate(t *testing.T, srv *mcp.Server, argsJSON string) string {
	t.Helper()
	res, ok, err := srv.DispatchTool(context.Background(), "lenny/delegate_task", json.RawMessage(argsJSON))
	if err != nil || !ok {
		t.Fatalf("DispatchTool(delegate_task) = (ok=%v, err=%v), want (true, nil)", ok, err)
	}
	if res.IsError {
		t.Fatalf("delegate_task returned a tool error: %s", dispatchToolText(res))
	}
	var handle struct {
		ChildSessionID string `json:"childSessionId"`
	}
	if err := json.Unmarshal([]byte(dispatchToolText(res)), &handle); err != nil {
		t.Fatalf("delegate_task result is not a TaskHandle envelope: %v (%q)", err, dispatchToolText(res))
	}
	if handle.ChildSessionID == "" {
		t.Fatalf("delegate_task returned no childSessionId: %q", dispatchToolText(res))
	}
	return handle.ChildSessionID
}

// intraPodTreeChildren reads the parent's §8.5 task tree through the
// intra-pod lenny/get_task_tree surface and returns the immediate child
// task ids.
func intraPodTreeChildren(t *testing.T, srv *mcp.Server, parentID string) []string {
	t.Helper()
	res, ok, err := srv.DispatchTool(context.Background(), "lenny/get_task_tree",
		json.RawMessage(`{"sessionId":"`+parentID+`"}`))
	if err != nil || !ok {
		t.Fatalf("DispatchTool(get_task_tree) = (ok=%v, err=%v), want (true, nil)", ok, err)
	}
	if res.IsError {
		t.Fatalf("get_task_tree returned a tool error: %s", dispatchToolText(res))
	}
	var tree struct {
		TaskID   string `json:"taskId"`
		Children []struct {
			TaskID string `json:"taskId"`
		} `json:"children"`
	}
	if err := json.Unmarshal([]byte(dispatchToolText(res)), &tree); err != nil {
		t.Fatalf("get_task_tree result decode: %v (%q)", err, dispatchToolText(res))
	}
	if tree.TaskID != parentID {
		t.Fatalf("tree root taskId = %q, want %q", tree.TaskID, parentID)
	}
	ids := make([]string, 0, len(tree.Children))
	for _, c := range tree.Children {
		ids = append(ids, c.TaskID)
	}
	return ids
}

// dispatchToolText returns the first text content block of a
// DispatchTool result.
func dispatchToolText(res mcp.ToolResult) string {
	if len(res.Content) == 0 {
		return ""
	}
	return res.Content[0].Text
}

// testClientFacingMitigation drives lenny/delegate_task through the
// client-facing edge /mcp HTTP surface of the real cmd/lenny-gateway
// binary, with a durable Postgres §11.5 key cache. It asserts that the
// §11.5 middleware collapses a same-body retry (replays the cached
// result) and returns 422 IDEMPOTENCY_KEY_REUSED on a changed body. This
// pins the mitigation to the client-facing edge and confirms the
// recovery (intra-pod) path is unmitigated by contrast.
//
// spec: 8.10 (duplicate spawn across parent recovery), 11.5 (idempotency)
func testClientFacingMitigation(t *testing.T) {
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	// The §11.5 idempotency_keys table must exist for the durable key
	// cache the mitigation exercises; fail loudly if the migration set
	// did not create it.
	if !postgresHasTable(t, pg, "idempotency_keys") {
		t.Fatalf("idempotency_keys table missing after migrations; the §11.5 durable cache cannot be exercised")
	}

	gw := gateway.StartWith(t, "--dev-mode", "--postgres-dsn="+pg.DSN)
	c := mcpClient{t: t, base: gw.BaseURL()}

	// Bootstrap the built-in `default` MCP tenant with an `echo` runtime
	// so the delegate_task target resolves server-side; the platform MCP
	// tools dispatch against the fixed `default` tenant.
	bootstrapDefaultRuntime(t, gw.BaseURL())

	parent := c.runningSession()

	// The delegate_task JSON-RPC body the client-facing edge sees. The
	// same key + same body must replay; the same key + a changed body
	// must be rejected.
	spawnBody := delegateRPCBody(parent, "echo-child")

	first := mcpPostWithKey(t, gw.BaseURL(), "spawn-key-1", spawnBody)
	firstChild := childIDFromRPC(t, first)
	if firstChild == "" {
		t.Fatalf("first delegate_task produced no child: %v", first)
	}

	// Same key, same body: the §11.5 middleware replays the cached
	// response, so the same child id comes back without a second spawn.
	second := mcpPostWithKey(t, gw.BaseURL(), "spawn-key-1", spawnBody)
	secondChild := childIDFromRPC(t, second)
	if secondChild != firstChild {
		t.Errorf("client-facing same-key retry returned a new child (%q vs %q); "+
			"the §11.5 edge hook must collapse the retry to the cached result", secondChild, firstChild)
	}

	// The parent tree confirms only one child exists: the mitigation
	// suppressed the duplicate spawn on the client-facing edge, the exact
	// suppression the intra-pod recovery path does not get.
	code, tree := c.rest(http.MethodGet, "/v1/sessions/"+parent+"/tree", nil)
	if code != http.StatusOK {
		t.Fatalf("GET tree: status %d (%v)", code, tree)
	}
	if nc, _ := tree["nodeCount"].(float64); int(nc) != 2 {
		t.Errorf("nodeCount = %d, want 2 (parent + one child); the §11.5 edge retry must not create a second subtree", int(nc))
	}

	// Same key, changed body: the §11.5 middleware rejects with
	// 422 IDEMPOTENCY_KEY_REUSED. A different target changes the request
	// body under the reused key.
	resp := mcpPostRaw(t, gw.BaseURL(), "spawn-key-1", delegateRPCBody(parent, "echo-other"))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("changed body under reused key: want 422 IDEMPOTENCY_KEY_REUSED, got %d (%s)", resp.StatusCode, raw)
	}
	raw, _ := io.ReadAll(resp.Body)
	var envelope map[string]any
	_ = json.Unmarshal(raw, &envelope)
	errObj, _ := envelope["error"].(map[string]any)
	if errObj["code"] != "IDEMPOTENCY_KEY_REUSED" {
		t.Errorf("error code = %v, want IDEMPOTENCY_KEY_REUSED (%s)", errObj["code"], raw)
	}
}

// delegateRPCBody builds the JSON-RPC tools/call body for a
// lenny/delegate_task spawn against parent with the given target.
func delegateRPCBody(parentID, target string) string {
	return `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"lenny/delegate_task",` +
		`"arguments":{"parentSessionId":"` + parentID + `","target":"` + target + `"}}}`
}

// childIDFromRPC extracts the childSessionId from a delegate_task
// JSON-RPC response body.
func childIDFromRPC(t *testing.T, rpc map[string]any) string {
	t.Helper()
	text, isErr := toolResultText(t, rpc)
	if isErr {
		t.Fatalf("delegate_task over /mcp returned an error: %s", text)
	}
	var out struct {
		ChildSessionID string `json:"childSessionId"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("delegate_task result is not JSON: %v (%q)", err, text)
	}
	return out.ChildSessionID
}

// mcpPostWithKey POSTs a JSON-RPC body to /mcp with the Idempotency-Key
// header and returns the decoded JSON-RPC response, requiring a 200.
func mcpPostWithKey(t *testing.T, base, key, rpcBody string) map[string]any {
	t.Helper()
	resp := mcpPostRaw(t, base, key, rpcBody)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /mcp with key %q: want 200, got %d (%s)", key, resp.StatusCode, raw)
	}
	var out map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

// mcpPostRaw POSTs a JSON-RPC body to /mcp under the fixed `default` MCP
// tenant with the Idempotency-Key header, returning the raw response.
func mcpPostRaw(t *testing.T, base, key, rpcBody string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, base+"/mcp", bytes.NewReader([]byte(rpcBody)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", mcpTenant)
	req.Header.Set("X-Lenny-User-ID", "alice@acme.com")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /mcp: %v", err)
	}
	return resp
}

// bootstrapDefaultRuntime registers an `echo` runtime under the built-in
// `default` MCP tenant so lenny/delegate_task can resolve its target
// server-side. The platform-admin role authorizes the bootstrap in
// dev-mode.
func bootstrapDefaultRuntime(t *testing.T, base string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"tenants": []map[string]any{{"id": "default", "displayName": "Default"}},
		"runtimes": []map[string]any{{
			"name":   "echo",
			"image":  "lenny/echo@sha256:abc",
			"labels": map[string]string{"tier": "test"},
		}},
		"users": []map[string]any{{
			"subject": "auth0|alice", "tenantId": "default",
			"email": "alice@acme.com", "roles": []string{"tenant-admin"},
		}},
	})
	req, _ := http.NewRequest(http.MethodPost, base+"/v1/admin/bootstrap", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", mcpTenant)
	req.Header.Set("X-Lenny-User-ID", "alice@acme.com")
	req.Header.Set("X-Lenny-Roles", "platform-admin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("bootstrap default runtime: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("bootstrap default runtime: status %d (%s)", resp.StatusCode, raw)
	}
}

// postgresHasTable reports whether the named table exists in the
// container's public schema after migrations.
func postgresHasTable(t *testing.T, pg *containers.Postgres, table string) bool {
	t.Helper()
	var exists bool
	if err := pg.Pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = $1)`,
		table).Scan(&exists); err != nil {
		t.Fatalf("check table %s: %v", table, err)
	}
	return exists
}
