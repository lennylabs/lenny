// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
)

// spec: §5.1 lines 121-126, 197-198, 199, 208 — workspaceDefaults,
// sharedAssets, and runtimeOptionsSchema are modeled on the runtime and
// round-trip through the admin API.
func TestCreateRuntimeModelsWorkspaceDefaultsSharedAssetsOptionsSchema(t *testing.T) {
	router, store, _ := newRuntimeAdmin(t)
	schema := json.RawMessage(`{"type":"object","properties":{"model":{"type":"string"}},"additionalProperties":false}`)
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name:          "langgraph",
		Type:          "agent",
		Image:         "ghcr.io/acme/langgraph@sha256:abcdef",
		ExecutionMode: "service",
		WorkspaceDefaults: &runtimestore.WorkspaceDefaults{
			Files:         []runtimestore.WorkspaceFile{{Path: "agent.py", Content: "..."}},
			SetupCommands: []runtimestore.WorkspaceSetupCommand{{Cmd: "pip install -r requirements.txt"}},
		},
		RuntimeOptionsSchema: schema,
		SharedAssets: []runtimestore.SharedAsset{
			{Type: runtimestore.SharedAssetArtifact, Ref: "lenny-blob://tenant_acme/shared/models.tar.gz", DestPath: "models/"},
			{Type: runtimestore.SharedAssetInline, Path: "config.json", Content: `{"version":1}`, DestPath: "config.json"},
		},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rr.Code, rr.Body.String())
	}
	row, err := store.Get(context.Background(), "langgraph")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if row.WorkspaceDefaults == nil || len(row.WorkspaceDefaults.Files) != 1 ||
		row.WorkspaceDefaults.Files[0].Path != "agent.py" {
		t.Errorf("stored workspaceDefaults = %+v", row.WorkspaceDefaults)
	}
	if len(row.WorkspaceDefaults.SetupCommands) != 1 || row.WorkspaceDefaults.SetupCommands[0].Cmd != "pip install -r requirements.txt" {
		t.Errorf("stored workspaceDefaults.setupCommands = %+v", row.WorkspaceDefaults.SetupCommands)
	}
	if len(row.SharedAssets) != 2 || row.SharedAssets[0].DestPath != "models/" {
		t.Errorf("stored sharedAssets = %+v", row.SharedAssets)
	}
	if len(row.RuntimeOptionsSchema) == 0 {
		t.Errorf("stored runtimeOptionsSchema is empty")
	}
}

// spec: §5.1 — workspaceDefaults, sharedAssets, and runtimeOptionsSchema
// invalid values are rejected at registration.
func TestCreateRuntimeRejectsInvalidWorkspaceBlocks(t *testing.T) {
	cases := []struct {
		name    string
		payload admin.RuntimePayload
	}{
		{"file-missing-path", admin.RuntimePayload{
			Name: "r1", Image: "x@sha256:a",
			WorkspaceDefaults: &runtimestore.WorkspaceDefaults{Files: []runtimestore.WorkspaceFile{{Content: "x"}}},
		}},
		{"empty-setup-command", admin.RuntimePayload{
			Name: "r2", Image: "x@sha256:a",
			WorkspaceDefaults: &runtimestore.WorkspaceDefaults{SetupCommands: []runtimestore.WorkspaceSetupCommand{{Cmd: ""}}},
		}},
		{"shared-asset-bad-type", admin.RuntimePayload{
			Name: "r3", Image: "x@sha256:a",
			SharedAssets: []runtimestore.SharedAsset{{Type: "bogus", DestPath: "x/"}},
		}},
		{"shared-asset-missing-dest", admin.RuntimePayload{
			Name: "r4", Image: "x@sha256:a",
			SharedAssets: []runtimestore.SharedAsset{{Type: runtimestore.SharedAssetArtifact, Ref: "lenny-blob://x"}},
		}},
		{"shared-asset-artifact-missing-ref", admin.RuntimePayload{
			Name: "r5", Image: "x@sha256:a",
			SharedAssets: []runtimestore.SharedAsset{{Type: runtimestore.SharedAssetArtifact, DestPath: "x/"}},
		}},
		{"options-schema-not-object", admin.RuntimePayload{
			Name: "r6", Image: "x@sha256:a",
			RuntimeOptionsSchema: json.RawMessage(`["not","an","object"]`),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router, _, _ := newRuntimeAdmin(t)
			rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", tc.payload)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400; body=%s", rr.Code, rr.Body.String())
			}
		})
	}
}

// spec: §5.1 line 199 — a derived runtimeOptionsSchema may only reference
// property names present in the base schema; an unknown property is
// rejected with INVALID_DERIVED_RUNTIME: runtimeOptionsSchema declares
// forbidden property.
func TestCreateDerivedRuntimeRejectsForbiddenOptionsSchemaProperty(t *testing.T) {
	router, store, _ := newRuntimeAdmin(t)
	if err := store.Create(context.Background(), runtimestore.Runtime{
		Name:                 "base-rt",
		Image:                "lenny/base@sha256:abc",
		RuntimeOptionsSchema: json.RawMessage(`{"properties":{"model":{}}}`),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// A derived schema referencing only "model" is a subset → accepted.
	ok := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name: "subset", BaseRuntime: "base-rt",
		RuntimeOptionsSchema: json.RawMessage(`{"properties":{"model":{"type":"string"}}}`),
	})
	if ok.Code != http.StatusCreated {
		t.Fatalf("subset schema: status %d, body=%s", ok.Code, ok.Body.String())
	}
	// A derived schema introducing "temperature" is forbidden → rejected.
	bad := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", admin.RuntimePayload{
		Name: "superset", BaseRuntime: "base-rt",
		RuntimeOptionsSchema: json.RawMessage(`{"properties":{"temperature":{}}}`),
	})
	if bad.Code != http.StatusBadRequest || !strings.Contains(bad.Body.String(), "INVALID_DERIVED_RUNTIME") ||
		!strings.Contains(bad.Body.String(), "forbidden property") {
		t.Errorf("forbidden property: status %d body %s", bad.Code, bad.Body.String())
	}
}

// spec: §5.1 line 174 — a base runtime's image is immutable via the admin
// API; a PUT changing it is rejected with IMAGE_IMMUTABLE.
func TestUpdateRuntimeRejectsBaseImageMutation(t *testing.T) {
	router, store, _ := newRuntimeAdmin(t)
	if err := store.Create(context.Background(), runtimestore.Runtime{
		Name: "base-rt", Image: "lenny/base@sha256:abc",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	newImg := "lenny/base@sha256:def"
	rr := runtimeRequest(t, router.Handler(), http.MethodPut, "/v1/admin/runtimes/base-rt",
		admin.UpdateRuntimeRequest{Image: &newImg})
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "IMAGE_IMMUTABLE") {
		t.Errorf("base image mutation: status %d body %s", rr.Code, rr.Body.String())
	}
	// A PUT carrying the same image (no-op) is accepted.
	same := "lenny/base@sha256:abc"
	ok := runtimeRequest(t, router.Handler(), http.MethodPut, "/v1/admin/runtimes/base-rt",
		admin.UpdateRuntimeRequest{Image: &same})
	if ok.Code != http.StatusOK {
		t.Errorf("no-op image PUT: status %d body %s", ok.Code, ok.Body.String())
	}
}

// spec: §5.1 — a PUT replaces the workspaceDefaults / sharedAssets blocks
// and the changed-fields audit lists them.
func TestUpdateRuntimeReplacesWorkspaceBlocks(t *testing.T) {
	router, store, _ := newRuntimeAdmin(t)
	if err := store.Create(context.Background(), runtimestore.Runtime{
		Name: "agent-rt", Image: "lenny/agent@sha256:abc",
		WorkspaceDefaults: &runtimestore.WorkspaceDefaults{Files: []runtimestore.WorkspaceFile{{Path: "old.py"}}},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	wd := &runtimestore.WorkspaceDefaults{Files: []runtimestore.WorkspaceFile{{Path: "new.py", Content: "x"}}}
	rr := runtimeRequest(t, router.Handler(), http.MethodPut, "/v1/admin/runtimes/agent-rt",
		admin.UpdateRuntimeRequest{WorkspaceDefaults: wd})
	if rr.Code != http.StatusOK {
		t.Fatalf("update: status %d body %s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "agent-rt")
	if row.WorkspaceDefaults == nil || len(row.WorkspaceDefaults.Files) != 1 || row.WorkspaceDefaults.Files[0].Path != "new.py" {
		t.Errorf("workspaceDefaults not replaced: %+v", row.WorkspaceDefaults)
	}
}
