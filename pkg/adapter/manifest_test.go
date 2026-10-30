// SPDX-License-Identifier: MIT

package adapter

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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

// spec: §4.7 — the manifest is mounted read-only into the
// agent container; the file must be group-readable (so the runtime's
// distinct UID can read it via the shared lenny-cred-readers fsGroup)
// but never world-readable, since it carries the §15.4.3 mcpNonce.
func TestWriteManifestModeIsGroupReadableNotWorldReadable(t *testing.T) {
	dir := t.TempDir()
	if err := WriteManifest(dir, Manifest{Version: ManifestVersion, MCPNonce: "aa"}); err != nil {
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
	srv := &Server{WorkspaceBase: "/workspace", ManifestDir: dir}
	if _, err := srv.writeSessionManifest(manifestInputs{}); err != nil {
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
	srv := &Server{WorkspaceBase: "/workspace", ManifestDir: dir}

	readNonce := func() string {
		if _, err := srv.writeSessionManifest(manifestInputs{}); err != nil {
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

func TestWriteSessionManifestRuntimeOps(t *testing.T) {
	dir := t.TempDir()
	srv := &Server{WorkspaceBase: "/workspace", ManifestDir: dir}

	// A Basic-level adapter has no CH-RUNTIMEOPS; the manifest omits
	// the runtimeOps object entirely.
	if _, err := srv.writeSessionManifest(manifestInputs{}); err != nil {
		t.Fatalf("writeSessionManifest: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, ManifestFilename))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if strings.Contains(string(raw), "runtimeOps") {
		t.Error("Basic-level manifest should omit runtimeOps")
	}

	// With CH-RUNTIMEOPS configured, the manifest advertises its
	// socket so a Full-level runtime can dial it.
	lc, err := newTestRuntimeOps(t, shortSocketName(t, "lifecycle.sock"), SocketPeerAuth{ExpectedUID: uint32(os.Getuid())})
	if err != nil {
		t.Fatalf("NewRuntimeOps: %v", err)
	}
	defer lc.Close()
	srv.Lifecycle = lc

	if _, err := srv.writeSessionManifest(manifestInputs{}); err != nil {
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
	if m.RuntimeOps == nil {
		t.Fatal("Full-level manifest omits runtimeOps")
	}
	if m.RuntimeOps.Socket != lc.SocketPath() {
		t.Errorf("runtimeOps.socket = %q, want %q", m.RuntimeOps.Socket, lc.SocketPath())
	}
}

// spec: §4.7.6 (Adapter Manifest Field Reference) — WriteManifest
// round-trips the pod-scoped fields at the current schema version.
func TestWriteManifest(t *testing.T) {
	dir := t.TempDir()
	if err := WriteManifest(dir, Manifest{
		Version:            ManifestVersion,
		MCPNonce:           "aa",
		AgentInterface:     json.RawMessage(`{"description":"echo"}`),
		MinPlatformVersion: "1.4.0",
	}); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	m := readManifestForTest(t, dir)
	if m.Version != 1 || ManifestVersion != 1 {
		t.Errorf("manifest version = %d (ManifestVersion %d), want 1", m.Version, ManifestVersion)
	}
	var ai struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal(m.AgentInterface, &ai); err != nil || ai.Description != "echo" {
		t.Errorf("manifest agentInterface = %s (err %v), want the echo descriptor", m.AgentInterface, err)
	}
	if m.MCPNonce != "aa" || m.MinPlatformVersion != "1.4.0" {
		t.Errorf("manifest mcpNonce / minPlatformVersion = %q / %q, want aa / 1.4.0", m.MCPNonce, m.MinPlatformVersion)
	}
}

func TestWriteManifestNeverAbsentArrays(t *testing.T) {
	// §4.7 / §15: connectorServers, runtimeMcpServers, and
	// adapterLocalTools serialize as [], never null, never absent.
	dir := t.TempDir()
	if err := WriteManifest(dir, Manifest{Version: ManifestVersion}); err != nil {
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

func TestWriteSessionManifestSkipsWithoutDir(t *testing.T) {
	// An adapter with no ManifestDir writes nothing.
	srv := &Server{WorkspaceBase: "/workspace"}
	if _, err := srv.writeSessionManifest(manifestInputs{}); err != nil {
		t.Errorf("writeSessionManifest with no ManifestDir = %v, want nil", err)
	}
}

// spec: §4.7.6 (Adapter Manifest Field Reference, Per-session fields);
// §28.5.3 (CH-MSGSOCK, Inbound: session_start) — the manifest carries only
// pod-scoped fields. A start with experiment and tracing context, an
// assigned LLM lease, and a provisioned credential file still writes a
// manifest with no sessionId, taskId, credentialsPath, experimentContext,
// tracingContext, or llm member, because each of them reaches the runtime
// in the session's own session_start frame and a pod-global copy would be
// wrong for every other session the runtime process serves.
func TestWriteSessionManifestCarriesNoPerSessionMembers_spec_4_7_6(t *testing.T) {
	dir := t.TempDir()
	srv := &Server{WorkspaceBase: t.TempDir(), ManifestDir: dir, CredentialsDir: t.TempDir()}
	setSessionLeasesForTest(t, srv, "sess-y", true, map[string]*adapterv1.CredentialLease{
		"anthropic": {LeaseId: "l1", Provider: "anthropic", Payload: []byte(proxyLeasePayload)},
	})
	if _, err := srv.writeSessionManifest(manifestInputs{
		experimentContext: &adapterv1.ExperimentContext{ExperimentId: "exp_1", VariantId: "treatment"},
		tracingContext:    map[string]string{"run": "r1"},
	}); err != nil {
		t.Fatalf("writeSessionManifest: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, ManifestFilename))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	for _, member := range []string{"sessionId", "taskId", "credentialsPath", "experimentContext", "tracingContext", "llm"} {
		if fieldPresent(t, raw, member) {
			t.Errorf("manifest carries the per-session member %q: %s", member, raw)
		}
	}
	for _, member := range []string{"version", "mcpNonce", "agentInterface", "adapterLocalTools", "connectorServers", "runtimeMcpServers"} {
		if !fieldPresent(t, raw, member) {
			t.Errorf("manifest omits the pod-scoped member %q", member)
		}
	}
	if m := readManifestForTest(t, dir); m.Version != 1 {
		t.Errorf("manifest version = %d, want 1", m.Version)
	}
}

func readManifestForTest(t *testing.T, dir string) Manifest {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, ManifestFilename))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	return m
}

// spec: §4.7 — the manifest is one pod-global file at a fixed path, and a
// later session's start replaces its mcpNonce. The replacement is published as one whole document, so
// the file a reader on the pod opens decodes as exactly the start that
// wrote it last, and the staging the publication uses leaves nothing
// beside the manifest.
func TestWriteManifestPublishesOneWholeDocument(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ManifestFilename)
	if err := WriteManifest(dir, Manifest{Version: ManifestVersion, MCPNonce: "aa"}); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	if err := WriteManifest(dir, Manifest{Version: ManifestVersion, MCPNonce: "bb"}); err != nil {
		t.Fatalf("WriteManifest rewrite: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode the published manifest: %v", err)
	}
	if m.MCPNonce != "bb" {
		t.Errorf("published manifest mcpNonce = %q, want the rewriting start's %q", m.MCPNonce, "bb")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat manifest: %v", err)
	}
	if got := info.Mode().Perm(); got != ManifestFileMode {
		t.Errorf("manifest mode = %#o, want %#o", got, ManifestFileMode)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read manifest dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != ManifestFilename {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("manifest dir holds %v after two writes, want only %s", names, ManifestFilename)
	}
}

// spec: §4.7 — on a pod holding more than one bound session a later start
// replaces the manifest while an earlier session's runtime is still
// processing, so two starts can rewrite the one pod-global file at once.
// Whichever write lands last, the file carries exactly that session's
// document: a reader never observes the two documents interleaved. A write
// applied in place onto the live path fails this case with a decode error.
func TestConcurrentWriteManifestNeverPublishesATornDocument(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ManifestFilename)
	if err := WriteManifest(dir, Manifest{Version: ManifestVersion, MCPNonce: "cc"}); err != nil {
		t.Fatalf("seed manifest: %v", err)
	}
	// The two documents differ in length so a torn write leaves a
	// residue that is neither: the first writer's tools array pads its encoding well
	// past bob's.
	tools := make([]ManifestTool, 64)
	for i := range tools {
		tools[i] = ManifestTool{Name: fmt.Sprintf("lenny_tool_%03d", i), Description: strings.Repeat("d", 128)}
	}
	writers := []Manifest{
		{Version: ManifestVersion, MCPNonce: "aa", AdapterLocalTools: tools},
		{Version: ManifestVersion, MCPNonce: "bb"},
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for _, w := range writers {
		wg.Add(1)
		go func(m Manifest) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if err := WriteManifest(dir, m); err != nil {
					t.Errorf("WriteManifest(%s): %v", m.MCPNonce, err)
					return
				}
			}
		}(w)
	}
	deadline := time.Now().Add(2 * time.Second)
	reads := 0
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(path)
		if err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf("read manifest during the concurrent rewrites: %v", err)
		}
		var m Manifest
		if err := json.Unmarshal(b, &m); err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf("the manifest read during two concurrent rewrites does not decode as one document: %v", err)
		}
		if m.MCPNonce != "aa" && m.MCPNonce != "bb" && m.MCPNonce != "cc" {
			close(stop)
			wg.Wait()
			t.Fatalf("the manifest read during two concurrent rewrites carries mcpNonce %q, want one writer's whole document", m.MCPNonce)
		}
		reads++
	}
	close(stop)
	wg.Wait()
	if reads == 0 {
		t.Fatal("the case read the manifest no times, so it asserted nothing")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read manifest dir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("manifest dir holds %d entries after the concurrent rewrites, want only %s", len(entries), ManifestFilename)
	}
}
