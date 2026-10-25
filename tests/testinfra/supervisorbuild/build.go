// SPDX-License-Identifier: MIT

// Package supervisorbuild builds the runtime supervisor's C sources for
// the tests that check them: the tier-0 binary check and the tier-1
// driver of the C unit harness. Both build through supervisor/Makefile
// into a temporary directory, so no test writes into the source tree.
//
// The supervisor is a freestanding C program rather than a Go package,
// so its sources sit outside every Go package directory and this package
// carries no cgo.
package supervisorbuild

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Tool names the build needs on PATH.
var requiredTools = []string{"make", "cc"}

// ErrToolchainMissing reports that a tool the build needs does not
// resolve. A caller records an unverified verdict or a skip with the
// wrapped detail rather than a failure, because the host, not the
// supervisor, lacks something.
var ErrToolchainMissing = errors.New("supervisor toolchain missing")

// LookPath resolves a tool; tests replace it to exercise the missing-tool
// path without altering the host.
type LookPath func(name string) (string, error)

// CheckToolchain returns an error wrapping ErrToolchainMissing that names
// every required tool look cannot resolve, or nil.
func CheckToolchain(look LookPath) error {
	var missing []string
	for _, name := range requiredTools {
		if _, err := look(name); err != nil {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: %s not on PATH", ErrToolchainMissing, strings.Join(missing, ", "))
	}
	return nil
}

// Options select what Build makes.
type Options struct {
	// RepoRoot is the repository root that holds supervisor/.
	RepoRoot string
	// Out is the directory the build writes its binaries into.
	Out string
	// TestPrefix, when set, is the directory under which the harness
	// build places the supervisor's platform volume paths.
	TestPrefix string
	// Targets are the supervisor/Makefile targets; empty builds the
	// shipped binary alone.
	Targets []string
}

// Build runs supervisor/Makefile with the options and returns its
// combined output with any error.
func Build(ctx context.Context, opt Options) (string, error) {
	if err := CheckToolchain(exec.LookPath); err != nil {
		return "", err
	}
	args := []string{"-C", filepath.Join(opt.RepoRoot, "supervisor"), "OUT=" + opt.Out}
	if opt.TestPrefix != "" {
		args = append(args, "TEST_PREFIX="+opt.TestPrefix)
	}
	args = append(args, opt.Targets...)
	out, err := exec.CommandContext(ctx, "make", args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("make %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

// BinaryPath is the path of the shipped supervisor binary in out.
func BinaryPath(out string) string {
	return filepath.Join(out, "lenny-supervisor")
}
