// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/cmd/lenny-test/verdictstatus"
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
		"UNVERIFIED":   3,
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

// TestRecordTierUnverifiedRaisesVerdict pins the aggregation of a tier
// that could not reach a conclusion. The overall verdict starts at
// PASS, so a status that leaves it untouched reports a green run for
// work that was never verified.
func TestRecordTierUnverifiedRaisesVerdict(t *testing.T) {
	v := newVerdict(selector{tier: "static"})
	v.recordTier("static", verdictstatus.Unverified, 5*time.Millisecond, "protoc not on PATH")
	if v.Verdict != verdictstatus.VerdictUnverified {
		t.Fatalf("unverified tier left the verdict at %q; want %q", v.Verdict, verdictstatus.VerdictUnverified)
	}
	if got := exitCodeFor(v.Verdict); got == 0 {
		t.Fatalf("unverified run exited 0")
	}
	if stat := v.Tiers["static"]; stat.Reason != "protoc not on PATH" {
		t.Fatalf("unverified detail not carried to Reason: %q", stat.Reason)
	}
}

// TestRecordTierUnverifiedPrecedence pins the precedence of UNVERIFIED
// against FAIL, INCONCLUSIVE, and PASS in both record orders. The
// overall verdict must not depend on which tier ran first. INCONCLUSIVE
// outranks UNVERIFIED so an infrastructure-class failure still reports
// the verdict whose exit code drives the documented retry path
// (TESTING.md §21.3), instead of being masked by an unrelated check
// that could not reach a conclusion.
func TestRecordTierUnverifiedPrecedence(t *testing.T) {
	cases := []struct {
		name     string
		statuses []string
		want     string
	}{
		{"fail then unverified", []string{verdictstatus.Fail, verdictstatus.Unverified}, verdictstatus.VerdictFail},
		{"unverified then fail", []string{verdictstatus.Unverified, verdictstatus.Fail}, verdictstatus.VerdictFail},
		{"pass then unverified", []string{verdictstatus.Pass, verdictstatus.Unverified}, verdictstatus.VerdictUnverified},
		{"unverified then pass", []string{verdictstatus.Unverified, verdictstatus.Pass}, verdictstatus.VerdictUnverified},
		{"inconclusive then unverified", []string{verdictstatus.Inconclusive, verdictstatus.Unverified}, verdictstatus.VerdictInconclusive},
		{"unverified then inconclusive", []string{verdictstatus.Unverified, verdictstatus.Inconclusive}, verdictstatus.VerdictInconclusive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := newVerdict(selector{maxTier: "unit"})
			for i, status := range tc.statuses {
				v.recordTier(fmt.Sprintf("tier-%d", i), status, time.Millisecond, "")
			}
			if v.Verdict != tc.want {
				t.Fatalf("recording %v gave verdict %q; want %q", tc.statuses, v.Verdict, tc.want)
			}
		})
	}
}

// TestUnverifiedTierKeepsInfraFailureRetryable pins the exit code a run
// reports when an infrastructure-class failure and an unverified check
// land in the same run. The harness retries an infrastructure failure
// with fresh infrastructure on the INCONCLUSIVE exit code (TESTING.md
// §21.3), and a degraded environment where part of the toolchain is
// absent is also where infrastructure fails to start, so the two
// statuses coincide. The run must still exit 2 in either record order.
func TestUnverifiedTierKeepsInfraFailureRetryable(t *testing.T) {
	infraFailure := func(v *verdict) {
		v.recordTier("integration", verdictstatus.Fail, time.Millisecond, "docker daemon not running")
	}
	unverified := func(v *verdict) {
		v.recordTier("static", verdictstatus.Unverified, time.Millisecond, "protoc not on PATH")
	}
	orders := [][2]func(v *verdict){
		{infraFailure, unverified},
		{unverified, infraFailure},
	}
	for _, order := range orders {
		v := newVerdict(selector{maxTier: "integration"})
		order[0](v)
		order[1](v)
		if v.Verdict != verdictstatus.VerdictInconclusive {
			t.Fatalf("verdict %q; want %q so the infrastructure failure stays retryable", v.Verdict, verdictstatus.VerdictInconclusive)
		}
		if got := exitCodeFor(v.Verdict); got != 2 {
			t.Fatalf("exit code %d; want 2 so CI takes the infrastructure retry path", got)
		}
		if stat := v.Tiers["integration"]; stat.Status != verdictstatus.Inconclusive {
			t.Fatalf("infra-class failure recorded as %q; want %q", stat.Status, verdictstatus.Inconclusive)
		}
	}
}

// TestRecordTierUnknownStatusDoesNotKeepPass pins the fail-closed
// branch. A status no branch of the switch recognizes established
// nothing about the tier, and leaving the run at PASS with exit code 0
// would report the unrecognized outcome as success.
// The recorded tier status is normalized to "unverified" so the
// document stays inside the tier-status enum every consumer switches
// on, and the value that was passed in survives in the reason.
func TestRecordTierUnknownStatusDoesNotKeepPass(t *testing.T) {
	v := newVerdict(selector{tier: "static"})
	v.recordTier("static", "banana", time.Millisecond, "protoc not on PATH")
	if v.Verdict == verdictstatus.VerdictPass {
		t.Fatalf("unrecognized tier status left the verdict at PASS")
	}
	if got := exitCodeFor(v.Verdict); got == 0 {
		t.Fatalf("verdict %q after an unrecognized tier status exits 0", v.Verdict)
	}
	stat := v.Tiers["static"]
	if stat.Status != verdictstatus.Unverified {
		t.Fatalf("recorded tier status %q; want it normalized to %q", stat.Status, verdictstatus.Unverified)
	}
	if !strings.Contains(stat.Reason, `"banana"`) {
		t.Fatalf("recorded reason %q does not name the unrecognized status", stat.Reason)
	}
	if !strings.Contains(stat.Reason, "protoc not on PATH") {
		t.Fatalf("recorded reason %q dropped the detail passed in", stat.Reason)
	}
}

// TestRecordedTierStatusStaysInTheEnum holds every status recordTier
// writes to the declared tier-status set. A status outside it reaches
// no branch of the emitters, which switch on that set.
func TestRecordedTierStatusStaysInTheEnum(t *testing.T) {
	declared := map[string]bool{}
	for _, s := range verdictstatus.TierStatuses() {
		declared[s] = true
	}
	inputs := append(verdictstatus.TierStatuses(), "banana", "")
	for _, in := range inputs {
		v := newVerdict(selector{tier: "static"})
		v.recordTier("static", in, time.Millisecond, "")
		if got := v.Tiers["static"].Status; !declared[got] {
			t.Errorf("recordTier(%q) recorded status %q, which is outside the declared tier-status set", in, got)
		}
	}
}

// staticCheckStub builds a table entry whose run returns the given
// output and error and whose classifier reads the shared unverified
// marker, matching the tier-0 Go-test entry.
func staticCheckStub(name, out string, err error) staticCheck {
	return staticCheck{
		name:     name,
		run:      func() (string, error) { return out, err },
		classify: classifyUnverified,
	}
}

// TestComposeStaticChecksUnverifiedMarkerRaisesTierStatus pins the
// producer side of the unverified tier status. A tier-0 check that ran,
// exited zero, and wrote the marker reported that it proved nothing;
// collapsing it to pass is the fail-open outcome the status exists to
// prevent.
func TestComposeStaticChecksUnverifiedMarkerRaisesTierStatus(t *testing.T) {
	out := "ok  \tgithub.com/lennylabs/lenny/tests/tier0_static\t0.01s\n    " +
		verdictstatus.UnverifiedMarker + " protoc not on PATH\n"
	status, msg := composeStaticChecks([]staticCheck{
		staticCheckStub("go test ./tests/tier0_static/...", out, nil),
	})
	if status != verdictstatus.Unverified {
		t.Fatalf("check reporting no conclusion yielded status %q; want %q", status, verdictstatus.Unverified)
	}
	if !strings.Contains(msg, "protoc not on PATH") {
		t.Fatalf("tier message dropped the reason: %q", msg)
	}
}

// TestComposeStaticChecksFailOutranksUnverified holds a real failure
// above a check that reached no conclusion, in either order, so the new
// status cannot mask a failing tier.
func TestComposeStaticChecksFailOutranksUnverified(t *testing.T) {
	unverified := staticCheckStub("tier0 go test", verdictstatus.UnverifiedMarker+" generator absent", nil)
	failing := staticCheckStub("go vet ./...", "vet: bad code", fmt.Errorf("exit status 1"))
	cases := []struct {
		name   string
		checks []staticCheck
	}{
		{"unverified first", []staticCheck{unverified, failing}},
		{"failing first", []staticCheck{failing, unverified}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, msg := composeStaticChecks(tc.checks)
			if status != verdictstatus.Fail {
				t.Fatalf("got status %q; want %q", status, verdictstatus.Fail)
			}
			if !strings.Contains(msg, "go vet ./... failed") {
				t.Fatalf("failure message lost the failing check: %q", msg)
			}
		})
	}
}

// TestComposeStaticChecksSilentChecksPass keeps the pre-existing
// outcome for a table whose checks report nothing: the tier passes and
// carries no message.
func TestComposeStaticChecksSilentChecksPass(t *testing.T) {
	status, msg := composeStaticChecks([]staticCheck{
		staticCheckStub("tier0 go test", "ok  \tgithub.com/lennylabs/lenny/tests/tier0_static\t0.01s\n", nil),
		{name: "no classifier", run: func() (string, error) {
			return verdictstatus.UnverifiedMarker + " ignored without a classifier", nil
		}},
	})
	if status != verdictstatus.Pass {
		t.Fatalf("silent checks yielded status %q; want %q", status, verdictstatus.Pass)
	}
	if msg != "" {
		t.Fatalf("passing tier carried a message: %q", msg)
	}
}

// TestComposeStaticChecksPropagatesEveryClassifierStatus holds the
// composer to the per-check status contract: whatever tier status a
// classifier returns is the status the tier reports, rather than only
// unverified surviving and every other value collapsing to pass.
func TestComposeStaticChecksPropagatesEveryClassifierStatus(t *testing.T) {
	cases := []struct {
		classified string
		want       string
	}{
		{verdictstatus.Pass, verdictstatus.Pass},
		{verdictstatus.Unverified, verdictstatus.Unverified},
		{verdictstatus.Inconclusive, verdictstatus.Inconclusive},
		{verdictstatus.Fail, verdictstatus.Fail},
	}
	for _, tc := range cases {
		t.Run(tc.classified, func(t *testing.T) {
			status, msg := composeStaticChecks([]staticCheck{{
				name: "classified check",
				run:  func() (string, error) { return "", nil },
				classify: func(string) (string, string) {
					return tc.classified, "docker socket absent"
				},
			}})
			if status != tc.want {
				t.Fatalf("classifier returning %q yielded tier status %q; want %q", tc.classified, status, tc.want)
			}
			if tc.want == verdictstatus.Pass {
				return
			}
			if !strings.Contains(msg, "classified check") || !strings.Contains(msg, "docker socket absent") {
				t.Fatalf("tier message dropped the check or its detail: %q", msg)
			}
		})
	}
}

// TestComposeStaticChecksUnrecognizedClassifierStatusFailsClosed pins
// the fail-closed path for a classifier that returns a value outside
// the tier-status set. The composer starts at pass, so treating an
// unrecognized value as a pass would report a green tier that nothing
// established.
func TestComposeStaticChecksUnrecognizedClassifierStatusFailsClosed(t *testing.T) {
	for _, bogus := range []string{"banana", ""} {
		t.Run(fmt.Sprintf("status=%q", bogus), func(t *testing.T) {
			status, msg := composeStaticChecks([]staticCheck{{
				name:     "classified check",
				run:      func() (string, error) { return "", nil },
				classify: func(string) (string, string) { return bogus, "" },
			}})
			if status != verdictstatus.Unverified {
				t.Fatalf("classifier returning %q yielded tier status %q; want %q", bogus, status, verdictstatus.Unverified)
			}
			if !strings.Contains(msg, fmt.Sprintf("unrecognized tier status %q", bogus)) {
				t.Fatalf("tier message dropped the unrecognized value: %q", msg)
			}
		})
	}
}

// TestComposeStaticChecksStrongestClassifierStatusWins keeps the
// composer's ordering aligned with the verdict's: a failing classifier
// outranks an inconclusive one, which outranks a check that reached no
// conclusion, whatever order the table runs them in.
func TestComposeStaticChecksStrongestClassifierStatusWins(t *testing.T) {
	check := func(name, status string) staticCheck {
		return staticCheck{
			name:     name,
			run:      func() (string, error) { return "", nil },
			classify: func(string) (string, string) { return status, "" },
		}
	}
	unverified := check("tier0 go test", verdictstatus.Unverified)
	inconclusive := check("compose bring-up", verdictstatus.Inconclusive)
	failing := check("schema check", verdictstatus.Fail)
	cases := []struct {
		name   string
		checks []staticCheck
		want   string
	}{
		{"fail after unverified", []staticCheck{unverified, failing}, verdictstatus.Fail},
		{"fail before unverified", []staticCheck{failing, unverified}, verdictstatus.Fail},
		{"inconclusive after unverified", []staticCheck{unverified, inconclusive}, verdictstatus.Inconclusive},
		{"inconclusive before unverified", []staticCheck{inconclusive, unverified}, verdictstatus.Inconclusive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, _ := composeStaticChecks(tc.checks)
			if status != tc.want {
				t.Fatalf("got tier status %q; want %q", status, tc.want)
			}
		})
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
		{"tests/tier7a_load_local/x_test.go", "load_local"},
		{"tests/tier7b_load_kind/x_test.go", "load_kind"},
		{"tests/tier8_chaos/x_test.go", "chaos"},
		{"tests/tier9_security/x_test.go", "security"},
		{"tests/tier10_conformance/x_test.go", "conformance"},
		{"tests/tier11_docs/x_test.go", "docs"},
		{"tests/tier12_load_cloud/x_test.go", "load_cloud"},
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
		{"load_local", "load_local"},
		{"load_kind", "load_kind"},
		{"chaos", "chaos"},
		{"security", "security"},
		{"conformance", "conformance"},
		{"docs", ""},
		{"load_cloud", "load_cloud"},
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
