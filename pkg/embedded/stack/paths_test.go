// SPDX-License-Identifier: MIT

package stack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultRootHonoursLennyHome(t *testing.T) {
	t.Setenv("LENNY_HOME", "/tmp/lenny-home-test")
	root, err := DefaultRoot()
	if err != nil {
		t.Fatalf("DefaultRoot: %v", err)
	}
	if root != "/tmp/lenny-home-test" {
		t.Errorf("DefaultRoot = %q, want the LENNY_HOME override", root)
	}
}

func TestDefaultRootFallsBackToHome(t *testing.T) {
	t.Setenv("LENNY_HOME", "")
	root, err := DefaultRoot()
	if err != nil {
		t.Fatalf("DefaultRoot: %v", err)
	}
	// §17.4: ~/.lenny is the sole state directory.
	if !strings.HasSuffix(root, ".lenny") {
		t.Errorf("DefaultRoot = %q, want a path ending in .lenny", root)
	}
}

func TestNewPathsLayout(t *testing.T) {
	p := NewPaths("/state")
	cases := map[string]string{
		"Postgres":  p.Postgres,
		"K3s":       p.K3s,
		"KMS":       p.KMS,
		"OIDC":      p.OIDC,
		"Artifacts": p.Artifacts,
		"TLS":       p.TLS,
		"Logs":      p.Logs,
		"Run":       p.Run,
	}
	for name, got := range cases {
		if !strings.HasPrefix(got, "/state/") {
			t.Errorf("%s path %q is not under the root", name, got)
		}
	}
	if p.StateFile() != "/state/run/stack.json" {
		t.Errorf("StateFile = %q", p.StateFile())
	}
	if p.KMSMasterKey() != "/state/kms/master.key" {
		t.Errorf("KMSMasterKey = %q", p.KMSMasterKey())
	}
	if p.OIDCKeyFile() != "/state/oidc/signing.key" {
		t.Errorf("OIDCKeyFile = %q", p.OIDCKeyFile())
	}
}

func TestEnsureDirsCreatesLayout(t *testing.T) {
	root := t.TempDir()
	p := NewPaths(filepath.Join(root, "state"))
	if err := p.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	for _, d := range []string{p.Postgres, p.K3s, p.KMS, p.OIDC, p.Artifacts, p.TLS, p.Logs, p.Run} {
		fi, err := os.Stat(d)
		if err != nil {
			t.Errorf("expected %s to exist: %v", d, err)
			continue
		}
		if !fi.IsDir() {
			t.Errorf("%s is not a directory", d)
		}
	}
	// EnsureDirs is idempotent.
	if err := p.EnsureDirs(); err != nil {
		t.Fatalf("second EnsureDirs: %v", err)
	}
}
