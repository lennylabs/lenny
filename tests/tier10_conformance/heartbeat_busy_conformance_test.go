// SPDX-License-Identifier: MIT

//go:build conformance

// Tier-10 conformance cases for the Basic-level heartbeat_busy check: a
// heartbeat_ack that a runtime writes while a message for the session is
// outstanding carries busy: true and the session's sessionId. The check
// passes on every bundled reference runtime, on every SDK scaffold the
// platform ships, on a Go SDK runtime whose handler outlasts the heartbeat,
// and on a runtime that reports busy on every ack, and it fails on a runtime
// that answers a bare ack while it is handling a message.

package tier10_conformance_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/cmd/lenny-ctl/runtimescaffold"
)

// heartbeatBusyCheck is the conformance check this file pins.
const heartbeatBusyCheck = "heartbeat_busy"

// conformanceFixture builds the fixture under
// tests/testinfra/runtimes/conformance-fixtures/<name>.
func conformanceFixture(t *testing.T, name string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), name)
	cmd := exec.Command("go", "build", "-o", bin, "./tests/testinfra/runtimes/conformance-fixtures/"+name)
	cmd.Dir = repoRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fixture %s: %v\n%s", name, err, out)
	}
	return bin
}

// heartbeatBusyResult returns the heartbeat_busy check of the Basic battery
// run against binary.
func heartbeatBusyResult(t *testing.T, a *builtArtifacts, binary string) (bool, string) {
	t.Helper()
	report := runCompliance(t, a, binary, "basic")
	for _, c := range report.Checks {
		if c.Name == heartbeatBusyCheck {
			return c.Pass, c.Detail
		}
	}
	t.Fatalf("the Basic battery ran no %s check", heartbeatBusyCheck)
	return false, ""
}

// requireHeartbeatBusyPass fails when the check did not pass on binary.
func requireHeartbeatBusyPass(t *testing.T, a *builtArtifacts, name, binary string) {
	t.Helper()
	if pass, detail := heartbeatBusyResult(t, a, binary); !pass {
		t.Errorf("%s failed %s: %s", name, heartbeatBusyCheck, detail)
	}
}

// spec: 15.4.6 (Conformance Test Suite), 28.5.3 (CH-MSGSOCK)
// diagnosis: the heartbeat_busy check either rejects a conforming runtime
// (a reference runtime, a shipped SDK scaffold, a Go SDK runtime whose
// handler is still running when the heartbeat arrives, or a runtime that
// reports busy on every ack) or accepts a runtime that answers a bare ack
// while it handles a message. The first means an SDK or a reference runtime
// stopped attributing an in-flight handler to its session, or the check
// judges acks written after the response; the second means the check no
// longer reads the ack's busy field and sessionId.
func TestHeartbeatBusyConformance_spec_15_4_6(t *testing.T) {
	a := buildArtifacts(t)

	t.Run("reference runtimes", func(t *testing.T) {
		for name, bin := range map[string]string{
			"echo": a.echo, "streaming-echo": a.streamingEcho,
			"delegation-echo": a.delegationEcho, "echo-concurrent": a.echoConcurrent,
		} {
			requireHeartbeatBusyPass(t, a, name, bin)
		}
	})

	t.Run("SDK scaffolds", func(t *testing.T) {
		for _, cell := range scaffoldCells() {
			t.Run(string(cell.lang)+"/"+string(cell.tmpl), func(t *testing.T) {
				requireHeartbeatBusyPass(t, a, string(cell.lang)+"/"+string(cell.tmpl), buildScaffold(t, cell))
			})
		}
	})

	t.Run("SDK runtime that delays its response", func(t *testing.T) {
		bin := conformanceFixture(t, "sdk-delayed-response")
		requireHeartbeatBusyPass(t, a, "sdk-delayed-response", bin)
		// The pass is only meaningful when the ack precedes the response.
		ack := firstAckBeforeResponse(t, bin)
		if ack == nil {
			t.Fatal("the fixture answered the message before the heartbeat, so the check judged no in-flight ack")
		}
		if ack["busy"] != true || ack["sessionId"] != busySessionID {
			t.Errorf("in-flight ack = %v, want busy true for %s", ack, busySessionID)
		}
	})

	t.Run("always busy", func(t *testing.T) {
		requireHeartbeatBusyPass(t, a, "always-busy", conformanceFixture(t, "always-busy"))
	})

	t.Run("bare ack while handling", func(t *testing.T) {
		pass, detail := heartbeatBusyResult(t, a, conformanceFixture(t, "bare-ack-while-handling"))
		if pass {
			t.Fatal("heartbeat_busy passed a runtime that answers a bare ack while a message is outstanding")
		}
		if !strings.Contains(detail, "busy: true") {
			t.Errorf("failure detail = %q, want it to name the missing busy report", detail)
		}
	})
}

// scaffoldCell is one language and template the scaffolder emits.
type scaffoldCell struct {
	lang runtimescaffold.Language
	tmpl runtimescaffold.Template
}

// scaffoldCells are the cells the scaffolder supports: every template for
// each SDK language, and the minimal template for the SDK-free binary.
func scaffoldCells() []scaffoldCell {
	var out []scaffoldCell
	for _, lang := range []runtimescaffold.Language{runtimescaffold.LangGo, runtimescaffold.LangPython, runtimescaffold.LangTypeScript} {
		for _, tmpl := range []runtimescaffold.Template{runtimescaffold.TemplateMinimal, runtimescaffold.TemplateCoding, runtimescaffold.TemplateChat} {
			out = append(out, scaffoldCell{lang, tmpl})
		}
	}
	return append(out, scaffoldCell{runtimescaffold.LangBinary, runtimescaffold.TemplateMinimal})
}

// buildScaffold generates the cell and returns an executable that runs it
// against this repository's runtime-author SDK.
func buildScaffold(t *testing.T, cell scaffoldCell) string {
	t.Helper()
	base := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := runtimescaffold.Generate(runtimescaffold.Spec{
		Name: "busy", Language: cell.lang, Template: cell.tmpl,
	}, base, &stdout, &stderr); code != runtimescaffold.ExitOK {
		t.Fatalf("scaffold %s/%s: exit %d, stderr=%q", cell.lang, cell.tmpl, code, stderr.String())
	}
	src := filepath.Join(base, "busy")
	switch cell.lang {
	case runtimescaffold.LangGo:
		return buildGoScaffold(t, src)
	case runtimescaffold.LangPython:
		return pythonScaffold(t, src)
	case runtimescaffold.LangTypeScript:
		return typeScriptScaffold(t, src)
	default:
		return goBuild(t, src, os.Environ())
	}
}

// goBuild builds the module at dir and returns the binary.
func goBuild(t *testing.T, dir string, env []string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "runtime")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = dir
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", dir, err, out)
	}
	return bin
}

// goSDKShim is a module named as the published Go runtime-author SDK whose
// package re-exports, by alias, the identifiers the Go scaffolds use from
// this repository's SDK, so a scaffold builds offline against the SDK under
// test.
const goSDKShim = `package runtime

import sdk "github.com/lennylabs/lenny/sdks/runtime/go/runtime"

type (
	CreateRequest     = sdk.CreateRequest
	CredentialBundle  = sdk.CredentialBundle
	Handler           = sdk.Handler
	Message           = sdk.Message
	MessagePart       = sdk.MessagePart
	Reply             = sdk.Reply
	TerminationReason = sdk.TerminationReason
)

var (
	ErrIsProtocol         = sdk.ErrIsProtocol
	OnCheckpoint          = sdk.OnCheckpoint
	OnCredentialsRotated  = sdk.OnCredentialsRotated
	OnInterrupt           = sdk.OnInterrupt
	Run                   = sdk.Run
	Text                  = sdk.Text
	WithFullLevel         = sdk.WithFullLevel
	WithLifecycleHandlers = sdk.WithLifecycleHandlers
	WithStandardLevel     = sdk.WithStandardLevel
)
`

// buildGoScaffold builds a Go scaffold against the shim, resolving every
// dependency from the local module cache.
func buildGoScaffold(t *testing.T, src string) string {
	t.Helper()
	root := repoRoot(t)
	shim := t.TempDir()
	scaffoldWrite(t, filepath.Join(shim, "go.mod"), "module github.com/lennylabs/runtime-sdk-go\n\ngo 1.25\n\n"+
		"require github.com/lennylabs/lenny v0.0.0\n\nreplace github.com/lennylabs/lenny => "+root+"\n")
	scaffoldWrite(t, filepath.Join(shim, "shim.go"), goSDKShim)
	f, err := os.OpenFile(filepath.Join(src, "go.mod"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open the scaffold's go.mod: %v", err)
	}
	_, err = f.WriteString("\nreplace github.com/lennylabs/runtime-sdk-go => " + shim +
		"\n\nreplace github.com/lennylabs/lenny => " + root + "\n")
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatalf("write the scaffold's go.mod: %v", err)
	}
	return goBuild(t, src, append(os.Environ(), "GOFLAGS=-mod=mod", "GOPROXY=off", "GOSUMDB=off"))
}

// pythonScaffold returns a wrapper that runs the scaffold's main.py with
// this repository's Python SDK on PYTHONPATH.
func pythonScaffold(t *testing.T, src string) string {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skipf("blocked: python3 is not on PATH; the Python scaffold cell needs it")
	}
	sdk := filepath.Join(repoRoot(t), "sdks", "runtime", "python")
	return scaffoldWrapper(t, "export PYTHONPATH="+shellArg(sdk)+"\nexec "+shellArg(python)+" "+shellArg(filepath.Join(src, "main.py")))
}

// typeScriptScaffold compiles the scaffold's main.ts against this
// repository's TypeScript SDK and returns a wrapper that runs it with Node.
// The SDK's build emits its entry point under dist/src, so the package the
// scaffold imports is a manifest naming that entry point over the SDK's
// dist directory.
func typeScriptScaffold(t *testing.T, src string) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("blocked: node is not on PATH; the TypeScript scaffold cell needs it")
	}
	sdk := filepath.Join(repoRoot(t), "sdks", "runtime", "typescript")
	tsc := filepath.Join(sdk, "node_modules", ".bin", "tsc")
	if _, err := os.Stat(tsc); err != nil {
		t.Skipf("blocked: %s is absent; run npm install in the TypeScript SDK", tsc)
	}
	build := exec.Command(tsc, "-p", "tsconfig.json")
	build.Dir = sdk
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build the TypeScript SDK: %v\n%s", err, out)
	}
	pkg := t.TempDir()
	scaffoldWrite(t, filepath.Join(pkg, "package.json"),
		`{"name":"@lennylabs/runtime-sdk","version":"0.1.0","type":"module","main":"./dist/src/index.js","types":"./dist/src/index.d.ts"}`)
	scaffoldLink(t, filepath.Join(sdk, "dist"), filepath.Join(pkg, "dist"))
	scaffoldLink(t, pkg, filepath.Join(src, "node_modules", "@lennylabs", "runtime-sdk"))
	scaffoldLink(t, filepath.Join(sdk, "node_modules", "@types"), filepath.Join(src, "node_modules", "@types"))
	compile := exec.Command(tsc, "-p", "tsconfig.json")
	compile.Dir = src
	if out, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile the TypeScript scaffold: %v\n%s", err, out)
	}
	return scaffoldWrapper(t, "exec "+shellArg(node)+" "+shellArg(filepath.Join(src, "dist", "main.js")))
}

func scaffoldWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func scaffoldLink(t *testing.T, target, at string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
		t.Fatalf("make %s: %v", filepath.Dir(at), err)
	}
	if err := os.Symlink(target, at); err != nil {
		t.Fatalf("link %s: %v", at, err)
	}
}

// scaffoldWrapper writes an executable shell script running body.
func scaffoldWrapper(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runtime.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+" \"$@\"\n"), 0o755); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}
	return path
}

func shellArg(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// busySessionID is the session the direct drive below opens.
const busySessionID = "sess_busy_fixture"

// firstAckBeforeResponse runs bin, opens a session, writes a message and
// then a heartbeat naming the session, and returns the first heartbeat_ack
// the runtime wrote before the response, or nil when the response came
// first.
func firstAckBeforeResponse(t *testing.T, bin string) map[string]any {
	t.Helper()
	cmd := exec.Command(bin)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", bin, err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
		}
	})
	for _, line := range []string{
		`{"type":"session_start","sessionId":"` + busySessionID + `","startId":"start-1","experimentContext":null,"tracingContext":null,"llm":null}`,
		`{"type":"message","id":"msg-1","sessionId":"` + busySessionID + `","from":{"kind":"client","id":"alice"},"input":[{"type":"text","inline":"ping"}]}`,
		`{"type":"heartbeat","ts":1,"sessionId":"` + busySessionID + `"}`,
	} {
		if _, err := io.WriteString(stdin, line+"\n"); err != nil {
			t.Fatalf("write %s: %v", line, err)
		}
	}
	frames := make(chan map[string]any, 16)
	go func() {
		defer close(frames)
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			var f map[string]any
			if json.Unmarshal(sc.Bytes(), &f) == nil {
				frames <- f
			}
		}
	}()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				t.Fatal("the runtime closed stdout before answering")
			}
			switch f["type"] {
			case "heartbeat_ack":
				return f
			case "response":
				return nil
			}
		case <-deadline:
			t.Fatal("no heartbeat_ack or response within 10s")
		}
	}
}
