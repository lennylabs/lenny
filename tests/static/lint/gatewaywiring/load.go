// SPDX-License-Identifier: MIT

package gatewaywiring

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Program is one package of the module, parsed and type-checked against
// the export data of its dependencies.
type Program struct {
	Module string
	Fset   *token.FileSet
	Files  []*ast.File
	Pkg    *types.Package
	Info   *types.Info
}

type listed struct {
	ImportPath string
	Dir        string
	Export     string
	GoFiles    []string
	Module     *struct{ Path string }
}

// LoadProgram lists the package at pattern in the module rooted at dir with its
// dependencies' export data, parses its non-test files, and type-checks
// them. It needs a module whose packages compile.
func LoadProgram(dir, pattern string) (*Program, error) {
	out, err := goCommand(dir, "list", "-e", "-export", "-deps", "-json=ImportPath,Dir,Export,GoFiles,Module", pattern)
	if err != nil {
		return nil, err
	}
	exports := map[string]string{}
	var target *listed
	dec := json.NewDecoder(bytes.NewReader([]byte(out)))
	for {
		var p listed
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("decode go list output: %w", err)
		}
		exports[p.ImportPath] = p.Export
		q := p
		target = &q // go list -deps prints the named package last
	}
	if target == nil {
		return nil, fmt.Errorf("go list printed no package for %s", pattern)
	}
	if target.Module == nil {
		return nil, fmt.Errorf("%s is not inside a module", pattern)
	}
	fset := token.NewFileSet()
	prog := &Program{Module: target.Module.Path, Fset: fset}
	for _, name := range target.GoFiles {
		f, err := parser.ParseFile(fset, filepath.Join(target.Dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		prog.Files = append(prog.Files, f)
	}
	lookup := func(path string) (io.ReadCloser, error) {
		file, ok := exports[path]
		if !ok || file == "" {
			return nil, fmt.Errorf("no export data for %s", path)
		}
		return os.Open(file)
	}
	prog.Info = &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Uses:       map[*ast.Ident]types.Object{},
		Defs:       map[*ast.Ident]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
	}
	conf := types.Config{Importer: importer.ForCompiler(fset, "gc", lookup)}
	pkg, err := conf.Check(target.ImportPath, fset, prog.Files, prog.Info)
	if err != nil {
		return nil, fmt.Errorf("type-check %s: %w", target.ImportPath, err)
	}
	prog.Pkg = pkg
	return prog, nil
}

// Rel returns the repository-relative form of an import path in the
// module, or false for a path outside it.
func (p *Program) Rel(importPath string) (string, bool) {
	if importPath == p.Module {
		return "", true
	}
	return strings.CutPrefix(importPath, p.Module+"/")
}

func goCommand(dir string, args ...string) (string, error) {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOWORK=off", "GOFLAGS=")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("go %s in %s: %w\n%s", strings.Join(args, " "), dir, err, stderr.String())
	}
	return stdout.String(), nil
}
