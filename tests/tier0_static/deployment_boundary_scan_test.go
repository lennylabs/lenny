// SPDX-License-Identifier: MIT

package tier0_static

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// This file holds the source half of the deployment-boundary gate: it lists
// the in-module dependency closure of each binary a rendered agent pod runs,
// parses every file in it, and reports the flags the binary declares and the
// environment variables it reads. It also scans the controller binary's
// closure for the production composite literals that build podspec.Inputs.
//
// The scan runs no type checker, so it recognizes declarations and reads by
// syntax. Every form it cannot resolve to a literal name is reported as a
// defect rather than skipped, so a declaration the gate cannot see fails the
// gate instead of passing it.

// boundaryModulePrefix is the import-path prefix of this module. The scan
// stops at it, so third-party flags and reads are outside the gate.
const boundaryModulePrefix = "github.com/lennylabs/lenny/"

// boundaryPodspecImport is the import path of the pod builder.
const boundaryPodspecImport = boundaryModulePrefix + "pkg/controller/sandbox/podspec"

// boundaryProducerDir is the binary whose closure carries the production
// podspec.Inputs composite literals the producer check reads.
const boundaryProducerDir = "cmd/lenny-controller"

// boundaryListTimeout bounds each `go list` call.
const boundaryListTimeout = 2 * time.Minute

// boundaryBinary is one binary a rendered agent pod runs. Dir is the cmd/
// package; Forward selects whether the gate requires its reads to be rendered.
type boundaryBinary struct {
	Name    string // the subject prefix, which is the cmd/ base name
	Dir     string
	Forward bool
}

// boundaryBinaries are the binaries the gate scans. The adapter is checked in
// both directions. The two in-tree embedded runtimes and the egress-capture
// sidecar are checked in the reverse direction only: each runs in a rendered
// agent pod, so a rendered flag it does not declare exits with status 2.
var boundaryBinaries = []boundaryBinary{
	{Name: "lenny-adapter", Dir: "cmd/lenny-adapter", Forward: true},
	{Name: "echo-embedded", Dir: "cmd/runtimes/echo-embedded"},
	{Name: "preconnect-echo", Dir: "cmd/runtimes/preconnect-echo"},
	{Name: "lenny-egress-capture", Dir: "cmd/lenny-egress-capture"},
}

// declaredFlag is one flag declaration. Set is "" for the package-level set.
type declaredFlag struct {
	Set, Name, Method string
	// Default is the canonical default text: a string literal unquoted, an
	// integer literal in base 10, an env-helper call as its fallback, an
	// os.Getenv call as "", and any other expression as types.ExprString.
	// The Var, Func, BoolFunc, and TextVar forms carry "<opaque>".
	Default     string
	DefaultSpan [2]token.Pos
	Pos         token.Position
}

// key is the flag's subject without the binary prefix: `--name` for the main
// set and `<set> --name` for a named set.
func (d declaredFlag) key() string {
	if d.Set == "" {
		return "--" + d.Name
	}
	return d.Set + " --" + d.Name
}

// envRead is one environment read. BoundTo names the flag key whose default
// argument contains it, or is empty for a standalone read.
type envRead struct {
	Name, BoundTo string
	Pos           token.Position
	at            token.Pos
}

// binaryInputs is everything one binary reads.
type binaryInputs struct {
	Flags []declaredFlag
	Env   []envRead
}

// sourceFile is one Go source file. The scanners take parsed sourceFiles, so
// a negative test passes inline source through the same code path as the
// files the closure lists.
type sourceFile struct {
	Pkg  string // import path
	Name string // repository-relative path, used in defect positions
	Src  []byte
}

// parsedFile is a sourceFile after parsing.
type parsedFile struct {
	Pkg  string
	Name string
	File *ast.File
}

// opaqueDefault is the canonical default of a flag whose value the scan does
// not model.
const opaqueDefault = "<opaque>"

// flagValueMethods are the declaration methods that return a pointer to the
// value; the name is argument 0 and the default is argument 1.
var flagValueMethods = map[string]bool{
	"String": true, "Bool": true, "Int": true, "Int64": true,
	"Uint": true, "Uint64": true, "Float64": true, "Duration": true,
}

// flagMethodShape reports whether method declares a flag, the index of its
// name argument, and the index of its default argument (-1 for none).
func flagMethodShape(method string) (ok bool, nameArg, defaultArg int) {
	switch {
	case flagValueMethods[method]:
		return true, 0, 1
	case strings.HasSuffix(method, "Var") && flagValueMethods[strings.TrimSuffix(method, "Var")]:
		return true, 1, 2
	case method == "Func" || method == "BoolFunc":
		return true, 0, -1
	case method == "Var":
		return true, 1, -1
	case method == "TextVar":
		return true, 1, 2
	}
	return false, 0, 0
}

// opaqueFlagMethod reports whether a declaration method carries no default
// the gate can compare.
func opaqueFlagMethod(method string) bool {
	return method == "Var" || method == "Func" || method == "BoolFunc" || method == "TextVar"
}

// listClosure lists the in-module dependency closure of dir and reads every
// non-test Go file a linux/amd64, CGO-disabled build compiles, which is the
// platform the root Dockerfile builds the pod binaries for. It returns an
// error rather than taking a *testing.T so the measurement can run inside a
// sync.Once and every caller reports the stored error.
func listClosure(ctx context.Context, root, dir string) ([]sourceFile, error) {
	ctx, cancel := context.WithTimeout(ctx, boundaryListTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "list", "-deps",
		"-f", `{{.ImportPath}}{{"\t"}}{{.Dir}}{{"\t"}}{{join .GoFiles ","}}`, "./"+dir)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list -deps ./%s: %w: %s", dir, err, strings.TrimSpace(stderr.String()))
	}
	var files []sourceFile
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 3 {
			return nil, fmt.Errorf("go list -deps ./%s: unexpected line %q", dir, line)
		}
		importPath, pkgDir, goFiles := fields[0], fields[1], fields[2]
		if !strings.HasPrefix(importPath, boundaryModulePrefix) || goFiles == "" {
			continue
		}
		for _, name := range strings.Split(goFiles, ",") {
			abs := filepath.Join(pkgDir, name)
			src, err := os.ReadFile(abs)
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", abs, err)
			}
			rel, err := filepath.Rel(root, abs)
			if err != nil {
				return nil, fmt.Errorf("relativize %s: %w", abs, err)
			}
			files = append(files, sourceFile{Pkg: importPath, Name: filepath.ToSlash(rel), Src: src})
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("go list -deps ./%s listed no in-module file", dir)
	}
	return files, nil
}

// parseSourceFiles parses each file once, reusing cache entries keyed by name
// so the overlapping closures share one parse.
func parseSourceFiles(fset *token.FileSet, files []sourceFile, cache map[string]*ast.File) ([]parsedFile, error) {
	out := make([]parsedFile, 0, len(files))
	for _, f := range files {
		file, ok := cache[f.Name]
		if !ok {
			var err error
			file, err = parser.ParseFile(fset, f.Name, f.Src, parser.SkipObjectResolution)
			if err != nil {
				return nil, fmt.Errorf("parse %s: %w", f.Name, err)
			}
			if cache != nil {
				cache[f.Name] = file
			}
		}
		out = append(out, parsedFile{Pkg: f.Pkg, Name: f.Name, File: file})
	}
	return out, nil
}

// collectStringConsts returns, per import path, the string constants the
// package declares, so a read through a constant resolves to its name.
func collectStringConsts(files []parsedFile) map[string]map[string]string {
	consts := map[string]map[string]string{}
	pending := map[string]map[string]string{} // pkg -> name -> referenced const
	for _, pf := range files {
		if consts[pf.Pkg] == nil {
			consts[pf.Pkg] = map[string]string{}
			pending[pf.Pkg] = map[string]string{}
		}
		for _, decl := range pf.File.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					if i >= len(vs.Values) {
						continue
					}
					switch v := vs.Values[i].(type) {
					case *ast.BasicLit:
						if v.Kind == token.STRING {
							if s, err := strconv.Unquote(v.Value); err == nil {
								consts[pf.Pkg][name.Name] = s
							}
						}
					case *ast.Ident:
						pending[pf.Pkg][name.Name] = v.Name
					}
				}
			}
		}
	}
	// A constant defined as another constant of the same package resolves
	// once its operand does; a bounded number of passes covers any chain.
	for pass := 0; pass < 8; pass++ {
		for pkg, refs := range pending {
			for name, ref := range refs {
				if s, ok := consts[pkg][ref]; ok {
					consts[pkg][name] = s
					delete(refs, name)
				}
			}
		}
	}
	return consts
}

// fileImports maps each local import name in f to its import path. A path's
// default name is the package name the closure declares for it, or the last
// path element for a package outside the closure.
func fileImports(f *ast.File, pkgNames map[string]string) map[string]string {
	out := map[string]string{}
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		name := pkgNames[p]
		if name == "" {
			name = path.Base(p)
		}
		if imp.Name != nil {
			name = imp.Name.Name
		}
		out[name] = p
	}
	return out
}

// importAlias returns the local name f uses for importPath, or "".
func importAlias(imports map[string]string, importPath string) string {
	for name, p := range imports {
		if p == importPath && name != "_" && name != "." {
			return name
		}
	}
	return ""
}

// boundaryWalk visits every node of root with its parent.
func boundaryWalk(root ast.Node, visit func(n, parent ast.Node, stack []ast.Node)) {
	var stack []ast.Node
	ast.Inspect(root, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		var parent ast.Node
		if len(stack) > 0 {
			parent = stack[len(stack)-1]
		}
		visit(n, parent, stack)
		stack = append(stack, n)
		return true
	})
}

// isCallFun reports whether n is the function expression of parent.
func isCallFun(n, parent ast.Node) bool {
	call, ok := parent.(*ast.CallExpr)
	return ok && call.Fun == n
}

// isPkgSelector reports whether e is `<alias>.<name>` for a package alias.
func isPkgSelector(e ast.Node, alias string, names ...string) (string, bool) {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || alias == "" {
		return "", false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok || id.Name != alias {
		return "", false
	}
	for _, n := range names {
		if sel.Sel.Name == n {
			return n, true
		}
	}
	return "", false
}

// boundaryScanContext carries what one file's scan resolves against.
type boundaryScanContext struct {
	fset    *token.FileSet
	pf      parsedFile
	imports map[string]string
	consts  map[string]map[string]string
	helpers map[string]envHelper // this package's env helpers, by name
	defects []string
}

// envHelper is a function whose first parameter flows directly into an
// environment read, so its call sites are the reads.
type envHelper struct {
	Param string
	Decl  *ast.FuncDecl
}

func (c *boundaryScanContext) defect(pos token.Pos, format string, args ...any) {
	p := c.fset.Position(pos)
	c.defects = append(c.defects, fmt.Sprintf("%s:%d: %s", p.Filename, p.Line, fmt.Sprintf(format, args...)))
}

// isGetenvFun reports whether fun is a function that reads one environment
// variable by name.
func (c *boundaryScanContext) isGetenvFun(fun ast.Node) bool {
	if _, ok := isPkgSelector(fun, importAlias(c.imports, "os"), "Getenv", "LookupEnv"); ok {
		return true
	}
	_, ok := isPkgSelector(fun, importAlias(c.imports, "syscall"), "Getenv")
	return ok
}

// helperCall returns the env helper a call invokes, if any.
func (c *boundaryScanContext) helperCall(call *ast.CallExpr) (envHelper, bool) {
	id, ok := call.Fun.(*ast.Ident)
	if !ok {
		return envHelper{}, false
	}
	h, ok := c.helpers[id.Name]
	return h, ok
}

// canonicalDefault normalizes a flag's default argument (see declaredFlag).
func (c *boundaryScanContext) canonicalDefault(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.BasicLit:
		switch v.Kind {
		case token.STRING:
			if s, err := strconv.Unquote(v.Value); err == nil {
				return s
			}
		case token.INT:
			if n, err := strconv.ParseInt(v.Value, 0, 64); err == nil {
				return strconv.FormatInt(n, 10)
			}
		}
	case *ast.CallExpr:
		if _, ok := c.helperCall(v); ok && len(v.Args) >= 2 {
			return c.canonicalDefault(v.Args[1])
		}
		if c.isGetenvFun(v.Fun) {
			return ""
		}
	}
	return types.ExprString(e)
}

// findEnvHelpers returns, per package, the functions whose first parameter is
// passed directly to an environment read.
func findEnvHelpers(files []parsedFile, pkgNames map[string]string) map[string]map[string]envHelper {
	out := map[string]map[string]envHelper{}
	for _, pf := range files {
		c := &boundaryScanContext{pf: pf, imports: fileImports(pf.File, pkgNames)}
		for _, decl := range pf.File.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Recv != nil || fd.Body == nil || fd.Type.Params == nil ||
				len(fd.Type.Params.List) == 0 || len(fd.Type.Params.List[0].Names) == 0 {
				continue
			}
			param := fd.Type.Params.List[0].Names[0].Name
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || !c.isGetenvFun(call.Fun) || len(call.Args) != 1 {
					return true
				}
				if id, ok := call.Args[0].(*ast.Ident); ok && id.Name == param {
					if out[pf.Pkg] == nil {
						out[pf.Pkg] = map[string]envHelper{}
					}
					out[pf.Pkg][fd.Name.Name] = envHelper{Param: param, Decl: fd}
				}
				return true
			})
		}
	}
	return out
}

// scanInputs scans one binary's closure and returns the flags it declares and
// the environment variables it reads, plus one defect per declaration or read
// the scan cannot resolve. pkgConsts carries the string constants of every
// package the reads may name.
func scanInputs(fset *token.FileSet, files []parsedFile, pkgConsts map[string]map[string]string) (binaryInputs, []string) {
	pkgNames := map[string]string{}
	for _, pf := range files {
		pkgNames[pf.Pkg] = pf.File.Name.Name
	}
	helpers := findEnvHelpers(files, pkgNames)
	var in binaryInputs
	var defects []string
	setNames := map[string]token.Position{}
	for _, pf := range files {
		c := &boundaryScanContext{
			fset:    fset,
			pf:      pf,
			imports: fileImports(pf.File, pkgNames),
			consts:  pkgConsts,
			helpers: helpers[pf.Pkg],
		}
		c.checkDotImports()
		flags, sets := c.scanFlags()
		reads := c.scanEnv()
		for _, s := range sets {
			if prev, dup := setNames[s.name]; dup {
				c.defect(s.pos, "a second flag set named %q; the first is at %s:%d, and the gate cannot tell their flags apart",
					s.name, prev.Filename, prev.Line)
				continue
			}
			setNames[s.name] = fset.Position(s.pos)
		}
		for i := range reads {
			for _, f := range flags {
				if reads[i].at >= f.DefaultSpan[0] && reads[i].at < f.DefaultSpan[1] {
					reads[i].BoundTo = f.key()
				}
			}
		}
		in.Flags = append(in.Flags, flags...)
		in.Env = append(in.Env, reads...)
		defects = append(defects, c.defects...)
	}
	defects = append(defects, duplicateFlagDefects(in.Flags)...)
	sort.SliceStable(in.Flags, func(i, j int) bool { return positionLess(in.Flags[i].Pos, in.Flags[j].Pos) })
	sort.SliceStable(in.Env, func(i, j int) bool { return positionLess(in.Env[i].Pos, in.Env[j].Pos) })
	return in, defects
}

// duplicateFlagDefects reports a flag declared twice on one set, which panics
// in the binary at startup.
func duplicateFlagDefects(flags []declaredFlag) []string {
	var defects []string
	seen := map[string]token.Position{}
	for _, f := range flags {
		if prev, dup := seen[f.key()]; dup {
			defects = append(defects, fmt.Sprintf("%s:%d: flag %s is declared again; the first declaration is at %s:%d",
				f.Pos.Filename, f.Pos.Line, f.key(), prev.Filename, prev.Line))
			continue
		}
		seen[f.key()] = f.Pos
	}
	return defects
}

func positionLess(a, b token.Position) bool {
	if a.Filename != b.Filename {
		return a.Filename < b.Filename
	}
	return a.Offset < b.Offset
}

// checkDotImports reports a dot import of a package the scan resolves by
// qualifier, because its declarations and reads would be unqualified.
func (c *boundaryScanContext) checkDotImports() {
	for _, imp := range c.pf.File.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil || imp.Name == nil || imp.Name.Name != "." {
			continue
		}
		if p == "flag" || p == "os" || p == "syscall" {
			c.defect(imp.Pos(), "dot import of %q; the gate resolves its declarations and reads by qualifier", p)
		}
	}
}

// boundSet is one flag.NewFlagSet bound to a local identifier.
type boundSet struct {
	name string
	pos  token.Pos
}

// flagSetBindings returns, for one function body, the identifiers bound to a
// flag.NewFlagSet call, the set name each carries, the identifier nodes that
// bind them, and the NewFlagSet calls the bindings accept.
func (c *boundaryScanContext) flagSetBindings(body *ast.BlockStmt, flagAlias string) (map[string]string, map[ast.Node]bool, map[*ast.CallExpr]bool, []boundSet) {
	bound := map[string]string{}
	binders := map[ast.Node]bool{}
	accepted := map[*ast.CallExpr]bool{}
	var sets []boundSet
	bind := func(id *ast.Ident, rhs ast.Expr) {
		call, ok := rhs.(*ast.CallExpr)
		if !ok {
			return
		}
		if _, ok := isPkgSelector(call.Fun, flagAlias, "NewFlagSet"); !ok {
			return
		}
		accepted[call] = true
		binders[id] = true
		if len(call.Args) == 0 {
			c.defect(call.Pos(), "flag.NewFlagSet without a name")
			return
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			c.defect(call.Pos(), "flag set name is not a string literal")
			return
		}
		name, err := strconv.Unquote(lit.Value)
		if err != nil {
			c.defect(call.Pos(), "flag set name %s does not unquote: %v", lit.Value, err)
			return
		}
		bound[id.Name] = name
		sets = append(sets, boundSet{name: name, pos: call.Pos()})
	}
	ast.Inspect(body, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.AssignStmt:
			if s.Tok == token.DEFINE && len(s.Lhs) == 1 && len(s.Rhs) == 1 {
				if id, ok := s.Lhs[0].(*ast.Ident); ok {
					bind(id, s.Rhs[0])
				}
			}
		case *ast.DeclStmt:
			gen, ok := s.Decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				return true
			}
			for _, spec := range gen.Specs {
				if vs, ok := spec.(*ast.ValueSpec); ok && len(vs.Names) == 1 && len(vs.Values) == 1 {
					bind(vs.Names[0], vs.Values[0])
				}
			}
		}
		return true
	})
	return bound, binders, accepted, sets
}

// scanFlags returns the flag declarations in one file and the named flag sets
// it binds. It walks every node, including package-level initializers.
func (c *boundaryScanContext) scanFlags() ([]declaredFlag, []boundSet) {
	flagAlias := importAlias(c.imports, "flag")
	if flagAlias == "" {
		return nil, nil
	}
	type funcScope struct {
		bound    map[string]string
		binders  map[ast.Node]bool
		accepted map[*ast.CallExpr]bool
	}
	scopes := map[*ast.FuncDecl]funcScope{}
	var sets []boundSet
	for _, decl := range c.pf.File.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok && fd.Body != nil {
			bound, binders, accepted, s := c.flagSetBindings(fd.Body, flagAlias)
			scopes[fd] = funcScope{bound, binders, accepted}
			sets = append(sets, s...)
		}
	}
	enclosing := func(stack []ast.Node) funcScope {
		for i := len(stack) - 1; i >= 0; i-- {
			if fd, ok := stack[i].(*ast.FuncDecl); ok {
				return scopes[fd]
			}
		}
		return funcScope{}
	}
	var flags []declaredFlag
	boundaryWalk(c.pf.File, func(n, parent ast.Node, stack []ast.Node) {
		scope := enclosing(stack)
		switch v := n.(type) {
		case *ast.Ident:
			if _, isBound := scope.bound[v.Name]; isBound && !scope.binders[v] {
				// A method call on the set is its one admitted use, so the
				// set's selector must be the function of a call: a method
				// value such as `decl := fs.String` hides the declarations
				// it later makes. The identifier as the selected name of
				// another selector is a field or method of something else.
				sel, ok := parent.(*ast.SelectorExpr)
				methodCall := ok && sel.X == v && len(stack) >= 2 && isCallFun(sel, stack[len(stack)-2])
				if !methodCall && (!ok || sel.Sel != v) {
					c.defect(v.Pos(), "flag set %q is reassigned, passed, returned, or stored; the gate cannot follow it", scope.bound[v.Name])
				}
			}
		case *ast.SelectorExpr:
			c.checkFlagSelector(v, parent, stack, flagAlias, scope.accepted)
		case *ast.CallExpr:
			if d, ok := c.flagDeclaration(v, flagAlias, scope.bound); ok {
				flags = append(flags, d)
			}
		}
	})
	return flags, sets
}

// checkFlagSelector reports a reference to the flag package the scan cannot
// follow: a FlagSet type, a CommandLine alias, an unbound NewFlagSet, or a
// declaration function used as a value.
func (c *boundaryScanContext) checkFlagSelector(sel *ast.SelectorExpr, parent ast.Node, stack []ast.Node, flagAlias string, accepted map[*ast.CallExpr]bool) {
	id, ok := sel.X.(*ast.Ident)
	if !ok || id.Name != flagAlias {
		return
	}
	switch name := sel.Sel.Name; name {
	case "FlagSet":
		c.defect(sel.Pos(), "flag.FlagSet type expression; a set of unknown origin cannot be named")
	case "CommandLine":
		outer, ok := parent.(*ast.SelectorExpr)
		allowed := ok && outer.X == sel && len(stack) >= 2 && isCallFun(outer, stack[len(stack)-2])
		if allowed {
			declares, _, _ := flagMethodShape(outer.Sel.Name)
			allowed = declares || outer.Sel.Name == "Parse"
		}
		if !allowed {
			c.defect(sel.Pos(), "flag.CommandLine used other than as the receiver of a declaration or Parse")
		}
	case "NewFlagSet":
		call, isCall := parent.(*ast.CallExpr)
		if !isCall || call.Fun != sel || !accepted[call] {
			c.defect(sel.Pos(), "flag.NewFlagSet not assigned directly to a local identifier")
		}
	default:
		if declares, _, _ := flagMethodShape(name); declares && !isCallFun(sel, parent) {
			c.defect(sel.Pos(), "flag.%s used as a value; the gate cannot see the declarations it makes", name)
		}
	}
}

// flagDeclaration returns the declaration a call makes, if it is one.
func (c *boundaryScanContext) flagDeclaration(call *ast.CallExpr, flagAlias string, bound map[string]string) (declaredFlag, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return declaredFlag{}, false
	}
	declares, nameArg, defaultArg := flagMethodShape(sel.Sel.Name)
	if !declares {
		return declaredFlag{}, false
	}
	set, recognized := "", false
	switch x := sel.X.(type) {
	case *ast.Ident:
		if x.Name == flagAlias {
			recognized = true
		} else if name, isBound := bound[x.Name]; isBound {
			set, recognized = name, true
		}
	case *ast.SelectorExpr:
		_, recognized = isPkgSelector(x, flagAlias, "CommandLine")
	}
	if !recognized {
		return declaredFlag{}, false
	}
	if nameArg >= len(call.Args) {
		c.defect(call.Pos(), "flag.%s call carries no name argument", sel.Sel.Name)
		return declaredFlag{}, false
	}
	lit, ok := call.Args[nameArg].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		c.defect(call.Pos(), "flag name is not a string literal")
		return declaredFlag{}, false
	}
	name, err := strconv.Unquote(lit.Value)
	if err != nil {
		c.defect(call.Pos(), "flag name %s does not unquote: %v", lit.Value, err)
		return declaredFlag{}, false
	}
	d := declaredFlag{Set: set, Name: name, Method: sel.Sel.Name, Default: opaqueDefault, Pos: c.fset.Position(call.Pos())}
	if defaultArg >= 0 && defaultArg < len(call.Args) {
		arg := call.Args[defaultArg]
		d.DefaultSpan = [2]token.Pos{arg.Pos(), arg.End()}
		if !opaqueFlagMethod(d.Method) {
			d.Default = c.canonicalDefault(arg)
		}
	}
	return d, true
}

// scanEnv returns the environment reads in one file.
func (c *boundaryScanContext) scanEnv() []envRead {
	osAlias := importAlias(c.imports, "os")
	syscallAlias := importAlias(c.imports, "syscall")
	helperBodies := map[*ast.BlockStmt]string{}
	for _, h := range c.helpers {
		helperBodies[h.Decl.Body] = h.Param
	}
	var reads []envRead
	boundaryWalk(c.pf.File, func(n, parent ast.Node, stack []ast.Node) {
		switch v := n.(type) {
		case *ast.SelectorExpr:
			if name, ok := isPkgSelector(v, osAlias, "ExpandEnv", "Expand"); ok {
				c.defect(v.Pos(), "os.%s reads variables the gate cannot name", name)
			}
			if name, ok := isPkgSelector(v, osAlias, "Getenv", "LookupEnv"); ok && !isCallFun(v, parent) {
				c.defect(v.Pos(), "os.%s used as a value; the gate cannot see the reads it makes", name)
			}
			if _, ok := isPkgSelector(v, syscallAlias, "Getenv"); ok && !isCallFun(v, parent) {
				c.defect(v.Pos(), "syscall.Getenv used as a value; the gate cannot see the reads it makes")
			}
		case *ast.Ident:
			if h, ok := c.helpers[v.Name]; ok && v != h.Decl.Name && !isCallFun(v, parent) {
				c.defect(v.Pos(), "env helper %s referenced other than by a direct call", v.Name)
			}
		case *ast.CallExpr:
			if r, ok := c.envReadAt(v, stack, helperBodies); ok {
				reads = append(reads, r)
			}
		}
	})
	for name, h := range c.helpers {
		if ast.IsExported(name) {
			c.defect(h.Decl.Pos(), "env helper %s is exported; its reads at call sites in other packages cannot be resolved", name)
		}
	}
	c.checkHelperParamWrites()
	return reads
}

// checkHelperParamWrites reports each env helper declared in this file whose
// body writes, rebinds, or takes the address of its name parameter. The scan
// records a helper's call-site argument as the variable read, which holds
// only while the parameter reaches the read unchanged: after
// `name = "P_" + name`, a call `h("X")` reads P_X while the gate records X.
func (c *boundaryScanContext) checkHelperParamWrites() {
	for _, decl := range c.pf.File.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		h, isHelper := c.helpers[fd.Name.Name]
		if !isHelper || h.Decl != fd {
			continue
		}
		for _, pos := range paramWrites(fd.Body, h.Param) {
			c.defect(pos, "env helper %s changes its parameter %s before the read; the gate cannot name the variable it reads", fd.Name.Name, h.Param)
		}
	}
}

// paramWrites returns the positions in body that assign to, rebind, or take
// the address of the identifier param.
func paramWrites(body *ast.BlockStmt, param string) []token.Pos {
	var out []token.Pos
	is := func(e ast.Expr) bool {
		id, ok := e.(*ast.Ident)
		return ok && id.Name == param
	}
	fieldsBind := func(fl *ast.FieldList) {
		if fl == nil {
			return
		}
		for _, f := range fl.List {
			for _, n := range f.Names {
				if n.Name == param {
					out = append(out, n.Pos())
				}
			}
		}
	}
	ast.Inspect(body, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range s.Lhs {
				if is(lhs) {
					out = append(out, lhs.Pos())
				}
			}
		case *ast.IncDecStmt:
			if is(s.X) {
				out = append(out, s.X.Pos())
			}
		case *ast.RangeStmt:
			if s.Key != nil && is(s.Key) {
				out = append(out, s.Key.Pos())
			}
			if s.Value != nil && is(s.Value) {
				out = append(out, s.Value.Pos())
			}
		case *ast.ValueSpec:
			for _, n := range s.Names {
				if n.Name == param {
					out = append(out, n.Pos())
				}
			}
		case *ast.UnaryExpr:
			if s.Op == token.AND && is(s.X) {
				out = append(out, s.X.Pos())
			}
		case *ast.FuncLit:
			fieldsBind(s.Type.Params)
			fieldsBind(s.Type.Results)
		}
		return true
	})
	return out
}

// envReadAt resolves the read a call makes, if it is one. A read of the
// helper's own parameter inside its body is skipped, because the helper's
// call sites are the reads.
func (c *boundaryScanContext) envReadAt(call *ast.CallExpr, stack []ast.Node, helperBodies map[*ast.BlockStmt]string) (envRead, bool) {
	_, isHelper := c.helperCall(call)
	if (!isHelper && !c.isGetenvFun(call.Fun)) || len(call.Args) == 0 {
		return envRead{}, false
	}
	arg := call.Args[0]
	var name string
	switch a := arg.(type) {
	case *ast.BasicLit:
		if a.Kind == token.STRING {
			name, _ = strconv.Unquote(a.Value)
		}
	case *ast.Ident:
		if !isHelper {
			for _, n := range stack {
				if body, ok := n.(*ast.BlockStmt); ok && helperBodies[body] == a.Name {
					return envRead{}, false
				}
			}
		}
		name = c.consts[c.pf.Pkg][a.Name]
	case *ast.SelectorExpr:
		if x, ok := a.X.(*ast.Ident); ok {
			if p, imported := c.imports[x.Name]; imported && strings.HasPrefix(p, boundaryModulePrefix) {
				name = c.consts[p][a.Sel.Name]
			}
		}
	}
	if name == "" {
		c.defect(call.Pos(), "environment read %s does not resolve to a string literal or constant", types.ExprString(arg))
		return envRead{}, false
	}
	return envRead{Name: name, Pos: c.fset.Position(call.Pos()), at: call.Pos()}, true
}

// scanInputsProducers reports each podspec.Inputs field that no composite
// literal in files keys. Files of the podspec package itself are skipped,
// because a producer is a caller of the builder.
func scanInputsProducers(files []parsedFile, inputsFields []string) []string {
	set := map[string]bool{}
	for _, pf := range files {
		if pf.Pkg == boundaryPodspecImport {
			continue
		}
		alias := importAlias(fileImports(pf.File, nil), boundaryPodspecImport)
		if alias == "" {
			continue
		}
		ast.Inspect(pf.File, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			if _, ok := isPkgSelector(lit.Type, alias, "Inputs"); !ok {
				return true
			}
			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					// A positional literal lists every field.
					for _, f := range inputsFields {
						set[f] = true
					}
					break
				}
				if id, ok := kv.Key.(*ast.Ident); ok {
					set[id.Name] = true
				}
			}
			return true
		})
	}
	var defects []string
	for _, f := range inputsFields {
		if !set[f] {
			defects = append(defects, fmt.Sprintf("podspec.Inputs.%s: no production composite literal sets it", f))
		}
	}
	return defects
}
