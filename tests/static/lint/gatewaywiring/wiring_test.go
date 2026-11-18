// SPDX-License-Identifier: MIT

package gatewaywiring

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/static/lint/gatewaylayout"
)

const fixtureModule = "example.com/fix"

var fixtureLibs = map[string]string{
	"go.mod": "module " + fixtureModule + "\n\ngo 1.21\n",
	"pkg/gateway/seam/seam.go": `package seam

type Stamper interface{ Stamp(id string) }
`,
	"pkg/gateway/platformtools/tools/tools.go": `package tools

import (
	"example.com/fix/pkg/gateway/quota/quotastore"
	"example.com/fix/pkg/gateway/seam"
)

type Terminal interface{ Begin(id string) }

type (
	Returned interface{ Begin(id string) }
	Declared interface{ Begin(id string) }
	Paired   interface{ Begin(id string) }
	Mapped   interface{ Begin(id string) }
	Listed   interface{ Begin(id string) }
	Sent     interface{ Begin(id string) }
)

type Pair struct {
	P    Paired
	Name string
}

type Deps struct {
	T     Terminal
	Seam  seam.Stamper
	F     func(id string)
	Iface interface{ Begin(id string) }
	Name  string
	Quota *quotastore.Store
}

func New(t Terminal)              {}
func NewMany(ts ...Terminal)      {}
func Stamped(s seam.Stamper)      {}

type Svc struct{}

func (s *Svc) Set(f func(id string)) {}
`,
	"pkg/gateway/quota/quotastore/store.go": `package quotastore

type Store struct{}
`,
	"pkg/gateway/core/session/session.go": `package session

type Server struct{}

func (s *Server) Begin(id string) {}
func (s *Server) Stamp(id string) {}
func (s *Server) Observe()         {}

func Hook(id string) {}

type Observer interface{ Observe() }

func Watch(o Observer) {}
`,
	"pkg/gateway/replica/metrics/metrics.go": `package metrics

type Metrics struct{}

func (Metrics) Observe() {}
`,
}

const crossingMain = `package main

import (
	"example.com/fix/pkg/gateway/core/session"
	"example.com/fix/pkg/gateway/platformtools/tools"
	"example.com/fix/pkg/gateway/quota/quotastore"
)

type adapter struct{}

func returned(srv *session.Server) tools.Returned { return srv }

func notACrossing() (tools.Returned, error) { return adapter{}, nil }

func (adapter) Begin(id string) {}

type holder struct{ s *session.Server }

func (h holder) Begin(id string) { h.s.Begin(id) }

type wiring struct {
	srv   *session.Server
	count int
}

func (w *wiring) hook(id string) { w.srv.Begin(id) }

func (w *wiring) idle(id string) { w.count++ }

func main() {
	srv := &session.Server{}
	tools.New(srv)
	_ = tools.Deps{T: srv, Seam: srv, F: srv.Begin, Iface: srv, Name: "x"}
	d := &tools.Deps{}
	d.F = session.Hook
	d.F = func(id string) {}
	tools.NewMany(srv, srv)
	s := &tools.Svc{}
	s.Set(srv.Begin)
	tools.New(adapter{})
	tools.Stamped(srv)
	_ = string(rune(65))
	_ = returned(srv)
	var declared tools.Declared = srv
	_ = declared
	_ = tools.Pair{srv, "x"}
	_ = map[string]tools.Mapped{"a": srv}
	_ = []tools.Listed{srv}
	var _ = session.Observer(nil)
	ch := make(chan tools.Sent, 1)
	ch <- srv
	_ = tools.Deps{Quota: &quotastore.Store{}}
	_ = func() tools.Returned { return adapter{} }
	tools.New(holder{s: srv})
	d.F = func(id string) { srv.Begin(id) }
	wr := &wiring{srv: srv}
	d.F = wr.hook
	d.F = wr.idle
	session.Watch(srv)
}
`

const cleanMain = `package main

import (
	"example.com/fix/pkg/gateway/core/session"
	"example.com/fix/pkg/gateway/platformtools/tools"
	"example.com/fix/pkg/gateway/replica/metrics"
)

type empty struct{}

func (empty) Begin(id string) {}

func main() {
	srv := &session.Server{}
	tools.Stamped(srv)
	session.Watch(metrics.Metrics{})
	_ = tools.Deps{Seam: srv, F: func(id string) {}}
	tools.New(empty{})
}
`

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

// TestCrossingsFindsEachWiringFormOfACrossing checks a composition root
// that wires a session core value into the platform tools seam's own types
// in every form the check reads: a named interface parameter, a variadic
// parameter, struct literal fields of a named interface, an unnamed
// interface, and a function type given a method value, a field assignment
// of a package function, an unnamed function parameter, a return statement
// in a function and in a function literal, a typed variable declaration, a
// positional struct literal, map and slice literal elements, and a channel
// send; an adapter type of the composition root by the values its fields
// hold, a function literal and a method value of the composition root by
// what its body calls and captures; and a value
// of a confined package handed to the seam. The seam interface, an adapter
// whose fields hold no part's value, a function literal that reaches no
// part, and a core value given to the core's own interface are not
// crossings.
// spec: GW-R1, GW-R2
func TestCrossingsFindsEachWiringFormOfACrossing(t *testing.T) {
	prog := loadFixture(t, crossingMain)
	got := Crossings(prog, classifier())
	var keys []string
	for _, c := range got {
		keys = append(keys, c.To+" "+c.Interface+" <- "+c.From+" "+c.Type)
		if c.Site == "" || !strings.HasPrefix(c.Site, "main.go:") {
			t.Errorf("crossing %v has no site in main.go", c)
		}
	}
	want := []string{
		"pkg/gateway/platformtools/tools Declared <- pkg/gateway/core/session Server",
		"pkg/gateway/platformtools/tools Deps.F <- pkg/gateway/core/session Hook",
		"pkg/gateway/platformtools/tools Deps.F <- pkg/gateway/core/session Server.Begin",
		"pkg/gateway/platformtools/tools Deps.F <- pkg/gateway/core/session func(Server)",
		"pkg/gateway/platformtools/tools Deps.F <- pkg/gateway/core/session func(Server.Begin)",
		"pkg/gateway/platformtools/tools Deps.F <- pkg/gateway/core/session wiring.hook(Server.Begin)",
		"pkg/gateway/platformtools/tools Deps.Iface <- pkg/gateway/core/session Server",
		"pkg/gateway/platformtools/tools Deps.Quota <- pkg/gateway/quota/quotastore Store",
		"pkg/gateway/platformtools/tools Listed <- pkg/gateway/core/session Server",
		"pkg/gateway/platformtools/tools Mapped <- pkg/gateway/core/session Server",
		"pkg/gateway/platformtools/tools Paired <- pkg/gateway/core/session Server",
		"pkg/gateway/platformtools/tools Returned <- pkg/gateway/core/session Server",
		"pkg/gateway/platformtools/tools Sent <- pkg/gateway/core/session Server",
		"pkg/gateway/platformtools/tools Svc.Set(0) <- pkg/gateway/core/session Server.Begin",
		"pkg/gateway/platformtools/tools Terminal <- pkg/gateway/core/session Server",
		"pkg/gateway/platformtools/tools Terminal <- pkg/gateway/core/session holder(Server)",
	}
	if strings.Join(keys, "\n") != strings.Join(want, "\n") {
		t.Errorf("crossings:\n%s\nwant:\n%s", strings.Join(keys, "\n"), strings.Join(want, "\n"))
	}
}

// TestCrossingsIsEmptyWhenTheRootWiresOnlyThroughTheSeamOrItsOwnAdapters
// checks the passing case: values given to the seam interface, a replica
// value given to the session core, which may import it, and an adapter and
// a function literal that reach no part.
// spec: GW-R1
func TestCrossingsIsEmptyWhenTheRootWiresOnlyThroughTheSeamOrItsOwnAdapters(t *testing.T) {
	if got := Crossings(loadFixture(t, cleanMain), classifier()); len(got) != 0 {
		t.Errorf("a composition root wired through the seam has crossings: %v", got)
	}
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
