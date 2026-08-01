//go:build contract

// SPDX-License-Identifier: MIT

// Package residualregister_test is the Tier 3 contract suite for the
// residual register file format. A residual register is a contract
// between two writers and two readers: the tier-0 residual gate writes
// and reads one register per class, and the specification migration
// passes read the same files through
// scripts/specshift/register. A second loader with an entry schema or a
// kind declaration of its own would accept files the other refuses, and
// nothing else in the tree would fail.
//
// This suite loads every tracked register through the one exported
// loader and holds it to the declared kind, the declared version, and
// the per-entry schema, so a register the gate writes stays a register a
// pass reads.
//
// These cases carry no spec annotation: the residual registers are
// migration tooling for the repository's own records rather than a
// platform behavior.
package residualregister_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/register"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// registerDir is where every class's residual register is tracked.
const registerDir = "tests/registers"

// trackedRegisters returns the absolute path of every residual register
// in the tree.
func trackedRegisters(t *testing.T) []string {
	t.Helper()
	dir := filepath.Join(schematest.RepoRoot(t), filepath.FromSlash(registerDir))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", registerDir, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "residual-") || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		out = append(out, filepath.Join(dir, e.Name()))
	}
	if len(out) == 0 {
		t.Fatalf("no residual register is tracked under %s, so this suite pins nothing", registerDir)
	}
	return out
}

// diagnosis: a tracked residual register no longer loads through the
// shared loader. Either a register was written by a second
// implementation with a kind or an entry schema of its own, or the
// shared loader's schema changed without the tracked files moving with
// it. The gate that writes these files and the passes that read them
// have diverged.
func TestEveryTrackedResidualRegisterLoadsThroughTheSharedLoader(t *testing.T) {
	t.Parallel()
	for _, path := range trackedRegisters(t) {
		rel := filepath.Base(path)
		t.Run(rel, func(t *testing.T) {
			t.Parallel()
			doc, err := register.Load(path)
			if err != nil {
				t.Fatalf("load %s through the shared residual-register loader: %v", rel, err)
			}
			if doc.Kind != register.Kind {
				t.Errorf("%s declares kind %q, and the shared loader reads %q", rel, doc.Kind, register.Kind)
			}
			if doc.Version != register.Version {
				t.Errorf("%s declares version %d, and the shared loader reads %d", rel, doc.Version, register.Version)
			}
			// The class a register declares is the class its filename
			// names, so a register cannot be read for one class while
			// carrying another's population.
			wantClass := strings.TrimSuffix(strings.TrimPrefix(rel, "residual-"), ".yaml")
			if doc.Class != wantClass {
				t.Errorf("%s declares class %q, and its path names %q", rel, doc.Class, wantClass)
			}
			if _, err := register.ReadFor(os.ReadFile, path, wantClass); err != nil {
				t.Errorf("read %s for the class its path names: %v", rel, err)
			}
			// Every entry is held to the one entry schema, which is what
			// makes an entry one side writes readable by the other.
			for _, e := range *doc.Entries {
				if err := register.ValidEntry(rel, doc.Class, e); err != nil {
					t.Errorf("%s: %v", rel, err)
				}
				if e.Disposition != register.InClass && e.Disposition != register.Excluded {
					t.Errorf("%s: member %q carries disposition %q", rel, e.Key(), e.Disposition)
				}
			}
		})
	}
}

// diagnosis: a register the shared writer produced does not reload
// through the shared loader, so the two halves of the one contract
// disagree about the format they exchange.
func TestTheSharedWriterProducesARegisterTheSharedLoaderReads(t *testing.T) {
	t.Parallel()
	entries := []register.Entry{{
		Member:      "pkg/carrier/carrier.go",
		Class:       "generated-artifacts",
		Disposition: register.InClass,
		Reason:      "producer output that carries no marker",
	}}
	path := filepath.Join(t.TempDir(), "residual-generated-artifacts.yaml")
	if err := register.Write(func(target string, content []byte) error {
		return os.WriteFile(target, content, 0o644)
	}, path, "generated-artifacts", entries); err != nil {
		t.Fatalf("write a register through the shared writer: %v", err)
	}
	doc, err := register.ReadFor(os.ReadFile, path, "generated-artifacts")
	if err != nil {
		t.Fatalf("reload the written register through the shared loader: %v", err)
	}
	if got := doc.Members(); len(got) != 1 || got[0] != "pkg/carrier/carrier.go" {
		t.Fatalf("the reloaded register carries %v", got)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the written register: %v", err)
	}
	if !strings.Contains(string(body), "kind: "+register.Kind+"\n") {
		t.Errorf("the written register declares no %q kind:\n%s", register.Kind, body)
	}
}
