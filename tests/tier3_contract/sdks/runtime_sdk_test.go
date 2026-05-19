// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 SDK contract suite for the Go runtime-author SDK
// (sdks/runtime/go). The SDK wraps the §15.4.1 adapter binary
// protocol, the §15.4.3 intra-pod MCP integration, the §8.5 platform
// MCP tool surface, and the Full-level lifecycle channel.
//
// The implemented tests build an SDK-based example runtime
// (sdks/runtime/go/example/{echo,delegate,lifecycle}) and the
// lenny-compliance conformance harness, then run the harness against
// the runtime at the integration level the example claims. A passing
// run with zero failed checks is the contract.
//
// The Python and TypeScript runtime SDKs are not part of this
// repository; their tests remain skipped with a skip reason naming the
// missing package. The quick-start scaffold test is skipped pending
// the `lenny runtime init` subcommand.

package sdks_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// runtimeRepoRoot walks up from the working directory to the module
// root (the directory holding go.mod).
func runtimeRepoRoot(t *testing.T) string {
	t.Helper()
	wd, _ := os.Getwd()
	for d := wd; d != "/" && d != ""; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
	}
	t.Fatalf("no go.mod found from %s", wd)
	return ""
}

// buildRuntimeBinary compiles a package under the module root into a
// throwaway binary and returns its path.
func buildRuntimeBinary(t *testing.T, pkg string) string {
	t.Helper()
	root := runtimeRepoRoot(t)
	out := filepath.Join(t.TempDir(), filepath.Base(pkg))
	cmd := exec.Command("go", "build", "-o", out, pkg)
	cmd.Dir = root
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", pkg, err, combined)
	}
	return out
}

// complianceReport is the lenny-compliance JSON report. Only the
// summary counts and the per-check pass flags are read here.
type complianceReport struct {
	Level  string `json:"level"`
	Checks []struct {
		Name   string `json:"name"`
		Pass   bool   `json:"pass"`
		Detail string `json:"detail"`
	} `json:"checks"`
	Summary struct {
		Total  int `json:"total"`
		Passed int `json:"passed"`
		Failed int `json:"failed"`
	} `json:"summary"`
}

// runCompliance runs lenny-compliance against runtimeBin at the named
// integration level and returns the parsed JSON report. lenny-compliance
// exits non-zero (one per failed check) when a check fails; the JSON
// report on stdout is still valid, so only an empty output is fatal.
func runCompliance(t *testing.T, complianceBin, runtimeBin, level string) complianceReport {
	t.Helper()
	cmd := exec.Command(complianceBin, "--binary", runtimeBin, "--level", level, "--json")
	out, err := cmd.Output()
	if len(out) == 0 {
		t.Fatalf("lenny-compliance produced no output: %v", err)
	}
	var report complianceReport
	if jerr := json.Unmarshal(out, &report); jerr != nil {
		t.Fatalf("lenny-compliance report not JSON: %v\n%s", jerr, out)
	}
	return report
}

// assertAllPassed fails the test with per-check detail when the report
// has any failed check.
func assertAllPassed(t *testing.T, report complianceReport) {
	t.Helper()
	if report.Summary.Failed == 0 && report.Summary.Total > 0 {
		t.Logf("%s level: %d/%d checks passed", report.Level, report.Summary.Passed, report.Summary.Total)
		return
	}
	for _, c := range report.Checks {
		if !c.Pass {
			t.Errorf("check %q failed: %s", c.Name, c.Detail)
		}
	}
	t.Fatalf("%s level: %d of %d checks failed", report.Level, report.Summary.Failed, report.Summary.Total)
}

// checkPassed reports whether the named check passed in the report.
func checkPassed(report complianceReport, name string) bool {
	for _, c := range report.Checks {
		if c.Name == name {
			return c.Pass
		}
	}
	return false
}

// checkDetail returns the detail string of the named check.
func checkDetail(report complianceReport, name string) string {
	for _, c := range report.Checks {
		if c.Name == name {
			return c.Detail
		}
	}
	return "check not found in report"
}

// spec: 15.4.1, 15.7 (Go runtime SDK, Basic level)
// diagnosis: the SDK-based echo runtime (sdks/runtime/go/example/echo)
// must clear every Basic-level lenny-compliance check: stdin/stdout
// JSON Lines framing, message/response round trip, heartbeat ack,
// shutdown within the deadline, unknown-type tolerance, and sequential
// messages. A failed check means the SDK protocol loop does not honor
// the §15.4.1 contract.
func TestRuntimeSDKAdapterBinaryProtocolGo(t *testing.T) {
	compliance := buildRuntimeBinary(t, "./cmd/lenny-compliance")
	runtimeBin := buildRuntimeBinary(t, "./sdks/runtime/go/example/echo")
	report := runCompliance(t, compliance, runtimeBin, "basic")
	assertAllPassed(t, report)
}

// spec: 15.7 (Python runtime SDK)
// diagnosis: the Python runtime SDK (lenny-runtime / runtime-sdk-python)
// is not part of this repository; there is no package to exercise.
func TestRuntimeSDKAdapterBinaryProtocolPython(t *testing.T) {
	t.Skip("not implemented: §15.7 Python runtime SDK — requires the lenny-runtime PyPI package, which is not in this repository")
}

// spec: 15.7 (TypeScript runtime SDK)
// diagnosis: the TypeScript runtime SDK (@lennylabs/runtime-sdk) is not
// part of this repository; there is no package to exercise.
func TestRuntimeSDKAdapterBinaryProtocolTypeScript(t *testing.T) {
	t.Skip("not implemented: §15.7 TypeScript runtime SDK — requires the @lennylabs/runtime-sdk npm package, which is not in this repository")
}

// spec: 15.4.3, 15.7 (Go runtime SDK, Standard level)
// diagnosis: the SDK-based delegate runtime
// (sdks/runtime/go/example/delegate) is started with WithStandardLevel.
// It must read the adapter manifest, complete the §15.4.3 nonce
// handshake against the platform MCP server and every connector MCP
// server, and invoke the §8.5 platform tools through the SDK helpers.
// A failed Standard-level check means the SDK MCP client or the typed
// tool helpers do not honor the contract.
func TestRuntimeSDKMCPSocketStandardLevel(t *testing.T) {
	compliance := buildRuntimeBinary(t, "./cmd/lenny-compliance")
	runtimeBin := buildRuntimeBinary(t, "./sdks/runtime/go/example/delegate")
	report := runCompliance(t, compliance, runtimeBin, "standard")
	assertAllPassed(t, report)
}

// spec: 15.4.3, 15.7 (Go runtime SDK, Full level)
// diagnosis: the SDK-based lifecycle runtime
// (sdks/runtime/go/example/lifecycle) is started with WithFullLevel. It
// must open the lifecycle channel, complete the lifecycle_capabilities
// / lifecycle_support handshake, and answer the checkpoint, interrupt,
// credential-rotation, and deadline events. A failed Full-level check
// means the SDK lifecycle channel does not honor the contract.
func TestRuntimeSDKLifecycleFullLevel(t *testing.T) {
	compliance := buildRuntimeBinary(t, "./cmd/lenny-compliance")
	runtimeBin := buildRuntimeBinary(t, "./sdks/runtime/go/example/lifecycle")
	report := runCompliance(t, compliance, runtimeBin, "full")
	assertAllPassed(t, report)
}

// spec: 15.4.1, 15.7 (Go runtime SDK workspace helpers)
// diagnosis: the SDK exposes the §15.4.1 adapter-local tool helpers
// (ReadFile, WriteFile, ListDir, DeleteFile via AdapterTools). The
// lenny-compliance harness does not ship an adversarial path-traversal
// corpus that drives the helpers; the helpers are unit-tested in
// sdks/runtime/go/runtime instead.
func TestRuntimeSDKWorkspaceHelpers(t *testing.T) {
	t.Skip("not implemented: §15.7 runtime SDK workspace helpers — the AdapterTools helpers ship in sdks/runtime/go/runtime, but the lenny-compliance harness has no path-traversal corpus to drive them")
}

// spec: 8.5, 15.7 (Go runtime SDK delegation tools)
// diagnosis: the SDK lenny/delegate_task wrapper, budget metadata
// propagation, and child-result decoding are exercised by the
// Standard-level delegate runtime and by the unit tests in
// sdks/runtime/go/runtime. This dedicated case is covered there.
func TestRuntimeSDKDelegationTools(t *testing.T) {
	t.Skip("covered: §8.5 runtime SDK delegation is exercised by TestRuntimeSDKMCPSocketStandardLevel and the sdks/runtime/go/runtime unit tests")
}

// spec: 15.4.1, 15.7 (Go runtime SDK heartbeat handling)
// diagnosis: the SDK answers a §15.4.1 heartbeat with heartbeat_ack
// without runtime-author intervention. The Basic-level heartbeat check
// in lenny-compliance asserts this; a failure means the SDK loop drops
// the heartbeat frame.
func TestRuntimeSDKHeartbeatHandling(t *testing.T) {
	compliance := buildRuntimeBinary(t, "./cmd/lenny-compliance")
	runtimeBin := buildRuntimeBinary(t, "./sdks/runtime/go/example/echo")
	report := runCompliance(t, compliance, runtimeBin, "basic")
	if !checkPassed(report, "heartbeat_emits_ack") {
		t.Fatalf("heartbeat_emits_ack did not pass: %s", checkDetail(report, "heartbeat_emits_ack"))
	}
}

// spec: 15.4.1, 15.7 (Go runtime SDK graceful shutdown)
// diagnosis: the SDK exits cleanly within deadline_ms of a §15.4.1
// shutdown frame. The Basic-level shutdown check in lenny-compliance
// asserts this; a failure means the SDK loop does not honor the
// shutdown deadline.
func TestRuntimeSDKGracefulShutdown(t *testing.T) {
	compliance := buildRuntimeBinary(t, "./cmd/lenny-compliance")
	runtimeBin := buildRuntimeBinary(t, "./sdks/runtime/go/example/echo")
	report := runCompliance(t, compliance, runtimeBin, "basic")
	if !checkPassed(report, "shutdown_exits_within_deadline") {
		t.Fatalf("shutdown_exits_within_deadline did not pass: %s", checkDetail(report, "shutdown_exits_within_deadline"))
	}
}

// spec: 16.3, 15.7 (Go runtime SDK telemetry pass-through)
// diagnosis: the SDK exposes lenny/set_tracing_context through the
// Tools.SetTracingContext helper. The lenny-compliance harness does not
// verify OTel context propagation end to end; that path is not driven
// by an automated check.
func TestRuntimeSDKTelemetryPassThrough(t *testing.T) {
	t.Skip("not implemented: §15.7 runtime SDK telemetry — Tools.SetTracingContext ships in sdks/runtime/go/runtime, but lenny-compliance has no OTel context-propagation check")
}

// spec: 15.7, 24.18 (Go runtime SDK quick-start)
// diagnosis: the timed quick-start mirror needs the `lenny runtime
// init` scaffold subcommand, which is not implemented.
func TestRuntimeSDKQuickStartTTHW(t *testing.T) {
	t.Skip("not implemented: §24.18 runtime SDK quick-start TTHW — requires the `lenny runtime init` scaffold subcommand")
}
