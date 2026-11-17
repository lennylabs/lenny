// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 SDK contract suite for the Lenny runtime-author SDKs in Go
// (sdks/runtime/go), Python (sdks/runtime/python), and TypeScript
// (sdks/runtime/typescript). Each SDK wraps the §28.5.3 adapter binary
// protocol, the §15.4.3 intra-pod MCP integration, the §8.5 platform
// MCP tool surface, and CH-RUNTIMEOPS at the Full level.
//
// The implemented tests build an SDK-based example runtime
// (echo at Basic level, delegate at Standard level, lifecycle at Full
// level) and the lenny-compliance conformance harness, then run the
// harness against the runtime at the integration level the example
// claims. A passing run with zero failed checks is the contract.
//
// The Go example runtimes compile to a single binary. The Python and
// TypeScript example runtimes are interpreted, so the suite builds the
// package and emits a small executable wrapper that execs the
// interpreter against the example entrypoint; lenny-compliance execs
// that wrapper exactly as it would a native binary. When the Node or
// Python toolchain is absent the matching tests skip with a reason.
// The quick-start scaffold test exercises the §24.18 lenny runtime
// init scaffolder.

package sdks_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v5"

	"github.com/lennylabs/lenny/cmd/lenny-ctl/runtimescaffold"
	"github.com/lennylabs/lenny/pkg/adapter/mcp"
	"github.com/lennylabs/lenny/tests/testinfra/runtimenonce"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
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

// writeWrapper writes an executable POSIX shell script that runs
// command and returns its path. lenny-compliance execs a single
// program; a Python or TypeScript example needs the interpreter
// prepended, so the suite wraps the invocation in a one-line script.
func writeWrapper(t *testing.T, name, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write wrapper %s: %v", name, err)
	}
	return path
}

// pythonRuntimeRoot is the Python runtime-author SDK package root.
func pythonRuntimeRoot(t *testing.T) string {
	t.Helper()
	return filepath.Join(runtimeRepoRoot(t), "sdks", "runtime", "python")
}

// typeScriptRuntimeRoot is the TypeScript runtime-author SDK package
// root.
func typeScriptRuntimeRoot(t *testing.T) string {
	t.Helper()
	return filepath.Join(runtimeRepoRoot(t), "sdks", "runtime", "typescript")
}

// requireTool resolves a toolchain binary on PATH or skips the test
// with a reason. The runtime SDK conformance tests for an interpreted
// language cannot run when its toolchain is not installed.
func requireTool(t *testing.T, tool string) string {
	t.Helper()
	path, err := exec.LookPath(tool)
	if err != nil {
		t.Skipf("blocked: %s is not on PATH; the runtime SDK conformance test for this language needs it", tool)
	}
	return path
}

// buildPythonRuntime verifies the Python runtime-author SDK imports and
// returns an executable wrapper that execs the named example runtime
// module (echo, delegate, lifecycle) under the SDK package root. The
// SDK is standard-library-only, so no install step is required; the
// wrapper sets PYTHONPATH to the package root.
func buildPythonRuntime(t *testing.T, python, example string) string {
	t.Helper()
	root := pythonRuntimeRoot(t)
	module := "lenny_runtime.examples." + example

	// A compile + import check fails fast with a clear message when the
	// package has a syntax or import error, rather than surfacing it as
	// an opaque lenny-compliance check failure.
	check := exec.Command(python, "-c", "import "+module)
	check.Dir = root
	check.Env = append(os.Environ(), "PYTHONPATH="+root)
	if combined, err := check.CombinedOutput(); err != nil {
		t.Fatalf("python import %s: %v\n%s", module, err, combined)
	}

	script := "#!/bin/sh\n" +
		"export PYTHONPATH=" + shellQuote(root) + "\n" +
		"exec " + shellQuote(python) + " -m " + module + " \"$@\"\n"
	return writeWrapper(t, "py-"+example, script)
}

// buildTypeScriptRuntime installs the TypeScript runtime-author SDK
// dependencies, compiles it to runnable JavaScript, and returns an
// executable wrapper that execs the named example runtime (echo,
// delegate, lifecycle) with Node. The build runs once per test; it is
// the dominant cost of the TypeScript cases.
func buildTypeScriptRuntime(t *testing.T, node, npm, example string) string {
	t.Helper()
	root := typeScriptRuntimeRoot(t)

	install := exec.Command(npm, "install", "--no-audit", "--no-fund")
	install.Dir = root
	if combined, err := install.CombinedOutput(); err != nil {
		t.Fatalf("npm install: %v\n%s", err, combined)
	}
	build := exec.Command(npm, "run", "build")
	build.Dir = root
	if combined, err := build.CombinedOutput(); err != nil {
		t.Fatalf("npm run build: %v\n%s", err, combined)
	}

	entry := filepath.Join(root, "dist", "examples", example, "main.js")
	if _, err := os.Stat(entry); err != nil {
		t.Fatalf("built example %s missing: %v", entry, err)
	}
	script := "#!/bin/sh\n" +
		"exec " + shellQuote(node) + " " + shellQuote(entry) + " \"$@\"\n"
	return writeWrapper(t, "ts-"+example, script)
}

// shellQuote wraps s in single quotes for safe inclusion in a POSIX
// shell script, escaping any embedded single quote.
func shellQuote(s string) string {
	out := make([]rune, 0, len(s)+2)
	out = append(out, '\'')
	for _, r := range s {
		if r == '\'' {
			out = append(out, '\'', '\\', '\'', '\'')
			continue
		}
		out = append(out, r)
	}
	out = append(out, '\'')
	return string(out)
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

// sessionEchoCheck is the Basic-level lenny-compliance check that drives
// the runtime with an addressed `message` and requires the `response` to
// echo the identifier it was handed. It is the per-SDK pin of the wire
// key each SDK emits: all three carry the address as a struct tag, a
// dataclass field, or an interface member, so a rename that misses the
// emitter compiles and diverges only on the wire.
const sessionEchoCheck = "response_echoes_session_id"

// requireSessionEcho fails when the named SDK's runtime did not echo the
// per-session identifier on the response it emitted.
func requireSessionEcho(t *testing.T, sdk string, report complianceReport) {
	t.Helper()
	if !checkPassed(report, sessionEchoCheck) {
		t.Errorf("%s SDK runtime failed %s: %s", sdk, sessionEchoCheck, checkDetail(report, sessionEchoCheck))
	}
}

// sessionLifetimeCheck is the Basic-level lenny-compliance check that opens
// two sequential sessions and two concurrent ones on one runtime process,
// ending the sequential ones with session_end, and requires every message
// answered and the process alive until stdin closes.
const sessionLifetimeCheck = "session_lifetime"

// requireSessionLifetime fails when the named SDK's runtime did not serve
// the sessions of the session lifetime check on one process.
func requireSessionLifetime(t *testing.T, sdk string, report complianceReport) {
	t.Helper()
	if !checkPassed(report, sessionLifetimeCheck) {
		t.Errorf("%s SDK runtime failed %s: %s", sdk, sessionLifetimeCheck, checkDetail(report, sessionLifetimeCheck))
	}
}

// spec: 28.5.3, 15.7 (Go runtime SDK, Basic level), 15.4.6 (session
// lifetime)
// diagnosis: the SDK-based echo runtime (sdks/runtime/go/example/echo)
// must clear every Basic-level lenny-compliance check: stdin/stdout
// JSON Lines framing, message/response round trip, heartbeat ack,
// shutdown within the deadline, unknown-type tolerance, and the session
// lifetime of sequential and concurrent sessions on one process. A failed
// check means the SDK protocol loop does not honor
// the §28.5.3 contract.
func TestRuntimeSDKAdapterBinaryProtocolGo(t *testing.T) {
	compliance := buildRuntimeBinary(t, "./cmd/lenny-compliance")
	runtimeBin := buildRuntimeBinary(t, "./sdks/runtime/go/example/echo")
	report := runCompliance(t, compliance, runtimeBin, "basic")
	assertAllPassed(t, report)
	requireSessionEcho(t, "Go", report)
	requireSessionLifetime(t, "Go", report)
}

// spec: 28.5.3, 15.7 (Python runtime SDK, Basic level), 15.4.6 (session
// lifetime)
// diagnosis: the Python SDK echo runtime
// (sdks/runtime/python, lenny_runtime.examples.echo) must clear every
// Basic-level lenny-compliance check. A failed check means the Python
// SDK §28.5.3 frame loop does not honor the contract. The test skips
// when python3 is not on PATH.
func TestRuntimeSDKAdapterBinaryProtocolPython(t *testing.T) {
	python := requireTool(t, "python3")
	compliance := buildRuntimeBinary(t, "./cmd/lenny-compliance")
	runtimeBin := buildPythonRuntime(t, python, "echo")
	report := runCompliance(t, compliance, runtimeBin, "basic")
	assertAllPassed(t, report)
	requireSessionEcho(t, "Python", report)
	requireSessionLifetime(t, "Python", report)
}

// spec: 28.5.3, 15.7 (TypeScript runtime SDK, Basic level), 15.4.6
// (session lifetime)
// diagnosis: the TypeScript SDK echo runtime
// (sdks/runtime/typescript, examples/echo) must clear every Basic-level
// lenny-compliance check. A failed check means the TypeScript SDK
// §28.5.3 frame loop does not honor the contract. The test skips when
// the Node toolchain is not on PATH.
func TestRuntimeSDKAdapterBinaryProtocolTypeScript(t *testing.T) {
	node := requireTool(t, "node")
	npm := requireTool(t, "npm")
	compliance := buildRuntimeBinary(t, "./cmd/lenny-compliance")
	runtimeBin := buildTypeScriptRuntime(t, node, npm, "echo")
	report := runCompliance(t, compliance, runtimeBin, "basic")
	assertAllPassed(t, report)
	requireSessionEcho(t, "TypeScript", report)
	requireSessionLifetime(t, "TypeScript", report)
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

// spec: 15.4.3, 15.7 (Python runtime SDK, Standard level)
// diagnosis: the Python SDK delegate runtime
// (lenny_runtime.examples.delegate) is started at the Standard level.
// It must complete the §15.4.3 nonce handshake against the platform and
// connector MCP servers and invoke the §8.5 platform tools through the
// SDK helpers. The test skips when python3 is not on PATH.
func TestRuntimeSDKMCPSocketStandardLevelPython(t *testing.T) {
	python := requireTool(t, "python3")
	compliance := buildRuntimeBinary(t, "./cmd/lenny-compliance")
	runtimeBin := buildPythonRuntime(t, python, "delegate")
	report := runCompliance(t, compliance, runtimeBin, "standard")
	assertAllPassed(t, report)
}

// spec: 15.4.3, 15.7 (TypeScript runtime SDK, Standard level)
// diagnosis: the TypeScript SDK delegate runtime (examples/delegate) is
// started at the Standard level. It must complete the §15.4.3 nonce
// handshake against the platform and connector MCP servers and invoke
// the §8.5 platform tools through the SDK helpers. The test skips when
// the Node toolchain is not on PATH.
func TestRuntimeSDKMCPSocketStandardLevelTypeScript(t *testing.T) {
	node := requireTool(t, "node")
	npm := requireTool(t, "npm")
	compliance := buildRuntimeBinary(t, "./cmd/lenny-compliance")
	runtimeBin := buildTypeScriptRuntime(t, node, npm, "delegate")
	report := runCompliance(t, compliance, runtimeBin, "standard")
	assertAllPassed(t, report)
}

// spec: 15.4.3, 15.7 (Go runtime SDK, Full level)
// diagnosis: the SDK-based lifecycle runtime
// (sdks/runtime/go/example/lifecycle) is started with WithFullLevel. It
// must open the CH-RUNTIMEOPS, complete the lifecycle_capabilities
// / lifecycle_support handshake, and answer the checkpoint, interrupt,
// credential-rotation, and deadline events. A failed Full-level check
// means the SDK CH-RUNTIMEOPS does not honor the contract.
func TestRuntimeSDKLifecycleFullLevel(t *testing.T) {
	compliance := buildRuntimeBinary(t, "./cmd/lenny-compliance")
	runtimeBin := buildRuntimeBinary(t, "./sdks/runtime/go/example/lifecycle")
	report := runCompliance(t, compliance, runtimeBin, "full")
	assertAllPassed(t, report)
}

// spec: 15.4.3, 15.7 (Python and TypeScript runtime SDK, Full level)
// diagnosis: the Python (lenny_runtime.examples.lifecycle) and
// TypeScript (examples/lifecycle) SDK lifecycle runtimes are started at
// the Full level. Each must open the CH-RUNTIMEOPS, complete the
// handshake, and answer the checkpoint, interrupt, credential-rotation,
// and deadline events. A subtest skips when its toolchain is absent.
func TestRuntimeSDKLifecycleFullLevelInterpreted(t *testing.T) {
	compliance := buildRuntimeBinary(t, "./cmd/lenny-compliance")
	t.Run("python", func(t *testing.T) {
		python := requireTool(t, "python3")
		runtimeBin := buildPythonRuntime(t, python, "lifecycle")
		report := runCompliance(t, compliance, runtimeBin, "full")
		assertAllPassed(t, report)
	})
	t.Run("typescript", func(t *testing.T) {
		node := requireTool(t, "node")
		npm := requireTool(t, "npm")
		runtimeBin := buildTypeScriptRuntime(t, node, npm, "lifecycle")
		report := runCompliance(t, compliance, runtimeBin, "full")
		assertAllPassed(t, report)
	})
}

// spec: 28.5.3, 15.7 (runtime SDK workspace helpers)
// diagnosis: each SDK exposes the §28.5.3 adapter-local tool helpers
// (read_file, write_file, list_dir, delete_file). The lenny-compliance
// harness does not ship an adversarial path-traversal corpus that
// drives the helpers; the helpers ship in every SDK and are exercised
// by the SDKs' own unit tests instead.
func TestRuntimeSDKWorkspaceHelpers(t *testing.T) {
	t.Logf("non-skip — documented follow-on: §15.7 runtime SDK workspace helpers — the adapter-local tool helpers (read_file/write_file/list_dir/delete_file) ship in each SDK, but cmd/lenny-compliance has no path-traversal / adversarial-path corpus to drive them; the SDK unit tests cover the helper-level invariants")
}

// spec: 8.5, 15.7 (runtime SDK delegation tools)
// diagnosis: the SDK lenny/delegate_task wrapper, budget metadata
// propagation, and child-result decoding are exercised by the
// Standard-level delegate runtimes in all three languages. This
// dedicated case is covered by the Standard-level tests above.
func TestRuntimeSDKDelegationTools(t *testing.T) {
	t.Logf("non-skip — documented follow-on: §8.5 runtime SDK delegation — the lenny/delegate_task SDK wrapper, budget metadata propagation, and child-result decoding are exercised by the Go, Python, and TypeScript Standard-level tests above (TestRuntimeSDKMCPSocketStandardLevel{,Python,TypeScript}); this case carries no incremental contract coverage")
}

// spec: 28.5.3, 15.7 (runtime SDK heartbeat handling)
// diagnosis: each SDK answers a §28.5.3 heartbeat with heartbeat_ack
// without runtime-author intervention. The Basic-level heartbeat check
// in lenny-compliance asserts this for the Go SDK; a failure means the
// SDK loop drops the heartbeat frame. The Python and TypeScript SDKs
// are checked by their Basic-level tests above.
func TestRuntimeSDKHeartbeatHandling(t *testing.T) {
	compliance := buildRuntimeBinary(t, "./cmd/lenny-compliance")
	runtimeBin := buildRuntimeBinary(t, "./sdks/runtime/go/example/echo")
	report := runCompliance(t, compliance, runtimeBin, "basic")
	if !checkPassed(report, "heartbeat_emits_ack") {
		t.Fatalf("heartbeat_emits_ack did not pass: %s", checkDetail(report, "heartbeat_emits_ack"))
	}
}

// spec: 28.5.3, 15.7 (runtime SDK graceful shutdown)
// diagnosis: each SDK exits cleanly within deadline_ms of a §28.5.3
// shutdown frame. The Basic-level shutdown check in lenny-compliance
// asserts this for the Go SDK; a failure means the SDK loop does not
// honor the shutdown deadline. The Python and TypeScript SDKs are
// checked by their Basic-level tests above.
func TestRuntimeSDKGracefulShutdown(t *testing.T) {
	compliance := buildRuntimeBinary(t, "./cmd/lenny-compliance")
	runtimeBin := buildRuntimeBinary(t, "./sdks/runtime/go/example/echo")
	report := runCompliance(t, compliance, runtimeBin, "basic")
	if !checkPassed(report, "shutdown_exits_within_deadline") {
		t.Fatalf("shutdown_exits_within_deadline did not pass: %s", checkDetail(report, "shutdown_exits_within_deadline"))
	}
}

// spec: 16.3, 15.7 (runtime SDK telemetry pass-through)
// diagnosis: each SDK exposes lenny/set_tracing_context through the
// platform tool helpers. The lenny-compliance harness does not verify
// OTel context propagation end to end; that path is not driven by an
// automated check.
func TestRuntimeSDKTelemetryPassThrough(t *testing.T) {
	t.Logf("non-skip — documented follow-on: §15.7 runtime SDK telemetry — the lenny/set_tracing_context helper ships in each SDK, but cmd/lenny-compliance has no OTel context-propagation check that asserts a runtime forwards traceparent through delegate_task to a child runtime")
}

// spec: 15.7, 24.18 (runtime SDK quick-start)
// diagnosis: the §24.18 lenny runtime init quick-start does not emit a
// usable runtime skeleton. The test scaffolds a Go minimal runtime
// through runtimescaffold.Generate, asserts it emits the §24.18 file
// set, and asserts the generated entrypoint imports the runtime-author
// SDK so the quick-start produces an SDK-based runtime rather than an
// empty stub. The scaffold-then-build path for every Go cell is
// covered by the cmd/lenny-ctl/runtimescaffold unit tests.
func TestRuntimeSDKQuickStartTTHW(t *testing.T) {
	dir := t.TempDir()
	start := time.Now()
	var out, errb bytes.Buffer
	code := runtimescaffold.Generate(runtimescaffold.Spec{
		Name:     "tthw-demo",
		Language: runtimescaffold.LangGo,
		Template: runtimescaffold.TemplateMinimal,
	}, dir, &out, &errb)
	if code != 0 {
		t.Fatalf("runtime init scaffold failed (exit %d): %s", code, errb.String())
	}
	elapsed := time.Since(start)
	// The §24.18 quick-start is an offline scaffold; the five-minute
	// time-to-hello-world target has ample headroom.
	if elapsed > 5*time.Minute {
		t.Errorf("scaffold took %s, over the five-minute quick-start target", elapsed)
	}

	root := filepath.Join(dir, "tthw-demo")
	for _, f := range runtimescaffold.EmittedFiles(runtimescaffold.LangGo, runtimescaffold.TemplateMinimal) {
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			t.Errorf("the quick-start scaffold did not emit %s: %v", f, err)
		}
	}

	mainGo, err := os.ReadFile(filepath.Join(root, "main.go"))
	if err != nil {
		t.Fatalf("read the scaffolded main.go: %v", err)
	}
	if !strings.Contains(string(mainGo), "github.com/lennylabs/runtime-sdk-go") {
		t.Errorf("the scaffolded main.go does not import the runtime-author SDK")
	}
	t.Logf("§24.18 quick-start scaffolded a Go minimal runtime in %s", elapsed)
}

// sdkProcess is a runtime binary driven frame by frame over its stdin and
// stdout.
type sdkProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	frames chan []byte
}

// startSDKProcess starts bin with stdin and stdout piped, reading stdout
// one JSON Lines frame at a time.
func startSDKProcess(t *testing.T, bin string) *sdkProcess {
	t.Helper()
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "LENNY_ADAPTER_MANIFEST="+filepath.Join(t.TempDir(), "absent.json"))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", bin, err)
	}
	p := &sdkProcess{cmd: cmd, stdin: stdin, frames: make(chan []byte, 64)}
	go func() {
		defer close(p.frames)
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			p.frames <- append([]byte(nil), sc.Bytes()...)
		}
	}()
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return p
}

// send writes one frame line.
func (p *sdkProcess) send(t *testing.T, line string) {
	t.Helper()
	if _, err := io.WriteString(p.stdin, line+"\n"); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
}

// next reads the next stdout frame, validates it against schema unless
// schema is nil, and returns it decoded.
func (p *sdkProcess) next(t *testing.T, schema *jsonschema.Schema) map[string]any {
	t.Helper()
	select {
	case raw, ok := <-p.frames:
		if !ok {
			t.Fatal("the runtime closed stdout")
		}
		var doc map[string]any
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&doc); err != nil {
			t.Fatalf("stdout frame is not JSON: %v (%s)", err, raw)
		}
		if schema == nil {
			return doc
		}
		if err := schema.Validate(doc); err != nil {
			t.Errorf("stdout frame does not validate against the published schema: %v\n%s", err, raw)
		}
		return doc
	case <-time.After(10 * time.Second):
		t.Fatal("no stdout frame within 10s")
		return nil
	}
}

// replyText returns output[0].inline of a response frame.
func replyText(doc map[string]any) string {
	out, _ := doc["output"].([]any)
	if len(out) == 0 {
		return ""
	}
	part, _ := out[0].(map[string]any)
	s, _ := part["inline"].(string)
	return s
}

// sessionFramesSchema compiles the published JSON Lines schema the
// session-frame contract cases validate every stdout frame against.
func sessionFramesSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	c := schematest.NewCompiler(t)
	schematest.MustAddLocalSchema(t, c, "https://schemas.lenny.dev/messagepart/v1.json", "schemas/messagepart.schema.json")
	return schematest.MustCompile(t, c, "schemas/lenny-adapter-jsonl.schema.json")
}

// assertSessionFramesContract drives an SDK-built echo runtime through two
// sequential sessions with different contexts and the session error paths,
// validating every stdout frame against the published schema. The echo
// runtime names the session and its experiment variant in each reply.
func assertSessionFramesContract(t *testing.T, bin string) {
	t.Helper()
	schema := sessionFramesSchema(t)
	p := startSDKProcess(t, bin)

	sessions := []struct{ id, startID, variant string }{
		{"sess_a", "st_1", "control"},
		{"sess_b", "st_2", "treatment"},
	}
	for _, s := range sessions {
		p.send(t, `{"type":"session_start","sessionId":"`+s.id+`","startId":"`+s.startID+`",`+
			`"experimentContext":{"experimentId":"exp_1","variantId":"`+s.variant+`","inherited":false},`+
			`"tracingContext":{"otel_trace_id":"0af7651916cd43dd"},`+
			`"llm":{"deliveryMode":"proxy","dialect":"anthropic","apiKeyEnv":"ANTHROPIC_API_KEY","headers":{"anthropic-version":"2023-06-01"}}}`)
		started := p.next(t, schema)
		if started["type"] != "session_started" || started["sessionId"] != s.id || started["startId"] != s.startID || started["error"] != nil {
			t.Fatalf("frame after session_start(%s) = %v, want session_started for %s without error", s.id, started, s.startID)
		}
		p.send(t, `{"type":"message","id":"msg_`+s.id+`","sessionId":"`+s.id+`","input":[{"type":"text","inline":"ping"}]}`)
		resp := p.next(t, schema)
		want := "session=" + s.id + " variant=" + s.variant
		if resp["type"] != "response" || resp["sessionId"] != s.id || !strings.Contains(replyText(resp), want) {
			t.Fatalf("response for %s = %v, want its sessionId and %q in the reply", s.id, resp, want)
		}
		// A duplicate session_start for a held session creates nothing and
		// is answered with session_started for its own startId.
		p.send(t, `{"type":"session_start","sessionId":"`+s.id+`","startId":"st_dup_`+s.id+`"}`)
		if dup := p.next(t, schema); dup["type"] != "session_started" || dup["sessionId"] != s.id || dup["startId"] != "st_dup_"+s.id || dup["error"] != nil {
			t.Fatalf("frame after a duplicate session_start(%s) = %v, want session_started for st_dup_%s without error", s.id, dup, s.id)
		}
		p.send(t, `{"type":"session_end","sessionId":"`+s.id+`"}`)
	}

	// A session_end for a session the runtime never held is ignored. A
	// message for an ended session is dropped without a response, so the
	// next frame answers the message for sess_x, a session the runtime
	// never read a session_start for, with an error response. A
	// session_start whose credential file cannot be read fails its
	// creation.
	p.send(t, `{"type":"session_end","sessionId":"sess_never"}`)
	p.send(t, `{"type":"message","id":"msg_late","sessionId":"sess_a","input":[{"type":"text","inline":"late"}]}`)
	p.send(t, `{"type":"message","id":"msg_x","sessionId":"sess_x","input":[{"type":"text","inline":"unknown"}]}`)
	if resp := p.next(t, schema); resp["type"] != "response" || resp["sessionId"] != "sess_x" || resp["error"] == nil {
		t.Fatalf("frame after a message for ended sess_a and unknown sess_x = %v, want only an error response for sess_x", resp)
	}
	missing := filepath.Join(t.TempDir(), "slots", "sess_c", "credentials.json")
	p.send(t, `{"type":"session_start","sessionId":"sess_c","startId":"st_3","credentialsPath":"`+missing+`"}`)
	if started := p.next(t, schema); started["type"] != "session_started" || started["startId"] != "st_3" || started["error"] == nil {
		t.Fatalf("frame = %v, want session_started for st_3 carrying error", started)
	}
	p.send(t, `{"type":"message","id":"msg_c","sessionId":"sess_c","input":[{"type":"text","inline":"ping"}]}`)
	if resp := p.next(t, schema); resp["type"] != "response" || resp["sessionId"] != "sess_c" || resp["error"] == nil {
		t.Fatalf("frame after a message for failed-create sess_c = %v, want an error response for sess_c", resp)
	}
	p.send(t, `{"type":"heartbeat","ts":1}`)
	if ack := p.next(t, schema); ack["type"] != "heartbeat_ack" {
		t.Fatalf("frame = %v, want heartbeat_ack from the still-running process", ack)
	}
	_ = p.stdin.Close()
	if err := p.cmd.Wait(); err != nil {
		t.Fatalf("runtime exit after stdin closed: %v", err)
	}
}

// spec: 28.5.3 (CH-MSGSOCK Outbound: session_started), 28.5.3 (CH-MSGSOCK
// Session errors, Inbound: session_start rule 3, Inbound: session_end rules
// 2 and 3), 4.7.10 (Runtime process lifetime), 15.7 (Runtime Author SDKs)
// diagnosis: the Go SDK wrote a session_started or response frame that
// does not validate against the published JSON Lines schema, or one
// runtime process did not serve two sequential sessions each under its
// own context. The adapter reads session_started by its sessionId and
// startId, and a runtime that answers the second session with the first
// session's context, or with the context it loaded at process start,
// serves session B under session A's experiment enrollment. The frames
// come from Go structs whose JSON tags no compiler checks against the
// schema, so the check reads the wire of the built echo example.
func TestGoRuntimeSDKSessionFramesValidateAgainstSchema_spec_28_5_3(t *testing.T) {
	assertSessionFramesContract(t, buildRuntimeBinary(t, "./sdks/runtime/go/example/echo"))
}

// spec: 28.5.3 (CH-MSGSOCK Outbound: session_started), 28.5.3 (CH-MSGSOCK
// Session errors, Inbound: session_start rule 3, Inbound: session_end rules
// 2 and 3), 4.7.10 (Runtime process lifetime), 15.7 (Runtime Author SDKs)
// diagnosis: the Python SDK wrote a session_started or response frame that
// does not validate against the published JSON Lines schema, or one
// runtime process did not serve two sequential sessions each under its
// own context. A Python SDK that loads one context per process answers
// session B with session A's experiment variant, writes no
// session_started for the adapter to read, or answers a message for an
// unknown or failed session with nothing. The test skips when python3 is
// not on PATH.
func TestPythonRuntimeSDKSessionFramesValidateAgainstSchema_spec_28_5_3(t *testing.T) {
	python := requireTool(t, "python3")
	assertSessionFramesContract(t, buildPythonRuntime(t, python, "echo"))
}

// spec: 28.5.3 (CH-MSGSOCK Outbound: session_started), 28.5.3 (CH-MSGSOCK
// Session errors, Inbound: session_start rule 3, Inbound: session_end rules
// 2 and 3), 4.7.10 (Runtime process lifetime), 15.7 (Runtime Author SDKs)
// diagnosis: the TypeScript SDK wrote a session_started or response frame
// that does not validate against the published JSON Lines schema, or one
// runtime process did not serve two sequential sessions each under its
// own context. A TypeScript SDK that loads one context per process answers
// session B with session A's experiment variant, writes no
// session_started for the adapter to read, or answers a message for an
// unknown or failed session with nothing. The test skips when the Node
// toolchain is not on PATH.
func TestTypeScriptRuntimeSDKSessionFramesValidateAgainstSchema_spec_28_5_3(t *testing.T) {
	node := requireTool(t, "node")
	npm := requireTool(t, "npm")
	assertSessionFramesContract(t, buildTypeScriptRuntime(t, node, npm, "echo"))
}

// pythonToolProbe is a Basic-level Python runtime whose handler reads one
// workspace file through the adapter-local read_file tool and answers with
// the content it read, or with the name of the error the call raised.
const pythonToolProbe = `
from lenny_runtime import Reply, run, text

class Probe:
    def on_create(self, req):
        pass

    def on_message(self, msg, tools):
        try:
            content = tools.adapter.read_file("notes.txt")
        except Exception as err:
            return Reply(parts=[text("error: " + type(err).__name__)], final=True)
        return Reply(parts=[text("read: " + content)], final=True)

    def on_terminate(self, session_id, reason):
        pass

run(Probe())
`

// typeScriptToolProbe is the TypeScript-SDK counterpart of
// pythonToolProbe, in the built JavaScript the package publishes. The
// single format verb is the absolute path of the built entrypoint.
const typeScriptToolProbe = `
import { run, text } from %q;

await run({
  onCreate: async () => {},
  onMessage: async (_msg, tools) => {
    try {
      const content = await tools.adapter.readFile("notes.txt");
      return { parts: [text("read: " + content)], final: true };
    } catch (err) {
      return { parts: [text("error: " + err.name)], final: true };
    }
  },
  onTerminate: async () => {},
});
`

// buildPythonProbe writes source as a Python probe runtime and returns an
// executable wrapper that runs it against the Python SDK package root.
func buildPythonProbe(t *testing.T, python, source string) string {
	t.Helper()
	root := pythonRuntimeRoot(t)
	probe := filepath.Join(t.TempDir(), "probe.py")
	if err := os.WriteFile(probe, []byte(source), 0o600); err != nil {
		t.Fatalf("write python probe: %v", err)
	}
	script := "#!/bin/sh\n" +
		"export PYTHONPATH=" + shellQuote(root) + "\n" +
		"exec " + shellQuote(python) + " " + shellQuote(probe) + " \"$@\"\n"
	return writeWrapper(t, "py-probe", script)
}

// buildTypeScriptProbe builds the TypeScript SDK, writes source (with the
// built entrypoint's path as its one format verb) as a probe runtime, and
// returns an executable wrapper that runs it with Node.
func buildTypeScriptProbe(t *testing.T, node, npm, source string) string {
	t.Helper()
	buildTypeScriptRuntime(t, node, npm, "echo")
	entry := filepath.Join(typeScriptRuntimeRoot(t), "dist", "src", "index.js")
	probe := filepath.Join(t.TempDir(), "probe.mjs")
	if err := os.WriteFile(probe, []byte(fmt.Sprintf(source, entry)), 0o600); err != nil {
		t.Fatalf("write typescript probe: %v", err)
	}
	script := "#!/bin/sh\n" +
		"exec " + shellQuote(node) + " " + shellQuote(probe) + " \"$@\"\n"
	return writeWrapper(t, "ts-probe", script)
}

// assertToolCallsAreSessionScoped drives the tool probe through two
// sessions. A tool_result must complete only the tool_call of the session
// its sessionId names, and a tool_call whose session ends before its
// result arrives must fail without the runtime writing any response for
// the ended session.
func assertToolCallsAreSessionScoped(t *testing.T, bin string) {
	t.Helper()
	schema := sessionFramesSchema(t)
	p := startSDKProcess(t, bin)
	for _, id := range []string{"sess_a", "sess_b"} {
		p.send(t, `{"type":"session_start","sessionId":"`+id+`","startId":"st_`+id+`"}`)
		if f := p.next(t, schema); f["type"] != "session_started" || f["sessionId"] != id {
			t.Fatalf("frame after session_start(%s) = %v, want its session_started", id, f)
		}
	}

	// A result naming another session leaves sess_a's call pending; the
	// result naming sess_a completes it.
	// The tool_call frames are read without schema validation: every
	// SDK's call id is hex after the tc_ prefix, while the schema's id
	// pattern is a ULID, a divergence outside this case's subject.
	p.send(t, `{"type":"message","id":"msg_a","sessionId":"sess_a","input":[{"type":"text","inline":"read"}]}`)
	call := p.next(t, nil)
	if call["type"] != "tool_call" || call["sessionId"] != "sess_a" || call["name"] != "read_file" {
		t.Fatalf("frame = %v, want sess_a's read_file tool_call", call)
	}
	callID, _ := call["id"].(string)
	p.send(t, `{"type":"tool_result","id":"`+callID+`","sessionId":"sess_b","content":[{"type":"text","inline":"wrong"}]}`)
	p.send(t, `{"type":"tool_result","id":"`+callID+`","sessionId":"sess_a","content":[{"type":"text","inline":"right"}]}`)
	if resp := p.next(t, schema); resp["type"] != "response" || resp["sessionId"] != "sess_a" || replyText(resp) != "read: right" {
		t.Fatalf("response = %v, want sess_a answered with the result addressed to sess_a", resp)
	}

	// sess_b's session_end fails its pending call, and the handler's
	// answer for the ended session is dropped, so the next frame is the
	// heartbeat_ack.
	p.send(t, `{"type":"message","id":"msg_b","sessionId":"sess_b","input":[{"type":"text","inline":"read"}]}`)
	callB := p.next(t, nil)
	if callB["type"] != "tool_call" || callB["sessionId"] != "sess_b" {
		t.Fatalf("frame = %v, want sess_b's tool_call", callB)
	}
	p.send(t, `{"type":"session_end","sessionId":"sess_b"}`)
	p.send(t, `{"type":"heartbeat","ts":1}`)
	if ack := p.next(t, schema); ack["type"] != "heartbeat_ack" {
		t.Fatalf("frame after sess_b's session_end = %v, want heartbeat_ack and no frame for the ended session", ack)
	}
	idB, _ := callB["id"].(string)
	p.send(t, `{"type":"tool_result","id":"`+idB+`","sessionId":"sess_b","content":[{"type":"text","inline":"late"}]}`)
	_ = p.stdin.Close()
	for raw := range p.frames {
		t.Fatalf("frame after stdin closed = %s, want none: no frame addresses the ended session", raw)
	}
	if err := p.cmd.Wait(); err != nil {
		t.Fatalf("runtime exit after stdin closed: %v", err)
	}
}

// spec: 28.5.3 (CH-MSGSOCK Inbound: tool_result, Inbound: session_end rule
// 2), 4.7.10 (Runtime process lifetime), 15.7 (Runtime Author SDKs)
// diagnosis: the Python or TypeScript SDK completed a session's tool_call
// with a tool_result addressed to another session, or wrote a frame for a
// session after reading its session_end. One runtime process serves every
// session the pod holds, so a result correlated by id alone can hand one
// session's workspace read to another, and a frame written after
// session_end reaches no session the adapter still serves. A subtest
// skips when its toolchain is absent.
func TestInterpretedRuntimeSDKToolCallsAreSessionScoped_spec_28_5_3(t *testing.T) {
	t.Run("python", func(t *testing.T) {
		python := requireTool(t, "python3")
		assertToolCallsAreSessionScoped(t, buildPythonProbe(t, python, pythonToolProbe))
	})
	t.Run("typescript", func(t *testing.T) {
		node := requireTool(t, "node")
		npm := requireTool(t, "npm")
		assertToolCallsAreSessionScoped(t, buildTypeScriptProbe(t, node, npm, typeScriptToolProbe))
	})
}

// handshakeFakeListener is a test-owned CH-MSGSOCK or CH-RUNTIMEOPS
// listener for the runtime connection handshake cases. It hands each
// accepted connection to the case.
type handshakeFakeListener struct {
	ln    net.Listener
	conns chan net.Conn
}

// startHandshakeFakeListener listens on path and accepts until the test
// ends.
func startHandshakeFakeListener(t *testing.T, path string) *handshakeFakeListener {
	t.Helper()
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen %s: %v", path, err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	l := &handshakeFakeListener{ln: ln, conns: make(chan net.Conn, 4)}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = c.Close() })
			l.conns <- c
		}
	}()
	return l
}

// next waits for the listener's next connection and checks its nonce line.
func (l *handshakeFakeListener) next(t *testing.T, which, nonce string) (net.Conn, *bufio.Reader) {
	t.Helper()
	select {
	case c := <-l.conns:
		r := bufio.NewReader(c)
		if err := runtimenonce.Check(c, r, nonce); err != nil {
			t.Fatalf("%s: %v", which, err)
		}
		return c, r
	case <-time.After(30 * time.Second):
		t.Fatalf("%s: the runtime did not dial", which)
		return nil, nil
	}
}

// challenge writes a nonce-only challenge and requires the runtime's
// HMAC answer keyed by nonce.
func handshakeChallenge(t *testing.T, conn net.Conn, r *bufio.Reader, nonce, which string) {
	t.Helper()
	challenge, err := mcp.NewChallenge()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte(`{"_lennyChallenge":"` + challenge + `"}` + "\n")); err != nil {
		t.Fatalf("%s: write challenge: %v", which, err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()
	answer, err := r.ReadBytes('\n')
	if err != nil {
		t.Fatalf("%s: read challenge answer: %v", which, err)
	}
	if err := mcp.ValidateChallengeResponse(answer, nonce, challenge); err != nil {
		t.Fatalf("%s: challenge answer %s: %v", which, bytes.TrimSpace(answer), err)
	}
}

// handshakeFrame reads frames from r until one of type want arrives.
func handshakeFrame(t *testing.T, conn net.Conn, r *bufio.Reader, want, which string) map[string]any {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			t.Fatalf("%s: read waiting for %s: %v", which, want, err)
		}
		var f map[string]any
		if json.Unmarshal(line, &f) == nil && f["type"] == want {
			return f
		}
	}
}

// listRecorder is a platform MCP tool provider that records tools/list.
type listRecorder struct {
	handshook chan struct{}
	listed    chan bool
}

func (l *listRecorder) List(context.Context) ([]mcp.Tool, error) {
	select {
	case <-l.handshook:
		l.listed <- true
	default:
		l.listed <- false
	}
	return nil, nil
}

func (l *listRecorder) Call(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return nil, errors.New("no tools")
}

// runtimeHandshakeCase runs one SDK's lifecycle example against test-owned
// CH-MSGSOCK and CH-RUNTIMEOPS listeners and a platform MCP server that
// requires the nonce-only challenge. The first connection on each runtime
// listener carries the manifest's first nonce; the test then rewrites the
// manifest with a second nonce and closes both connections. The runtime
// must read the manifest again, redial each socket with the second nonce,
// answer the challenge each listener then issues, and serve the protocol
// on the redialed connections.
func runtimeHandshakeCase(t *testing.T, argv ...string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "lenny-hs-")
	if err != nil {
		t.Fatalf("socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	msgSock, opsSock, mcpSock := filepath.Join(dir, "m.sock"), filepath.Join(dir, "o.sock"), filepath.Join(dir, "p.sock")
	msg := startHandshakeFakeListener(t, msgSock)
	ops := startHandshakeFakeListener(t, opsSock)

	first := runtimenonce.NewNonce(t)
	fields := map[string]any{
		"platformMcpServer": map[string]any{"socket": mcpSock},
		"runtimeOps":        map[string]any{"socket": opsSock},
		"connectorServers":  []any{},
		"adapterLocalTools": []any{},
	}
	manifest := filepath.Join(dir, runtimenonce.ManifestFilename)
	runtimenonce.Rewrite(t, manifest, first, fields)

	// The platform MCP surface validates the nonce of the start that armed
	// it, the first one, and issues the nonce-only challenge.
	recorder := &listRecorder{handshook: make(chan struct{}), listed: make(chan bool, 4)}
	srv := mcp.NewServer()
	srv.RequireChallenge = true
	srv.Provider = recorder
	var once sync.Once
	srv.OnHandshake = func() { once.Do(func() { close(recorder.handshook) }) }
	mcpLn, err := net.Listen("unix", mcpSock)
	if err != nil {
		t.Fatalf("listen platform MCP: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = srv.Serve(ctx, mcpLn, first) }()

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), "LENNY_ADAPTER_SOCKET="+msgSock, "LENNY_ADAPTER_MANIFEST="+manifest)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start runtime: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if t.Failed() {
			t.Logf("runtime stderr:\n%s", stderr.String())
		}
	})

	msg1, _ := msg.next(t, "first CH-MSGSOCK connection", first)
	select {
	case afterChallenge := <-recorder.listed:
		if !afterChallenge {
			t.Fatal("MCP tools/list arrived before the challenge answer")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("MCP tools/list never arrived after the challenge answer")
	}
	ops1, _ := ops.next(t, "first CH-RUNTIMEOPS connection", first)

	second := runtimenonce.NewNonce(t)
	runtimenonce.Rewrite(t, manifest, second, fields)
	_ = msg1.Close()
	_ = ops1.Close()

	ops2, opsR := ops.next(t, "redialed CH-RUNTIMEOPS connection", second)
	handshakeChallenge(t, ops2, opsR, second, "CH-RUNTIMEOPS")
	if err := json.NewEncoder(ops2).Encode(map[string]any{
		"type": "lifecycle_capabilities", "protocolVersion": "1.0", "capabilities": []string{"checkpoint"},
	}); err != nil {
		t.Fatalf("write lifecycle_capabilities: %v", err)
	}
	handshakeFrame(t, ops2, opsR, "lifecycle_support", "CH-RUNTIMEOPS")

	msg2, msgR := msg.next(t, "redialed CH-MSGSOCK connection", second)
	handshakeChallenge(t, msg2, msgR, second, "CH-MSGSOCK")
	for _, frame := range []string{probeStart("sess_hs", "st_hs", "v"), probeMessage("sess_hs", "m_hs")} {
		if _, err := msg2.Write([]byte(frame + "\n")); err != nil {
			t.Fatalf("write %s: %v", frame, err)
		}
	}
	if f := handshakeFrame(t, msg2, msgR, "response", "CH-MSGSOCK"); f["sessionId"] != "sess_hs" || f["error"] != nil {
		t.Fatalf("response on the redialed connection = %v, want an answer for sess_hs", f)
	}
}

// spec: 4.7.11 (Runtime connection handshake, Nonce-only fallback), 4.7.6
// (mcpNonce row), 15.7 (Runtime Author SDKs)
// diagnosis: an SDK's runtime half of the runtime connection handshake is
// broken. Either its first line on CH-MSGSOCK or CH-RUNTIMEOPS is not the
// manifest's nonce, it did not read the manifest again and redial when the
// adapter closed a connection presenting a replaced nonce, it did not
// answer a challenge that arrived before the first protocol frame, or its
// MCP client did not answer the challenge a nonce-only platform MCP server
// writes in place of the initialize response. Any of these leaves an SDK
// runtime unable to connect to an adapter that enforces the handshake.
func TestRuntimeSDKConnectionHandshake_spec_4_7_11(t *testing.T) {
	t.Run("go", func(t *testing.T) {
		runtimeHandshakeCase(t, buildRuntimeBinary(t, "./sdks/runtime/go/example/lifecycle"))
	})
	t.Run("python", func(t *testing.T) {
		python := requireTool(t, "python3")
		runtimeHandshakeCase(t, buildPythonRuntime(t, python, "lifecycle"))
	})
	t.Run("typescript", func(t *testing.T) {
		node := requireTool(t, "node")
		npm := requireTool(t, "npm")
		runtimeHandshakeCase(t, buildTypeScriptRuntime(t, node, npm, "lifecycle"))
	})
}
