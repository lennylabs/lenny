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
	if len(m.TracingContext) != 0 {
		t.Errorf("tracingContext = %v, want empty when none is set", m.TracingContext)
	}
}

func TestWriteManifestWithTracingContext(t *testing.T) {
	dir := t.TempDir()
	if err := WriteManifest(dir, Manifest{
		Version:        ManifestVersion,
		SessionID:      "sess-3",
		TracingContext: map[string]string{"langsmith_run_id": "run_abc"},
	}); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, ManifestFilename))
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m.TracingContext["langsmith_run_id"] != "run_abc" {
		t.Errorf("tracingContext = %v, want the langsmith run id", m.TracingContext)
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

func TestWriteSessionManifestSkipsWithoutDir(t *testing.T) {
	// An adapter with no ManifestDir writes nothing.
	srv := &Server{WorkspaceRoot: "/workspace/current"}
	if err := srv.writeSessionManifest("sess-x", nil, nil); err != nil {
		t.Errorf("writeSessionManifest with no ManifestDir = %v, want nil", err)
	}
}

func TestWriteSessionManifestWrites(t *testing.T) {
	dir := t.TempDir()
	srv := &Server{WorkspaceRoot: "/workspace/current", ManifestDir: dir}
	if err := srv.writeSessionManifest("sess-y", &adapterv1.ExperimentContext{
		ExperimentId: "exp_1", VariantId: "treatment",
	}, map[string]string{"run": "r1"}); err != nil {
		t.Fatalf("writeSessionManifest: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, ManifestFilename))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m.SessionID != "sess-y" || m.ExperimentContext == nil ||
		m.ExperimentContext.ExperimentID != "exp_1" || m.TracingContext["run"] != "r1" {
		t.Errorf("manifest = %+v", m)
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
