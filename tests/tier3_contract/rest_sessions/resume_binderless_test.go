// SPDX-License-Identifier: MIT

//go:build contract

package rest_sessions_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/errorclassify"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/tests/testinfra/ownerprincipal"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// spec: 15.1 (REST API), 7.3 (Retry and Resume)
// diagnosis: on a gateway that wires no pod binder, POST /resume from
// awaiting_client_action did not answer 200 with resume_pending, or POST
// /resume from suspended did not answer 200 with running. A 503 or a 409
// means the handler still waits on a restore it no longer runs, or still
// refuses suspended. A RESUME_FAILED hit means the removed error code is
// still in the error catalog or the served OpenAPI document.
func TestResumeAnswersTheNewStateWithNoPodBinder_spec_15_1(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{DevTenantHeader: true})
	ts := httptest.NewServer(ownerprincipal.Wrap(srv.Handler(), ownerprincipal.Alice))
	t.Cleanup(ts.Close)

	t.Run("awaiting_client_action answers resume_pending", func(t *testing.T) {
		const id = "00000000-0000-4000-8000-0000000000a1"
		now := time.Now().UTC()
		if err := store.Create(context.Background(), sessionstore.Session{
			ID: id, TenantID: "acme", UserID: "alice@acme.com", RuntimeRef: "claude-code",
			State: session.StateAwaitingClientAction, RetryCount: 2, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
		resp, body := transition(t, ts, id, "resume")
		if resp.StatusCode != http.StatusOK || body["state"] != string(session.StateResumePending) {
			t.Fatalf("POST /resume from awaiting_client_action = %d %v, want 200 with resume_pending", resp.StatusCode, body)
		}
	})

	t.Run("suspended answers running", func(t *testing.T) {
		id := createSession(t, ts)
		for _, step := range []string{"finalize", "start", "interrupt"} {
			if resp, body := transition(t, ts, id, step); resp.StatusCode != http.StatusOK {
				t.Fatalf("POST /%s = %d %v", step, resp.StatusCode, body)
			}
		}
		resp, body := transition(t, ts, id, "resume")
		if resp.StatusCode != http.StatusOK || body["state"] != string(session.StateRunning) {
			t.Fatalf("POST /resume from suspended = %d %v, want 200 with running", resp.StatusCode, body)
		}
	})

	t.Run("RESUME_FAILED is absent", func(t *testing.T) {
		if errorclassify.Known("RESUME_FAILED") {
			t.Error("the error catalog still names RESUME_FAILED")
		}
		doc, err := os.ReadFile(filepath.Join(schematest.RepoRoot(t), "pkg/gateway/externalapi/openapi/openapi.json"))
		if err != nil {
			t.Fatalf("read openapi.json: %v", err)
		}
		if strings.Contains(string(doc), "RESUME_FAILED") {
			t.Error("openapi.json still names RESUME_FAILED")
		}
	})
}
