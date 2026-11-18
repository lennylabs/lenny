// SPDX-License-Identifier: MIT

package gatewaywiring

import (
	"go/ast"
	"go/token"
	"go/types"
)

// maxDepth bounds how many hops the resolver follows through variables,
// fields, adapters, and composition-root functions before it gives up on a
// value. The composition root's chains are short; the bound keeps a cycle
// or a long chain from running away.
const maxDepth = 8

// origin is a package and a name a value is attributed to.
type origin struct {
	pkg  *types.Package
	name string
}

// valueRef is a value expression, or the index-th result of a call when
// index is not negative.
type valueRef struct {
	expr  ast.Expr
	index int
}

func ref(e ast.Expr) valueRef { return valueRef{expr: e, index: -1} }

// index records, for the composition root, every value given to a field
// of a struct it declares, every value given to one of its local variables,
// and every value its functions return, so the resolver can follow a value
// through them.
type index struct {
	fields  map[*types.Var][]valueRef
	locals  map[*types.Var][]valueRef
	returns map[*types.Func][][]valueRef
	decls   map[*types.Func]*ast.FuncDecl
}

func (w *walker) buildIndex() {
	ix := &index{
		fields:  map[*types.Var][]valueRef{},
		locals:  map[*types.Var][]valueRef{},
		returns: map[*types.Func][][]valueRef{},
		decls:   map[*types.Func]*ast.FuncDecl{},
	}
	w.ix = ix
	for _, f := range w.p.Files {
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			fn, ok := w.p.Info.Defs[fd.Name].(*types.Func)
			if !ok {
				continue
			}
			ix.decls[fn] = fd
			ix.returns[fn] = w.returnsOf(fd.Body)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CompositeLit:
				w.indexLiteral(n)
			case *ast.AssignStmt:
				w.indexAssign(n.Lhs, n.Rhs)
			case *ast.ValueSpec:
				lhs := make([]ast.Expr, len(n.Names))
				for i, id := range n.Names {
					lhs[i] = id
				}
				w.indexAssign(lhs, n.Values)
			}
			return true
		})
	}
}

// returnsOf collects the results of a function body's return statements,
// leaving out those of nested function literals.
func (w *walker) returnsOf(body *ast.BlockStmt) [][]valueRef {
	var out [][]valueRef
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.ReturnStmt:
			out = append(out, expandResults(n.Results))
		}
		return true
	})
	return out
}

// expandResults turns a result list into one reference per result, also
// when a single call supplies several.
func expandResults(results []ast.Expr) []valueRef {
	if len(results) == 1 {
		if call, ok := ast.Unparen(results[0]).(*ast.CallExpr); ok {
			return []valueRef{{expr: call, index: 0}, {expr: call, index: 1}, {expr: call, index: 2}, {expr: call, index: 3}}
		}
	}
	out := make([]valueRef, len(results))
	for i, r := range results {
		out[i] = ref(r)
	}
	return out
}

// pairs matches assigned expressions to their values, as single values or
// as the results of one call.
func pairs(lhs, rhs []ast.Expr) [][2]any {
	var out [][2]any
	switch {
	case len(lhs) == len(rhs):
		for i := range lhs {
			out = append(out, [2]any{lhs[i], ref(rhs[i])})
		}
	case len(rhs) == 1:
		for i := range lhs {
			out = append(out, [2]any{lhs[i], valueRef{expr: rhs[0], index: i}})
		}
	}
	return out
}

func (w *walker) indexAssign(lhs, rhs []ast.Expr) {
	for _, pr := range pairs(lhs, rhs) {
		l, v := pr[0].(ast.Expr), pr[1].(valueRef)
		switch l := ast.Unparen(l).(type) {
		case *ast.Ident:
			obj := w.p.Info.Defs[l]
			if obj == nil {
				obj = w.p.Info.Uses[l]
			}
			if vr, ok := obj.(*types.Var); ok && w.isRootLocal(vr) {
				w.ix.locals[vr] = append(w.ix.locals[vr], v)
			}
		case *ast.SelectorExpr:
			if s := w.p.Info.Selections[l]; s != nil && s.Kind() == types.FieldVal {
				if fv, ok := s.Obj().(*types.Var); ok && fv.Pkg() == w.p.Pkg {
					w.ix.fields[fv] = append(w.ix.fields[fv], v)
				}
			}
		}
	}
}

func (w *walker) indexLiteral(lit *ast.CompositeLit) {
	t := w.p.Info.TypeOf(lit)
	if t == nil {
		return
	}
	st, ok := t.Underlying().(*types.Struct)
	if !ok {
		return
	}
	for i, elt := range lit.Elts {
		var field *types.Var
		value := elt
		if kv, ok := elt.(*ast.KeyValueExpr); ok {
			key, _ := kv.Key.(*ast.Ident)
			if key != nil {
				field, _ = w.p.Info.Uses[key].(*types.Var)
			}
			value = kv.Value
		} else if i < st.NumFields() {
			field = st.Field(i)
		}
		if field != nil && field.Pkg() == w.p.Pkg {
			w.ix.fields[field] = append(w.ix.fields[field], ref(value))
		}
	}
}

// isRootLocal reports whether a variable is a local variable or parameter
// of the composition root rather than a package-level variable.
func (w *walker) isRootLocal(v *types.Var) bool {
	return v.Pkg() == w.p.Pkg && !v.IsField() && v.Parent() != w.p.Pkg.Scope()
}

// resolver follows one value through the composition root.
type resolver struct {
	w       *walker
	visited map[any]bool
}

func (w *walker) newResolver() *resolver { return &resolver{w: w, visited: map[any]bool{}} }

// originsOf returns what a value is attributed to, following it through
// conversions, type assertions, local variables and their assignments,
// fields of the composition root's structs and every value given to them,
// calls to composition-root functions and their returns, adapters (structs
// the composition root declares) by their field values, and function
// literals, methods, and functions of the composition root by what their
// bodies call and capture. A value of another part's package is attributed
// to that package: its named type, its method or function, the field of
// another part's struct it is read from, or the function of another part
// that returns it.
func (r *resolver) originsOf(v valueRef, depth int) []origin {
	if depth > maxDepth || v.expr == nil {
		return nil
	}
	info := r.w.p.Info
	e := ast.Unparen(v.expr)
	if v.index >= 0 {
		call, ok := e.(*ast.CallExpr)
		if !ok {
			return nil
		}
		return r.callResult(call, v.index, depth)
	}
	switch x := e.(type) {
	case *ast.CallExpr:
		if tv, ok := info.Types[x.Fun]; ok && tv.IsType() {
			if len(x.Args) == 1 {
				return r.originsOf(ref(x.Args[0]), depth+1)
			}
			return nil
		}
		return r.callResult(x, 0, depth)
	case *ast.TypeAssertExpr:
		return r.originsOf(ref(x.X), depth+1)
	case *ast.StarExpr:
		return r.originsOf(ref(x.X), depth+1)
	case *ast.UnaryExpr:
		if x.Op == token.AND {
			return r.originsOf(ref(x.X), depth+1)
		}
	case *ast.FuncLit:
		return r.body(x.Body, x.Pos(), x.End(), depth+1)
	case *ast.CompositeLit:
		if named := namedOf(info.TypeOf(x)); named != nil && named.Obj().Pkg() == r.w.p.Pkg {
			var out []origin
			for _, elt := range x.Elts {
				if kv, ok := elt.(*ast.KeyValueExpr); ok {
					elt = kv.Value
				}
				out = merge(out, r.originsOf(ref(elt), depth+1))
			}
			return out
		}
	case *ast.SelectorExpr:
		if s := info.Selections[x]; s != nil {
			switch s.Kind() {
			case types.MethodVal:
				named := namedOf(s.Recv())
				if named == nil {
					return nil
				}
				if named.Obj().Pkg() == r.w.p.Pkg {
					return r.decl(s.Obj(), depth+1)
				}
				return []origin{{named.Obj().Pkg(), named.Obj().Name() + "." + x.Sel.Name}}
			case types.FieldVal:
				fv := s.Obj().(*types.Var)
				if fv.Pkg() == r.w.p.Pkg {
					return r.field(fv, depth+1)
				}
				if isFuncOrInterface(fv.Type()) && namedOf(fv.Type()) == nil {
					structName := ""
					if named := namedOf(s.Recv()); named != nil {
						structName = named.Obj().Name() + "."
					}
					return []origin{{fv.Pkg(), structName + fv.Name()}}
				}
			}
		} else if fn, ok := info.Uses[x.Sel].(*types.Func); ok {
			return r.funcValue(fn, depth+1)
		}
	case *ast.Ident:
		switch obj := info.Uses[x].(type) {
		case *types.Func:
			return r.funcValue(obj, depth+1)
		case *types.Var:
			if r.w.isRootLocal(obj) {
				if out := r.local(obj, depth+1); len(out) > 0 {
					return out
				}
			}
		}
	}
	return r.namedType(info.TypeOf(e), depth)
}

// namedType attributes a value by its static type: another part's named
// type, or, for a struct the composition root declares, every value its
// fields are given.
func (r *resolver) namedType(t types.Type, depth int) []origin {
	if t == nil {
		return nil
	}
	named := namedOf(t)
	if named == nil || named.Obj().Pkg() == nil {
		return nil
	}
	if named.Obj().Pkg() != r.w.p.Pkg {
		return []origin{{named.Obj().Pkg(), named.Obj().Name()}}
	}
	if r.visited[named.Obj()] {
		return nil
	}
	r.visited[named.Obj()] = true
	st, ok := named.Underlying().(*types.Struct)
	if !ok {
		return nil
	}
	var out []origin
	for i := 0; i < st.NumFields(); i++ {
		out = merge(out, r.field(st.Field(i), depth+1))
	}
	return out
}

func (r *resolver) field(fv *types.Var, depth int) []origin {
	if r.visited[fv] {
		return nil
	}
	r.visited[fv] = true
	var out []origin
	for _, v := range r.w.ix.fields[fv] {
		out = merge(out, r.originsOf(v, depth))
	}
	return out
}

func (r *resolver) local(v *types.Var, depth int) []origin {
	if r.visited[v] {
		return nil
	}
	r.visited[v] = true
	var out []origin
	for _, val := range r.w.ix.locals[v] {
		out = merge(out, r.originsOf(val, depth))
	}
	return out
}

// funcValue attributes a function given as a value: a function of another
// part by itself, a function of the composition root by its body.
func (r *resolver) funcValue(fn *types.Func, depth int) []origin {
	if fn.Pkg() == nil {
		return nil
	}
	if fn.Pkg() == r.w.p.Pkg {
		return r.decl(fn, depth)
	}
	return []origin{{fn.Pkg(), funcName(fn)}}
}

// callResult attributes the index-th result of a call: a call to a
// composition-root function by the values it returns at that index, and a
// call to another part's function by the function, when the result is a
// function or an interface, or else by the result's named type.
func (r *resolver) callResult(call *ast.CallExpr, i int, depth int) []origin {
	info := r.w.p.Info
	fn := calleeFunc(info, call.Fun)
	if fn != nil && fn.Pkg() == r.w.p.Pkg {
		if r.visited[[2]any{fn, i}] {
			return nil
		}
		r.visited[[2]any{fn, i}] = true
		var out []origin
		for _, results := range r.w.ix.returns[fn] {
			if i < len(results) {
				out = merge(out, r.originsOf(results[i], depth+1))
			}
		}
		return out
	}
	sig, ok := info.TypeOf(call.Fun).Underlying().(*types.Signature)
	if !ok || i >= sig.Results().Len() {
		return nil
	}
	rt := sig.Results().At(i).Type()
	if fn != nil && fn.Pkg() != nil && isFuncOrInterface(rt) {
		return []origin{{fn.Pkg(), funcName(fn)}}
	}
	return r.namedType(rt, depth)
}

// decl attributes a function or method of the composition root, given as a
// value, to what its body calls and captures.
func (r *resolver) decl(obj types.Object, depth int) []origin {
	fn, ok := obj.(*types.Func)
	if !ok || r.visited[fn] {
		return nil
	}
	r.visited[fn] = true
	fd := r.w.ix.decls[fn]
	if fd == nil {
		return nil
	}
	return r.body(fd.Body, fd.Pos(), fd.End(), depth)
}

// body attributes a function body to the functions and methods of other
// parts it calls, to the bodies of the composition-root functions it calls,
// and to what the variables it captures resolve to: those declared outside
// the range [start, end).
func (r *resolver) body(b *ast.BlockStmt, start, end token.Pos, depth int) []origin {
	if depth > maxDepth {
		return nil
	}
	info := r.w.p.Info
	var out []origin
	ast.Inspect(b, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			fn := calleeFunc(info, n.Fun)
			if fn == nil || fn.Pkg() == nil {
				return true
			}
			if fn.Pkg() == r.w.p.Pkg {
				out = merge(out, r.decl(fn, depth+1))
			} else {
				out = merge(out, []origin{{fn.Pkg(), funcName(fn)}})
			}
		case *ast.Ident:
			v, ok := info.Uses[n].(*types.Var)
			if !ok || v.IsField() || (v.Pos() >= start && v.Pos() < end) {
				return true
			}
			if r.w.isRootLocal(v) {
				out = merge(out, r.local(v, depth+1))
				return true
			}
			if named := namedOf(v.Type()); named != nil && named.Obj().Pkg() != r.w.p.Pkg {
				out = merge(out, []origin{{named.Obj().Pkg(), named.Obj().Name()}})
			}
		}
		return true
	})
	return out
}

func calleeFunc(info *types.Info, fun ast.Expr) *types.Func {
	var id *ast.Ident
	switch f := ast.Unparen(fun).(type) {
	case *ast.Ident:
		id = f
	case *ast.SelectorExpr:
		id = f.Sel
	default:
		return nil
	}
	fn, _ := info.Uses[id].(*types.Func)
	return fn
}

func isFuncOrInterface(t types.Type) bool {
	switch t.Underlying().(type) {
	case *types.Interface, *types.Signature:
		return true
	}
	return false
}

func merge(list []origin, more []origin) []origin {
	for _, o := range more {
		if o.pkg == nil {
			continue
		}
		dup := false
		for _, x := range list {
			if x.pkg == o.pkg && x.name == o.name {
				dup = true
				break
			}
		}
		if !dup {
			list = append(list, o)
		}
	}
	return list
}
