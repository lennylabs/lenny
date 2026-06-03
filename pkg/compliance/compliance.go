// SPDX-License-Identifier: MIT

// Package compliance is the importable third-party entry point to
// the §12.10 / §15.4 conformance harness. A third-party runtime
// project that ships its own adapter binary embeds this package in
// its test suite and calls RegisterAdapterUnderTest from a single
// `go test` target. The helper shells out to the `lenny-compliance`
// binary against the registered adapter's path and asserts every
// check in the declared integration level passes.
//
// The harness implementation itself lives in cmd/lenny-compliance.
// This package is the import-side facade so a downstream project
// does not depend on the binary's main package.
package compliance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"testing"
)

// ErrHarnessNotFound is returned by RunSuite when the lenny-compliance
// harness binary cannot be located (Options.HarnessPath unset and the
// binary is not on $PATH). Callers that drive the suite outside of a
// test — notably the gateway's §24.8 validate handler — match this
// sentinel to distinguish "the validation gate cannot run here" (which
// must leave the adapter in pending_validation rather than fail it)
// from a genuine conformance failure.
//
// spec: §24.8 line 113; §15 line 1414 (the registration gate runs the
// suite in a sandboxed environment).
var ErrHarnessNotFound = errors.New("compliance: lenny-compliance harness not found")

// Level is the §15.4 integration level the adapter declares.
type Level string

const (
	LevelBasic    Level = "basic"
	LevelStandard Level = "standard"
	LevelFull     Level = "full"
)

// IsValid reports whether l is one of the three documented §15.4
// integration levels.
func (l Level) IsValid() bool {
	return l == LevelBasic || l == LevelStandard || l == LevelFull
}

// Adapter is the contract a third-party runtime implements to
// register itself against the conformance harness. The runtime test
// constructs an Adapter (typically by building its binary in
// TestMain) and passes it to RegisterAdapterUnderTest.
type Adapter interface {
	// BinaryPath returns the absolute path to the adapter binary
	// under test. The harness drives it via stdin/stdout per §15.4.
	BinaryPath() string

	// DeclaredLevel returns the integration level the adapter
	// claims. The harness runs the matching battery and fails any
	// check the adapter does not satisfy.
	DeclaredLevel() Level
}

// Options tunes a RegisterAdapterUnderTest run. The zero value is
// usable: the harness binary is resolved from $PATH, the per-check
// timeout matches the harness default, and verbose output is off.
type Options struct {
	// HarnessPath overrides the path to the lenny-compliance binary.
	// When empty the helper looks up `lenny-compliance` on PATH.
	HarnessPath string

	// Verbose passes --verbose to the harness so stdin/stdout traces
	// appear alongside the JSON report.
	Verbose bool
}

// Report mirrors the §12.10 JSON document the harness emits with
// --json. The fields the assertion path reads are exported; unknown
// fields on the report are ignored.
type Report struct {
	Harness string  `json:"harness"`
	Binary  string  `json:"binary"`
	Level   string  `json:"level"`
	Checks  []Check `json:"checks"`
	Summary Summary `json:"summary"`
}

// Check is one row in the conformance report.
type Check struct {
	Name   string `json:"name"`
	Spec   string `json:"spec"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail"`
}

// Summary is the aggregate count over Checks.
type Summary struct {
	Total  int `json:"total"`
	Passed int `json:"passed"`
	Failed int `json:"failed"`
}

// RunSuite drives the conformance harness against the supplied Adapter
// and returns the parsed Report. It does not depend on the testing
// package, so it is the entry point the gateway's §24.8
// `POST /v1/admin/external-adapters/{name}/validate` handler uses to
// run the suite server-side and translate the Report into the wire
// response. A non-zero failure count in the returned Report is NOT an
// error: it is a passing run that found conformance violations. RunSuite
// returns a non-nil error only when the suite could not be executed at
// all (adapter unusable, harness missing, harness produced no report).
//
// The harness binary path resolves as: opts.HarnessPath when set,
// otherwise `lenny-compliance` on $PATH. When the binary cannot be
// located RunSuite returns ErrHarnessNotFound (wrapped) so callers can
// distinguish "cannot validate here" from "validation failed".
//
// spec: §24.8 line 113 (the validate command runs the
// RegisterAdapterUnderTest compliance suite); §15 line 1414 (the
// registration gate runs the suite in a sandboxed environment and
// transitions status on the result).
func RunSuite(ctx context.Context, a Adapter, opts Options) (Report, error) {
	if a == nil {
		return Report{}, errors.New("compliance.RunSuite: adapter is nil")
	}
	binary := a.BinaryPath()
	if binary == "" {
		return Report{}, errors.New("compliance.RunSuite: adapter BinaryPath is empty")
	}
	level := a.DeclaredLevel()
	if !level.IsValid() {
		return Report{}, fmt.Errorf("compliance.RunSuite: adapter declared an unknown level %q", level)
	}

	harness := opts.HarnessPath
	if harness == "" {
		path, err := exec.LookPath("lenny-compliance")
		if err != nil {
			return Report{}, fmt.Errorf("%w: %v", ErrHarnessNotFound, err)
		}
		harness = path
	}

	args := []string{"--binary", binary, "--level", string(level), "--json"}
	if opts.Verbose {
		args = append(args, "--verbose")
	}
	cmd := exec.CommandContext(ctx, harness, args...)
	out, runErr := cmd.Output()
	// A non-zero exit signals "one or more checks failed"; the report
	// body is still emitted, so a missing body is the real failure
	// signal.
	if len(out) == 0 {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			return Report{}, fmt.Errorf("compliance.RunSuite: harness emitted no JSON (exit=%d): %s", exitErr.ExitCode(), exitErr.Stderr)
		}
		return Report{}, fmt.Errorf("compliance.RunSuite: harness emitted no JSON: %w", runErr)
	}
	var report Report
	if err := json.Unmarshal(out, &report); err != nil {
		return Report{}, fmt.Errorf("compliance.RunSuite: decode report: %w\nraw: %s", err, out)
	}
	if report.Summary.Passed+report.Summary.Failed != report.Summary.Total {
		return report, fmt.Errorf("compliance.RunSuite: report summary inconsistent: passed=%d failed=%d total=%d", report.Summary.Passed, report.Summary.Failed, report.Summary.Total)
	}
	return report, nil
}

// RegisterAdapterUnderTest drives the conformance harness against
// the supplied Adapter and asserts every check at the declared
// level passes. A failure is reported through t.Errorf so the test
// surfaces every diagnostic, not only the first one. It is the thin
// testing-package wrapper over RunSuite; the registration logic itself
// lives in RunSuite so it can be reused outside of `go test`.
//
// The harness is skipped (t.Skip) when the binary cannot be located;
// this is the same degradation pattern the other testinfra helpers use,
// so a third-party CI without lenny-compliance on PATH skips cleanly
// instead of failing.
func RegisterAdapterUnderTest(t *testing.T, a Adapter, opts Options) Report {
	t.Helper()
	report, err := RunSuite(context.Background(), a, opts)
	if err != nil {
		if errors.Is(err, ErrHarnessNotFound) {
			t.Skipf("compliance.RegisterAdapterUnderTest: lenny-compliance not on PATH (%v); install per TESTING.md §12.10 or set Options.HarnessPath", err)
		}
		// Summary-inconsistency surfaces both a non-nil error and a
		// populated report; treat it as a hard failure here.
		if report.Summary.Total != 0 {
			t.Errorf("compliance: %v", err)
		} else {
			t.Fatalf("compliance.RegisterAdapterUnderTest: %v", err)
		}
	}
	binary := a.BinaryPath()
	level := a.DeclaredLevel()
	if report.Summary.Failed != 0 {
		for _, c := range report.Checks {
			if !c.Pass {
				t.Errorf("compliance: %s level %s check %q failed (spec %s): %s", binary, level, c.Name, c.Spec, c.Detail)
			}
		}
	}
	if report.Summary.Total == 0 {
		t.Errorf("compliance: %s level %s battery ran no checks", binary, level)
	}
	return report
}

// staticAdapter is a small Adapter for ad-hoc one-off registrations.
type staticAdapter struct {
	binary string
	level  Level
}

func (s staticAdapter) BinaryPath() string   { return s.binary }
func (s staticAdapter) DeclaredLevel() Level { return s.level }

// NewAdapter builds a minimal Adapter that returns the supplied
// binary path and integration level. Test code that only needs the
// two values constructs an Adapter via NewAdapter rather than
// defining its own struct.
func NewAdapter(binaryPath string, level Level) Adapter {
	return staticAdapter{binary: binaryPath, level: level}
}

// Static type assertion so a build-time failure surfaces if the
// helper's contract drifts.
var _ Adapter = staticAdapter{}
