// SPDX-License-Identifier: MIT

package credfile_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter/credfile"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

func lease(id, provider string, expiresMs int64, payload string) *adapterv1.CredentialLease {
	return &adapterv1.CredentialLease{
		LeaseId:         id,
		Provider:        provider,
		ExpiresAtUnixMs: expiresMs,
		Payload:         []byte(payload),
	}
}

// readDoc reads and decodes the credential file written into dir.
func readDoc(t *testing.T, dir string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, credfile.FileName))
	if err != nil {
		t.Fatalf("read credential file: %v", err)
	}
	var doc struct {
		Providers []map[string]any `json:"providers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("credential file is not valid JSON: %v", err)
	}
	return doc.Providers
}

func TestWriteProducesAProvidersDocument(t *testing.T) {
	dir := t.TempDir()
	err := credfile.Write(dir, []*adapterv1.CredentialLease{
		lease("lease_a", "anthropic_direct", 1_700_000_000_000,
			`{"deliveryMode":"direct","materializedConfig":{"apiKey":"sk-x"}}`),
		lease("lease_b", "openai_direct", 0,
			`{"deliveryMode":"proxy","materializedConfig":{"proxyUrl":"https://p/v1"}}`),
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	providers := readDoc(t, dir)
	if len(providers) != 2 {
		t.Fatalf("providers count = %d, want 2", len(providers))
	}
	first := providers[0]
	if first["leaseId"] != "lease_a" || first["provider"] != "anthropic_direct" {
		t.Errorf("first entry = %v, want lease_a / anthropic_direct", first)
	}
	if first["deliveryMode"] != "direct" {
		t.Errorf("first entry deliveryMode = %v, want direct (merged from payload)", first["deliveryMode"])
	}
	if first["expiresAt"] != "2023-11-14T22:13:20Z" {
		t.Errorf("first entry expiresAt = %v, want the RFC3339 form of the unix ms", first["expiresAt"])
	}
	if _, ok := first["materializedConfig"].(map[string]any); !ok {
		t.Errorf("first entry materializedConfig = %v, want a nested object", first["materializedConfig"])
	}
}

func TestWriteOmitsZeroExpiry(t *testing.T) {
	dir := t.TempDir()
	if err := credfile.Write(dir, []*adapterv1.CredentialLease{
		lease("lease_b", "openai_direct", 0, `{"deliveryMode":"direct"}`),
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, present := readDoc(t, dir)[0]["expiresAt"]; present {
		t.Error("expiresAt was written for a lease with no expiry")
	}
}

func TestWriteEmptyLeasesProducesAnEmptyArray(t *testing.T) {
	dir := t.TempDir()
	if err := credfile.Write(dir, nil); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if providers := readDoc(t, dir); len(providers) != 0 {
		t.Errorf("providers = %v, want an empty array", providers)
	}
}

func TestWriteSetsRestrictiveMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file mode is not meaningful on Windows")
	}
	dir := t.TempDir()
	if err := credfile.Write(dir, nil); err != nil {
		t.Fatalf("Write: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, credfile.FileName))
	if err != nil {
		t.Fatalf("stat credential file: %v", err)
	}
	if info.Mode().Perm() != credfile.FileMode {
		t.Errorf("credential file mode = %o, want %o", info.Mode().Perm(), credfile.FileMode)
	}
}

func TestWriteReplacesAnExistingFile(t *testing.T) {
	dir := t.TempDir()
	if err := credfile.Write(dir, []*adapterv1.CredentialLease{
		lease("old", "anthropic_direct", 0, `{}`),
	}); err != nil {
		t.Fatalf("first Write: %v", err)
	}
	if err := credfile.Write(dir, []*adapterv1.CredentialLease{
		lease("new", "openai_direct", 0, `{}`),
	}); err != nil {
		t.Fatalf("second Write: %v", err)
	}
	providers := readDoc(t, dir)
	if len(providers) != 1 || providers[0]["leaseId"] != "new" {
		t.Errorf("providers = %v, want only the second write's lease", providers)
	}
}

func TestWriteRejectsANonObjectPayload(t *testing.T) {
	dir := t.TempDir()
	err := credfile.Write(dir, []*adapterv1.CredentialLease{
		lease("lease_bad", "anthropic_direct", 0, `[1,2,3]`),
	})
	if err == nil {
		t.Error("Write accepted a lease whose payload is a JSON array, want an error")
	}
}

func TestWriteFailsWhenTheDirectoryIsMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-dir")
	if err := credfile.Write(missing, nil); err == nil {
		t.Error("Write succeeded against a missing directory, want an error")
	}
}
