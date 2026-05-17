// SPDX-License-Identifier: MIT

package adapter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

func TestWriteManifest(t *testing.T) {
	dir := t.TempDir()
	if err := WriteManifest(dir, Manifest{
		Version:       ManifestVersion,
		SessionID:     "sess-1",
		WorkspaceRoot: "/workspace/current",
	}); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, ManifestFilename))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if m.Version != ManifestVersion || m.SessionID != "sess-1" || m.WorkspaceRoot != "/workspace/current" {
		t.Errorf("manifest = %+v", m)
	}
	if m.ExperimentContext != nil {
		t.Errorf("experimentContext = %+v, want nil for an unenrolled session", m.ExperimentContext)
	}
}

func TestWriteManifestWithExperimentContext(t *testing.T) {
	dir := t.TempDir()
	if err := WriteManifest(dir, Manifest{
		Version:   ManifestVersion,
		SessionID: "sess-2",
		ExperimentContext: &ManifestExperimentContext{
			ExperimentID: "exp_1", VariantID: "treatment", Inherited: true,
		},
	}); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, ManifestFilename))
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m.ExperimentContext == nil || m.ExperimentContext.ExperimentID != "exp_1" ||
		m.ExperimentContext.VariantID != "treatment" || !m.ExperimentContext.Inherited {
		t.Errorf("experimentContext = %+v, want exp_1/treatment inherited", m.ExperimentContext)
	}
}

func TestManifestExperimentContextNil(t *testing.T) {
	if got := manifestExperimentContext(nil); got != nil {
		t.Errorf("manifestExperimentContext(nil) = %v, want nil", got)
	}
}

func TestManifestExperimentContextMapsProtoFields(t *testing.T) {
	got := manifestExperimentContext(&adapterv1.ExperimentContext{
		ExperimentId: "exp_9", VariantId: "control", Inherited: false,
	})
	if got == nil {
		t.Fatal("manifestExperimentContext returned nil for a populated proto")
	}
	if got.ExperimentID != "exp_9" || got.VariantID != "control" || got.Inherited {
		t.Errorf("manifest experimentContext = %+v", got)
	}
}
