// SPDX-License-Identifier: MIT

package gatewaywiring

import (
	"fmt"
	"go/ast"
	"go/types"
	"path/filepath"
	"sort"

	"github.com/lennylabs/lenny/tests/static/lint/gatewaylayout"
)

// Crossing is one value of a part passed where another part expects an
// interface or a function.
type Crossing struct {
	// To is the repository-relative package that declares the target: the
	// interface or function type, or, for an unnamed type, the struct or
	// function whose field or parameter it is.
	To string
	// Interface names the target in To: a type name, Struct.Field, or
	// Func(n) for the n-th parameter of a function or method.
	Interface string
	// From is the repository-relative package of the value.
	From string
	// Type names the value in From: a type name, Type.Method for a method
	// value, or a function name.
	Type string
	// Site is the file and line of the first place the composition root
	// makes the crossing.
	Site string
}

// Key identifies a crossing independent of where it is made.
func (c Crossing) Key() string {
	return c.To + "\x00" + c.Interface + "\x00" + c.From + "\x00" + c.Type
}

func (c Crossing) String() string {
	return fmt.Sprintf("%s.%s receives %s.%s (%s)", c.To, c.Interface, c.From, c.Type, c.Site)
}

// PartOf classifies a repository-relative package path.
type PartOf func(rel string) (part string, kind gatewaylayout.Kind)

// Crossings returns every crossing the program makes, sorted by key. A
// crossing is a value whose package belongs to one gateway part, passed to
// a target whose package belongs to another, unless the target's package
// is the seam interface.
func Crossings(p *Program, partOf PartOf) []Crossing {
	w := &walker{p: p, partOf: partOf, seen: map[string]bool{}}
	for _, f := range p.Files {
		ast.Inspect(f, w.visit)
	}
	sort.Slice(w.out, func(i, j int) bool { return w.out[i].Key() < w.out[j].Key() })
	return w.out
}

type walker struct {
	p      *Program
	partOf PartOf
	seen   map[string]bool
	out    []Crossing
}

func (w *walker) visit(n ast.Node) bool {
	switch n := n.(type) {
	case *ast.CallExpr:
		w.call(n)
	case *ast.CompositeLit:
		w.literal(n)
	case *ast.AssignStmt:
		w.assign(n)
	}
	return true
}

// call checks each argument of a call against its parameter.
func (w *walker) call(call *ast.CallExpr) {
	if tv, ok := w.p.Info.Types[call.Fun]; !ok || tv.IsType() {
		return
	}
	sig, ok := w.p.Info.TypeOf(call.Fun).Underlying().(*types.Signature)
	if !ok {
		return
	}
	ownerPkg, ownerName := w.callee(call.Fun)
	params := sig.Params()
	for i, arg := range call.Args {
		var pt types.Type
		switch {
		case sig.Variadic() && i >= params.Len()-1 && !call.Ellipsis.IsValid():
			pt = params.At(params.Len() - 1).Type().(*types.Slice).Elem()
		case i < params.Len():
			pt = params.At(i).Type()
		default:
			continue
		}
		w.check(pt, ownerPkg, fmt.Sprintf("%s(%d)", ownerName, i), arg)
	}
}

// callee returns the package and name of the function or method a call
// names, for a target whose parameter type is unnamed.
func (w *walker) callee(fun ast.Expr) (*types.Package, string) {
	var id *ast.Ident
	switch f := ast.Unparen(fun).(type) {
	case *ast.Ident:
		id = f
	case *ast.SelectorExpr:
		id = f.Sel
	default:
		return nil, ""
	}
	fn, ok := w.p.Info.Uses[id].(*types.Func)
	if !ok {
		return nil, ""
	}
	name := fn.Name()
	if recv := fn.Type().(*types.Signature).Recv(); recv != nil {
		if named := namedOf(recv.Type()); named != nil {
			name = named.Obj().Name() + "." + name
		}
	}
	return fn.Pkg(), name
}

// literal checks each keyed field of a struct literal.
func (w *walker) literal(lit *ast.CompositeLit) {
	t := w.p.Info.TypeOf(lit)
	if t == nil {
		return
	}
	if _, ok := t.Underlying().(*types.Struct); !ok {
		return
	}
	structName := ""
	if named := namedOf(t); named != nil {
		structName = named.Obj().Name()
	}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		field, ok := w.p.Info.Uses[key].(*types.Var)
		if !ok {
			continue
		}
		w.check(field.Type(), field.Pkg(), structName+"."+field.Name(), kv.Value)
	}
}

// assign checks each assignment to a struct field.
func (w *walker) assign(as *ast.AssignStmt) {
	if len(as.Lhs) != len(as.Rhs) {
		return
	}
	for i, lhs := range as.Lhs {
		sel, ok := ast.Unparen(lhs).(*ast.SelectorExpr)
		if !ok {
			continue
		}
		s := w.p.Info.Selections[sel]
		if s == nil || s.Kind() != types.FieldVal {
			continue
		}
		field := s.Obj().(*types.Var)
		structName := ""
		if named := namedOf(s.Recv()); named != nil {
			structName = named.Obj().Name()
		}
		w.check(field.Type(), field.Pkg(), structName+"."+field.Name(), as.Rhs[i])
	}
}

// check records a crossing when target is an interface or a function type
// of one part and value belongs to another.
func (w *walker) check(target types.Type, ownerPkg *types.Package, ownerName string, value ast.Expr) {
	switch target.Underlying().(type) {
	case *types.Interface, *types.Signature:
	default:
		return
	}
	toPkg, toName := ownerPkg, ownerName
	if named, ok := types.Unalias(target).(*types.Named); ok {
		toPkg, toName = named.Obj().Pkg(), named.Obj().Name()
	}
	if toPkg == nil {
		return
	}
	fromPkg, fromName := w.source(target, value)
	if fromPkg == nil {
		return
	}
	to, ok1 := w.p.Rel(toPkg.Path())
	from, ok2 := w.p.Rel(fromPkg.Path())
	if !ok1 || !ok2 {
		return
	}
	toPart, toKind := w.partOf(to)
	fromPart, fromKind := w.partOf(from)
	if !gatewaylayout.IsGatewayPart(toKind) || !gatewaylayout.IsGatewayPart(fromKind) {
		return
	}
	if toPart == fromPart || toKind == gatewaylayout.KindSeamInterface {
		return
	}
	c := Crossing{To: to, Interface: toName, From: from, Type: fromName, Site: w.site(value)}
	if w.seen[c.Key()] {
		return
	}
	w.seen[c.Key()] = true
	w.out = append(w.out, c)
}

// source returns the package and name the value belongs to: its named
// type for an interface target, and its method or function for a function
// target. A function literal or a value of an unnamed type belongs to no
// package.
func (w *walker) source(target types.Type, value ast.Expr) (*types.Package, string) {
	value = ast.Unparen(value)
	if _, isFunc := target.Underlying().(*types.Signature); isFunc {
		var id *ast.Ident
		switch v := value.(type) {
		case *ast.SelectorExpr:
			if s := w.p.Info.Selections[v]; s != nil && s.Kind() == types.MethodVal {
				if named := namedOf(s.Recv()); named != nil {
					return named.Obj().Pkg(), named.Obj().Name() + "." + v.Sel.Name
				}
				return nil, ""
			}
			id = v.Sel
		case *ast.Ident:
			id = v
		default:
			return nil, ""
		}
		if fn, ok := w.p.Info.Uses[id].(*types.Func); ok {
			return fn.Pkg(), fn.Name()
		}
		return nil, ""
	}
	t := w.p.Info.TypeOf(value)
	if t == nil {
		return nil, ""
	}
	if named := namedOf(t); named != nil {
		return named.Obj().Pkg(), named.Obj().Name()
	}
	return nil, ""
}

func (w *walker) site(n ast.Node) string {
	pos := w.p.Fset.Position(n.Pos())
	return fmt.Sprintf("%s:%d", filepath.Base(pos.Filename), pos.Line)
}

// namedOf returns the named type of t or of the type t points to.
func namedOf(t types.Type) *types.Named {
	t = types.Unalias(t)
	if p, ok := t.(*types.Pointer); ok {
		t = types.Unalias(p.Elem())
	}
	named, _ := t.(*types.Named)
	return named
}
