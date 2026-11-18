// SPDX-License-Identifier: MIT

package gatewaywiring

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"

	"github.com/lennylabs/lenny/tests/static/lint/gatewaylayout"
)

// Crossing is one value of a part passed where another part expects an
// interface or a function, or one value of a confined package handed to a
// part GW-R2 does not allow that package.
type Crossing struct {
	// To is the repository-relative package that declares the target: the
	// interface or function type, or, for an unnamed type, the struct or
	// function whose field, parameter, or result it is. For a confined
	// value it is the package of the struct or function that receives it.
	To string
	// Interface names the target in To: a type name, Struct.Field,
	// Func(n) for the n-th parameter of a function or method, or Func.n for
	// its n-th result.
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

// Layout classifies repository-relative package paths and answers the
// import rules; gatewaylayout.Classifier implements it.
type Layout interface {
	Part(rel string) (part string, kind gatewaylayout.Kind)
	ImportRefused(importer, imported string) bool
}

// origin is a package and a name a value is attributed to.
type origin struct {
	pkg  *types.Package
	name string
}

// Crossings returns every crossing the program makes, sorted by key. A
// crossing is a value whose package belongs to one gateway part, passed to
// a target whose package belongs to another part that the import rules
// refuse importing the value's package, unless the target's package is the
// seam interface; or a value whose named type a confined package declares,
// handed to a part GW-R2 does not allow that package. An adapter type of
// cmd/lenny-gateway counts as what its fields are given, and a function
// literal, method value, or function of cmd/lenny-gateway as what its body
// calls and captures (GW-R1).
//
// The check reads call arguments, keyed and positional struct literal
// fields, map, slice, and array literal elements, assignments to fields,
// variable declarations with a type, return statements against the
// enclosing function's results, and channel sends.
func Crossings(p *Program, layout Layout) []Crossing {
	w := &walker{p: p, layout: layout, seen: map[string]bool{}, adapters: map[*types.TypeName][]origin{}, decls: map[*types.Func]*ast.FuncDecl{}}
	for _, f := range p.Files {
		for _, decl := range f.Decls {
			if fd, ok := decl.(*ast.FuncDecl); ok && fd.Body != nil {
				if fn, ok := p.Info.Defs[fd.Name].(*types.Func); ok {
					w.decls[fn] = fd
				}
			}
		}
	}
	w.collectAdapters()
	for _, f := range p.Files {
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Body == nil {
					continue
				}
				fn, _ := p.Info.Defs[d.Name].(*types.Func)
				var sig *types.Signature
				if fn != nil {
					sig, _ = fn.Type().(*types.Signature)
				}
				w.body(d.Body, sig, fn)
			default:
				ast.Inspect(decl, w.visit)
			}
		}
	}
	sort.Slice(w.out, func(i, j int) bool { return w.out[i].Key() < w.out[j].Key() })
	return w.out
}

type walker struct {
	p        *Program
	layout   Layout
	seen     map[string]bool
	out      []Crossing
	adapters map[*types.TypeName][]origin
	decls    map[*types.Func]*ast.FuncDecl
}

// collectAdapters records, for each struct type the composition root
// declares, the origins of the values its fields are given anywhere in the
// composition root: in keyed or positional struct literals and in field
// assignments.
func (w *walker) collectAdapters() {
	add := func(named *types.Named, value ast.Expr) {
		if named == nil || named.Obj().Pkg() != w.p.Pkg {
			return
		}
		for _, o := range w.origins(value, false) {
			if !hasOrigin(w.adapters[named.Obj()], o) {
				w.adapters[named.Obj()] = append(w.adapters[named.Obj()], o)
			}
		}
	}
	for _, f := range w.p.Files {
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CompositeLit:
				t := w.p.Info.TypeOf(n)
				if t == nil {
					return true
				}
				if _, ok := t.Underlying().(*types.Struct); !ok {
					return true
				}
				for _, elt := range n.Elts {
					if kv, ok := elt.(*ast.KeyValueExpr); ok {
						elt = kv.Value
					}
					add(namedOf(t), elt)
				}
			case *ast.AssignStmt:
				if len(n.Lhs) != len(n.Rhs) {
					return true
				}
				for i, lhs := range n.Lhs {
					sel, ok := ast.Unparen(lhs).(*ast.SelectorExpr)
					if !ok {
						continue
					}
					if s := w.p.Info.Selections[sel]; s != nil && s.Kind() == types.FieldVal {
						add(namedOf(s.Recv()), n.Rhs[i])
					}
				}
			}
			return true
		})
	}
}

func hasOrigin(list []origin, o origin) bool {
	for _, x := range list {
		if x.pkg == o.pkg && x.name == o.name {
			return true
		}
	}
	return false
}

// origins returns the packages and names a value is attributed to: a
// method value's receiver type, a function, a function literal's calls and
// captures, a value of an adapter type the composition root declares by
// the adapter's field values (when expand is set), and otherwise the
// value's named type.
func (w *walker) origins(value ast.Expr, expand bool) []origin {
	value = ast.Unparen(value)
	switch v := value.(type) {
	case *ast.FuncLit:
		return w.literalOrigins(v)
	case *ast.SelectorExpr:
		if s := w.p.Info.Selections[v]; s != nil && s.Kind() == types.MethodVal {
			named := namedOf(s.Recv())
			if named == nil {
				return nil
			}
			if expand && named.Obj().Pkg() == w.p.Pkg {
				return w.declOrigins(s.Obj())
			}
			return []origin{{named.Obj().Pkg(), named.Obj().Name() + "." + v.Sel.Name}}
		}
		if fn, ok := w.p.Info.Uses[v.Sel].(*types.Func); ok {
			if expand && fn.Pkg() == w.p.Pkg {
				return w.declOrigins(fn)
			}
			return []origin{{fn.Pkg(), fn.Name()}}
		}
	case *ast.Ident:
		if fn, ok := w.p.Info.Uses[v].(*types.Func); ok {
			if expand && fn.Pkg() == w.p.Pkg {
				return w.declOrigins(fn)
			}
			return []origin{{fn.Pkg(), fn.Name()}}
		}
	}
	t := w.p.Info.TypeOf(value)
	if t == nil {
		return nil
	}
	named := namedOf(t)
	if named == nil {
		return nil
	}
	if expand && named.Obj().Pkg() == w.p.Pkg {
		return w.adapters[named.Obj()]
	}
	return []origin{{named.Obj().Pkg(), named.Obj().Name()}}
}

// declOrigins attributes a function or method the composition root
// declares, given as a function value, to what its body calls and
// captures, as a function literal is.
func (w *walker) declOrigins(obj types.Object) []origin {
	fn, ok := obj.(*types.Func)
	if !ok {
		return nil
	}
	fd := w.decls[fn]
	if fd == nil {
		return nil
	}
	return w.bodyOrigins(fd.Body, fd.Pos(), fd.End())
}

// literalOrigins attributes a function literal to the functions and
// methods it calls and to the named types of the variables it captures,
// leaving out what the composition root declares itself.
func (w *walker) literalOrigins(lit *ast.FuncLit) []origin {
	return w.bodyOrigins(lit.Body, lit.Pos(), lit.End())
}

// bodyOrigins attributes a function body to the functions and methods it
// calls and to the named types of the variables it captures: those
// declared outside the range [start, end).
func (w *walker) bodyOrigins(body *ast.BlockStmt, start, end token.Pos) []origin {
	var out []origin
	add := func(o origin) {
		if o.pkg != nil && o.pkg != w.p.Pkg && !hasOrigin(out, o) {
			out = append(out, o)
		}
	}
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			var id *ast.Ident
			switch f := ast.Unparen(n.Fun).(type) {
			case *ast.Ident:
				id = f
			case *ast.SelectorExpr:
				id = f.Sel
			}
			if fn, ok := w.p.Info.Uses[id].(*types.Func); ok && id != nil {
				add(origin{fn.Pkg(), funcName(fn)})
			}
		case *ast.Ident:
			v, ok := w.p.Info.Uses[n].(*types.Var)
			if !ok || v.IsField() || (v.Pos() >= start && v.Pos() < end) {
				return true
			}
			if named := namedOf(v.Type()); named != nil {
				add(origin{named.Obj().Pkg(), named.Obj().Name()})
			}
		}
		return true
	})
	return out
}

// body walks a function body, checking its return statements against the
// function's results and walking each nested function literal with its own
// results.
func (w *walker) body(b *ast.BlockStmt, sig *types.Signature, fn *types.Func) {
	ast.Inspect(b, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			litSig, _ := w.p.Info.TypeOf(n).(*types.Signature)
			w.body(n.Body, litSig, nil)
			return false
		case *ast.ReturnStmt:
			w.ret(n, sig, fn)
		}
		return w.visit(n)
	})
}

func (w *walker) visit(n ast.Node) bool {
	switch n := n.(type) {
	case *ast.CallExpr:
		w.call(n)
	case *ast.CompositeLit:
		w.literal(n)
	case *ast.AssignStmt:
		w.assign(n)
	case *ast.ValueSpec:
		w.valueSpec(n)
	case *ast.SendStmt:
		w.send(n)
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
	return fn.Pkg(), funcName(fn)
}

func funcName(fn *types.Func) string {
	name := fn.Name()
	if recv := fn.Type().(*types.Signature).Recv(); recv != nil {
		if named := namedOf(recv.Type()); named != nil {
			name = named.Obj().Name() + "." + name
		}
	}
	return name
}

// literal checks each field of a struct literal, keyed or positional, and
// each element of a map, slice, or array literal.
func (w *walker) literal(lit *ast.CompositeLit) {
	t := w.p.Info.TypeOf(lit)
	if t == nil {
		return
	}
	typeName := ""
	var typePkg *types.Package
	if named := namedOf(t); named != nil {
		typeName, typePkg = named.Obj().Name(), named.Obj().Pkg()
	}
	switch u := t.Underlying().(type) {
	case *types.Struct:
		for i, elt := range lit.Elts {
			if kv, ok := elt.(*ast.KeyValueExpr); ok {
				key, ok := kv.Key.(*ast.Ident)
				if !ok {
					continue
				}
				if field, ok := w.p.Info.Uses[key].(*types.Var); ok {
					w.check(field.Type(), field.Pkg(), typeName+"."+field.Name(), kv.Value)
				}
				continue
			}
			if i < u.NumFields() {
				field := u.Field(i)
				w.check(field.Type(), field.Pkg(), typeName+"."+field.Name(), elt)
			}
		}
	case *types.Map:
		for _, elt := range lit.Elts {
			if kv, ok := elt.(*ast.KeyValueExpr); ok {
				w.check(u.Elem(), typePkg, typeName+"[]", kv.Value)
			}
		}
	case *types.Slice:
		w.elements(lit, u.Elem(), typePkg, typeName)
	case *types.Array:
		w.elements(lit, u.Elem(), typePkg, typeName)
	}
}

func (w *walker) elements(lit *ast.CompositeLit, elem types.Type, pkg *types.Package, name string) {
	for _, elt := range lit.Elts {
		if kv, ok := elt.(*ast.KeyValueExpr); ok {
			elt = kv.Value
		}
		w.check(elem, pkg, name+"[]", elt)
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

// valueSpec checks a variable declaration that names its type.
func (w *walker) valueSpec(vs *ast.ValueSpec) {
	if vs.Type == nil || len(vs.Values) != len(vs.Names) {
		return
	}
	t := w.p.Info.TypeOf(vs.Type)
	if t == nil {
		return
	}
	for _, v := range vs.Values {
		w.check(t, nil, "", v)
	}
}

// send checks a channel send against the channel's element type.
func (w *walker) send(s *ast.SendStmt) {
	t := w.p.Info.TypeOf(s.Chan)
	if t == nil {
		return
	}
	if ch, ok := t.Underlying().(*types.Chan); ok {
		w.check(ch.Elem(), nil, "", s.Value)
	}
}

// ret checks each result of a return statement against the enclosing
// function's result types.
func (w *walker) ret(r *ast.ReturnStmt, sig *types.Signature, fn *types.Func) {
	if sig == nil || len(r.Results) != sig.Results().Len() {
		return
	}
	var ownerPkg *types.Package
	ownerName := "func"
	if fn != nil {
		ownerPkg, ownerName = fn.Pkg(), funcName(fn)
	}
	for i, res := range r.Results {
		w.check(sig.Results().At(i).Type(), ownerPkg, fmt.Sprintf("%s.%d", ownerName, i), res)
	}
}

// check records a crossing for one value given to one target.
func (w *walker) check(target types.Type, ownerPkg *types.Package, ownerName string, value ast.Expr) {
	w.confined(ownerPkg, ownerName, value)
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
	to, ok := w.p.Rel(toPkg.Path())
	if !ok {
		return
	}
	toPart, toKind := w.layout.Part(to)
	if !gatewaylayout.IsGatewayPart(toKind) || toKind == gatewaylayout.KindSeamInterface {
		return
	}
	for _, o := range w.valueOrigins(target, value) {
		from, ok := w.p.Rel(o.pkg.Path())
		if !ok {
			continue
		}
		fromPart, fromKind := w.layout.Part(from)
		if !gatewaylayout.IsGatewayPart(fromKind) || toPart == fromPart || !w.layout.ImportRefused(to, from) {
			continue
		}
		w.record(Crossing{To: to, Interface: toName, From: from, Type: o.name, Site: w.site(value)})
	}
}

// valueOrigins returns what a value given to an interface or function
// target is attributed to: for a function target, the method value's
// receiver type, the function, or the function literal's calls and
// captures; for an interface target, the value's named type. A value of an
// adapter type the composition root declares is attributed to the values
// its fields hold, each named after the adapter.
func (w *walker) valueOrigins(target types.Type, value ast.Expr) []origin {
	value = ast.Unparen(value)
	_, isFunc := target.Underlying().(*types.Signature)
	if lit, ok := value.(*ast.FuncLit); ok {
		if !isFunc {
			return nil
		}
		var out []origin
		for _, o := range w.literalOrigins(lit) {
			out = append(out, origin{o.pkg, "func(" + o.name + ")"})
		}
		return out
	}
	direct := w.origins(value, false)
	if len(direct) != 1 || direct[0].pkg == nil {
		return nil
	}
	if direct[0].pkg != w.p.Pkg {
		if isFunc && !w.isFuncValue(value) {
			return nil
		}
		return direct
	}
	var out []origin
	for _, o := range w.origins(value, true) {
		if o.pkg != nil && o.pkg != w.p.Pkg {
			out = append(out, origin{o.pkg, direct[0].name + "(" + o.name + ")"})
		}
	}
	return out
}

// isFuncValue reports whether a value given to a function target is a
// method value or a function, the forms a function target's origin is
// read from.
func (w *walker) isFuncValue(value ast.Expr) bool {
	switch v := value.(type) {
	case *ast.SelectorExpr:
		if s := w.p.Info.Selections[v]; s != nil {
			return s.Kind() == types.MethodVal
		}
		_, ok := w.p.Info.Uses[v.Sel].(*types.Func)
		return ok
	case *ast.Ident:
		_, ok := w.p.Info.Uses[v].(*types.Func)
		return ok
	}
	return false
}

// confined records a value whose named type a confined package declares,
// handed to a struct field, parameter, or result of a part that GW-R2 does
// not allow that package.
func (w *walker) confined(ownerPkg *types.Package, ownerName string, value ast.Expr) {
	if ownerPkg == nil {
		return
	}
	t := w.p.Info.TypeOf(value)
	if t == nil {
		return
	}
	named := namedOf(t)
	if named == nil || named.Obj().Pkg() == nil {
		return
	}
	to, ok1 := w.p.Rel(ownerPkg.Path())
	from, ok2 := w.p.Rel(named.Obj().Pkg().Path())
	if !ok1 || !ok2 {
		return
	}
	part, kind := w.layout.Part(to)
	if !gatewaylayout.ConfinedImport(kind, part, from) {
		return
	}
	w.record(Crossing{To: to, Interface: ownerName, From: from, Type: named.Obj().Name(), Site: w.site(value)})
}

func (w *walker) record(c Crossing) {
	if w.seen[c.Key()] {
		return
	}
	w.seen[c.Key()] = true
	w.out = append(w.out, c)
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
