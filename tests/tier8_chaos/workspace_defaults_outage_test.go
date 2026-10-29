//go:build chaos

// SPDX-License-Identifier: MIT

// Tier-8 chaos coverage for composing a runtime's workspaceDefaults into a
// binding plan under a real Postgres outage. The session server reads the
// runtime registry from a real Postgres through a fault proxy. With the proxy
// cut, a request that binds a plan cannot read the runtime's defaults, so it
// fails closed with the retryable 503 SERVICE_UNAVAILABLE and writes nothing,
// rather than binding the plan without the defaults. Once Postgres returns,
// the same request binds the plan with the defaults composed ahead of the
// client's sources.
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
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	runtimepg "github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
	"github.com/lennylabs/lenny/tests/testinfra/tcpfault"
)

const (
	wsdOutageTenant  = "acme"
	wsdOutageRuntime = "echo-defaults"
)

// wsdOutageEnv is a session server whose runtime registry is a real Postgres
// behind a fault proxy, with an in-memory session store so the runtime read
// is the only store dependency the outage reaches.
type wsdOutageEnv struct {
	proxy    *tcpfault.Proxy
	sessions *memstore.Store
	handler  http.Handler
	ids      []string
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

	e := &wsdOutageEnv{proxy: proxy, sessions: memstore.New()}
	srv := sessionserver.New(e.sessions, sessionserver.Options{
		DevTenantHeader:         true,
		IDFunc:                  e.nextID,
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		Runtimes:                runtimepg.New(pool),
	})
	e.handler = srv.Handler()
	return e
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

func (e *wsdOutageEnv) post(t *testing.T, path string, body any) *httptest.ResponseRecorder {
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
	e.handler.ServeHTTP(rr, req)
	return rr
}

func (e *wsdOutageEnv) createWithPlan(t *testing.T, id string) *httptest.ResponseRecorder {
	t.Helper()
	e.expectID(id)
	plan, err := json.Marshal(map[string]any{
		"schemaVersion": 1,
		"sources":       []map[string]any{{"type": "inlineFile", "path": "client.txt", "content": "client content"}},
	})
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	return e.post(t, "/v1/sessions", sessionserver.CreateSessionRequest{
		RuntimeRef: wsdOutageRuntime, UserID: "alice@acme.com", WorkspacePlan: plan,
	})
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

// spec: 14.1 (WorkspacePlan Schema Versioning, Runtime workspace defaults),
// 12.8, 15.1
//
// diagnosis: a Postgres outage while a plan was binding did not fail closed.
// A create carrying a plan, or a finalize of a session created without one,
// either bound the plan without the runtime's workspaceDefaults (201 or a
// finalizing row), returned a status other than 503 SERVICE_UNAVAILABLE with
// Retry-After, or wrote session state before rejecting; or the create did not
// bind the plan with the defaults ahead of the client's source once Postgres
// returned.
func TestWorkspaceDefaultsPostgresOutageFailsClosed_spec_14_1(t *testing.T) {
	e := newWsdOutageEnv(t)

	// A session created without a plan binds its plan at finalize.
	e.expectID("sess-wsd-finalize")
	if rr := e.post(t, "/v1/sessions", sessionserver.CreateSessionRequest{
		RuntimeRef: wsdOutageRuntime, UserID: "alice@acme.com",
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

	e.proxy.Restore(t)

	if rr := e.createWithPlan(t, "sess-wsd-ok"); rr.Code != http.StatusCreated {
		t.Fatalf("create with a plan after Postgres returned: status %d, body=%s", rr.Code, rr.Body.String())
	}
	row, err = e.row(t, "sess-wsd-ok")
	if err != nil {
		t.Fatalf("session row after recovery: %v", err)
	}
	got := storedSourcePaths(t, row.WorkspacePlan)
	want := []string{"inlineFile:defaults.txt", "inlineFile:client.txt"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("stored plan sources after recovery = %v, want %v (defaults ahead of the client's source)", got, want)
	}
}
