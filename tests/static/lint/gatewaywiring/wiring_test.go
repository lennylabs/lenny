// SPDX-License-Identifier: MIT

package gatewaywiring

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/static/lint/gatewaylayout"
)

func loadFixture(t *testing.T, main string) *Program {
	t.Helper()
	prog, err := LoadProgram(writeFixture(t, main), "./cmd/lenny-gateway")
	if err != nil {
		t.Fatal(err)
	}
	return prog
}

func writeFixture(t *testing.T, main string) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{"cmd/lenny-gateway/main.go": main}
	for k, v := range fixtureLibs {
		files[k] = v
	}
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func classifier() Layout {
	return gatewaylayout.NewClassifier(gatewaylayout.Layout, gatewaylayout.Register{Version: 1})
}

// TestReconcileAcceptsRegisteredCrossingsAndRefusesTheRest checks the
// register: a registered crossing passes, an unregistered one fails, a row
// with no crossing is stale, and malformed rows fail.
// spec: GW-R1
func TestReconcileAcceptsRegisteredCrossingsAndRefusesTheRest(t *testing.T) {
	c1 := Crossing{To: "pkg/gateway/platformtools/tools", Interface: "Terminal", From: "pkg/gateway/core/session", Type: "Server", Site: "main.go:1"}
	c2 := Crossing{To: "pkg/gateway/platformtools/tools", Interface: "Deps.F", From: "pkg/gateway/core/session", Type: "Hook", Site: "main.go:2"}
	row := Row{To: c1.To, Interface: c1.Interface, From: c1.From, Type: c1.Type, Step: "0117.S7"}
	res := Reconcile([]Crossing{c1}, Register{Version: 1, Crossings: []Row{row}})
	if res.Failed() {
		t.Fatalf("a registered crossing failed: %+v", res)
	}
	if res := Reconcile(nil, Register{Version: 1}); res.Failed() {
		t.Fatalf("an empty register with no crossing failed: %+v", res)
	}
	res = Reconcile([]Crossing{c1, c2}, Register{Version: 1, Crossings: []Row{row}})
	if len(res.Unregistered) != 1 || res.Unregistered[0].Key() != c2.Key() || !res.Failed() {
		t.Fatalf("unregistered = %v", res.Unregistered)
	}
	if !strings.Contains(res.Unregistered[0].String(), "Deps.F receives") {
		t.Errorf("crossing string %q", res.Unregistered[0].String())
	}
	res = Reconcile(nil, Register{Version: 1, Crossings: []Row{row}})
	if len(res.Stale) != 1 || !strings.Contains(res.Stale[0], "no longer makes the crossing") {
		t.Fatalf("stale = %v", res.Stale)
	}
	bad := Register{Version: 2, Crossings: []Row{row, row, {To: "x", Interface: "", From: "y", Type: "T", Step: "S7"}}}
	got := strings.Join(Reconcile([]Crossing{c1}, bad).Malformed, "\n")
	for _, want := range []string{"register version 2, want 1", ": duplicated", "names no target or no value", `step "S7" is not a step identifier`} {
		if !strings.Contains(got, want) {
			t.Errorf("malformed rows lack %q in:\n%s", want, got)
		}
	}
}

// TestRatchetRefusesNewWiringRows checks the merge-base comparison.
// spec: GW-R1
func TestRatchetRefusesNewWiringRows(t *testing.T) {
	base := Register{Version: 1, Crossings: []Row{
		{To: "pkg/gateway/mcpfabric/mcptools", Interface: "Terminal", From: "pkg/gateway/sessionserver", Type: "Server", Step: "0117.S7"},
	}}
	moves := []gatewaylayout.Move{{Old: "pkg/gateway/mcpfabric/mcptools", New: "pkg/gateway/platformtools/mcptools", Step: "0117.S7"}}
	if got := Ratchet(base, Register{Version: 1}, moves); len(got) != 0 {
		t.Errorf("a deletion was refused: %v", got)
	}
	moved := Register{Version: 1, Crossings: []Row{{To: "pkg/gateway/platformtools/mcptools", Interface: "Terminal", From: "pkg/gateway/sessionserver", Type: "Server", Step: "0117.S7"}}}
	if got := Ratchet(base, moved, moves); len(got) != 0 {
		t.Errorf("a row a move rewrote was refused: %v", got)
	}
	added := Register{Version: 1, Crossings: append(base.Crossings, Row{To: "pkg/gateway/x", Interface: "I", From: "pkg/gateway/y", Type: "T", Step: "0117.S11"})}
	if got := Ratchet(base, added, moves); len(got) != 1 || !strings.Contains(got[0], "new since the merge base") {
		t.Errorf("a new row was not refused: %v", got)
	}
	restepped := Register{Version: 1, Crossings: []Row{{To: "pkg/gateway/mcpfabric/mcptools", Interface: "Terminal", From: "pkg/gateway/sessionserver", Type: "Server", Step: "0117.S8"}}}
	if got := Ratchet(base, restepped, moves); len(got) != 1 {
		t.Errorf("a re-stepped row was not refused: %v", got)
	}
}

// TestRegisterParsingRefusesUnknownKeys checks the decoder.
func TestRegisterParsingRefusesUnknownKeys(t *testing.T) {
	if _, err := ParseRegister([]byte("version: 1\ncrossings:\n  - to: a\n    interfce: b\n")); err == nil {
		t.Error("an unknown key parsed")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "r.yaml")
	if err := os.WriteFile(path, []byte("version: 1\ncrossings: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r, err := LoadRegister(path); err != nil || r.Version != 1 {
		t.Errorf("LoadRegister = %+v, %v", r, err)
	}
	if _, err := LoadRegister(filepath.Join(dir, "absent.yaml")); err == nil {
		t.Error("a missing register loaded")
	}
}

// TestLoadRefusesAPackageOutsideAModule checks the loader's error path.
func TestLoadRefusesAPackageOutsideAModule(t *testing.T) {
	if _, err := LoadProgram(t.TempDir(), "./..."); err == nil {
		t.Error("LoadProgram succeeded outside a module")
	}
}

// TestLoadProgramFailsOnACompileErrorAndOnNoGoFiles checks that the check
// refuses to pass when it cannot read the composition root: a compile error
// in the target, and a target with no Go files.
// spec: GW-R1
func TestLoadProgramFailsOnACompileErrorAndOnNoGoFiles(t *testing.T) {
	broken := writeFixture(t, "package main\n\nfunc main() { undefinedCall() }\n")
	if _, err := LoadProgram(broken, "./cmd/lenny-gateway"); err == nil {
		t.Error("a compile error in the target loaded")
	}
	empty := writeFixture(t, "package main\n\nfunc main() {}\n")
	if err := os.Remove(filepath.Join(empty, "cmd", "lenny-gateway", "main.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(empty, "cmd", "lenny-gateway", "main_test.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProgram(empty, "./cmd/lenny-gateway"); err == nil || !strings.Contains(err.Error(), "no Go files") {
		t.Errorf("a target with no Go files: %v", err)
	}
}
