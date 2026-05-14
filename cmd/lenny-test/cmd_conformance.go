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

	// Locate cmd/lenny-compliance.
	if _, err := exec.LookPath("lenny-compliance"); err != nil {
		fmt.Fprintf(os.Stderr,
			"conformance: lenny-compliance not on PATH.\n"+
				"  Per TESTING.md §12.10, cmd/lenny-compliance ships the conformance harness.\n"+
				"  Build it with `go build -o lenny-compliance ./cmd/lenny-compliance` once the\n"+
				"  binary lands in the repository.\n")
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
