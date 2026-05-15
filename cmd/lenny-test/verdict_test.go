// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestTriggerMode(t *testing.T) {
	cases := []struct {
		name string
		s    selector
		want string
	}{
		{"group", selector{group: "phase-0-gate"}, "group:phase-0-gate"},
		{"tier", selector{tier: "static"}, "tier:static"},
		{"max_tier", selector{maxTier: "integration"}, "max_tier:integration"},
		{"changed", selector{changed: true}, "changed"},
		{"specs", selector{specs: []string{"§4.6"}}, "spec"},
		{"pkgs", selector{pkgs: []string{"./pkg/audit/..."}}, "pkg"},
		{"empty", selector{}, "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := triggerMode(tc.s)
			if got != tc.want {
				t.Fatalf("triggerMode(%+v) = %q; want %q", tc.s, got, tc.want)
			}
		})
	}
}

func TestTriggerModePrecedence(t *testing.T) {
	// Group beats every other field when multiple are set; this is
	// what the harness relies on when a developer passes both --group
	// and --tier.
	s := selector{
		group:   "phase-0-gate",
		tier:    "static",
		changed: true,
		specs:   []string{"§4.6"},
		pkgs:    []string{"./pkg/audit/..."},
	}
	if got := triggerMode(s); got != "group:phase-0-gate" {
		t.Fatalf("triggerMode precedence: got %q; want group:phase-0-gate", got)
	}
}

func TestReasonFromStatus(t *testing.T) {
	cases := []struct {
		status, detail, want string
	}{
		{"skipped", "no tests", "no tests"},
		{"skipped", "", "skipped"},
		{"pass", "", ""},
		{"fail", "anything", ""},
	}
	for _, tc := range cases {
		got := reasonFromStatus(tc.status, tc.detail)
		if got != tc.want {
			t.Fatalf("reasonFromStatus(%q,%q)=%q; want %q", tc.status, tc.detail, got, tc.want)
		}
	}
}

func TestSetToSortedSlice(t *testing.T) {
	in := map[string]bool{"c": true, "a": true, "b": true}
	got := setToSortedSlice(in)
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("setToSortedSlice = %v; want %v", got, want)
	}
	if got := setToSortedSlice(map[string]bool{}); len(got) != 0 {
		t.Fatalf("setToSortedSlice(empty) = %v; want []", got)
	}
}

func TestRecordTierMarksFailVerdict(t *testing.T) {
	v := newVerdict(selector{tier: "static"})
	v.recordTier("static", "pass", 100*time.Millisecond, "")
	if v.Verdict != "PASS" {
		t.Fatalf("after a single pass, verdict = %q; want PASS", v.Verdict)
	}
	v.recordTier("unit", "fail", 200*time.Millisecond, "vet errors")
	if v.Verdict != "FAIL" {
		t.Fatalf("after a fail recording, verdict = %q; want FAIL", v.Verdict)
	}
	// Subsequent passes must not flip the verdict back.
	v.recordTier("component", "pass", 50*time.Millisecond, "")
	if v.Verdict != "FAIL" {
		t.Fatalf("a later pass cleared the FAIL verdict: %q", v.Verdict)
	}
}

func TestClassifyInfraFailure(t *testing.T) {
	infra := []string{
		"compose up: services did not reach healthy state",
		"Docker daemon is not running",
		"kind cluster bring-up failed",
		"could not reach the apiserver after 60s",
		"image pull backoff for postgres:16-alpine",
		"connection refused dialing 127.0.0.1:5432",
	}
	for _, d := range infra {
		if !classifyInfraFailure(d) {
			t.Errorf("classifyInfraFailure(%q) = false; want true", d)
		}
	}
	tests := []string{
		"assertion failed: want 5, got 4",
		"TestFoo: panic at line 42",
		"compile error: undeclared identifier",
		"",
	}
	for _, d := range tests {
		if classifyInfraFailure(d) {
			t.Errorf("classifyInfraFailure(%q) = true; want false", d)
		}
	}
}

func TestRecordTierReclassifiesInfraFailure(t *testing.T) {
	v := newVerdict(selector{tier: "integration"})
	v.recordTier("integration", "fail", 10*time.Millisecond, "compose up: services did not reach healthy state")
	if got := v.Tiers["integration"].Status; got != "inconclusive" {
		t.Fatalf("infra failure should reclassify to inconclusive; got %q", got)
	}
	if v.Verdict != "INCONCLUSIVE" {
		t.Fatalf("overall verdict should be INCONCLUSIVE; got %q", v.Verdict)
	}
}

func TestRecordTierFailOutranksInconclusive(t *testing.T) {
	v := newVerdict(selector{tier: "static"})
	v.recordTier("static", "fail", 5*time.Millisecond, "go vet found three issues")
	v.recordTier("integration", "fail", 5*time.Millisecond, "compose up failed: docker daemon down")
	if v.Verdict != "FAIL" {
		t.Fatalf("FAIL must outrank INCONCLUSIVE; got %q", v.Verdict)
	}
}

func TestExitCodeFor(t *testing.T) {
	cases := map[string]int{
		"PASS":         0,
		"FAIL":         1,
		"INCONCLUSIVE": 2,
		"":             1,
		"weird":        1,
	}
	for verdict, want := range cases {
		if got := exitCodeFor(verdict); got != want {
			t.Errorf("exitCodeFor(%q) = %d; want %d", verdict, got, want)
		}
	}
}

func TestRecordTierSkippedKeepsPass(t *testing.T) {
	v := newVerdict(selector{tier: "static"})
	v.recordTier("integration", "skipped", 0, "no docker on PATH")
	if v.Verdict != "PASS" {
		t.Fatalf("skipped flipped the verdict: %q", v.Verdict)
	}
	stat := v.Tiers["integration"]
	if stat.Reason != "no docker on PATH" {
		t.Fatalf("skipped detail not carried to Reason: %q", stat.Reason)
	}
}

func TestSynthesizeNextActionFallback(t *testing.T) {
	v := newVerdict(selector{tier: "static"})
	v.recordTier("static", "fail", 10*time.Millisecond, "go vet failed")
	got := v.synthesizeNextAction("static", "FALLBACK")
	if got != "FALLBACK" {
		t.Fatalf("no failures → expected fallback; got %q", got)
	}
	got = v.synthesizeNextAction("nonexistent", "OTHER_FALLBACK")
	if got != "OTHER_FALLBACK" {
		t.Fatalf("unknown tier → expected fallback; got %q", got)
	}
}

func TestSynthesizeNextActionWithFailures(t *testing.T) {
	v := newVerdict(selector{tier: "unit"})
	v.Tiers["unit"] = tierStat{
		Status: "fail",
		Failures: []failureEntry{
			{Test: "TestA", Package: "pkg/audit", SpecSections: []string{"§8.2", "§13.5"}},
			{Test: "TestB", Package: "pkg/audit", SpecSections: []string{"§8.2"}},
			{Test: "TestC", Package: "pkg/gateway", SpecSections: []string{"§4.6"}},
		},
		Failed: 3,
	}
	got := v.synthesizeNextAction("unit", "ignored")
	// Both packages and specs come through sort.Strings — that is
	// a lexicographic sort, so §13.5 < §4.6 < §8.2 here.
	want := "Fix 3 unit-tier failure(s) in pkg/audit, pkg/gateway. See spec sections §13.5, §4.6, §8.2."
	if got != want {
		t.Fatalf("synthesizeNextAction = %q; want %q", got, want)
	}
}

func TestInferTierFromPath(t *testing.T) {
	cases := []struct {
		path, want string
	}{
		{"tests/tier0_static/foo_test.go", "static"},
		{"tests/tier2_component/x_test.go", "component"},
		{"tests/tier3_contract/x_test.go", "contract"},
		{"tests/tier4_integration/x_test.go", "integration"},
		{"tests/tier5_e2e_kind/x_test.go", "e2e_kind"},
		{"tests/tier6_e2e_cloud/x_test.go", "e2e_cloud"},
		{"tests/tier7_load/x_test.go", "load"},
		{"tests/tier8_chaos/x_test.go", "chaos"},
		{"tests/tier9_security/x_test.go", "security"},
		{"tests/tier10_conformance/x_test.go", "conformance"},
		{"tests/tier11_docs/x_test.go", "docs"},
		{"pkg/audit/audit_test.go", "unit"},
		{"foo/bar_test.go", "unit"},
		{"docs/index.md", ""},
		{"", ""},
	}
	for _, tc := range cases {
		got := inferTierFromPath(tc.path)
		if got != tc.want {
			t.Errorf("inferTierFromPath(%q)=%q; want %q", tc.path, got, tc.want)
		}
	}
}

func TestTierBuildTag(t *testing.T) {
	cases := []struct {
		tier, want string
	}{
		{"static", ""},
		{"unit", ""},
		{"component", "component"},
		{"contract", "contract"},
		{"integration", "integration"},
		{"e2e_kind", "e2e_kind"},
		{"e2e_cloud", "e2e_cloud"},
		{"load", "load"},
		{"chaos", "chaos"},
		{"security", "security"},
		{"conformance", "conformance"},
		{"docs", ""},
		{"made-up", ""},
	}
	for _, tc := range cases {
		if got := tierBuildTag(tc.tier); got != tc.want {
			t.Errorf("tierBuildTag(%q)=%q; want %q", tc.tier, got, tc.want)
		}
	}
}

func TestIsPhaseGate(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"phase-0-gate", true},
		{"phase-14-gate", true},
		{"phase-launch-gate", true},
		{"phase--gate", true}, // empty middle still matches prefix+suffix
		{"phase-0-pre", false},
		{"daily-gate", false},
		{"phase-gate", false}, // too short
		{"", false},
	}
	for _, tc := range cases {
		if got := isPhaseGate(tc.name); got != tc.want {
			t.Errorf("isPhaseGate(%q)=%v; want %v", tc.name, got, tc.want)
		}
	}
}

func TestNewVerdictContainerCache(t *testing.T) {
	v := newVerdict(selector{tier: "integration", cached: false})
	if v.Infra.ContainerCache != "cold" {
		t.Fatalf("cold path: ContainerCache=%q; want cold", v.Infra.ContainerCache)
	}
	v = newVerdict(selector{tier: "integration", cached: true})
	if v.Infra.ContainerCache != "warm" {
		t.Fatalf("warm path: ContainerCache=%q; want warm", v.Infra.ContainerCache)
	}
}

func TestNewVerdictRunIDUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		v := newVerdict(selector{tier: "static"})
		if seen[v.RunID] {
			t.Fatalf("duplicate run_id: %q", v.RunID)
		}
		seen[v.RunID] = true
		if len(v.RunID) < 8 {
			t.Fatalf("run_id too short: %q", v.RunID)
		}
	}
}

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out.json")
	payload := []byte(`{"hello":"world"}` + "\n")
	if err := writeFileAtomic(target, payload, 0o644); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("payload mismatch: got %q; want %q", got, payload)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Fatalf("perm=%v; want 0o644", perm)
	}
	// No leftover temp file from the rename.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("temp file remained after rename: %s", e.Name())
		}
	}
}

func TestWriteFileAtomicOverwrites(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out.json")
	if err := os.WriteFile(target, []byte("OLD"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := writeFileAtomic(target, []byte("NEW"), 0o644); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "NEW" {
		t.Fatalf("overwrite failed: %q", got)
	}
}

func TestWriteFileAtomicCleansTempOnRenameFailure(t *testing.T) {
	// Renaming to a path whose parent doesn't exist fails; the temp
	// file must still be cleaned up.
	dir := t.TempDir()
	bad := filepath.Join(dir, "nope", "out.json")
	err := writeFileAtomic(bad, []byte("x"), 0o644)
	if err == nil {
		t.Fatal("expected error when parent directory is missing")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("temp file remained after failed rename: %s", e.Name())
		}
	}
}

func TestVerdictWriteRoundtrip(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "latest.json")
	v := newVerdict(selector{tier: "static"})
	v.recordTier("static", "pass", 5*time.Millisecond, "")
	v.SpecStatus["§13.5"] = "pass"
	if err := v.write(target); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Both latest.json and verdict-<id>.json must exist.
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read latest: %v", err)
	}
	var back verdict
	if err := json.Unmarshal(body, &back); err != nil {
		t.Fatalf("decode latest: %v", err)
	}
	if back.RunID != v.RunID || back.Verdict != "PASS" {
		t.Fatalf("roundtrip mismatch: got %+v", back)
	}
	rotated := filepath.Join(dir, "verdict-"+v.RunID+".json")
	if _, err := os.Stat(rotated); err != nil {
		t.Fatalf("rotated verdict missing: %v", err)
	}
}

func TestVerdictWriteEmptyPathNoop(t *testing.T) {
	v := newVerdict(selector{tier: "static"})
	v.recordTier("static", "pass", 1*time.Millisecond, "")
	if err := v.write(""); err != nil {
		t.Fatalf("write empty path should be a no-op: %v", err)
	}
}

func TestPruneOldVerdicts(t *testing.T) {
	dir := t.TempDir()
	// Seed 25 verdict files with monotonically increasing mtimes.
	base := time.Now().Add(-25 * time.Minute)
	for i := 0; i < 25; i++ {
		name := filepath.Join(dir, "verdict-"+padded(i)+".json")
		if err := os.WriteFile(name, []byte("{}"), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
		ts := base.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(name, ts, ts); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}
	// Drop a latest.json — pruneOldVerdicts must skip it.
	latest := filepath.Join(dir, "latest.json")
	if err := os.WriteFile(latest, []byte("{}"), 0o644); err != nil {
		t.Fatalf("seed latest: %v", err)
	}
	pruneOldVerdicts(dir, 10)
	if _, err := os.Stat(latest); err != nil {
		t.Fatalf("latest.json was pruned: %v", err)
	}
	remaining := []string{}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "verdict-") && strings.HasSuffix(e.Name(), ".json") {
			remaining = append(remaining, e.Name())
		}
	}
	if len(remaining) != 10 {
		t.Fatalf("expected 10 verdict files after prune; got %d (%v)", len(remaining), remaining)
	}
	// The newest 10 (indices 15..24) survive.
	sort.Strings(remaining)
	for _, name := range remaining {
		idx := strings.TrimSuffix(strings.TrimPrefix(name, "verdict-"), ".json")
		if idx < "15" {
			t.Fatalf("kept too-old verdict %s", name)
		}
	}
}

func padded(i int) string {
	if i < 10 {
		return "0" + itoa(i)
	}
	return itoa(i)
}

func itoa(i int) string {
	const digits = "0123456789"
	if i == 0 {
		return "0"
	}
	out := []byte{}
	for i > 0 {
		out = append([]byte{digits[i%10]}, out...)
		i /= 10
	}
	return string(out)
}
