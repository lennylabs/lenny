// SPDX-License-Identifier: MIT

//go:build contract

// Test the SDK harness against an in-process echo helper. The echo
// helper accepts every command and returns ok:true with a result
// that mirrors the args. Two echo helpers must always agree per
// AssertEquivalent.
//
// When real SDK helpers ship under sdks/client/{go,python,typescript}/
// test-helper they swap in by changing the path passed to
// harness.Spawn.

package harness_test

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/tests/tier3_contract/sdks/harness"
)

// buildEchoHelper compiles tests/testinfra/sdkhelper/echo into a
// throwaway binary that the harness drives. The binary is built
// once per test invocation.
func buildEchoHelper(t *testing.T) string {
	t.Helper()
	root := repoRoot(t)
	src := filepath.Join(root, "tests", "testinfra", "sdkhelper", "echo")
	out := filepath.Join(t.TempDir(), "echo-helper")
	cmd := exec.Command("go", "build", "-o", out, "./tests/testinfra/sdkhelper/echo")
	cmd.Dir = root
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build echo helper: %v\n%s", err, combined)
	}
	_ = src
	return out
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, _ := os.Getwd()
	for d := wd; d != "/" && d != ""; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
	}
	t.Fatalf("no go.mod from %s", wd)
	return ""
}

// spec: 14.13 (two helpers reading the same matrix agree on every command)
// diagnosis: A divergence between two identical helpers means the
//
//	harness reads / writes the pipe wrong, or the equivalence
//	comparator misreads the response shape.
func TestEchoHelpersAgree(t *testing.T) {
	bin := buildEchoHelper(t)
	a := harness.Spawn(t, "echo-a", bin, nil)
	b := harness.Spawn(t, "echo-b", bin, nil)
	cases := []harness.MatrixCase{
		{Name: "create_session", Command: harness.Command{Op: "create_session", Args: map[string]any{"runtime": "echo"}}},
		{Name: "get_session", Command: harness.Command{Op: "get_session", Args: map[string]any{"id": "s-1"}}},
	}
	harness.AssertEquivalent(t, []*harness.Helper{a, b}, cases)
}

// spec: 14.13 (round-trip through the harness preserves bytes)
// diagnosis: A request mutated or dropped a field on its way through
//
//	the helper. The harness or the helper is misframing.
func TestEchoRoundTripPreservesArgs(t *testing.T) {
	bin := buildEchoHelper(t)
	h := harness.Spawn(t, "echo", bin, nil)
	resp, err := h.Send(harness.Command{Op: "create_session", Args: map[string]any{"runtime": "echo", "extra": []any{"a", "b"}}})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if !resp.OK {
		t.Errorf("expected ok, got %v", resp)
	}
	body, _ := json.Marshal(resp.Result)
	if !contains(string(body), `"runtime":"echo"`) {
		t.Errorf("result lost runtime: %s", body)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// guard against unused imports — the harness package supports
// streaming + cancellation paths in future expansion that will need
// bufio + io.
var (
	_ = bufio.NewReader
	_ = io.Discard
)
