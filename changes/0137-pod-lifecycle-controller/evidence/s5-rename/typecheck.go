// SPDX-License-Identifier: MIT

//go:build ignore

// Command typecheck loads every package of the module at the directory
// given first, with its tests, and prints each type error once.
// fieldfix.py reads its output to rename the struct fields of 0137.S5 at
// the sites the type checker reports. It runs in a module of its own so the
// Lenny module takes no dependency on golang.org/x/tools:
//
//	mkdir /tmp/tc && cp typecheck.go /tmp/tc/main.go && cd /tmp/tc
//	go mod init tc && go get golang.org/x/tools@v0.45.0 && go mod tidy
//	go run . <repository root> ./... > /tmp/errs.txt
package main

import (
	"fmt"
	"os"

	"golang.org/x/tools/go/packages"
)

func main() {
	cfg := &packages.Config{
		Mode:  packages.NeedName | packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedDeps | packages.NeedImports,
		Tests: true,
		Dir:   os.Args[1],
		Env:   append(os.Environ(), "CGO_ENABLED=0"),
	}
	pkgs, err := packages.Load(cfg, os.Args[2:]...)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	seen := map[string]bool{}
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		for _, e := range p.Errors {
			s := e.Error()
			if !seen[s] {
				seen[s] = true
				fmt.Println(s)
			}
		}
	})
}
