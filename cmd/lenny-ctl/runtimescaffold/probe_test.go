// SPDX-License-Identifier: MIT

package runtimescaffold

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/pkg/compliance"
)

// spec: §15.4.6 declared-vs-observed reconciliation.

// chk builds a compliance.Check with the given name and pass state.
func chk(name string, pass bool) compliance.Check {
	return compliance.Check{Name: name, Pass: pass}
}

// fullPassChecks returns a check set where every category passes, so the
// observed level is full.
func fullPassChecks() []compliance.Check {
	var cs []compliance.Check
	for name := range checkLevel {
		cs = append(cs, chk(name, true))
	}
	return cs
}

func TestDeriveObservedLevel(t *testing.T) {
	tests := []struct {
		name   string
		checks []compliance.Check
		want   compliance.Level
	}{
		{"full when lifecycle passes", fullPassChecks(), compliance.LevelFull},
		{
			"standard when only nonce passes",
			[]compliance.Check{chk("mcp_nonce_handshake", true), chk("lifecycle_channel_opening", false)},
			compliance.LevelStandard,
		},
		{
			"basic when neither gate passes",
			[]compliance.Check{chk("mcp_nonce_handshake", false), chk("lifecycle_channel_opening", false)},
			compliance.LevelBasic,
		},
		{"basic on an empty set", nil, compliance.LevelBasic},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := deriveObservedLevel(tc.checks); got != tc.want {
				t.Errorf("deriveObservedLevel = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReconcileStatus(t *testing.T) {
	tests := []struct {
		declared, observed compliance.Level
		want               string
	}{
		{compliance.LevelBasic, compliance.LevelBasic, StatusMatch},
		{compliance.LevelFull, compliance.LevelFull, StatusMatch},
		{compliance.LevelBasic, compliance.LevelFull, StatusUnderdeclared},
		{compliance.LevelStandard, compliance.LevelFull, StatusUnderdeclared},
		{compliance.LevelFull, compliance.LevelStandard, StatusUnderperforms},
		{compliance.LevelStandard, compliance.LevelBasic, StatusUnderperforms},
	}
	for _, tc := range tests {
		if got := reconcileStatus(tc.declared, tc.observed); got != tc.want {
			t.Errorf("reconcileStatus(%s,%s) = %s, want %s", tc.declared, tc.observed, got, tc.want)
		}
	}
}

func TestMissingCapabilities(t *testing.T) {
	// Declared full, observed standard (lifecycle failed): the full-level
	// failures are the missing capabilities; standard-level passes are not.
	checks := []compliance.Check{
		chk("mcp_nonce_handshake", true),
		chk("platform_mcp_tool_invocation", true),
		chk("lifecycle_channel_opening", false),
		chk("checkpoint_quiesce_resume", false),
		chk("interrupt_acknowledgement", true),
	}
	got := missingCapabilities(checks, compliance.LevelStandard, compliance.LevelFull)
	want := map[string]bool{"lifecycle_channel_opening": true, "checkpoint_quiesce_resume": true}
	if len(got) != len(want) {
		t.Fatalf("missing = %v, want the two failing full-level checks", got)
	}
	for _, g := range got {
		if !want[g] {
			t.Errorf("unexpected missing capability %q", g)
		}
	}
}

func TestFailuresAtOrBelow(t *testing.T) {
	checks := []compliance.Check{
		chk("heartbeat_emits_ack", false),       // basic, failing
		chk("mcp_nonce_handshake", false),       // standard, failing
		chk("lifecycle_channel_opening", false), // full, failing
	}
	// At declared=basic, only the basic failure counts.
	if got := failuresAtOrBelow(checks, compliance.LevelBasic); len(got) != 1 || got[0] != "heartbeat_emits_ack" {
		t.Errorf("failuresAtOrBelow(basic) = %v, want [heartbeat_emits_ack]", got)
	}
	// At declared=standard, basic + standard failures count.
	if got := failuresAtOrBelow(checks, compliance.LevelStandard); len(got) != 2 {
		t.Errorf("failuresAtOrBelow(standard) = %v, want 2 entries", got)
	}
}

// buildProbeStub compiles a stub lenny-compliance harness that emits a
// canned full-battery report. The pass map names which checks pass; any
// check absent from the map is reported as failing. The stub ignores its
// --binary argument so no real adapter is needed.
func buildProbeStub(t *testing.T, pass map[string]bool) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go toolchain not on PATH: %v", err)
	}
	// Build a JSON report literal at test-author time.
	checksJSON, total, passed, failed := "", 0, 0, 0
	for name := range checkLevel {
		ok := pass[name]
		total++
		if ok {
			passed++
		} else {
			failed++
		}
		if checksJSON != "" {
			checksJSON += ","
		}
		checksJSON += `{"name":"` + name + `","spec":"15.4.6","pass":` + boolLit(ok) + `}`
	}
	report := `{"harness":"stub","binary":"x","level":"full","checks":[` + checksJSON +
		`],"summary":{"total":` + itoa(total) + `,"passed":` + itoa(passed) + `,"failed":` + itoa(failed) + `}}`

	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	body := "package main\nimport (\"fmt\";\"os\")\nfunc main(){fmt.Print(`" + report + "`); if " +
		itoa(failed) + " > 0 { os.Exit(1) } }\n"
	if err := os.WriteFile(src, []byte(body), 0o600); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	bin := filepath.Join(dir, "lenny-compliance-stub")
	if out, err := exec.Command("go", "build", "-o", bin, src).CombinedOutput(); err != nil {
		t.Fatalf("build stub: %v\n%s", err, out)
	}
	return bin
}

func boolLit(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// TestProbeObservedLevelMatch drives the probe end-to-end through the
// stub harness: a full-passing runtime declaring full reconciles to a
// match.
func TestProbeObservedLevelMatch(t *testing.T) {
	all := map[string]bool{}
	for name := range checkLevel {
		all[name] = true
	}
	stub := buildProbeStub(t, all)
	res, err := probeObservedLevel(context.Background(), "/tmp/fake-adapter", "full", stub)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if res.Observed != "full" || res.Status != StatusMatch {
		t.Errorf("observed=%s status=%s, want full/match", res.Observed, res.Status)
	}
}

// TestProbeObservedLevelUnderperforms drives a runtime that declares full
// but only clears the standard gate.
func TestProbeObservedLevelUnderperforms(t *testing.T) {
	pass := map[string]bool{
		"binary_exists_and_executes": true, "empty_stdin_exits_cleanly": true,
		"message_emits_response": true, "heartbeat_emits_ack": true,
		"unknown_type_ignored": true, "shutdown_exits_within_deadline": true,
		"sequential_messages_handled": true,
		"mcp_nonce_handshake":         true, "platform_mcp_tool_invocation": true,
		"connector_mcp_server_reachability": true, "tool_call_tool_result_correlation": true,
		// full-level checks all fail.
	}
	stub := buildProbeStub(t, pass)
	res, err := probeObservedLevel(context.Background(), "/tmp/fake-adapter", "full", stub)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if res.Observed != "standard" || res.Status != StatusUnderperforms {
		t.Fatalf("observed=%s status=%s, want standard/underperforms", res.Observed, res.Status)
	}
	if len(res.Missing) == 0 {
		t.Error("underperforms should list missing full-level capabilities")
	}
}
