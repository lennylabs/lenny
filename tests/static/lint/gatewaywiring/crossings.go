// SPDX-License-Identifier: MIT

package gatewaywiring

import (
	"fmt"
	"go/ast"
	"go/types"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lennylabs/lenny/tests/static/lint/gatewaylayout"
)

// Crossing is one value of a part passed where another part expects an
// interface or a function, or one value of a confined package handed to a
// part GW-R2 does not allow that package.
type Crossing struct {
	// To is the repository-relative package that receives the value: the
	// package of the struct whose field, or the function whose parameter,
	// takes it. Where the receiver is not known, as for a return statement,
	// a variable, or a channel, it is the package that declares the target
	// type.
	To string
	// Interface names the receiving slot in To: Struct.Field, Func(n) for
	// the n-th parameter of a function or method, or, where the receiver is
	// not known, the target type's name.
	Interface string
	// From is the repository-relative package the value is attributed to.
	From string
	// Type names what the value is attributed to in From: a type name,
	// Type.Method for a method, a function name, or Struct.Field for a
	// function read from another part's struct.
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

// Crossings returns every crossing the program makes, sorted by key. A
// crossing is a value attributed to one gateway part, given to a target
// whose package belongs to another part that the import rules refuse
// importing the value's package, unless the target's package is the seam
// interface; or a value attributed to a confined package, handed to a part
// GW-R2 does not allow that package. The resolver in resolve.go decides
// what a value is attributed to.
//
// The check reads call arguments, keyed and positional struct literal
// fields, map, slice, and array literal elements, assignments to fields,
// local variables, and index expressions (tuple assignments included),
// variable declarations, return statements against the enclosing
// function's results, and channel sends.
func Crossings(p *Program, layout Layout) ([]Crossing, error) {
	w := &walker{p: p, layout: layout, seen: map[string]bool{}, memo: map[any][]origin{}, inProgress: map[any]bool{}}
	w.buildIndex()
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
	if len(w.truncated) > 0 {
		sort.Strings(w.truncated)
		return w.out, fmt.Errorf("the resolver reached its depth bound of %d at %s; the check cannot vouch for those values", maxDepth, strings.Join(w.truncated, ", "))
	}
	return w.out, nil
}

type walker struct {
	p          *Program
	layout     Layout
	seen       map[string]bool
	out        []Crossing
	ix         *index
	memo       map[any][]origin
	inProgress map[any]bool
	truncated  []string
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
		w.assign(n.Lhs, n.Rhs)
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
	var ownerPkg *types.Package
	ownerName := ""
	if fn := calleeFunc(w.p.Info, call.Fun); fn != nil {
		ownerPkg, ownerName = fn.Pkg(), funcName(fn)
	}
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
		slot := i
		if sig.Variadic() && slot >= params.Len()-1 {
			slot = params.Len() - 1
		}
		w.check(pt, ownerPkg, fmt.Sprintf("%s(%d)", ownerName, slot), ref(arg), arg)
	}
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
					w.check(field.Type(), field.Pkg(), typeName+"."+field.Name(), ref(kv.Value), kv.Value)
				}
				continue
			}
			if i < u.NumFields() {
				field := u.Field(i)
				w.check(field.Type(), field.Pkg(), typeName+"."+field.Name(), ref(elt), elt)
			}
		}
	case *types.Map:
		for _, elt := range lit.Elts {
			if kv, ok := elt.(*ast.KeyValueExpr); ok {
				w.check(u.Elem(), typePkg, typeName+"[]", ref(kv.Value), kv.Value)
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
		w.check(elem, pkg, name+"[]", ref(elt), elt)
	}
}

// assign checks each assignment to a struct field, a local variable, or an
// index expression, one value to one target or the results of one call to
// several.
func (w *walker) assign(lhs, rhs []ast.Expr) {
	for _, pr := range pairs(lhs, rhs) {
		l, v := pr[0].(ast.Expr), pr[1].(valueRef)
		switch x := ast.Unparen(l).(type) {
		case *ast.SelectorExpr:
			s := w.p.Info.Selections[x]
			if s == nil || s.Kind() != types.FieldVal {
				continue
			}
			field := s.Obj().(*types.Var)
			structName := ""
			if named := namedOf(s.Recv()); named != nil {
				structName = named.Obj().Name()
			}
			w.check(field.Type(), field.Pkg(), structName+"."+field.Name(), v, v.expr)
		case *ast.Ident, *ast.IndexExpr:
			t := w.p.Info.TypeOf(x)
			if t == nil {
				continue
			}
			w.check(t, nil, "", v, v.expr)
		}
	}
}

// valueSpec checks a variable declaration that names its type.
func (w *walker) valueSpec(vs *ast.ValueSpec) {
	if vs.Type == nil {
		return
	}
	t := w.p.Info.TypeOf(vs.Type)
	if t == nil {
		return
	}
	lhs := make([]ast.Expr, len(vs.Names))
	for i, id := range vs.Names {
		lhs[i] = id
	}
	for _, pr := range pairs(lhs, vs.Values) {
		v := pr[1].(valueRef)
		w.check(t, nil, "", v, v.expr)
	}
}

// send checks a channel send against the channel's element type.
func (w *walker) send(s *ast.SendStmt) {
	t := w.p.Info.TypeOf(s.Chan)
	if t == nil {
		return
	}
	if ch, ok := t.Underlying().(*types.Chan); ok {
		w.check(ch.Elem(), nil, "", ref(s.Value), s.Value)
	}
}

// ret checks each result of a return statement against the enclosing
// function's result types, also when one call supplies several results.
func (w *walker) ret(r *ast.ReturnStmt, sig *types.Signature, fn *types.Func) {
	if sig == nil {
		return
	}
	var ownerPkg *types.Package
	ownerName := "func"
	if fn != nil {
		ownerPkg, ownerName = fn.Pkg(), funcName(fn)
	}
	n := sig.Results().Len()
	var refs []valueRef
	switch {
	case len(r.Results) == n:
		for _, res := range r.Results {
			refs = append(refs, ref(res))
		}
	case len(r.Results) == 1 && n > 1:
		for i := 0; i < n; i++ {
			refs = append(refs, valueRef{expr: r.Results[0], index: i})
		}
	default:
		return
	}
	for i, v := range refs {
		w.check(sig.Results().At(i).Type(), ownerPkg, fmt.Sprintf("%s.%d", ownerName, i), v, v.expr)
	}
}

// check records the crossings one value makes at one target: a crossing of
// a part boundary when the target is an interface or a function and the
// receiving part may not import the value's package, and a confined value
// when the receiving part may not hold it. The receiving part is the
// package of the struct field or function parameter that takes the value
// (ownerPkg), and the package that declares the target type where that is
// not known. Target types from the standard library or a dependency are
// left out (decision 17 of change 0117).
func (w *walker) check(target types.Type, ownerPkg *types.Package, ownerName string, v valueRef, site ast.Node) {
	to, toName, crossing := w.receiver(target, ownerPkg, ownerName)
	confined := w.holdsConfined(ownerPkg)
	if !crossing && !confined {
		return
	}
	r := w.newResolver()
	origins := r.originsOf(v, 0)
	if r.truncated {
		w.truncated = append(w.truncated, w.site(site))
	}
	if confined {
		w.confined(ownerPkg, ownerName, origins, site)
	}
	if !crossing {
		return
	}
	toPart, _ := w.layout.Part(to)
	for _, o := range origins {
		from, ok := w.p.Rel(o.pkg.Path())
		if !ok {
			continue
		}
		fromPart, fromKind := w.layout.Part(from)
		if !gatewaylayout.IsGatewayPart(fromKind) || toPart == fromPart || !w.layout.ImportRefused(to, from) {
			continue
		}
		w.record(Crossing{To: to, Interface: toName, From: from, Type: o.name, Site: w.site(site)})
	}
}

// receiver returns the receiving package and slot of a target that can be
// crossed: an interface or a function type, not declared outside the module
// or in the seam interface, received by a gateway part other than the seam
// interface.
func (w *walker) receiver(target types.Type, ownerPkg *types.Package, ownerName string) (string, string, bool) {
	if !isFuncOrInterface(target) {
		return "", "", false
	}
	named, _ := types.Unalias(target).(*types.Named)
	if named != nil {
		tpkg := named.Obj().Pkg()
		if tpkg == nil {
			return "", "", false
		}
		rel, inModule := w.p.Rel(tpkg.Path())
		if !inModule {
			return "", "", false
		}
		if _, kind := w.layout.Part(rel); kind == gatewaylayout.KindSeamInterface {
			return "", "", false
		}
	}
	recvPkg, recvName := ownerPkg, ownerName
	if recvPkg == nil || recvPkg == w.p.Pkg {
		if named == nil {
			return "", "", false
		}
		recvPkg, recvName = named.Obj().Pkg(), named.Obj().Name()
	}
	to, ok := w.p.Rel(recvPkg.Path())
	if !ok {
		return "", "", false
	}
	if _, kind := w.layout.Part(to); !gatewaylayout.IsGatewayPart(kind) || kind == gatewaylayout.KindSeamInterface {
		return "", "", false
	}
	return to, recvName, true
}

// holdsConfined reports whether the receiving package is a gateway part, so
// a confined value it receives is worth checking.
func (w *walker) holdsConfined(ownerPkg *types.Package) bool {
	if ownerPkg == nil {
		return false
	}
	to, ok := w.p.Rel(ownerPkg.Path())
	if !ok {
		return false
	}
	_, kind := w.layout.Part(to)
	return gatewaylayout.IsGatewayPart(kind)
}

// confined records each origin of a value that a confined package
// declares, when the struct field, parameter, or result that receives the
// value belongs to a part GW-R2 does not allow that package.
func (w *walker) confined(ownerPkg *types.Package, ownerName string, origins []origin, site ast.Node) {
	if ownerPkg == nil {
		return
	}
	to, ok := w.p.Rel(ownerPkg.Path())
	if !ok {
		return
	}
	part, kind := w.layout.Part(to)
	for _, o := range origins {
		from, ok := w.p.Rel(o.pkg.Path())
		if !ok || !gatewaylayout.ConfinedImport(kind, part, from) {
			continue
		}
		w.record(Crossing{To: to, Interface: ownerName, From: from, Type: o.name, Site: w.site(site)})
	}
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
	if t == nil {
		return nil
	}
	t = types.Unalias(t)
	if p, ok := t.(*types.Pointer); ok {
		t = types.Unalias(p.Elem())
	}
	named, _ := t.(*types.Named)
	return named
}
