// SPDX-License-Identifier: MIT

package runtimescaffold

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/lennylabs/lenny/pkg/compliance"
)

// ValidateExit codes for `lenny runtime validate` (§24.18).
const (
	// ValidateOK indicates the repository passed every static check.
	ValidateOK = 0
	// ValidateFailed indicates one or more static checks failed.
	ValidateFailed = 1
	// ValidateUsage indicates the path argument could not be read.
	ValidateUsage = 2
)

// runtimeManifest is the subset of the §5.1 Runtime definition that
// `lenny runtime validate` inspects. The full schema is enforced by the
// gateway at registration; the static validator checks the fields a
// repository author can get wrong before publishing.
type runtimeManifest struct {
	Name             string         `json:"name"`
	BaseRuntime      string         `json:"baseRuntime"`
	Image            string         `json:"image"`
	Type             string         `json:"type"`
	IntegrationLevel string         `json:"integrationLevel"`
	ExecutionMode    string         `json:"executionMode"`
	IsolationProfile string         `json:"isolationProfile"`
	Capabilities     map[string]any `json:"capabilities"`
	Limits           map[string]any `json:"limits"`
}

// validIntegrationLevels is the closed set of §5.1 integration levels.
var validIntegrationLevels = map[string]bool{
	"basic": true, "standard": true, "full": true,
}

// validRuntimeTypes is the closed set of §5.1 runtime types.
var validRuntimeTypes = map[string]bool{
	"agent": true, "mcp": true,
}

// ValidateOptions configures a `lenny runtime validate` run.
type ValidateOptions struct {
	// Path is the runtime repository root. Empty defaults to ".".
	Path string

	// BinaryPath, when set, names a locally-built adapter binary to run
	// the §15.4.6 declared-vs-observed conformance probe against. When
	// empty the validator runs static checks only and the observed level
	// is reported as "not probed".
	BinaryPath string

	// ReportPath, when set, names a file to write the machine-readable
	// JSON validation report to (§15.4.6 `--report <path>`).
	ReportPath string

	// HarnessPath overrides the lenny-compliance binary location for the
	// observed-level probe. Empty resolves the harness from $PATH. It is
	// an internal seam used by tests; the CLI does not expose it.
	HarnessPath string
}

// ValidateReport is the machine-readable document `lenny runtime validate
// --report` writes. It carries the static-validation result and, when a
// binary probe ran, the §15.4.6 declared-vs-observed reconciliation and
// the full conformance battery (the same document cmd/lenny-compliance
// --json emits).
type ValidateReport struct {
	Repository       string             `json:"repository"`
	ChecksPerformed  []string           `json:"checksPerformed"`
	Findings         []string           `json:"findings"`
	DeclaredLevel    string             `json:"declaredLevel"`
	IntegrationLevel *ObservedResult    `json:"integrationLevel,omitempty"`
	Conformance      *compliance.Report `json:"conformance,omitempty"`
	Result           string             `json:"result"`
}

// Validate runs `lenny runtime validate`. It always runs the static
// checks against the runtime.yaml contract (§5.1) and repository layout
// (§15.4). When opts.BinaryPath names a locally-built adapter binary it
// additionally runs the §15.4.6 declared-vs-observed probe: the full
// conformance battery is executed, the observed level is derived from the
// gating checks, and the command exits non-zero when the runtime
// under-performs its declared level or fails a category at or below it.
//
// It returns one of the Validate* codes.
//
// spec: §24.18 line 231 (declared-vs-observed reconciliation); §15.4.6
// (conformance categories and the observed-level algorithm).
func Validate(opts ValidateOptions, stdout, stderr io.Writer) int {
	path := opts.Path
	if path == "" {
		path = "."
	}
	info, err := os.Stat(path)
	if err != nil {
		fmt.Fprintf(stderr, "lenny runtime validate: %v\n", err)
		return ValidateUsage
	}
	if !info.IsDir() {
		fmt.Fprintf(stderr, "lenny runtime validate: %s is not a directory\n", path)
		return ValidateUsage
	}

	var findings []string
	var checks []string

	manifestPath := filepath.Join(path, "runtime.yaml")
	manifest, manifestErrs := loadManifest(manifestPath)
	checks = append(checks, "runtime.yaml is present and parses as YAML")
	findings = append(findings, manifestErrs...)

	if manifest != nil {
		findings = append(findings, checkManifest(manifest)...)
		checks = append(
			checks,
			"runtime.yaml declares a valid name, type, and integrationLevel",
			"runtime.yaml declares the adapter contract fields for its type",
		)
	}

	findings = append(findings, checkLayout(path)...)
	checks = append(checks, "repository carries a Dockerfile")

	declared := "basic"
	if manifest != nil && manifest.IntegrationLevel != "" {
		declared = manifest.IntegrationLevel
	}
	sort.Strings(findings)

	report := ValidateReport{
		Repository:      path,
		ChecksPerformed: checks,
		Findings:        findings,
		DeclaredLevel:   declared,
	}
	exit := ValidateOK
	if len(findings) > 0 {
		exit = ValidateFailed
	}

	// Static-validation section.
	fmt.Fprintf(stdout, "Runtime repository: %s\n", path)
	fmt.Fprintln(stdout, "Checks performed:")
	for _, c := range checks {
		fmt.Fprintf(stdout, "  - %s\n", c)
	}
	if len(findings) > 0 {
		fmt.Fprintf(stdout, "Static issues (%d):\n", len(findings))
		for _, f := range findings {
			fmt.Fprintf(stdout, "  - %s\n", f)
		}
	}
	if manifest != nil {
		fmt.Fprintf(stdout, "Declared integration level: %s\n", declared)
	}

	// Observed-level reconciliation (§15.4.6).
	if opts.BinaryPath == "" {
		fmt.Fprintln(stdout,
			"Observed integration level: not probed. Pass --binary <path> to a "+
				"locally-built adapter binary to run the §15.4.6 declared-vs-observed "+
				"reconciliation.")
	} else {
		obs, perr := probeObservedLevel(context.Background(), opts.BinaryPath, declared, opts.HarnessPath)
		switch {
		case errors.Is(perr, compliance.ErrHarnessNotFound):
			fmt.Fprintln(stdout,
				"Observed integration level: not probed — the lenny-compliance harness "+
					"was not found on PATH. Install it (TESTING.md) to run the §15.4.6 "+
					"reconciliation.")
		case perr != nil:
			findings = append(findings, "observed-level probe failed: "+perr.Error())
			report.Findings = findings
			exit = ValidateFailed
			fmt.Fprintf(stdout, "Observed integration level: probe error: %v\n", perr)
		default:
			rep := obs.Report
			report.IntegrationLevel = &obs
			report.Conformance = &rep
			fmt.Fprintf(stdout, "Observed integration level: %s (declared %s) — %s\n",
				obs.Observed, obs.Declared, obs.Status)
			fmt.Fprintf(stdout, "Conformance: %d checks, %d passed, %d failed\n",
				rep.Summary.Total, rep.Summary.Passed, rep.Summary.Failed)
			switch obs.Status {
			case StatusUnderperforms:
				exit = ValidateFailed
				fmt.Fprintf(stdout,
					"ERROR: runtime_level_underperforms — declared %s, observed %s; missing capabilities: %s\n",
					obs.Declared, obs.Observed, strings.Join(obs.Missing, ", "))
			case StatusUnderdeclared:
				fmt.Fprintf(stdout,
					"WARN: runtime is underdeclared — declared %s, observed %s; raise integrationLevel in runtime.yaml to %s\n",
					obs.Declared, obs.Observed, obs.Observed)
			}
			// A failure in a category at or below the declared level fails
			// the command even when it does not change the observed gate
			// (§15.4.6: "exits 0 on a full pass and non-zero otherwise").
			if df := failuresAtOrBelow(rep.Checks, compliance.Level(declared)); len(df) > 0 {
				exit = ValidateFailed
				fmt.Fprintf(stdout,
					"Failed conformance categories at or below declared level %s: %s\n",
					declared, strings.Join(df, ", "))
			}
		}
	}

	if exit == ValidateOK {
		report.Result = "pass"
		fmt.Fprintln(stdout, "Result: pass")
	} else {
		report.Result = "fail"
		fmt.Fprintln(stdout, "Result: fail")
	}

	if opts.ReportPath != "" {
		if err := writeValidateReport(opts.ReportPath, report); err != nil {
			fmt.Fprintf(stderr, "lenny runtime validate: write report: %v\n", err)
			if exit == ValidateOK {
				exit = ValidateFailed
			}
		} else {
			fmt.Fprintf(stdout, "Report written to %s\n", opts.ReportPath)
		}
	}
	return exit
}

// writeValidateReport marshals the report as indented JSON and writes it
// to path (§15.4.6 `--report <path>`).
func writeValidateReport(path string, r ValidateReport) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

// loadManifest reads and parses runtime.yaml. It returns the parsed
// manifest and a list of structural findings; a nil manifest means the
// file could not be parsed at all.
func loadManifest(path string) (*runtimeManifest, []string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, []string{"runtime.yaml is missing from the repository root"}
		}
		return nil, []string{fmt.Sprintf("runtime.yaml could not be read: %v", err)}
	}
	var m runtimeManifest
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, []string{fmt.Sprintf("runtime.yaml is not valid YAML: %v", err)}
	}
	return &m, nil
}

// checkManifest validates the parsed runtime.yaml against the §5.1
// Runtime contract. It returns one finding per problem.
func checkManifest(m *runtimeManifest) []string {
	var f []string

	if m.Name == "" {
		f = append(f, "runtime.yaml: name is required")
	} else if err := validateName(m.Name); err != nil {
		f = append(f, "runtime.yaml: "+err.Error())
	}

	if m.Type == "" {
		f = append(f, "runtime.yaml: type is required (agent or mcp)")
	} else if !validRuntimeTypes[m.Type] {
		f = append(f, fmt.Sprintf("runtime.yaml: type %q is not one of agent or mcp", m.Type))
	}

	if m.IntegrationLevel != "" && !validIntegrationLevels[m.IntegrationLevel] {
		f = append(f, fmt.Sprintf(
			"runtime.yaml: integrationLevel %q is not one of basic, standard, or full",
			m.IntegrationLevel,
		))
	}
	// integrationLevel is only meaningful on type: agent (§5.1).
	if m.IntegrationLevel != "" && m.Type == "mcp" {
		f = append(f, "runtime.yaml: integrationLevel is only valid on type: agent runtimes")
	}

	// A base (non-derived) runtime needs an image; a derived runtime
	// inherits its parent's image.
	if m.BaseRuntime == "" && m.Image == "" {
		f = append(f, "runtime.yaml: image is required for a non-derived runtime")
	}

	// agent runtimes carry the adapter-protocol surface; check the
	// fields the §15.4 adapter spec and §5.1 expect on an agent.
	if m.Type == "agent" {
		if m.ExecutionMode != "" && m.ExecutionMode != "session" && m.ExecutionMode != "task" {
			f = append(f, fmt.Sprintf(
				"runtime.yaml: executionMode %q is not one of session or task",
				m.ExecutionMode,
			))
		}
		if m.Capabilities == nil {
			f = append(f, "runtime.yaml: capabilities block is missing for an agent runtime")
		} else if _, ok := m.Capabilities["interaction"]; !ok {
			f = append(f, "runtime.yaml: capabilities.interaction is required for an agent runtime")
		}
	}

	return f
}

// checkLayout verifies the repository layout an adapter-conforming
// runtime repository is expected to carry.
func checkLayout(root string) []string {
	var f []string
	if !fileExists(filepath.Join(root, "Dockerfile")) {
		f = append(f, "Dockerfile is missing from the repository root")
	}
	return f
}

// fileExists reports whether path names an existing regular file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
