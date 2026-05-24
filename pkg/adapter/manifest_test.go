// SPDX-License-Identifier: MIT

package adapter

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

func TestNewMCPNonce(t *testing.T) {
	a, err := newMCPNonce()
	if err != nil {
		t.Fatalf("newMCPNonce: %v", err)
	}
	raw, err := hex.DecodeString(a)
	if err != nil {
		t.Errorf("MCP nonce %q is not valid hex: %v", a, err)
	}
	if len(raw) != MCPNonceBytes {
		t.Errorf("MCP nonce decodes to %d bytes, want %d", len(raw), MCPNonceBytes)
	}
	if b, _ := newMCPNonce(); a == b {
		t.Error("newMCPNonce returned the same value twice")
	}
}

// spec: §4.7 line 846 — the manifest is mounted read-only into the
// agent container; the file must be group-readable (so the runtime's
// distinct UID can read it via the shared lenny-cred-readers fsGroup)
// but never world-readable, since it carries the §15.4.3 mcpNonce.
func TestWriteManifestModeIsGroupReadableNotWorldReadable(t *testing.T) {
	dir := t.TempDir()
	if err := WriteManifest(dir, Manifest{Version: ManifestVersion, SessionID: "sess-mode"}); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, ManifestFilename))
	if err != nil {
		t.Fatalf("stat manifest: %v", err)
	}
	if got := info.Mode().Perm(); got != ManifestFileMode {
		t.Fatalf("manifest mode = %#o, want %#o", got, ManifestFileMode)
	}
	if info.Mode().Perm()&0o004 != 0 {
		t.Errorf("manifest is world-readable (%#o); the mcpNonce must not be exposed to other UIDs", info.Mode().Perm())
	}
	if info.Mode().Perm()&0o040 == 0 {
		t.Errorf("manifest is not group-readable (%#o); the agent runtime reads it via the shared fsGroup", info.Mode().Perm())
	}
}

func TestWriteSessionManifestAdvertisesLocalTools(t *testing.T) {
	dir := t.TempDir()
	srv := &Server{WorkspaceRoot: "/workspace/current", ManifestDir: dir}
	if _, err := srv.writeSessionManifest("sess-t", nil, nil); err != nil {
		t.Fatalf("writeSessionManifest: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, ManifestFilename))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if len(m.AdapterLocalTools) != 4 {
		t.Fatalf("manifest advertises %d adapter-local tools, want 4", len(m.AdapterLocalTools))
	}
	names := map[string]bool{}
	for _, tool := range m.AdapterLocalTools {
		names[tool.Name] = true
		if tool.Description == "" || len(tool.InputSchema) == 0 {
			t.Errorf("manifest tool %q is missing a description or inputSchema", tool.Name)
		}
	}
	for _, want := range []string{"read_file", "write_file", "list_dir", "delete_file"} {
		if !names[want] {
			t.Errorf("manifest does not advertise the %q tool", want)
		}
	}
}

func TestWriteSessionManifestIncludesMCPNonce(t *testing.T) {
	dir := t.TempDir()
	srv := &Server{WorkspaceRoot: "/workspace/current", ManifestDir: dir}

	readNonce := func() string {
		if _, err := srv.writeSessionManifest("sess-n", nil, nil); err != nil {
			t.Fatalf("writeSessionManifest: %v", err)
		}
		b, err := os.ReadFile(filepath.Join(dir, ManifestFilename))
		if err != nil {
			t.Fatalf("read manifest: %v", err)
		}
		var m Manifest
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("decode manifest: %v", err)
		}
		return m.MCPNonce
	}

	first := readNonce()
	raw, err := hex.DecodeString(first)
	if err != nil || len(raw) != MCPNonceBytes {
		t.Errorf("manifest mcpNonce = %q, want a %d-byte hex string", first, MCPNonceBytes)
	}
	// §15.4.3: the nonce is regenerated per session manifest write.
	if second := readNonce(); second == first {
		t.Error("writeSessionManifest reused the MCP nonce across writes")
	}
}

func TestWriteSessionManifestLifecycleChannel(t *testing.T) {
	dir := t.TempDir()
	srv := &Server{WorkspaceRoot: "/workspace/current", ManifestDir: dir}

	// A Basic-level adapter has no lifecycle channel; the manifest omits
	// the lifecycleChannel object entirely.
	if _, err := srv.writeSessionManifest("sess-basic", nil, nil); err != nil {
		t.Fatalf("writeSessionManifest: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, ManifestFilename))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if strings.Contains(string(raw), "lifecycleChannel") {
		t.Error("Basic-level manifest should omit lifecycleChannel")
	}

	// With a lifecycle channel configured, the manifest advertises its
	// socket so a Full-level runtime can dial it.
	sockDir, err := os.MkdirTemp("", "lc-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(sockDir)
	lc, err := NewLifecycleChannel(filepath.Join(sockDir, "lifecycle.sock"))
	if err != nil {
		t.Fatalf("NewLifecycleChannel: %v", err)
	}
	defer lc.Close()
	srv.Lifecycle = lc

	if _, err := srv.writeSessionManifest("sess-full", nil, nil); err != nil {
		t.Fatalf("writeSessionManifest: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, ManifestFilename))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if m.LifecycleChannel == nil {
		t.Fatal("Full-level manifest omits lifecycleChannel")
	}
	if m.LifecycleChannel.Socket != lc.SocketPath() {
		t.Errorf("lifecycleChannel.socket = %q, want %q", m.LifecycleChannel.Socket, lc.SocketPath())
	}
}

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

func TestWriteManifestNeverAbsentArrays(t *testing.T) {
	// §4.7 / §15: connectorServers, runtimeMcpServers, and
	// adapterLocalTools serialize as [], never null, never absent.
	dir := t.TempDir()
	if err := WriteManifest(dir, Manifest{Version: ManifestVersion, SessionID: "s"}); err != nil {
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
	if m.ConnectorServers == nil {
		t.Error("connectorServers serialized as null, want an empty array")
	}
	if m.RuntimeMcpServers == nil {
		t.Error("runtimeMcpServers serialized as null, want an empty array")
	}
	if m.AdapterLocalTools == nil {
		t.Error("adapterLocalTools serialized as null, want an empty array")
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
	if _, err := srv.writeSessionManifest("sess-x", nil, nil); err != nil {
		t.Errorf("writeSessionManifest with no ManifestDir = %v, want nil", err)
	}
}

func TestWriteSessionManifestWrites(t *testing.T) {
	dir := t.TempDir()
	srv := &Server{WorkspaceRoot: "/workspace/current", ManifestDir: dir}
	if _, err := srv.writeSessionManifest("sess-y", &adapterv1.ExperimentContext{
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
