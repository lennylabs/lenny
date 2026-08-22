// SPDX-License-Identifier: MIT

// Tests for the Full-level battery's fake adapter. The adapter side of
// the §4.7 rotation contract is that the credentials_rotated frame names
// a file the adapter has already written, so the harness that plays that
// side writes the per-session credential file the manifest and the frame
// both name.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// spec: 4.7 (manifest credentialsPath, Full-level rotation protocol),
// 6.1 (per-session credential file)
//
// A harness that names a credential file it never writes lets the
// rotation check pass for a runtime that never opens the file, and makes
// every conforming runtime report a read failure on a check the harness
// records as passing.
func TestFakeAdapterWritesTheCredentialFileItNames(t *testing.T) {
	fa, cleanup, err := newFakeAdapter()
	if err != nil {
		t.Fatalf("newFakeAdapter: %v", err)
	}
	defer cleanup()

	want := filepath.Join(fa.dir, "run", "lenny", "slots", fullSessionID, "credentials.json")
	if fa.credentialsPath != want {
		t.Fatalf("credential path = %q, want the per-session slot path %q", fa.credentialsPath, want)
	}

	manifestBody, err := os.ReadFile(fa.manifest)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest struct {
		CredentialsPath string `json:"credentialsPath"`
	}
	if err := json.Unmarshal(manifestBody, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if manifest.CredentialsPath != fa.credentialsPath {
		t.Fatalf("manifest credentialsPath = %q, want %q", manifest.CredentialsPath, fa.credentialsPath)
	}

	body, err := os.ReadFile(fa.credentialsPath)
	if err != nil {
		t.Fatalf("read the credential file the manifest names: %v", err)
	}
	var bundle struct {
		Mode     string `json:"mode"`
		Provider string `json:"provider"`
		LeaseID  string `json:"leaseId"`
	}
	if err := json.Unmarshal(body, &bundle); err != nil {
		t.Fatalf("decode credential bundle: %v", err)
	}
	if bundle.Provider == "" || bundle.Mode == "" || bundle.LeaseID == "" {
		t.Fatalf("credential bundle = %+v, want a §6.1 bundle naming a mode, a provider, and a lease", bundle)
	}
}
