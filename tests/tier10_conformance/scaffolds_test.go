// SPDX-License-Identifier: MIT

//go:build conformance

// Tier-10 conformance suite. cmd/lenny-compliance/ is the conformance
// harness: a standalone binary that takes a runtime binary path and a
// declared integration level, runs the §15.4 test battery for that
// level, and produces a JSON report. These tests build the harness and
// the bundled reference runtimes (echo, streaming-echo,
// delegation-echo) and run the harness against them, asserting the
// Basic, Standard, and Full batteries pass for a conformant runtime and
// fail for a runtime that declares a lower level.
//
// The tests gate on the Go toolchain being available (it is needed to
// build the harness and the runtimes); a missing toolchain is a
// genuine external-dependency skip.
//
// Sections of §12.10 that need an unbuilt deliverable carry a precise
// blocked: skip naming the missing piece:
//   - TestReferenceCatalogNightly needs the §26 reference-runtime OCI
//     images published to a registry.
//   - TestThirdPartyRegistration needs the RegisterAdapterUnderTest
//     entry point in cmd/lenny-compliance.
//   - TestFidelityMatrix needs the documented per-OutputPart fidelity
//     table and the OpenAI/Anthropic translators.

package tier10_conformance_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// complianceReport mirrors the JSON document cmd/lenny-compliance emits
// with --json. Only the fields the conformance assertions read are
// declared; unknown fields on the report are ignored.
type complianceReport struct {
	Harness string `json:"harness"`
	Binary  string `json:"binary"`
	Level   string `json:"level"`
	Checks  []struct {
		Name   string `json:"name"`
		Spec   string `json:"spec"`
		Pass   bool   `json:"pass"`
		Detail string `json:"detail"`
	} `json:"checks"`
	Summary struct {
		Total  int `json:"total"`
		Passed int `json:"passed"`
		Failed int `json:"failed"`
	} `json:"summary"`
}

// builtArtifacts holds the harness and runtime binaries built once per
// test process. Building each binary is a ~1s `go build`; the sync.Once
// amortizes it across the conformance tests.
type builtArtifacts struct {
	dir            string
	compliance     string
	echo           string
	streamingEcho  string
	delegationEcho string
}

var (
	artifactsOnce sync.Once
	artifacts     *builtArtifacts
	artifactsErr  error
)

// buildArtifacts builds cmd/lenny-compliance and the three bundled
// reference runtimes into a shared temp dir. It skips the calling test
// when the Go toolchain is absent (a genuine external-dependency
// guard). The build runs once per process.
func buildArtifacts(t *testing.T) *builtArtifacts {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go toolchain not on PATH: %v", err)
	}
	artifactsOnce.Do(func() {
		root := repoRoot(t)
		dir, err := os.MkdirTemp("", "tier10-conformance-*")
		if err != nil {
			artifactsErr = err
			return
		}
		build := func(name, pkg string) string {
			if artifactsErr != nil {
				return ""
			}
			bin := filepath.Join(dir, name)
			cmd := exec.Command("go", "build", "-o", bin, pkg)
			cmd.Dir = root
			if out, err := cmd.CombinedOutput(); err != nil {
				artifactsErr = &buildError{pkg: pkg, output: string(out), err: err}
				return ""
			}
			return bin
		}
		a := &builtArtifacts{dir: dir}
		a.compliance = build("lenny-compliance", "./cmd/lenny-compliance")
		a.echo = build("echo", "./cmd/runtimes/echo")
		a.streamingEcho = build("streaming-echo", "./cmd/runtimes/streaming-echo")
		a.delegationEcho = build("delegation-echo", "./cmd/runtimes/delegation-echo")
		if artifactsErr != nil {
			os.RemoveAll(dir)
			return
		}
		artifacts = a
	})
	if artifactsErr != nil {
		t.Fatalf("building conformance artifacts: %v", artifactsErr)
	}
	return artifacts
}

// buildError carries the failing package and the build output so a
// build failure produces an actionable diagnosis.
type buildError struct {
	pkg    string
	output string
	err    error
}

func (e *buildError) Error() string {
	return "go build " + e.pkg + ": " + e.err.Error() + "\n" + e.output
}

// repoRoot walks upward from the test's working directory until it
// finds go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for dir := wd; ; {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("repoRoot: walked past filesystem root looking for go.mod")
		}
		dir = parent
	}
}

// runCompliance runs the compliance harness against binary at level and
// decodes the JSON report. A non-zero exit is expected when checks fail
// (the harness exits with the failed-check count), so the exit code is
// not itself an error; the report's summary is the source of truth.
func runCompliance(t *testing.T, a *builtArtifacts, binary, level string) complianceReport {
	t.Helper()
	cmd := exec.Command(a.compliance, "--binary", binary, "--level", level, "--json")
	out, err := cmd.Output()
	if len(out) == 0 {
		t.Fatalf("lenny-compliance --level %s emitted no JSON report (err: %v)", level, err)
	}
	var report complianceReport
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("decode compliance report (level %s): %v\nraw: %s", level, err, out)
	}
	if report.Summary.Passed+report.Summary.Failed != report.Summary.Total {
		t.Errorf("report summary inconsistent: passed=%d failed=%d total=%d",
			report.Summary.Passed, report.Summary.Failed, report.Summary.Total)
	}
	return report
}

// assertAllPass fails the test, naming every failed check, when the
// report records any failure.
func assertAllPass(t *testing.T, runtime, level string, r complianceReport) {
	t.Helper()
	if r.Summary.Failed != 0 {
		for _, c := range r.Checks {
			if !c.Pass {
				t.Errorf("%s failed the %s check %q: %s", runtime, level, c.Name, c.Detail)
			}
		}
	}
	if r.Summary.Total == 0 {
		t.Errorf("%s %s battery ran no checks", runtime, level)
	}
}

// spec: 12.10 (Basic-level adapter-protocol conformance battery)
// diagnosis: The Basic battery — JSON Lines framing, message/response,
//
//	heartbeat ack, unknown-type tolerance, shutdown deadline — fails
//	for a bundled runtime, so the §15.4 adapter protocol regressed.
func TestBasicAdapterProtocol(t *testing.T) {
	a := buildArtifacts(t)
	// Every bundled runtime is at least Basic-conformant: echo is the
	// reference Basic runtime; streaming-echo and delegation-echo
	// inherit the Basic protocol.
	for _, rt := range []struct{ name, binary string }{
		{"echo", a.echo},
		{"streaming-echo", a.streamingEcho},
		{"delegation-echo", a.delegationEcho},
	} {
		t.Run(rt.name, func(t *testing.T) {
			report := runCompliance(t, a, rt.binary, "basic")
			if report.Level != "basic" {
				t.Errorf("report level = %q, want basic", report.Level)
			}
			assertAllPass(t, rt.name, "basic", report)
			// Every Basic check is tagged with the §15.4 adapter
			// protocol section.
			for _, c := range report.Checks {
				if c.Spec != "15.4" {
					t.Errorf("Basic check %q spec = %q, want 15.4", c.Name, c.Spec)
				}
			}
		})
	}
}

// spec: 12.10 (Standard-level conformance battery)
// diagnosis: The Standard battery — Basic plus the §15.4.6 intra-pod
//
//	MCP nonce handshake, platform-tool invocation, connector
//	reachability, and tool-call correlation — fails for delegation-echo
//	or passes for a Basic-only runtime, so the Standard contract
//	regressed.
func TestStandardLevel(t *testing.T) {
	a := buildArtifacts(t)

	// delegation-echo is the reference Standard runtime: it reads the
	// adapter manifest, completes the nonce handshake, and invokes the
	// §8.5 platform delegation tools. It must pass the whole battery.
	report := runCompliance(t, a, a.delegationEcho, "standard")
	if report.Level != "standard" {
		t.Errorf("report level = %q, want standard", report.Level)
	}
	assertAllPass(t, "delegation-echo", "standard", report)
	// The four §15.4.6 Standard categories are present.
	wantStandard := map[string]bool{
		"mcp_nonce_handshake":               false,
		"platform_mcp_tool_invocation":      false,
		"connector_mcp_server_reachability": false,
		"tool_call_tool_result_correlation": false,
	}
	for _, c := range report.Checks {
		if _, ok := wantStandard[c.Name]; ok {
			wantStandard[c.Name] = true
		}
	}
	for name, seen := range wantStandard {
		if !seen {
			t.Errorf("Standard battery is missing the §15.4.6 category %q", name)
		}
	}

	// echo is a Basic-only runtime: it never reads the manifest, so it
	// cannot complete the nonce handshake or invoke platform tools. The
	// MCP-dependent Standard checks must fail it.
	basicReport := runCompliance(t, a, a.echo, "standard")
	if basicReport.Summary.Failed == 0 {
		t.Error("echo (Basic-only) passed the entire Standard battery; the MCP checks must fail it")
	}
	byName := map[string]bool{}
	for _, c := range basicReport.Checks {
		byName[c.Name] = c.Pass
	}
	for _, name := range []string{"mcp_nonce_handshake", "platform_mcp_tool_invocation", "connector_mcp_server_reachability"} {
		if byName[name] {
			t.Errorf("Standard check %q passed for echo; a Basic-only runtime cannot satisfy it", name)
		}
	}
}

// spec: 12.10 (Full-level conformance battery)
// diagnosis: The Full battery — Standard plus the §15.4.6 lifecycle
//
//	channel handshake, checkpoint, interrupt, credential rotation, and
//	deadline-signal handling — fails for streaming-echo, so the Full
//	lifecycle contract regressed.
func TestFullLevel(t *testing.T) {
	a := buildArtifacts(t)

	// streaming-echo is the reference Full runtime: it opens the
	// lifecycle channel and answers every §15.4.6 lifecycle event.
	report := runCompliance(t, a, a.streamingEcho, "full")
	if report.Level != "full" {
		t.Errorf("report level = %q, want full", report.Level)
	}
	assertAllPass(t, "streaming-echo", "full", report)
	// The five §15.4.6 Full categories are present.
	wantFull := map[string]bool{
		"lifecycle_channel_opening":         false,
		"checkpoint_quiesce_resume":         false,
		"interrupt_acknowledgement":         false,
		"credential_rotation_no_disruption": false,
		"deadline_signal_handling":          false,
	}
	for _, c := range report.Checks {
		if _, ok := wantFull[c.Name]; ok {
			wantFull[c.Name] = true
		}
	}
	for name, seen := range wantFull {
		if !seen {
			t.Errorf("Full battery is missing the §15.4.6 category %q", name)
		}
	}

	// echo has no lifecycle channel; the Full lifecycle checks must
	// fail it while the inherited Basic checks still pass.
	basicReport := runCompliance(t, a, a.echo, "full")
	if basicReport.Summary.Failed == 0 {
		t.Error("echo (Basic-only) passed the entire Full battery; the lifecycle checks must fail it")
	}
}

// spec: 12.10 (bundled-runtime conformance runs on every PR)
// diagnosis: One of the three bundled runtimes (echo, streaming-echo,
//
//	delegation-echo) no longer clears the conformance battery for the
//	level it declares, so a per-PR conformance gate would break.
func TestBundledRuntimesEveryPR(t *testing.T) {
	a := buildArtifacts(t)
	// Each bundled runtime declares an integration level and must pass
	// the matching battery cleanly. This is the per-PR subset from
	// §12.10: cheap, hermetic, and run on every change.
	bundled := []struct {
		name   string
		binary string
		level  string
	}{
		{"echo", a.echo, "basic"},
		{"delegation-echo", a.delegationEcho, "standard"},
		{"streaming-echo", a.streamingEcho, "full"},
	}
	for _, rt := range bundled {
		t.Run(rt.name+"_"+rt.level, func(t *testing.T) {
			report := runCompliance(t, a, rt.binary, rt.level)
			assertAllPass(t, rt.name, rt.level, report)
			if report.Binary != rt.binary {
				t.Errorf("report binary = %q, want %q", report.Binary, rt.binary)
			}
			if !strings.HasPrefix(report.Harness, "lenny-compliance/") {
				t.Errorf("report harness = %q, want a lenny-compliance/ id", report.Harness)
			}
		})
	}
}

// spec: 12.10 (reference-catalog conformance runs nightly)
// diagnosis: A §26 reference runtime regressed against its declared
//
//	integration level on the nightly conformance run.
func TestReferenceCatalogNightly(t *testing.T) {
	// The §26 reference runtimes (claude-code, gemini-cli, codex,
	// cursor-cli, chat, langgraph, mastra, openai-assistants, crewai)
	// are not built in-repo: they are external runtime projects whose
	// OCI images the nightly job pulls from a registry. The
	// conformance harness drives a local binary path (--binary); the
	// image-driven path (lenny-test conformance --image) needs the
	// reference images published. Neither the images nor the registry
	// coordinates exist yet.
	t.Skip("blocked: the §26 reference-runtime OCI images are not published; " +
		"the nightly run needs the reference images in a registry plus " +
		"the image-pull path in lenny-test conformance --image")
}

// spec: 12.10 (third-party runtime self-registration with the harness)
// diagnosis: A third-party runtime cannot register itself with the
//
//	conformance harness, so its adapter never enters the battery.
func TestThirdPartyRegistration(t *testing.T) {
	// §12.10 specifies a RegisterAdapterUnderTest(adapter) entry point
	// so a third-party runtime project can register its adapter with
	// the harness from its own test code. cmd/lenny-compliance today
	// exposes only the --binary CLI surface and the per-level
	// runBasicBattery / runStandardBattery / runFullBattery functions;
	// it has no exported RegisterAdapterUnderTest registration API.
	t.Skip("blocked: cmd/lenny-compliance has no RegisterAdapterUnderTest " +
		"entry point; the third-party self-registration API in §12.10 is unbuilt")
}

// spec: 12.10 (translation fidelity matrix)
// diagnosis: The documented OpenAI/Anthropic translation fidelity no
//
//	longer matches the translators — a field documented as preserved
//	was dropped, or a field documented as lossy now survives.
func TestFidelityMatrix(t *testing.T) {
	// §12.10 specifies a table-driven test asserting the documented
	// translation fidelity for the OpenAI and Anthropic
	// Completions/Responses surfaces: for each OutputPart type the
	// lossy fields are documented and the suite confirms the exact
	// loss. The per-OutputPart fidelity table and the
	// OpenAI/Anthropic translators it would validate are not built.
	t.Skip("blocked: the per-OutputPart fidelity table and the " +
		"OpenAI/Anthropic Completions/Responses translators are unbuilt")
}
