// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
)

// TestCreateRuntimeSDKWarmBlockingPaths covers the §5.1 lines 16-24
// preConnect / sdkWarmBlockingPaths admission surface: the fields are
// persisted and round-tripped, and the path list defaults when preConnect
// is true and the runtime declares none.
func TestCreateRuntimeSDKWarmBlockingPaths(t *testing.T) {
	router, store, _ := newRuntimeAdmin(t)

	// preConnect true with no list → ApplyDefaults seeds the default set.
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:         "warm-rt",
		Image:        "lenny/warm@sha256:abc",
		Capabilities: &runtimestore.RuntimeCapabilities{PreConnect: true},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create preConnect runtime: status %d, body=%s", rr.Code, rr.Body.String())
	}
	var created admin.RuntimePayload
	_ = json.Unmarshal(rr.Body.Bytes(), &created)
	if created.Capabilities == nil || !created.Capabilities.PreConnect {
		t.Errorf("capabilities.preConnect not persisted: %+v", created.Capabilities)
	}
	if got := created.SDKWarmBlockingPaths; len(got) != 2 || got[0] != "CLAUDE.md" || got[1] != ".claude/*" {
		t.Errorf("sdkWarmBlockingPaths default not seeded: %v", got)
	}
	stored, _ := store.Get(context.Background(), "warm-rt")
	if len(stored.SDKWarmBlockingPaths) != 2 {
		t.Errorf("store did not persist sdkWarmBlockingPaths: %v", stored.SDKWarmBlockingPaths)
	}

	// An explicit list with preConnect true is kept verbatim.
	rr = runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:                 "warm-explicit",
		Image:                "lenny/warm2@sha256:abc",
		Capabilities:         &runtimestore.RuntimeCapabilities{PreConnect: true},
		SDKWarmBlockingPaths: []string{"AGENTS.md"},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create explicit-list runtime: status %d, body=%s", rr.Code, rr.Body.String())
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &created)
	if len(created.SDKWarmBlockingPaths) != 1 || created.SDKWarmBlockingPaths[0] != "AGENTS.md" {
		t.Errorf("explicit sdkWarmBlockingPaths not preserved: %v", created.SDKWarmBlockingPaths)
	}
}

// TestUpdateRuntimeSetsSDKWarmBlockingPaths covers the §5.1 PUT path for
// the sdkWarmBlockingPaths field.
func TestUpdateRuntimeSetsSDKWarmBlockingPaths(t *testing.T) {
	router, store, _ := newRuntimeAdmin(t)
	createBaseRuntime(t, router.Handler(), "warm-rt")

	paths := []string{"README.md", ".lenny/*"}
	rr := runtimeRequest(t, router.Handler(), http.MethodPut, "/v1/admin/runtimes/warm-rt",
		admin.UpdateRuntimeRequest{SDKWarmBlockingPaths: &paths})
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT sdkWarmBlockingPaths: status %d, body=%s", rr.Code, rr.Body.String())
	}
	stored, _ := store.Get(context.Background(), "warm-rt")
	if len(stored.SDKWarmBlockingPaths) != 2 || stored.SDKWarmBlockingPaths[1] != ".lenny/*" {
		t.Errorf("PUT did not set sdkWarmBlockingPaths: %v", stored.SDKWarmBlockingPaths)
	}
}

// TestCreateDerivedRuntimeRejectsSetupTimeoutPairing covers the §5.1 line
// 195 note "neither can be zero if the other is set" at derived
// registration: a derived setupPolicy.timeoutSeconds of zero (no cap)
// against a base that sets a finite value (and the reverse) is rejected,
// while two finite values and two no-cap values are accepted.
func TestCreateDerivedRuntimeRejectsSetupTimeoutPairing(t *testing.T) {
	router, _, _ := newRuntimeAdmin(t)
	// Base declares a finite 300s cap.
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:        "base-finite",
		Image:       "lenny/base@sha256:abc",
		SetupPolicy: &runtimestore.SetupPolicy{TimeoutSeconds: 300, OnTimeout: runtimestore.SetupTimeoutFail},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create base-finite: status %d, body=%s", rr.Code, rr.Body.String())
	}
	// Base declares no cap (zero).
	rr = runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:        "base-nocap",
		Image:       "lenny/base2@sha256:abc",
		SetupPolicy: &runtimestore.SetupPolicy{TimeoutSeconds: 0, OnTimeout: runtimestore.SetupTimeoutFail},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create base-nocap: status %d, body=%s", rr.Code, rr.Body.String())
	}

	// derived zero against a finite base → rejected.
	rr = runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name: "d-zero", BaseRuntime: "base-finite",
		SetupPolicy: &runtimestore.SetupPolicy{TimeoutSeconds: 0, OnTimeout: runtimestore.SetupTimeoutFail},
	})
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "INVALID_DERIVED_RUNTIME") {
		t.Errorf("derived zero vs finite base: status %d body %s", rr.Code, rr.Body.String())
	}
	// derived finite against a no-cap base → rejected.
	rr = runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name: "d-finite", BaseRuntime: "base-nocap",
		SetupPolicy: &runtimestore.SetupPolicy{TimeoutSeconds: 120, OnTimeout: runtimestore.SetupTimeoutFail},
	})
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "INVALID_DERIVED_RUNTIME") {
		t.Errorf("derived finite vs no-cap base: status %d body %s", rr.Code, rr.Body.String())
	}
	// Two finite values → accepted (the Maximum merge applies).
	rr = runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name: "d-finite-ok", BaseRuntime: "base-finite",
		SetupPolicy: &runtimestore.SetupPolicy{TimeoutSeconds: 120, OnTimeout: runtimestore.SetupTimeoutFail},
	})
	if rr.Code != http.StatusCreated {
		t.Errorf("derived finite vs finite base: status %d body %s", rr.Code, rr.Body.String())
	}
	// Two no-cap values → accepted.
	rr = runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name: "d-nocap-ok", BaseRuntime: "base-nocap",
		SetupPolicy: &runtimestore.SetupPolicy{TimeoutSeconds: 0, OnTimeout: runtimestore.SetupTimeoutFail},
	})
	if rr.Code != http.StatusCreated {
		t.Errorf("derived no-cap vs no-cap base: status %d body %s", rr.Code, rr.Body.String())
	}
}

// TestUpdateBaseRuntimeImpactValidation covers §5.1 line 174: a base
// runtime mutation that would invalidate an existing derived runtime is
// rejected with the affected runtime names; a benign mutation and a
// widening mutation are accepted.
func TestUpdateBaseRuntimeImpactValidation(t *testing.T) {
	router, _, _ := newRuntimeAdmin(t)
	// Base supports two providers and allows self-recursion.
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:               "base-rt",
		Image:              "lenny/base@sha256:abc",
		SupportedProviders: []string{"anthropic_direct", "openai_direct"},
		AllowSelfRecursion: true,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create base: status %d, body=%s", rr.Code, rr.Body.String())
	}
	// Derived restricts to one provider and keeps self-recursion.
	rr = runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name: "derived-rt", BaseRuntime: "base-rt",
		SupportedProviders: []string{"openai_direct"},
		AllowSelfRecursion: true,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create derived: status %d, body=%s", rr.Code, rr.Body.String())
	}

	// Narrowing the base provider set drops a provider the derived still
	// declares → rejected, naming the derived runtime.
	narrow := []string{"anthropic_direct"}
	rr = runtimeRequest(t, router.Handler(), http.MethodPut, "/v1/admin/runtimes/base-rt",
		admin.UpdateRuntimeRequest{SupportedProviders: &narrow})
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "BASE_RUNTIME_MUTATION_INVALIDATES_DERIVED") {
		t.Fatalf("narrowing base providers: status %d body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "derived-rt") {
		t.Errorf("rejection must name the affected runtime: %s", rr.Body.String())
	}

	// Revoking base self-recursion while the derived keeps it → rejected.
	deny := false
	rr = runtimeRequest(t, router.Handler(), http.MethodPut, "/v1/admin/runtimes/base-rt",
		admin.UpdateRuntimeRequest{AllowSelfRecursion: &deny})
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "BASE_RUNTIME_MUTATION_INVALIDATES_DERIVED") {
		t.Errorf("revoking base self-recursion: status %d body %s", rr.Code, rr.Body.String())
	}

	// Widening the base provider set keeps the derived valid → accepted.
	widen := []string{"anthropic_direct", "openai_direct", "bedrock"}
	rr = runtimeRequest(t, router.Handler(), http.MethodPut, "/v1/admin/runtimes/base-rt",
		admin.UpdateRuntimeRequest{SupportedProviders: &widen})
	if rr.Code != http.StatusOK {
		t.Errorf("widening base providers: status %d body %s", rr.Code, rr.Body.String())
	}

	// A benign description change is accepted.
	desc := "updated"
	rr = runtimeRequest(t, router.Handler(), http.MethodPut, "/v1/admin/runtimes/base-rt",
		admin.UpdateRuntimeRequest{Description: &desc})
	if rr.Code != http.StatusOK {
		t.Errorf("benign base mutation: status %d body %s", rr.Code, rr.Body.String())
	}
}

// TestBootstrapAutoGeneratesAgentCard covers §5.1 lines 283-291 on the
// bootstrap install path: a runtime registered (and re-upserted) with an
// agentInterface gets a write-time auto-generated A2A agent card.
func TestBootstrapAutoGeneratesAgentCard(t *testing.T) {
	router, _, runtimes, _, _ := newBootstrapRouter(t)

	post := func(desc, query string) {
		body := admin.BootstrapRequest{
			Runtimes: []admin.RuntimePayload{{
				Name:   "carded",
				Image:  "lenny/carded@sha256:abc",
				Type:   "agent",
				Labels: map[string]string{"tier": "test"},
				AgentInterface: &runtimestore.AgentInterface{
					Description: desc,
					Skills:      []runtimestore.AgentInterfaceSkill{{ID: "review"}},
				},
			}},
		}
		buf, _ := json.Marshal(body)
		req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/bootstrap"+query, bytes.NewReader(buf)))
		rr := httptest.NewRecorder()
		router.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("bootstrap (%s): status %d, body=%s", desc, rr.Code, rr.Body.String())
		}
	}

	// Create branch.
	post("Analyzes codebases", "")
	stored, err := runtimes.Get(context.Background(), "carded")
	if err != nil {
		t.Fatalf("runtime not stored: %v", err)
	}
	entry := agentCardEntry(t, stored.PublishedMetadata)
	if entry.Content == "" || !strings.Contains(entry.Content, "Analyzes codebases") {
		t.Errorf("bootstrap create did not generate an agent card: %+v", entry)
	}

	// Update branch: re-upsert with a new description regenerates the card.
	// §17.6 line 450 — a differing agentInterface is a conflict, so the
	// overwrite requires --force-update (?forceUpdate=true).
	post("Reviews pull requests", "?forceUpdate=true")
	stored, _ = runtimes.Get(context.Background(), "carded")
	entry = agentCardEntry(t, stored.PublishedMetadata)
	if !strings.Contains(entry.Content, "Reviews pull requests") {
		t.Errorf("bootstrap update did not regenerate the agent card: %+v", entry)
	}
	// Exactly one agent-card entry — regeneration replaces rather than appends.
	count := 0
	for _, e := range stored.PublishedMetadata {
		if e.Key == "agent-card" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly one agent-card entry, got %d", count)
	}
}
