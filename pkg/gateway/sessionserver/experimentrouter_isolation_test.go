// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/experimentstore"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// spec: §10.7 ExperimentRouter isolation monotonicity — a session is
// rejected closed when its assigned variant pool is less isolated than
// the session's own profile.

// routerServerWithPools builds a session server whose §10.7
// ExperimentRouter is wired with both an experiment registry and the
// §5.2 warm-pool registry, so the isolation-monotonicity check runs.
func routerServerWithPools(t *testing.T, sessionID string, exps experimentstore.Store, pools poolstore.Store) (http.Handler, *memstore.Store) {
	t.Helper()
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		Experiments: exps,
		Pools:       pools,
		IDFunc:      func() string { return sessionID },
	})
	return srv.Handler(), store
}

// seedPool registers a warm pool at the given §5.3 isolation profile.
func seedPool(t *testing.T, pools poolstore.Store, name string, profile isolation.Profile) {
	t.Helper()
	if err := pools.Create(context.Background(), poolstore.Pool{
		Name: name, RuntimeRef: "claude-code-v2", IsolationProfile: profile,
	}); err != nil {
		t.Fatalf("seed pool %q: %v", name, err)
	}
}

// postSessionIso posts a create request carrying an explicit §5.3
// isolation-profile override.
func postSessionIso(t *testing.T, h http.Handler, path, runtime, iso string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{
		"runtimeRef": runtime, "isolationProfile": iso, "userId": "alice",
	})
	req := asUser(httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body)), "alice", "default")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// recordingRejectionReporter captures the §10.7 rejection reports the
// ExperimentRouter emits when it fails a session closed.
type recordingRejectionReporter struct {
	events []sessionserver.ExperimentIsolationRejection
}

func (r *recordingRejectionReporter) ReportExperimentIsolationRejection(_ context.Context, ev sessionserver.ExperimentIsolationRejection) {
	r.events = append(r.events, ev)
}

// errorDetails parses a §15.1 error envelope and returns its code and
// details map.
func errorDetails(t *testing.T, rr *httptest.ResponseRecorder) (string, map[string]any) {
	t.Helper()
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v; body %s", err, rr.Body.String())
	}
	return env.Error.Code, env.Error.Details
}

func TestExperimentRouterFailsClosedOnWeakerVariantPool(t *testing.T) {
	exps := experimentstore.NewMemory()
	routedExperiment(t, exps, "claude-code", "claude-code-v2", "cc-v2-pool")
	pools := poolstore.NewMemory()
	// The variant pool runs `sandboxed`, weaker than the session's
	// `microvm` floor.
	seedPool(t, pools, "cc-v2-pool", isolation.ProfileSandboxed)
	h, store := routerServerWithPools(t, "sess_weak", exps, pools)

	rr := postSessionIso(t, h, "/v1/sessions", "claude-code", string(isolation.ProfileMicrovm))
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("create: status %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	code, details := errorDetails(t, rr)
	if code != "VARIANT_ISOLATION_UNAVAILABLE" {
		t.Errorf("error code = %q, want VARIANT_ISOLATION_UNAVAILABLE", code)
	}
	if details["experimentId"] != "exp_1" || details["variantId"] != "treatment" {
		t.Errorf("details experiment/variant = %v/%v, want exp_1/treatment",
			details["experimentId"], details["variantId"])
	}
	if details["sessionMinIsolation"] != "microvm" || details["variantPoolIsolation"] != "sandboxed" {
		t.Errorf("details isolation = session %v / pool %v, want microvm / sandboxed",
			details["sessionMinIsolation"], details["variantPoolIsolation"])
	}
	// §10.7 fail-closed: the session row must not be persisted.
	if _, err := store.Get(context.Background(), "default", "sess_weak"); err == nil {
		t.Error("session was created despite the fail-closed isolation rejection")
	}
}

func TestExperimentRouterAllowsEqualIsolationVariantPool(t *testing.T) {
	exps := experimentstore.NewMemory()
	routedExperiment(t, exps, "claude-code", "claude-code-v2", "cc-v2-pool")
	pools := poolstore.NewMemory()
	seedPool(t, pools, "cc-v2-pool", isolation.ProfileMicrovm)
	h, store := routerServerWithPools(t, "sess_equal", exps, pools)

	rr := postSessionIso(t, h, "/v1/sessions", "claude-code", string(isolation.ProfileMicrovm))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	got, err := store.Get(context.Background(), "default", "sess_equal")
	if err != nil {
		t.Fatalf("Get created session: %v", err)
	}
	if got.ExperimentContext == nil {
		t.Fatal("session was not enrolled despite the variant pool meeting the isolation floor")
	}
}

func TestExperimentRouterAllowsStrongerVariantPool(t *testing.T) {
	exps := experimentstore.NewMemory()
	routedExperiment(t, exps, "claude-code", "claude-code-v2", "cc-v2-pool")
	pools := poolstore.NewMemory()
	// The variant pool is more isolated than the session asked for.
	seedPool(t, pools, "cc-v2-pool", isolation.ProfileMicrovm)
	h, store := routerServerWithPools(t, "sess_strong", exps, pools)

	rr := postSessionIso(t, h, "/v1/sessions", "claude-code", string(isolation.ProfileSandboxed))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	got, err := store.Get(context.Background(), "default", "sess_strong")
	if err != nil {
		t.Fatalf("Get created session: %v", err)
	}
	if got.ExperimentContext == nil {
		t.Fatal("session was not enrolled despite the variant pool exceeding the isolation floor")
	}
}

func TestExperimentRouterCreateAndStartFailsClosed(t *testing.T) {
	exps := experimentstore.NewMemory()
	routedExperiment(t, exps, "claude-code", "claude-code-v2", "cc-v2-pool")
	pools := poolstore.NewMemory()
	seedPool(t, pools, "cc-v2-pool", isolation.ProfileSandboxed)
	h, store := routerServerWithPools(t, "sess_start_weak", exps, pools)

	// The §15.1 create-and-start convenience path runs the same router.
	rr := postSessionIso(t, h, "/v1/sessions/start", "claude-code", string(isolation.ProfileMicrovm))
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("create-and-start: status %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	code, _ := errorDetails(t, rr)
	if code != "VARIANT_ISOLATION_UNAVAILABLE" {
		t.Errorf("error code = %q, want VARIANT_ISOLATION_UNAVAILABLE", code)
	}
	if _, err := store.Get(context.Background(), "default", "sess_start_weak"); err == nil {
		t.Error("session was created despite the fail-closed isolation rejection")
	}
}

func TestExperimentRouterRejectionReporterReceivesEvent(t *testing.T) {
	exps := experimentstore.NewMemory()
	routedExperiment(t, exps, "claude-code", "claude-code-v2", "cc-v2-pool")
	pools := poolstore.NewMemory()
	seedPool(t, pools, "cc-v2-pool", isolation.ProfileSandboxed)
	reporter := &recordingRejectionReporter{}
	h := sessionserver.New(memstore.New(), sessionserver.Options{
		Experiments:          exps,
		Pools:                pools,
		ExperimentRejections: reporter,
		IDFunc:               func() string { return "sess_report" },
	}).Handler()

	rr := postSessionIso(t, h, "/v1/sessions", "claude-code", string(isolation.ProfileMicrovm))
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("create: status %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	if len(reporter.events) != 1 {
		t.Fatalf("reporter received %d events, want exactly 1", len(reporter.events))
	}
	want := sessionserver.ExperimentIsolationRejection{
		TenantID:             "default",
		UserID:               "alice",
		ExperimentID:         "exp_1",
		VariantID:            "treatment",
		SessionMinIsolation:  "microvm",
		VariantPoolIsolation: "sandboxed",
	}
	if reporter.events[0] != want {
		t.Errorf("rejection event = %+v, want %+v", reporter.events[0], want)
	}
}

func TestExperimentRouterReporterSilentOnSuccessfulRouting(t *testing.T) {
	exps := experimentstore.NewMemory()
	routedExperiment(t, exps, "claude-code", "claude-code-v2", "cc-v2-pool")
	pools := poolstore.NewMemory()
	seedPool(t, pools, "cc-v2-pool", isolation.ProfileMicrovm)
	reporter := &recordingRejectionReporter{}
	h := sessionserver.New(memstore.New(), sessionserver.Options{
		Experiments:          exps,
		Pools:                pools,
		ExperimentRejections: reporter,
		IDFunc:               func() string { return "sess_ok" },
	}).Handler()

	rr := postSessionIso(t, h, "/v1/sessions", "claude-code", string(isolation.ProfileMicrovm))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	if len(reporter.events) != 0 {
		t.Errorf("reporter received %d events on a successful routing, want 0", len(reporter.events))
	}
}

func TestExperimentRouterSkipsIsolationCheckWithoutPoolStore(t *testing.T) {
	// With no pool registry wired the router cannot resolve the variant
	// pool's profile, so the isolation check is skipped and the session
	// is enrolled. Production always wires the pool store.
	exps := experimentstore.NewMemory()
	routedExperiment(t, exps, "claude-code", "claude-code-v2", "cc-v2-pool")
	h, store := routerServerWithPools(t, "sess_nopool", exps, nil)

	rr := postSessionIso(t, h, "/v1/sessions", "claude-code", string(isolation.ProfileMicrovm))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	got, err := store.Get(context.Background(), "default", "sess_nopool")
	if err != nil {
		t.Fatalf("Get created session: %v", err)
	}
	if got.ExperimentContext == nil {
		t.Error("session was not enrolled when the isolation check is skipped")
	}
}
