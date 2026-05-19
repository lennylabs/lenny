// SPDX-License-Identifier: MIT

package runtimescaffold

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"sigs.k8s.io/yaml"
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

// Validate runs the static checks `lenny runtime validate` performs
// against the runtime repository rooted at path. It returns one of the
// Validate* codes.
//
// Scope. This is a static validator: it checks the runtime.yaml
// structure against the §5.1 Runtime contract and the repository layout
// against the §15.4 adapter-spec expectations. It does not start the
// runtime, so it cannot observe the lifecycle-channel handshake; the
// declared-vs-observed reconciliation in §24.18 is reported here as a
// declared level only, with the observed-level probe documented as a
// step the operator runs by standing the runtime up against a gateway.
// The report states this explicitly.
func Validate(path string, stdout, stderr io.Writer) int {
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

	// Report.
	fmt.Fprintf(stdout, "Runtime repository: %s\n", path)
	fmt.Fprintln(stdout, "Checks performed:")
	for _, c := range checks {
		fmt.Fprintf(stdout, "  - %s\n", c)
	}
	if manifest != nil {
		fmt.Fprintf(stdout, "Declared integration level: %s\n", declared)
		fmt.Fprintln(stdout,
			"Observed integration level: not probed. The static validator does "+
				"not start the runtime. To reconcile declared against observed, "+
				"register the runtime with a gateway and inspect the "+
				"lifecycle_capabilities / lifecycle_support handshake; the gateway "+
				"reports RUNTIME_LEVEL_UNDERPERFORMS when the observed level is "+
				"below the declared level.")
	}

	if len(findings) == 0 {
		fmt.Fprintln(stdout, "Result: pass — no static issues found.")
		return ValidateOK
	}
	sort.Strings(findings)
	fmt.Fprintf(stdout, "Result: %d issue(s) found:\n", len(findings))
	for _, f := range findings {
		fmt.Fprintf(stdout, "  - %s\n", f)
	}
	return ValidateFailed
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
