// SPDX-License-Identifier: MIT

package seam

import (
	"bufio"
	"errors"
	"fmt"
	"go/build"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSeamInterfaceDependenciesExcludeConfinedTenancyCredentialAndSessionStore
// checks that no package this package links, directly or through another
// package, is a tenancy package, a credential package, or the session
// store. Every part of the gateway imports this package, so a package it
// links is linked into every part, including the parts that may not import
// the confined tenant store or credential packages. The session row the
// session record carries comes from pkg/session/sessionrow for this reason:
// the session store imports the tenant and user stores, and the tenant store
// imports pkg/credential. The layout gate refuses only direct imports, so
// this test holds the indirect reach of this one package.
//
// The test reads the import graph from the source with go/build for each
// target operating system the gateway builds for, without cgo, as the gate
// builds it. It asserts that the closure includes the session row package,
// so a walk that stopped early cannot pass.
//
// spec: GW-R2
func TestSeamInterfaceDependenciesExcludeConfinedTenancyCredentialAndSessionStore(t *testing.T) {
	root, module, err := moduleRoot()
	if err != nil {
		t.Fatal(err)
	}
	seamPath := module + "/pkg/gateway/seam"
	forbidden := []string{
		module + "/pkg/tenancy",
		module + "/pkg/credential",
		module + "/pkg/session/sessionstore",
	}
	for _, goos := range []string{"linux", "darwin"} {
		t.Run(goos, func(t *testing.T) {
			ctxt := build.Default
			ctxt.GOOS = goos
			ctxt.CgoEnabled = false
			closure, err := importClosure(ctxt, root, module, seamPath)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := closure[module+"/pkg/session/sessionrow"]; !ok {
				t.Fatalf("the closure of %s does not include the session row package; the walk is incomplete: %v", seamPath, keys(closure))
			}
			for p, via := range closure {
				for _, f := range forbidden {
					if p == f || strings.HasPrefix(p, f+"/") {
						t.Errorf("%s links %s (imported by %s)", seamPath, p, via)
					}
				}
			}
		})
	}
}

// importClosure returns every package of the module that start imports,
// directly or through another package, mapped to a package that imports
// it; start maps to itself. It follows only imports inside the module: a
// package outside the module cannot import one inside it, so the module's
// packages a closure reaches are all reached through module packages.
// Test files are left out, because they are not linked into a binary.
func importClosure(ctxt build.Context, root, module, start string) (map[string]string, error) {
	closure := map[string]string{start: start}
	queue := []string{start}
	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]
		rel := strings.TrimPrefix(strings.TrimPrefix(path, module), "/")
		pkg, err := ctxt.ImportDir(filepath.Join(root, filepath.FromSlash(rel)), 0)
		if err != nil {
			return nil, fmt.Errorf("read package %s for %s: %w", path, ctxt.GOOS, err)
		}
		for _, imp := range pkg.Imports {
			if imp != module && !strings.HasPrefix(imp, module+"/") {
				continue
			}
			if _, seen := closure[imp]; seen {
				continue
			}
			closure[imp] = path
			queue = append(queue, imp)
		}
	}
	return closure, nil
}

// moduleRoot returns the directory holding go.mod, found by walking up from
// the working directory, and the module path go.mod declares.
func moduleRoot() (root, module string, err error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", "", err
	}
	for {
		f, err := os.Open(filepath.Join(dir, "go.mod"))
		if err == nil {
			defer f.Close()
			sc := bufio.NewScanner(f)
			for sc.Scan() {
				if m, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "module "); ok {
					return dir, strings.Trim(strings.TrimSpace(m), `"`), nil
				}
			}
			if err := sc.Err(); err != nil {
				return "", "", fmt.Errorf("read %s: %w", filepath.Join(dir, "go.mod"), err)
			}
			return "", "", fmt.Errorf("%s declares no module", filepath.Join(dir, "go.mod"))
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", "", err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", errors.New("no go.mod above the working directory")
		}
		dir = parent
	}
}

// keys returns the packages of a closure, for a failure message.
func keys(closure map[string]string) []string {
	out := make([]string, 0, len(closure))
	for p := range closure {
		out = append(out, p)
	}
	return out
}
