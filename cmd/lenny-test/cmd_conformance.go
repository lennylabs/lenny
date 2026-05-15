// SPDX-License-Identifier: MIT

package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
)

// runConformanceSubcommand invokes the conformance harness against a
// third-party runtime image. Per TESTING.md §6 and §12.10:
//
//	lenny-test conformance --image <runtime-image> --level <basic|standard|full>
//
// Today this is a thin shim: it dispatches to cmd/lenny-compliance
// (which is the standalone conformance binary). When
// lenny-compliance is not yet built the subcommand reports a clear
// "not yet shipped" diagnosis pointing at TESTING.md §12.10.
func runConformanceSubcommand(args []string) int {
	fs := flag.NewFlagSet("conformance", flag.ExitOnError)
	image := fs.String("image", "", "OCI image reference for the runtime under test")
	level := fs.String("level", "basic", "conformance level: basic | standard | full")
	jsonOut := fs.Bool("json", false, "emit machine-readable JSON output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *image == "" {
		fmt.Fprintln(os.Stderr, "conformance: --image is required")
		return 2
	}
	if !validLevel(*level) {
		fmt.Fprintf(os.Stderr, "conformance: --level must be basic | standard | full, got %q\n", *level)
		return 2
	}

	// Locate cmd/lenny-compliance. The harness exists today as a
	// stub; the full Standard-and-Full battery ships in the Phase
	// 2.8 deliverable per TESTING.md §13. Report that staging
	// explicitly so users don't think the missing binary is an
	// install bug.
	if _, err := exec.LookPath("lenny-compliance"); err != nil {
		fmt.Fprintf(os.Stderr,
			"conformance: lenny-compliance is not yet available.\n"+
				"  The conformance harness ships in TESTING.md §13.x phases 2 (Basic), 2.8 (Full),\n"+
				"  and 9 (Standard via delegation-echo). Build the in-repo skeleton with\n"+
				"  `go build -o $(go env GOPATH)/bin/lenny-compliance ./cmd/lenny-compliance`\n"+
				"  and rerun. Run `make install` to set this up alongside lenny-test.\n")
		return 1
	}

	cargs := []string{"--image", *image, "--level", *level}
	if *jsonOut {
		cargs = append(cargs, "--json")
	}
	cmd := exec.Command("lenny-compliance", cargs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return 1
	}
	return 0
}

func validLevel(level string) bool {
	switch level {
	case "basic", "standard", "full":
		return true
	}
	return false
}
