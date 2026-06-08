// SPDX-License-Identifier: MIT

package ctlcli

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runtimeInitInDir runs `lenny-ctl runtime init` with the process
// working directory set to a fresh temp dir, so the scaffolder writes
// into an isolated location. It returns the exit code and the temp dir.
func runtimeInitInDir(t *testing.T, args ...string) (int, string) {
	t.Helper()
	dir := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	var stdout, stderr bytes.Buffer
	code := run(append([]string{"runtime", "init"}, args...), &stdout, &stderr)
	return code, dir
}

// TestRuntimeInitMissingLanguageExit6 checks that omitting --language
// exits 6 (MISSING_REQUIRED_FLAG).
func TestRuntimeInitMissingLanguageExit6(t *testing.T) {
	code, _ := runtimeInitInDir(t, "my-agent", "--template", "minimal")
	if code != 6 {
		t.Errorf("missing --language: exit %d, want 6", code)
	}
}

// TestRuntimeInitMissingTemplateExit6 checks that omitting --template
// exits 6 (MISSING_REQUIRED_FLAG).
func TestRuntimeInitMissingTemplateExit6(t *testing.T) {
	code, _ := runtimeInitInDir(t, "my-agent", "--language", "go")
	if code != 6 {
		t.Errorf("missing --template: exit %d, want 6", code)
	}
}

// TestRuntimeInitInvalidLanguageExit6 checks that an unknown --language
// value exits 6.
func TestRuntimeInitInvalidLanguageExit6(t *testing.T) {
	code, _ := runtimeInitInDir(t, "my-agent", "--language", "rust", "--template", "minimal")
	if code != 6 {
		t.Errorf("invalid --language: exit %d, want 6", code)
	}
}

// TestRuntimeInitInvalidNameExit2 checks that an invalid runtime name
// exits 2 (INVALID_RUNTIME_NAME).
func TestRuntimeInitInvalidNameExit2(t *testing.T) {
	code, _ := runtimeInitInDir(t, "Bad_Name", "--language", "go", "--template", "minimal")
	if code != 2 {
		t.Errorf("invalid name: exit %d, want 2", code)
	}
}

// TestRuntimeInitRejectedCombinationExit5 checks that binary+coding
// exits 5 (SCAFFOLD_UNSUPPORTED_COMBINATION).
func TestRuntimeInitRejectedCombinationExit5(t *testing.T) {
	code, _ := runtimeInitInDir(t, "x", "--language", "binary", "--template", "coding")
	if code != 5 {
		t.Errorf("binary+coding: exit %d, want 5", code)
	}
}

// TestRuntimeInitTargetExistsExit3 checks that an existing target
// directory exits 3 (TARGET_DIRECTORY_EXISTS) without --force.
func TestRuntimeInitTargetExistsExit3(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	if err := os.Mkdir(filepath.Join(dir, "taken"), 0o755); err != nil {
		t.Fatalf("pre-create target: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"runtime", "init", "taken", "--language", "go", "--template", "minimal",
	}, &stdout, &stderr)
	if code != 3 {
		t.Errorf("existing target: exit %d, want 3", code)
	}
}

// TestRuntimeInitSuccessGo checks the success path: a Go minimal cell
// exits 0 and writes the entrypoint and runtime.yaml.
func TestRuntimeInitSuccessGo(t *testing.T) {
	code, dir := runtimeInitInDir(t, "good-rt", "--language", "go", "--template", "minimal")
	if code != 0 {
		t.Fatalf("go minimal init: exit %d, want 0", code)
	}
	for _, f := range []string{"main.go", "runtime.yaml", "Dockerfile", "Makefile", "go.mod"} {
		if _, err := os.Stat(filepath.Join(dir, "good-rt", f)); err != nil {
			t.Errorf("expected %s to be written: %v", f, err)
		}
	}
}

// TestRuntimeInitHelp checks that `runtime init --help` exits 0 and
// prints usage.
func TestRuntimeInitHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"runtime", "init", "--help"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runtime init --help: exit %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "Language x Template matrix") {
		t.Errorf("runtime init --help: usage text missing the matrix reference")
	}
}

// TestRuntimeUnknownSubcommand checks that an unknown runtime
// subcommand exits 2.
func TestRuntimeUnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"runtime", "frobnicate"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("unknown runtime subcommand: exit %d, want 2", code)
	}
}

// TestRuntimeValidateOnScaffold checks that `runtime validate` accepts a
// freshly scaffolded repository.
func TestRuntimeValidateOnScaffold(t *testing.T) {
	code, dir := runtimeInitInDir(t, "vr", "--language", "python", "--template", "chat")
	if code != 0 {
		t.Fatalf("scaffold for validate: exit %d", code)
	}
	var stdout, stderr bytes.Buffer
	vc := run([]string{"runtime", "validate", filepath.Join(dir, "vr")}, &stdout, &stderr)
	if vc != 0 {
		t.Fatalf("validate scaffold: exit %d, want 0\nstdout=%q", vc, stdout.String())
	}
	if !strings.Contains(stdout.String(), "Result: pass") {
		t.Errorf("validate scaffold: report does not show a pass:\n%s", stdout.String())
	}
}

// TestRuntimeValidateRejectsBrokenRepo checks that `runtime validate`
// exits 1 and reports the issues for a malformed runtime repository.
func TestRuntimeValidateRejectsBrokenRepo(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "runtime.yaml"),
		[]byte("name: NOT VALID\ntype: gadget\n"), 0o600); err != nil {
		t.Fatalf("write runtime.yaml: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"runtime", "validate", dir}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("broken repo: exit %d, want 1", code)
	}
	out := stdout.String()
	if !strings.Contains(out, "Dockerfile is missing") {
		t.Errorf("broken repo: report does not flag the missing Dockerfile:\n%s", out)
	}
	if !strings.Contains(out, "type \"gadget\"") {
		t.Errorf("broken repo: report does not flag the invalid type:\n%s", out)
	}
}

// TestRuntimeValidateReportFlagWritesFile checks that `runtime validate
// --report <path>` writes the machine-readable JSON report (§15.4.6).
func TestRuntimeValidateReportFlagWritesFile(t *testing.T) {
	code, dir := runtimeInitInDir(t, "rr", "--language", "go", "--template", "minimal")
	if code != 0 {
		t.Fatalf("scaffold: exit %d", code)
	}
	reportPath := filepath.Join(t.TempDir(), "report.json")
	var stdout, stderr bytes.Buffer
	vc := run([]string{"runtime", "validate", filepath.Join(dir, "rr"), "--report", reportPath}, &stdout, &stderr)
	if vc != 0 {
		t.Fatalf("validate --report: exit %d, want 0\nstdout=%q", vc, stdout.String())
	}
	if _, err := os.Stat(reportPath); err != nil {
		t.Fatalf("--report did not write the file: %v", err)
	}
	if !strings.Contains(stdout.String(), "Report written to") {
		t.Errorf("validate --report: stdout should confirm the write:\n%s", stdout.String())
	}
}

// TestRuntimeValidateBinaryRequiresValue checks the --binary flag rejects
// a missing value with exit 2.
func TestRuntimeValidateBinaryRequiresValue(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"runtime", "validate", ".", "--binary"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("validate --binary with no value: exit %d, want 2", code)
	}
}

// TestRuntimeValidateUnknownFlag checks that an unknown validate flag is
// a usage error.
func TestRuntimeValidateUnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"runtime", "validate", ".", "--nope"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("validate --nope: exit %d, want 2", code)
	}
}

// TestRuntimeValidateMissingPathExit2 checks that an unreadable path
// argument exits 2.
func TestRuntimeValidateMissingPathExit2(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"runtime", "validate", filepath.Join(t.TempDir(), "absent")},
		&stdout, &stderr)
	if code != 2 {
		t.Errorf("validate absent path: exit %d, want 2", code)
	}
}

// TestRuntimePublishRequiresImage checks that `runtime publish` without
// --image exits 2 before any gateway call.
func TestRuntimePublishRequiresImage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"runtime", "publish", "my-agent"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("publish without --image: exit %d, want 2", code)
	}
}

// TestRuntimePublishRegistersAgainstGateway checks that `runtime
// publish --skip-push` registers the runtime against the gateway via
// POST /v1/admin/runtimes.
func TestRuntimePublishRegistersAgainstGateway(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"name":"my-agent"}`,
		"--token", "admin-tok",
		"runtime", "publish", "my-agent",
		"--image", "ghcr.io/acme/runtime-my-agent:1.0.0", "--skip-push")
	if code != 0 {
		t.Fatalf("publish --skip-push: exit %d, want 0", code)
	}
	if got.method != http.MethodPost || got.path != "/v1/admin/runtimes" {
		t.Fatalf("publish request: %s %s, want POST /v1/admin/runtimes", got.method, got.path)
	}
	if got.body["name"] != "my-agent" {
		t.Errorf("publish body name: %+v", got.body)
	}
	if got.body["image"] != "ghcr.io/acme/runtime-my-agent:1.0.0" {
		t.Errorf("publish body image: %+v", got.body)
	}
	if got.body["type"] != "agent" {
		t.Errorf("publish body type: %+v, want agent", got.body)
	}
}

// TestRuntimePublishRequiresAdminToken checks the §24.18 line 232
// requirement: publish fails fast with a CLI-side diagnostic when no
// admin token is configured, before any docker push or gateway call.
func TestRuntimePublishRequiresAdminToken(t *testing.T) {
	clearCLIEnv(t)
	var stdout, stderr bytes.Buffer
	// No --token and no dev headers: the client carries no credential.
	code := run([]string{"--api-url", "https://gw.example.com",
		"runtime", "publish", "my-agent",
		"--image", "ghcr.io/acme/runtime-my-agent:1.0.0", "--skip-push"},
		&stdout, &stderr)
	if code != 2 {
		t.Fatalf("publish without a token: exit %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "no admin token configured") {
		t.Errorf("publish without a token: stderr %q, want the admin-token diagnostic", stderr.String())
	}
	if !strings.Contains(stderr.String(), "gw.example.com") {
		t.Errorf("publish without a token: diagnostic should name the --api-url, got %q", stderr.String())
	}
}

// TestRuntimePublishWithManifest checks that `runtime publish
// --manifest` registers the runtime.yaml fields, with the name and
// image arguments overriding the manifest.
func TestRuntimePublishWithManifest(t *testing.T) {
	manifest := writeSeedFile(t, "runtime.yaml", `name: from-manifest
image: from-manifest-image
type: agent
integrationLevel: full
`)
	code, got := runAgainstGateway(t, http.StatusOK, `{"name":"my-agent"}`,
		"--token", "admin-tok",
		"runtime", "publish", "my-agent",
		"--image", "ghcr.io/acme/runtime-my-agent:2.0.0",
		"--manifest", manifest, "--skip-push")
	if code != 0 {
		t.Fatalf("publish --manifest: exit %d, want 0", code)
	}
	// The name and image arguments win over the manifest.
	if got.body["name"] != "my-agent" || got.body["image"] != "ghcr.io/acme/runtime-my-agent:2.0.0" {
		t.Errorf("publish --manifest: arguments did not override manifest: %+v", got.body)
	}
	// Other manifest fields are carried through.
	if got.body["integrationLevel"] != "full" {
		t.Errorf("publish --manifest: integrationLevel not carried: %+v", got.body)
	}
}

// TestAdminRuntimesRegister checks that `admin runtimes register`
// posts the runtime.yaml to POST /v1/admin/runtimes.
func TestAdminRuntimesRegister(t *testing.T) {
	manifest := writeSeedFile(t, "runtime.yaml", `name: echo
image: ghcr.io/lennylabs/echo:latest
type: agent
`)
	code, got := runAgainstGateway(t, http.StatusOK, `{"name":"echo"}`,
		"admin", "runtimes", "register", "--manifest", manifest)
	if code != 0 {
		t.Fatalf("admin runtimes register: exit %d, want 0", code)
	}
	if got.method != http.MethodPost || got.path != "/v1/admin/runtimes" {
		t.Fatalf("register request: %s %s", got.method, got.path)
	}
	if got.body["name"] != "echo" {
		t.Errorf("register body: %+v", got.body)
	}
}

// TestAdminRuntimesRegisterRequiresManifest checks that `admin runtimes
// register` without --manifest exits 2.
func TestAdminRuntimesRegisterRequiresManifest(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"admin", "runtimes", "register"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("register without --manifest: exit %d, want 2", code)
	}
}
