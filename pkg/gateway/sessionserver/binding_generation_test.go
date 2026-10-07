// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// raiseGeneration moves a seeded row's coordination_generation to gen, so a
// stamp equal to the row's generation cannot pass by coincidence with the
// create-time value of 1.
func raiseGeneration(t *testing.T, store sessionstore.Store, id string, gen int64) {
	t.Helper()
	if _, err := store.Update(context.Background(), "acme", id, func(r *sessionstore.Session) error {
		r.CoordinationGeneration = gen
		return nil
	}); err != nil {
		t.Fatalf("raise generation of %s: %v", id, err)
	}
}

// assertBindingGeneration fails unless the registry's binding for id carries
// the row's committed coordination_generation, and that generation is want.
func assertBindingGeneration(t *testing.T, store sessionstore.Store, registry *podsession.Registry, id string, want int64) {
	t.Helper()
	row, err := store.Get(context.Background(), "acme", id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	bind, ok := registry.Get(id)
	if !ok {
		t.Fatalf("no binding published for %s", id)
	}
	if bind.CoordinationGeneration != row.CoordinationGeneration || bind.CoordinationGeneration != want {
		t.Fatalf("binding generation = %d, row generation = %d, want both %d",
			bind.CoordinationGeneration, row.CoordinationGeneration, want)
	}
}

// spec: 10.1.1 (Stateless Replicas and Per-Session Coordination), 10.1.5
// (Stale Replica Behavior)
// Every site that publishes a binding stamps it with the session row's
// committed coordination_generation: the single-call create (1, the value
// the store raises a zero generation to), POST /start, the delegated-child
// materialize, and both resume branches. A missing stamp leaves zero, which
// a stream-failure report treats as superseded, so a stream failure on that
// binding would be evicted instead of reported.
func TestPublishedBindingCarriesRowGeneration_spec_10_1_1(t *testing.T) {
	t.Run("single-call create", func(t *testing.T) {
		srv, registry, store := podBindServerWithStore(t, "sess-gen-create")
		body, _ := json.Marshal(sessionserver.CreateAndStartRequest{RuntimeRef: "echo", UserID: "alice@acme.com"})
		req := httptest.NewRequest(http.MethodPost, "/v1/sessions/start", bytes.NewReader(body))
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusCreated {
			t.Fatalf("create-and-start: status %d, body=%s", rr.Code, rr.Body.String())
		}
		assertBindingGeneration(t, store, registry, "sess-gen-create", 1)
	})

	t.Run("start", func(t *testing.T) {
		srv, registry, store := podBindServerWithStore(t, "sess-gen-start")
		h := srv.Handler()
		createBody, _ := json.Marshal(sessionserver.CreateSessionRequest{
			RuntimeRef: "echo", UserID: "alice@acme.com",
			WorkspacePlan: json.RawMessage(`{"schemaVersion": 1, "sources": []}`),
		})
		if rr := postSessionStep(t, h, "/v1/sessions", createBody); rr.Code != http.StatusCreated {
			t.Fatalf("create: status %d, body=%s", rr.Code, rr.Body.String())
		}
		if rr := postSessionStep(t, h, "/v1/sessions/sess-gen-start/finalize", nil); rr.Code != http.StatusOK {
			t.Fatalf("finalize: status %d, body=%s", rr.Code, rr.Body.String())
		}
		raiseGeneration(t, store, "sess-gen-start", 3)
		if rr := postSessionStep(t, h, "/v1/sessions/sess-gen-start/start", nil); rr.Code != http.StatusOK {
			t.Fatalf("start: status %d, body=%s", rr.Code, rr.Body.String())
		}
		assertBindingGeneration(t, store, registry, "sess-gen-start", 3)
	})

	t.Run("delegated child", func(t *testing.T) {
		store := memstore.New()
		seedDelegatedChild(t, store, "child-gen", `{"schemaVersion": 1, "sources": []}`)
		raiseGeneration(t, store, "child-gen", 4)
		cluster, dial, _ := materializeCluster(t)
		registry := podsession.NewRegistry()
		srv := sessionserver.New(store, sessionserver.Options{
			IDFunc:                  func() string { return "unused" },
			DefaultIsolationProfile: isolation.ProfileSandboxed,
			PodBinder:               podBindBinder(cluster, dial),
			PodRegistry:             registry,
			AgentNamespace:          podTestNS,
		})
		if _, err := srv.MaterializeDelegatedChild(context.Background(), "acme", "child-gen"); err != nil {
			t.Fatalf("MaterializeDelegatedChild: %v", err)
		}
		assertBindingGeneration(t, store, registry, "child-gen", 4)
	})

	for _, tc := range []struct {
		name string
		row  sessionstore.Session
	}{
		{"checkpoint resume", sessionstore.Session{WorkspaceSnapshot: checkpointed()}},
		{"snapshotless resume", sessionstore.Session{WorkspacePlan: json.RawMessage(`{
			"schemaVersion": 1,
			"sources": [{"type":"inlineFile","path":"CLAUDE.md","content":"# resumed","mode":"0644"}]
		}`)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := "sess-gen-resume"
			srv, store, registry, _ := podResumeServer(t, id)
			row := tc.row
			row.ID = id
			row.CoordinationGeneration = 5
			seedAwaitingSession(t, store, row)
			if rr := postSessionStep(t, srv.Handler(), "/v1/sessions/"+id+"/resume", nil); rr.Code != http.StatusOK {
				t.Fatalf("resume: status %d, body=%s", rr.Code, rr.Body.String())
			}
			assertBindingGeneration(t, store, registry, id, 5)
		})
	}
}

// podBindServerWithStore is podBindServer with the session store returned,
// so a test can read and raise the row's coordination_generation.
func podBindServerWithStore(t *testing.T, id string) (*sessionserver.Server, *podsession.Registry, *memstore.Store) {
	t.Helper()
	srv, store, registry, _ := podResumeServer(t, id)
	return srv, registry, store
}
