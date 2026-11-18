// SPDX-License-Identifier: MIT

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// This file holds the order guard of decision 7 of change 0117; the flag
// guard and the golden-file helpers are in composition_guard_test.go. The
// guard pins the binary's existing order rather than a requirement, so it
// carries no specification annotation.

// TestGatewayStartupAndShutdownOrderIsFixed compares the order in which the
// composition root builds, starts, and stops the gateway's parts against the
// golden file. It reads the order from the source:
//
//   - runGateway and runServers: every call on the wiring accumulator `w`
//     and every go, defer, and range statement over the accumulator, in
//     source order, descending into each build and start method of the
//     accumulator they call, so the goroutines the start workers spawn are
//     listed under the step that spawns them;
//   - shutdownSequence.run: the phases of the post-signal shutdown;
//   - finalShutdownSteps: the final closes in the order they run.
//
// A deferred call runs in the reverse of the order listed.
func TestGatewayStartupAndShutdownOrderIsFixed(t *testing.T) {
	ix, err := parsePackageFuncs(".")
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, fn := range []struct{ key, recv, recvType string }{
		{"runGateway", "w", "gatewayWiring"},
		{"gatewayWiring.runServers", "", ""},
		{"shutdownSequence.run", "", ""},
	} {
		steps, err := ix.steps(fn.key, fn.recv, fn.recvType)
		if err != nil {
			t.Fatal(err)
		}
		b.WriteString(fn.key + ":\n")
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

// TestCompositionStepsDescendIntoBuildAndStartMethods pins the extraction
// the order guard relies on, over a fixed source: calls on the receiver at
// any depth, deferred and spawned calls rendered with the receiver calls
// inside a function literal, range statements over the receiver, descent
// into the receiver's build and start methods with the receiver they
// declare, no descent into other methods or twice into one method, and
// calls on anything other than the receiver left out.
func TestCompositionStepsDescendIntoBuildAndStartMethods(t *testing.T) {
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

func (v *wiring) buildA() {
	v.store.Open()
	go v.loop()
	v.helper()
}

func (v *wiring) startC() {
	for _, s := range v.steps {
		s.run()
	}
	v.buildA()
}

func (v *wiring) helper() { v.deep() }
`
	ix, err := parseSourceFuncs(src)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ix.steps("root", "w", "wiring")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"w.buildA",
		"  v.store.Open",
		"  go v.loop",
		"  v.loop",
		"  v.helper",
		"w.buildB",
		"defer func{w.conn.Close}",
		"w.conn.Close",
		"w.startC",
		"  range v.steps",
		"  v.buildA (listed above)",
		"go w.serve",
		"w.serve",
		"defer cancel",
		"w.stop(nil).run",
		"w.stop",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("steps:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if _, err := ix.steps("absent", "w", "wiring"); err == nil {
		t.Error("a function the source does not declare returned no error")
	}
	method, err := ix.steps("wiring.startC", "", "")
	if err != nil || len(method) == 0 || method[0] != "range v.steps" {
		t.Errorf("a method looked up by its receiver type: %v, %v", method, err)
	}
	if _, err := parseSourceFuncs("package main\nfunc ("); err == nil {
		t.Error("a malformed source parsed")
	}
}

// funcIndex holds the functions of a package by name, and the methods by
// "<receiver type>.<name>".
type funcIndex map[string]*ast.FuncDecl

// parsePackageFuncs indexes the functions of the non-test Go files in dir.
func parsePackageFuncs(dir string) (funcIndex, error) {
	names, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	ix := funcIndex{}
	fset := token.NewFileSet()
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			return nil, err
		}
		file, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			return nil, err
		}
		ix.add(file)
	}
	return ix, nil
}

// parseSourceFuncs indexes the functions of one source text.
func parseSourceFuncs(src string) (funcIndex, error) {
	file, err := parser.ParseFile(token.NewFileSet(), "src.go", src, 0)
	if err != nil {
		return nil, err
	}
	ix := funcIndex{}
	ix.add(file)
	return ix, nil
}

func (ix funcIndex) add(file *ast.File) {
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		key := fd.Name.Name
		if t := receiverType(fd); t != "" {
			key = t + "." + key
		}
		ix[key] = fd
	}
}

// receiverType returns the type name of a method's receiver, or "".
func receiverType(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return ""
	}
	t := fd.Recv.List[0].Type
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// receiverName returns the name a method gives its receiver, or "".
func receiverName(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 || len(fd.Recv.List[0].Names) == 0 {
		return ""
	}
	return fd.Recv.List[0].Names[0].Name
}

// steps returns the steps of the function or method key. For a method the
// receiver and its type come from the declaration; for a function the
// caller names the variable that plays the receiver and its type.
func (ix funcIndex) steps(key, recv, recvType string) ([]string, error) {
	fd, ok := ix[key]
	if !ok {
		return nil, &os.PathError{Op: "find function", Path: key, Err: os.ErrNotExist}
	}
	if fd.Recv != nil {
		recv, recvType = receiverName(fd), receiverType(fd)
	}
	return ix.walk(fd.Body, recv, recvType, "", map[string]bool{key: true}), nil
}

// walk lists the steps of one body. A call on the receiver to one of its
// type's build or start methods is followed by that method's steps,
// indented one level; a method already listed on the path from the root is
// marked rather than listed again.
func (ix funcIndex) walk(body *ast.BlockStmt, recv, recvType, indent string, seen map[string]bool) []string {
	var steps []string
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.DeferStmt:
			steps = append(steps, indent+"defer "+renderCall(n.Call, recv))
		case *ast.GoStmt:
			steps = append(steps, indent+"go "+renderCall(n.Call, recv))
		case *ast.RangeStmt:
			if rootedAt(n.X, recv) {
				steps = append(steps, indent+"range "+types.ExprString(n.X))
			}
		case *ast.CallExpr:
			if !rootedAt(n.Fun, recv) {
				return true
			}
			steps = append(steps, indent+types.ExprString(n.Fun))
			nested := ix.descend(n, recv, recvType, indent, seen, &steps)
			steps = append(steps, nested...)
		}
		return true
	})
	return steps
}

// descend returns the steps of the build or start method a call on the
// receiver names, or nothing for any other call.
func (ix funcIndex) descend(call *ast.CallExpr, recv, recvType, indent string, seen map[string]bool, steps *[]string) []string {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	if id, ok := sel.X.(*ast.Ident); !ok || id.Name != recv {
		return nil
	}
	name := sel.Sel.Name
	if !strings.HasPrefix(name, "build") && !strings.HasPrefix(name, "start") {
		return nil
	}
	key := recvType + "." + name
	fd, ok := ix[key]
	if !ok {
		return nil
	}
	if seen[key] {
		last := len(*steps) - 1
		(*steps)[last] += " (listed above)"
		return nil
	}
	seen[key] = true
	return ix.walk(fd.Body, receiverName(fd), recvType, indent+"  ", seen)
}

// renderCall renders a deferred or spawned call: the callee for a named
// call, and the receiver calls inside the body for a function literal.
func renderCall(call *ast.CallExpr, recv string) string {
	lit, ok := call.Fun.(*ast.FuncLit)
	if !ok {
		return types.ExprString(call.Fun)
	}
	var inner []string
	ast.Inspect(lit.Body, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok && rootedAt(c.Fun, recv) {
			inner = append(inner, types.ExprString(c.Fun))
		}
		return true
	})
	return "func{" + strings.Join(inner, ", ") + "}"
}

// rootedAt reports whether an expression is a selector chain whose root is
// the identifier recv, through any intervening calls.
func rootedAt(e ast.Expr, recv string) bool {
	if recv == "" {
		return false
	}
	for {
		switch x := e.(type) {
		case *ast.SelectorExpr:
			e = x.X
		case *ast.CallExpr:
			e = x.Fun
		case *ast.Ident:
			return x.Name == recv
		default:
			return false
		}
	}
}
