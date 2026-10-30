// SPDX-License-Identifier: MIT

// Package supervisor drives the runtime supervisor's C unit harness and
// fuzz driver from go test. The C sources live under supervisor/, which
// holds no Go file, so go vet and go test never compile them as cgo; this
// package builds them with supervisor/Makefile into a temporary directory
// and runs each harness case as a subtest.
package supervisor

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
	"github.com/lennylabs/lenny/tests/testinfra/supervisorbuild"
)

const (
	buildTimeout = 3 * time.Minute
	// caseTimeout bounds one harness case. A case that hangs, such as a
	// cleanup worker that waits past its deadline, fails at this bound.
	caseTimeout = 30 * time.Second
	fuzzTimeout = 2 * time.Minute
	// caseSkipStatus is the harness's exit status for a case whose host
	// lacks what it needs.
	caseSkipStatus       = 77
	fuzzParserIterations = "200000"
	fuzzWalkerIterations = "300"
	fuzzSeed             = "20261023"
)

// harnessBuild builds the harness, its probe, the fuzz driver, and both
// supervisor binaries once per test binary run.
func harnessBuild(t *testing.T) (bin, prefix string) {
	t.Helper()
	if err := supervisorbuild.CheckToolchain(exec.LookPath); err != nil {
		t.Skipf("blocked: the runtime supervisor's C harness needs a C toolchain: %v", err)
	}
	root := t.TempDir()
	bin = filepath.Join(root, "bin")
	prefix = filepath.Join(root, "prefix")
	if err := os.MkdirAll(prefix, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), buildTimeout)
	defer cancel()
	log, err := supervisorbuild.Build(ctx, supervisorbuild.Options{
		RepoRoot:   schematest.RepoRoot(t),
		Out:        bin,
		TestPrefix: prefix,
		Targets:    []string{"harness"},
	})
	if err != nil {
		t.Fatalf("build the supervisor harness: %v\n%s", err, log)
	}
	return bin, prefix
}

// runBounded runs a command in its own process group and kills the group
// when the timeout ends, so a hung case or anything it started cannot
// outlive the test.
func runBounded(timeout time.Duration, name string, args ...string) (string, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	if ctx.Err() != nil {
		return out.String(), -1, ctx.Err()
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return out.String(), exit.ExitCode(), nil
	}
	if err != nil {
		return out.String(), -1, err
	}
	return out.String(), 0, nil
}

// spec: 4.7.10 (Deployment Model), 4.7.11 (Adapter-Agent Security Boundary), 28.5.3 (Intra-pod)
//
// Each subtest is one case of supervisor/test: the wire parser and reply
// templates of the CH-SUPERVISE card, one case per Children table cell and
// Signals table row, the end with its pids.current read, the sweep and
// cleanup workers, the launch environment, EINTR injection into every
// wrapped call, the signal mask and descriptors read from each launched
// child, and the freestanding binary run as the init of a scratch PID
// namespace with the test as its adapter.
func TestRuntimeSupervisorCHarness_spec_4_7_10(t *testing.T) {
	bin, _ := harnessBuild(t)
	harness := filepath.Join(bin, "harness")
	out, code, err := runBounded(caseTimeout, harness, "--list")
	if err != nil || code != 0 {
		t.Fatalf("harness --list: exit %d, %v\n%s", code, err, out)
	}
	cases := strings.Fields(out)
	if len(cases) == 0 {
		t.Fatal("the harness lists no case")
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			out, code, err := runBounded(caseTimeout, harness, "--run", name)
			switch {
			case err != nil:
				t.Fatalf("case did not finish within %s: %v\n%s", caseTimeout, err, out)
			case code == caseSkipStatus:
				t.Skipf("blocked: %s", strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(out), "blocked:")))
			case code != 0:
				t.Fatalf("case failed (exit %d):\n%s", code, out)
			}
		})
	}
}

// spec: 28.5.3 (Intra-pod), 4.7.10 (Deployment Model)
//
// The bounded fuzz driver: mutated request lines through the line reader
// and the strict parser, and random trees through the sweep walker, which
// must follow no symbolic link, keep slots/, and stop at its bounds. The
// driver is built with the address and undefined-behaviour sanitizers
// where the toolchain runs them.
func TestRuntimeSupervisorBoundedFuzz_spec_28_5_3(t *testing.T) {
	bin, prefix := harnessBuild(t)
	fuzz := filepath.Join(bin, "fuzz")
	t.Run("parser", func(t *testing.T) {
		out, code, err := runBounded(fuzzTimeout, fuzz, "parser", fuzzParserIterations, fuzzSeed)
		if err != nil || code != 0 {
			t.Fatalf("fuzz parser: exit %d, %v\n%s", code, err, out)
		}
	})
	t.Run("walker", func(t *testing.T) {
		dir := filepath.Join(prefix, "fuzz-walker")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		out, code, err := runBounded(fuzzTimeout, fuzz, "walker", fuzzWalkerIterations, fuzzSeed, dir)
		if err != nil || code != 0 {
			t.Fatalf("fuzz walker: exit %d, %v\n%s", code, err, out)
		}
	})
}
