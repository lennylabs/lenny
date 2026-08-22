// SPDX-License-Identifier: MIT

package adapter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

const proxyLeasePayload = `{"deliveryMode":"proxy","materializedConfig":` +
	`{"proxyUrl":"https://proxy.lenny-system/v1","proxyDialect":"anthropic","leaseToken":"lease-tok"}}`

const directLeasePayload = `{"deliveryMode":"direct","materializedConfig":{"apiKey":"sk-x"}}`

// spec: §4.7 — a proxy-mode lease yields a manifest llm object with the
// dialect, base URL, and canonical API-key env var the runtime points its
// SDK at.
func TestManifestLLMFromProxyLease_spec_4_7(t *testing.T) {
	llm := manifestLLMFromPayload([]byte(proxyLeasePayload))
	if llm == nil {
		t.Fatal("manifestLLMFromPayload(proxy) = nil, want an llm object")
	}
	if llm.DeliveryMode != "proxy" {
		t.Errorf("deliveryMode = %q, want proxy", llm.DeliveryMode)
	}
	if llm.Dialect != "anthropic" {
		t.Errorf("dialect = %q, want anthropic", llm.Dialect)
	}
	if llm.BaseURL != "https://proxy.lenny-system/v1" {
		t.Errorf("baseUrl = %q, want the proxy url", llm.BaseURL)
	}
	if llm.APIKeyEnv != "ANTHROPIC_API_KEY" {
		t.Errorf("apiKeyEnv = %q, want ANTHROPIC_API_KEY", llm.APIKeyEnv)
	}
}

// spec: §4.7 — a direct-mode lease omits dialect/baseUrl because the
// runtime uses the upstream provider's native SDK.
func TestManifestLLMFromDirectLease_spec_4_7(t *testing.T) {
	llm := manifestLLMFromPayload([]byte(directLeasePayload))
	if llm == nil {
		t.Fatal("manifestLLMFromPayload(direct) = nil, want an llm object")
	}
	if llm.DeliveryMode != "direct" {
		t.Errorf("deliveryMode = %q, want direct", llm.DeliveryMode)
	}
	if llm.Dialect != "" || llm.BaseURL != "" {
		t.Errorf("direct-mode llm carries dialect=%q baseUrl=%q, want both empty", llm.Dialect, llm.BaseURL)
	}
}

func TestManifestLLMFromEmptyPayload_spec_4_7(t *testing.T) {
	if llm := manifestLLMFromPayload(nil); llm != nil {
		t.Errorf("manifestLLMFromPayload(nil) = %+v, want nil", llm)
	}
	if llm := manifestLLMFromPayload([]byte(`{}`)); llm != nil {
		t.Errorf("manifestLLMFromPayload(no deliveryMode) = %+v, want nil", llm)
	}
}

func TestAPIKeyEnvForDialect_spec_4_7(t *testing.T) {
	// spec: §4.9 — anthropic + openai are the launch
	// dialects; §26.5 / §26.8 / §26.9 add google; §26.6 adds
	// cursor. An unrecognized dialect yields the empty string.
	cases := map[string]string{
		"anthropic": "ANTHROPIC_API_KEY",
		"openai":    "OPENAI_API_KEY",
		"google":    "GOOGLE_API_KEY",
		"cursor":    "CURSOR_API_KEY",
		"mystery":   "",
	}
	for dialect, want := range cases {
		if got := apiKeyEnvForDialect(dialect); got != want {
			t.Errorf("apiKeyEnvForDialect(%q) = %q, want %q", dialect, got, want)
		}
	}
}

// spec: §4.7 — the manifest llm field is derived from the session's
// assigned lease, and is JSON null when no lease is assigned.
func TestWriteSessionManifestLLMField_spec_4_7(t *testing.T) {
	dir := t.TempDir()
	srv := &Server{WorkspaceBase: "/workspace", ManifestDir: dir}

	// No lease assigned: llm is null.
	if _, err := srv.writeSessionManifest(manifestInputs{sessionID: "sess-1"}); err != nil {
		t.Fatalf("writeSessionManifest: %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, ManifestFilename))
	if !jsonFieldIsNull(t, raw, "llm") {
		t.Errorf("manifest llm = %s, want null when no lease is assigned", fieldJSON(t, raw, "llm"))
	}

	// With a proxy lease assigned, llm reflects it.
	setSessionLeasesForTest(t, srv, "sess-1", map[string]*adapterv1.CredentialLease{
		"anthropic": {LeaseId: "l1", Provider: "anthropic", Payload: []byte(proxyLeasePayload)},
	})
	if _, err := srv.writeSessionManifest(manifestInputs{sessionID: "sess-1"}); err != nil {
		t.Fatalf("writeSessionManifest: %v", err)
	}
	raw, _ = os.ReadFile(filepath.Join(dir, ManifestFilename))
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if m.LLM == nil || m.LLM.DeliveryMode != "proxy" || m.LLM.BaseURL != "https://proxy.lenny-system/v1" {
		t.Errorf("manifest llm = %+v, want the proxy lease config", m.LLM)
	}
}

// spec: §4.7 — when more than one provider lease is assigned, the llm
// field is derived from a deterministic (provider-sorted) lease.
func TestManifestLLMMultiProviderDeterministic_spec_4_7(t *testing.T) {
	dir := t.TempDir()
	srv := &Server{WorkspaceBase: "/workspace", ManifestDir: dir}
	setSessionLeasesForTest(t, srv, "sess-1", map[string]*adapterv1.CredentialLease{
		"openai":    {LeaseId: "l2", Provider: "openai", Payload: []byte(directLeasePayload)},
		"anthropic": {LeaseId: "l1", Provider: "anthropic", Payload: []byte(proxyLeasePayload)},
	})
	if _, err := srv.writeSessionManifest(manifestInputs{sessionID: "sess-1"}); err != nil {
		t.Fatalf("writeSessionManifest: %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, ManifestFilename))
	var m Manifest
	_ = json.Unmarshal(raw, &m)
	// "anthropic" sorts before "openai", so its proxy lease drives llm.
	if m.LLM == nil || m.LLM.Dialect != "anthropic" {
		t.Errorf("manifest llm = %+v, want the provider-sorted (anthropic) lease", m.LLM)
	}
}

// spec: §4.7 — the observability object carries the deployment's OTLP
// endpoint, and is omitted when none is configured.
func TestWriteSessionManifestObservability_spec_4_7(t *testing.T) {
	dir := t.TempDir()
	srv := &Server{
		WorkspaceBase: "/workspace", ManifestDir: dir,
		OTLPEndpoint: "https://otel.lenny-system:4317",
	}
	if _, err := srv.writeSessionManifest(manifestInputs{sessionID: "sess-1"}); err != nil {
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
	if _, err := srv.writeSessionManifest(manifestInputs{sessionID: "sess-1"}); err != nil {
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
	if err := WriteManifest(dir, Manifest{Version: ManifestVersion + 1, SessionID: "sess-1"}); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	if _, err := ReadManifest(dir); err != ErrManifestVersionTooHigh {
		t.Errorf("ReadManifest of a higher version = %v, want ErrManifestVersionTooHigh", err)
	}

	if err := WriteManifest(dir, Manifest{Version: ManifestVersion, SessionID: "sess-1", TaskID: "t"}); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	m, err := ReadManifest(dir)
	if err != nil {
		t.Fatalf("ReadManifest of the current version: %v", err)
	}
	if m.SessionID != "sess-1" {
		t.Errorf("ReadManifest sessionId = %q, want sess-1", m.SessionID)
	}
}

// fieldJSON returns the raw JSON of a top-level manifest field.
func fieldJSON(t *testing.T, raw []byte, field string) string {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	return string(obj[field])
}

func jsonFieldIsNull(t *testing.T, raw []byte, field string) bool {
	return fieldJSON(t, raw, field) == "null"
}

func fieldPresent(t *testing.T, raw []byte, field string) bool {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	_, ok := obj[field]
	return ok
}

// setSessionLeasesForTest puts the named session's own §6.1 lease set in
// place, which is where the manifest's llm object is derived from now that
// every assignment lands on the session's own slot. spec: §6.1.
func setSessionLeasesForTest(t *testing.T, srv *Server, sessionID string, leases map[string]*adapterv1.CredentialLease) {
	t.Helper()
	srv.WorkspaceBase = t.TempDir()
	srv.mu.Lock()
	defer srv.mu.Unlock()
	st, err := srv.ensureSlotStateLocked(sessionID)
	if err != nil {
		t.Fatalf("ensure slot state for %s: %v", sessionID, err)
	}
	st.sessionID = sessionID
	st.creds = leases
}

// spec: §4.7 — the manifest names this session's own credential file,
// and §6.1 puts that file at /run/lenny/slots/{sessionId}/credentials.json.
// The path is not a fixed location: two sessions on one pod resolve to
// two files, which is what a runtime cannot derive without the manifest.
func TestWriteSessionManifestCredentialsPath_spec_4_7(t *testing.T) {
	dir := t.TempDir()
	credRoot := t.TempDir()
	srv := &Server{WorkspaceBase: "/workspace", ManifestDir: dir, CredentialsDir: credRoot}

	read := func(sessionID string) string {
		t.Helper()
		if _, err := srv.writeSessionManifest(manifestInputs{sessionID: sessionID}); err != nil {
			t.Fatalf("writeSessionManifest(%s): %v", sessionID, err)
		}
		raw, err := os.ReadFile(filepath.Join(dir, ManifestFilename))
		if err != nil {
			t.Fatalf("read manifest: %v", err)
		}
		var m Manifest
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("decode manifest: %v", err)
		}
		if !fieldPresent(t, raw, "credentialsPath") {
			t.Error("manifest carries no credentialsPath member; a runtime cannot locate its credential file")
		}
		return m.CredentialsPath
	}

	alice := read("sess-alice")
	want := filepath.Join(credRoot, "slots", "sess-alice", "credentials.json")
	if alice != want {
		t.Errorf("manifest credentialsPath = %q, want %q", alice, want)
	}
	bob := read("sess-bob")
	if bob == alice {
		t.Errorf("both sessions' manifests name %q; the path must be per session", bob)
	}

	// The path the manifest names is the path the credential handlers
	// write, so a runtime that reads the member finds the file its own
	// session's leases were materialized into.
	setSessionLeasesForTest(t, srv, "sess-alice", map[string]*adapterv1.CredentialLease{
		"anthropic": {LeaseId: "l1", Provider: "anthropic", Payload: []byte(proxyLeasePayload)},
	})
	handlerPath, err := srv.sessionCredentialFile("sess-alice")
	if err != nil {
		t.Fatalf("sessionCredentialFile: %v", err)
	}
	if handlerPath != alice {
		t.Errorf("credential handlers write %q but the manifest names %q", handlerPath, alice)
	}
}

// spec: §4.7 — a session identifier that is not a safe path segment
// cannot name a credential file inside the slot tree, so the manifest
// write fails closed rather than emitting a path outside it.
func TestWriteSessionManifestRejectsUnsafeSessionCredentialPath_spec_4_7(t *testing.T) {
	dir := t.TempDir()
	srv := &Server{WorkspaceBase: "/workspace", ManifestDir: dir, CredentialsDir: t.TempDir()}
	if _, err := srv.writeSessionManifest(manifestInputs{sessionID: "../escape"}); err == nil {
		t.Fatal("writeSessionManifest accepted a traversal session identifier; want an error")
	}
	if _, err := os.Stat(filepath.Join(dir, ManifestFilename)); err == nil {
		t.Error("a manifest was written for a rejected session identifier")
	}
}
