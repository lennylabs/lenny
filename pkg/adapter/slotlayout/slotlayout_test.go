// SPDX-License-Identifier: MIT

package slotlayout

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// prodRoots mirrors the production base directories so the derived paths
// can be asserted against the literal §6.4 / §6.1 spec patterns.
func prodRoots() Roots {
	return Roots{
		Workspace:   "/workspace",
		Sessions:    "/sessions",
		Artifacts:   "/artifacts",
		Credentials: "/run/lenny",
	}
}

func TestResolveMatchesSpecPaths_spec_6_4(t *testing.T) {
	p, err := Resolve(prodRoots(), "sess_abc")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	cases := map[string]string{
		"current":          "/workspace/slots/sess_abc/current",
		"staging":          "/workspace/slots/sess_abc/staging",
		"sessions":         "/sessions/sess_abc",
		"artifacts":        "/artifacts/sess_abc",
		"credentials dir":  "/run/lenny/slots/sess_abc",
		"credentials file": "/run/lenny/slots/sess_abc/credentials.json",
	}
	got := map[string]string{
		"current":          p.Current,
		"staging":          p.Staging,
		"sessions":         p.Sessions,
		"artifacts":        p.Artifacts,
		"credentials dir":  p.CredentialsDir,
		"credentials file": p.CredentialsFile,
	}
	for name, want := range cases {
		if got[name] != want {
			t.Errorf("%s = %q, want %q", name, got[name], want)
		}
	}
}

// spec: §6.1 line 28 — the per-slot credential file replaces the single
// global /run/lenny/credentials.json so a rotation on one slot does not
// rewrite a sibling's file.
func TestResolveCredentialFileIsPerSlot_spec_6_1(t *testing.T) {
	a, _ := Resolve(prodRoots(), "slot-a")
	b, _ := Resolve(prodRoots(), "slot-b")
	if a.CredentialsFile == b.CredentialsFile {
		t.Fatalf("two slots resolved to the same credential file %q", a.CredentialsFile)
	}
	if a.CredentialsFile == "/run/lenny/credentials.json" {
		t.Fatalf("per-slot credential file collapsed to the global path")
	}
}

func TestResolveEmptyRootSkipsPath_spec_6_4(t *testing.T) {
	p, err := Resolve(Roots{Workspace: "/workspace"}, "s1")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if p.Current == "" {
		t.Errorf("workspace root set but Current empty")
	}
	if p.Sessions != "" || p.Artifacts != "" || p.CredentialsFile != "" {
		t.Errorf("unconfigured roots should yield empty paths, got sessions=%q artifacts=%q cred=%q",
			p.Sessions, p.Artifacts, p.CredentialsFile)
	}
}

func TestValidateSlotIDRejectsTraversal_spec_6_4(t *testing.T) {
	bad := []string{"", ".", "..", "a/b", "../escape", "a/../b", "with\x00nul"}
	if runtime.GOOS == "windows" {
		bad = append(bad, `a\b`)
	}
	for _, id := range bad {
		if err := ValidateSlotID(id); err == nil {
			t.Errorf("ValidateSlotID(%q) = nil, want rejection", id)
		}
		// A rejected id must not resolve into a path either.
		if _, err := Resolve(prodRoots(), id); err == nil {
			t.Errorf("Resolve with bad slot id %q = nil error, want rejection", id)
		}
	}
}

func TestValidateSlotIDAcceptsSafeSegments_spec_6_4(t *testing.T) {
	for _, id := range []string{"sess_abc", "s-1", "01HXYZ", "a.b", "UUIDv8-style-id"} {
		if err := ValidateSlotID(id); err != nil {
			t.Errorf("ValidateSlotID(%q) = %v, want nil", id, err)
		}
	}
}

func tmpRoots(t *testing.T) Roots {
	t.Helper()
	base := t.TempDir()
	return Roots{
		Workspace:   filepath.Join(base, "workspace"),
		Sessions:    filepath.Join(base, "sessions"),
		Artifacts:   filepath.Join(base, "artifacts"),
		Credentials: filepath.Join(base, "run", "lenny"),
	}
}

func TestEnsureTreeCreatesAllDirs_spec_6_4(t *testing.T) {
	roots := tmpRoots(t)
	p, err := Resolve(roots, "slot1")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if err := EnsureTree(p); err != nil {
		t.Fatalf("EnsureTree: %v", err)
	}
	for _, d := range []string{p.Current, p.Staging, p.Sessions, p.Artifacts, p.CredentialsDir} {
		info, err := os.Stat(d)
		if err != nil {
			t.Fatalf("expected %q to exist: %v", d, err)
		}
		if !info.IsDir() {
			t.Errorf("%q is not a directory", d)
		}
	}
}

// EnsureTree pins the exact mode so the inherited umask cannot strip the
// runtime/group bits. spec: §6.1 line 28 (credential dir) / §6.1 line 11.
func TestEnsureTreeSetsExactModes_spec_6_4(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes not meaningful on windows")
	}
	roots := tmpRoots(t)
	p, _ := Resolve(roots, "slot1")
	if err := EnsureTree(p); err != nil {
		t.Fatalf("EnsureTree: %v", err)
	}
	want := map[string]os.FileMode{
		p.Current:        CurrentMode,
		p.Staging:        StagingMode,
		p.CredentialsDir: CredentialsDirMode,
	}
	for dir, mode := range want {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("stat %q: %v", dir, err)
		}
		if info.Mode().Perm() != mode {
			t.Errorf("%q mode = %o, want %o", dir, info.Mode().Perm(), mode)
		}
	}
}

func TestEnsureTreeIsIdempotent_spec_6_4(t *testing.T) {
	roots := tmpRoots(t)
	p, _ := Resolve(roots, "slot1")
	if err := EnsureTree(p); err != nil {
		t.Fatalf("EnsureTree (first): %v", err)
	}
	// Drop a file into the current dir; a second EnsureTree must not wipe it.
	canary := filepath.Join(p.Current, "keep.txt")
	if err := os.WriteFile(canary, []byte("x"), 0o644); err != nil {
		t.Fatalf("write canary: %v", err)
	}
	if err := EnsureTree(p); err != nil {
		t.Fatalf("EnsureTree (second): %v", err)
	}
	if _, err := os.Stat(canary); err != nil {
		t.Errorf("idempotent EnsureTree removed existing content: %v", err)
	}
}

func TestRemoveTreeDropsAllDirs_spec_6_4(t *testing.T) {
	roots := tmpRoots(t)
	p, _ := Resolve(roots, "slot1")
	if err := EnsureTree(p); err != nil {
		t.Fatalf("EnsureTree: %v", err)
	}
	// Stage content so removal proves it drops a populated tree.
	if err := os.WriteFile(filepath.Join(p.Current, "f"), []byte("x"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.WriteFile(p.CredentialsFile, []byte("{}"), 0o440); err != nil {
		t.Fatalf("seed cred: %v", err)
	}
	if err := RemoveTree(p); err != nil {
		t.Fatalf("RemoveTree: %v", err)
	}
	for _, d := range []string{p.slotRoot(), p.Sessions, p.Artifacts, p.CredentialsDir} {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Errorf("expected %q removed, stat err = %v", d, err)
		}
	}
}

func TestRemoveTreeMissingIsNoError_spec_6_4(t *testing.T) {
	roots := tmpRoots(t)
	p, _ := Resolve(roots, "never-created")
	if err := RemoveTree(p); err != nil {
		t.Errorf("RemoveTree on absent tree = %v, want nil", err)
	}
}
