// SPDX-License-Identifier: MIT

package adapter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// spec: §4.7 — the observability object carries the deployment's OTLP
// endpoint, and is omitted when none is configured.
func TestWriteSessionManifestObservability_spec_4_7(t *testing.T) {
	dir := t.TempDir()
	srv := &Server{
		WorkspaceBase: "/workspace", ManifestDir: dir,
		OTLPEndpoint: "https://otel.lenny-system:4317",
	}
	if _, err := srv.writeSessionManifest(manifestInputs{}); err != nil {
		t.Fatalf("writeSessionManifest: %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, ManifestFilename))
	var m Manifest
	_ = json.Unmarshal(raw, &m)
	if m.Observability == nil || m.Observability.OTLPEndpoint != "https://otel.lenny-system:4317" {
		t.Errorf("manifest observability = %+v, want the OTLP endpoint", m.Observability)
	}

	// No endpoint: the object is omitted.
	srv.OTLPEndpoint = ""
	if _, err := srv.writeSessionManifest(manifestInputs{}); err != nil {
		t.Fatalf("writeSessionManifest: %v", err)
	}
	raw, _ = os.ReadFile(filepath.Join(dir, ManifestFilename))
	if fieldPresent(t, raw, "observability") {
		t.Error("manifest carries observability with no endpoint configured; want it omitted")
	}
}

// spec: §4.7 — ReadManifest enforces the forward-compat rule: a manifest
// whose version exceeds the highest understood is rejected.
func TestReadManifestRejectsHigherVersion_spec_4_7(t *testing.T) {
	dir := t.TempDir()
	if err := WriteManifest(dir, Manifest{Version: ManifestVersion + 1, MCPNonce: "aa"}); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	if _, err := ReadManifest(dir); err != ErrManifestVersionTooHigh {
		t.Errorf("ReadManifest of a higher version = %v, want ErrManifestVersionTooHigh", err)
	}

	if err := WriteManifest(dir, Manifest{Version: ManifestVersion, MCPNonce: "bb"}); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	m, err := ReadManifest(dir)
	if err != nil {
		t.Fatalf("ReadManifest of the current version: %v", err)
	}
	if m.MCPNonce != "bb" {
		t.Errorf("ReadManifest mcpNonce = %q, want bb", m.MCPNonce)
	}
}

// fieldPresent reports whether the top-level JSON object raw carries the
// named member.
func fieldPresent(t *testing.T, raw []byte, field string) bool {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	_, ok := obj[field]
	return ok
}
