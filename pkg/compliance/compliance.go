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
	"encoding/json"
	"errors"
	"os/exec"
	"testing"
)

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

// RegisterAdapterUnderTest drives the conformance harness against
// the supplied Adapter and asserts every check at the declared
// level passes. A failure is reported through t.Errorf so the test
// surfaces every diagnostic, not only the first one.
//
// The harness binary path resolves as: opts.HarnessPath when set,
// otherwise `lenny-compliance` on $PATH. The harness is skipped
// (t.Skip) when the binary cannot be located; this is the same
// degradation pattern the other testinfra helpers use, so a
// third-party CI without lenny-compliance on PATH skips cleanly
// instead of failing.
func RegisterAdapterUnderTest(t *testing.T, a Adapter, opts Options) Report {
	t.Helper()
	if a == nil {
		t.Fatal("compliance.RegisterAdapterUnderTest: adapter is nil")
	}
	binary := a.BinaryPath()
	if binary == "" {
		t.Fatal("compliance.RegisterAdapterUnderTest: adapter BinaryPath is empty")
	}
	level := a.DeclaredLevel()
	if level != LevelBasic && level != LevelStandard && level != LevelFull {
		t.Fatalf("compliance.RegisterAdapterUnderTest: adapter declared an unknown level %q", level)
	}

	harness := opts.HarnessPath
	if harness == "" {
		path, err := exec.LookPath("lenny-compliance")
		if err != nil {
			t.Skipf("compliance.RegisterAdapterUnderTest: lenny-compliance not on PATH (%v); install per TESTING.md §12.10 or set Options.HarnessPath", err)
		}
		harness = path
	}

	args := []string{"--binary", binary, "--level", string(level), "--json"}
	if opts.Verbose {
		args = append(args, "--verbose")
	}
	cmd := exec.Command(harness, args...)
	out, runErr := cmd.Output()
	// A non-zero exit signals "one or more checks failed"; the report
	// body is still emitted, so a missing body is the real failure
	// signal.
	if len(out) == 0 {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			t.Fatalf("compliance.RegisterAdapterUnderTest: harness emitted no JSON (exit=%d): %s", exitErr.ExitCode(), exitErr.Stderr)
		}
		t.Fatalf("compliance.RegisterAdapterUnderTest: harness emitted no JSON: %v", runErr)
	}
	var report Report
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("compliance.RegisterAdapterUnderTest: decode report: %v\nraw: %s", err, out)
	}
	if report.Summary.Passed+report.Summary.Failed != report.Summary.Total {
		t.Errorf("compliance: report summary inconsistent: passed=%d failed=%d total=%d", report.Summary.Passed, report.Summary.Failed, report.Summary.Total)
	}
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
