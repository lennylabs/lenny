// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The tests in this file are the guards that decision 7 of change 0117
// (gateway package layout) puts in place before any of its move steps runs.
// Each move step relocates the wiring of the part it moves out of this
// binary: its constructor, the flags only it reads, and its route
// registration. The guards hold what the binary exposes and the order it
// builds, starts, and stops its parts in, so a move that changes either
// fails here and the change is reviewed as a change to the golden file
// rather than slipping through as a side effect of moving code.
//
// They pin the binary's existing surface rather than a requirement, so they
// carry no specification annotation.
//
// To accept a deliberate change, run the tests with LENNY_GUARD_UPDATE=1,
// which rewrites the golden files under testdata/, and review the diff.

// guardUpdateEnv names the environment variable that rewrites the golden
// files instead of comparing against them.
const guardUpdateEnv = "LENNY_GUARD_UPDATE"

// TestGatewayFlagSurfaceIsFixed builds the gateway binary and compares its
// --help output, run in an empty environment so that no default read from
// an environment variable varies with the host, against the golden file. The
// output names every flag, its type, its default, and its help text,
// including the flags that imported packages register on the process-wide
// flag set, so a flag a move drops, renames, re-defaults, or re-documents is
// a difference.
func TestGatewayFlagSurfaceIsFixed(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the gateway binary; skipped in -short mode")
	}
	repo := repoRootDir(t)
	bin := filepath.Join(t.TempDir(), "lenny-gateway")
	build := exec.Command("go", "build", "-o", bin, "./cmd/lenny-gateway")
	build.Dir = repo
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build the gateway binary: %v\n%s", err, out)
	}

	help := exec.Command(bin, "-help")
	help.Env = []string{}
	out, err := help.CombinedOutput()
	if err != nil {
		t.Fatalf("run the gateway binary with -help: %v\n%s", err, out)
	}
	got := normalizeHelp(string(out), bin)
	compareGolden(t, filepath.Join("testdata", "flag-surface.golden"), got)
}

// normalizeHelp replaces the binary's temporary path in the usage line with
// its name, so the output does not depend on where the test built it.
func normalizeHelp(help, bin string) string {
	return strings.ReplaceAll(help, bin, "lenny-gateway")
}

// TestGatewayStartupAndShutdownOrderIsFixed compares the order in which the
// composition root builds, starts, and stops the gateway's parts against the
// golden file. It reads the order from the source of runGateway and
// runServers: every call on the wiring accumulator `w`, and every go and
// defer statement, in source order. A deferred call runs in the reverse of
// the order listed. It also lists the final shutdown closes in the order
// finalShutdownSteps returns them, which is the order they run in.
func TestGatewayStartupAndShutdownOrderIsFixed(t *testing.T) {
	var b strings.Builder
	for _, fn := range []struct{ file, name string }{
		{"main.go", "runGateway"},
		{"runserver.go", "runServers"},
	} {
		steps, err := compositionSteps(fn.file, fn.name)
		if err != nil {
			t.Fatal(err)
		}
		b.WriteString(fn.name + ":\n")
		for _, s := range steps {
			b.WriteString("  " + s + "\n")
		}
	}
	b.WriteString("finalShutdownSteps:\n")
	w := &gatewayWiring{}
	for _, step := range w.finalShutdownSteps() {
		b.WriteString("  " + step.name + "\n")
	}
	compareGolden(t, filepath.Join("testdata", "startup-order.golden"), b.String())
}

// TestCompositionStepsListsAccumulatorCallsDefersAndGoroutinesInOrder pins
// the extraction the order guard relies on, over a fixed source: calls on
// the accumulator at any depth, deferred and spawned calls rendered with the
// accumulator calls inside a function literal, and calls on anything other
// than the accumulator left out.
func TestCompositionStepsListsAccumulatorCallsDefersAndGoroutinesInOrder(t *testing.T) {
	src := `package main

func root(w *wiring) {
	w.buildA()
	x := w.buildB(other())
	defer func() { w.conn.Close() }()
	if x != nil {
		w.startC()
	}
	go w.serve()
	defer cancel()
	log(x)
	w.stop(nil).run()
}
`
	got, err := stepsFromSource(src, "root")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"w.buildA",
		"w.buildB",
		"defer func{w.conn.Close}",
		"w.conn.Close",
		"w.startC",
		"go w.serve",
		"w.serve",
		"defer cancel",
		"w.stop(nil).run",
		"w.stop",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("steps:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if _, err := stepsFromSource(src, "absent"); err == nil {
		t.Error("a function the source does not declare returned no error")
	}
}

// TestCompareGoldenReportsTheFirstDifferingLine pins the failure message the
// guards print, so a reviewer sees which flag or step changed.
func TestCompareGoldenReportsTheFirstDifferingLine(t *testing.T) {
	if msg := goldenDiff("a\nb\nc\n", "a\nb\nc\n"); msg != "" {
		t.Errorf("equal texts reported a difference: %s", msg)
	}
	msg := goldenDiff("a\nb\nc\n", "a\nx\nc\n")
	if !strings.Contains(msg, "line 2") || !strings.Contains(msg, `"b"`) || !strings.Contains(msg, `"x"`) {
		t.Errorf("difference message does not name line 2 and both texts: %s", msg)
	}
	msg = goldenDiff("a\n", "a\nb\n")
	if !strings.Contains(msg, "line 2") {
		t.Errorf("an added line was not reported at line 2: %s", msg)
	}
}

// compositionSteps parses one file of this package and returns the steps of
// the named function.
func compositionSteps(file, name string) ([]string, error) {
	src, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	return stepsFromSource(string(src), name)
}

// stepsFromSource returns, in source order, every call on the accumulator
// `w` in the named function's body and every go and defer statement. A go
// or defer statement whose call is a function literal is rendered with the
// accumulator calls inside the literal.
func stepsFromSource(src, name string) ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "src.go", src, 0)
	if err != nil {
		return nil, err
	}
	var body *ast.BlockStmt
	for _, decl := range file.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok && fd.Name.Name == name && fd.Body != nil {
			body = fd.Body
		}
	}
	if body == nil {
		return nil, &os.PathError{Op: "find function", Path: name, Err: os.ErrNotExist}
	}
	var steps []string
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.DeferStmt:
			steps = append(steps, "defer "+renderCall(n.Call))
		case *ast.GoStmt:
			steps = append(steps, "go "+renderCall(n.Call))
		case *ast.CallExpr:
			if rootedAtAccumulator(n.Fun) {
				steps = append(steps, types.ExprString(n.Fun))
			}
		}
		return true
	})
	return steps, nil
}

// renderCall renders a deferred or spawned call: the callee for a named
// call, and the accumulator calls inside the body for a function literal.
func renderCall(call *ast.CallExpr) string {
	lit, ok := call.Fun.(*ast.FuncLit)
	if !ok {
		return types.ExprString(call.Fun)
	}
	var inner []string
	ast.Inspect(lit.Body, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok && rootedAtAccumulator(c.Fun) {
			inner = append(inner, types.ExprString(c.Fun))
		}
		return true
	})
	return "func{" + strings.Join(inner, ", ") + "}"
}

// rootedAtAccumulator reports whether a callee expression is a selector
// chain whose root is the identifier `w`, through any intervening calls.
func rootedAtAccumulator(e ast.Expr) bool {
	for {
		switch x := e.(type) {
		case *ast.SelectorExpr:
			e = x.X
		case *ast.CallExpr:
			e = x.Fun
		case *ast.Ident:
			return x.Name == "w"
		default:
			return false
		}
	}
}

// compareGolden compares got against the golden file, or rewrites the file
// when guardUpdateEnv is set.
func compareGolden(t *testing.T, path, got string) {
	t.Helper()
	if os.Getenv(guardUpdateEnv) == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden file %s: %v (run with %s=1 to create it)", path, err, guardUpdateEnv)
	}
	if msg := goldenDiff(string(want), got); msg != "" {
		t.Errorf("%s no longer matches: %s\nWhen the change is deliberate, rerun with %s=1 and review the diff of %s.", path, msg, guardUpdateEnv, path)
	}
}

// goldenDiff returns a description of the first line at which got differs
// from want, or "" when they are equal.
func goldenDiff(want, got string) string {
	if want == got {
		return ""
	}
	w := strings.Split(want, "\n")
	g := strings.Split(got, "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl || i >= len(w) || i >= len(g) {
			return fmt.Sprintf("first difference at line %d: golden %q, now %q", i+1, wl, gl)
		}
	}
	return "the texts differ"
}

// repoRootDir returns the repository root from this package's directory.
func repoRootDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		t.Fatalf("repository root %s has no go.mod: %v", dir, err)
	}
	return dir
}
