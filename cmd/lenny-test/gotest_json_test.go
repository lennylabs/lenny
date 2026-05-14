// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"testing"
)

func TestParseGoTestJSONHappyPath(t *testing.T) {
	stream := strings.Join([]string{
		`{"Action":"run","Package":"pkg/a","Test":"TestPass"}`,
		`{"Action":"output","Package":"pkg/a","Test":"TestPass","Output":"--- PASS: TestPass\n"}`,
		`{"Action":"pass","Package":"pkg/a","Test":"TestPass","Elapsed":0.10}`,
		`{"Action":"run","Package":"pkg/a","Test":"TestSkip"}`,
		`{"Action":"output","Package":"pkg/a","Test":"TestSkip","Output":"--- SKIP: TestSkip\n"}`,
		`{"Action":"skip","Package":"pkg/a","Test":"TestSkip","Elapsed":0.0}`,
		`{"Action":"run","Package":"pkg/b","Test":"TestFail"}`,
		`{"Action":"output","Package":"pkg/b","Test":"TestFail","Output":"    foo_test.go:42: expected 5 got 4\n"}`,
		`{"Action":"output","Package":"pkg/b","Test":"TestFail","Output":"--- FAIL: TestFail\n"}`,
		`{"Action":"fail","Package":"pkg/b","Test":"TestFail","Elapsed":0.05}`,
	}, "\n")

	r := parseGoTestJSON([]byte(stream), nil)
	if r.Total != 3 || r.Passed != 1 || r.Failed != 1 || r.Skipped != 1 {
		t.Fatalf("counts: total=%d passed=%d failed=%d skipped=%d (want 3/1/1/1)",
			r.Total, r.Passed, r.Failed, r.Skipped)
	}
	if len(r.Failures) != 1 {
		t.Fatalf("want 1 failure entry; got %d", len(r.Failures))
	}
	f := r.Failures[0]
	if f.Package != "pkg/b" || f.Test != "TestFail" {
		t.Fatalf("failure identification: pkg=%q test=%q", f.Package, f.Test)
	}
	if f.File != "foo_test.go" || f.Line != 42 {
		t.Fatalf("file/line extraction: %q:%d", f.File, f.Line)
	}
	if f.DurationMS != 50 {
		t.Fatalf("duration: got %d ms; want 50", f.DurationMS)
	}
	if !strings.Contains(f.RerunCommand, "TestFail") || !strings.Contains(f.RerunCommand, "pkg/b") {
		t.Fatalf("rerun command lacks name or pkg: %q", f.RerunCommand)
	}
}

func TestParseGoTestJSONStableFailureOrder(t *testing.T) {
	stream := strings.Join([]string{
		`{"Action":"fail","Package":"pkg/b","Test":"TestB","Elapsed":0.01}`,
		`{"Action":"fail","Package":"pkg/a","Test":"TestA","Elapsed":0.01}`,
		`{"Action":"fail","Package":"pkg/a","Test":"TestZ","Elapsed":0.01}`,
	}, "\n")
	r := parseGoTestJSON([]byte(stream), nil)
	want := []string{"pkg/a::TestA", "pkg/a::TestZ", "pkg/b::TestB"}
	for i, f := range r.Failures {
		got := f.Package + "::" + f.Test
		if got != want[i] {
			t.Errorf("failure[%d] = %q; want %q", i, got, want[i])
		}
	}
}

func TestParseGoTestJSONNonJSONStderr(t *testing.T) {
	stream := `{"Action":"pass","Package":"pkg/a","Test":"TestX","Elapsed":0.01}` + "\n"
	stderr := []byte("build failure: undefined symbol XYZ\n")
	r := parseGoTestJSON([]byte(stream), stderr)
	if r.Passed != 1 {
		t.Fatalf("passed count: got %d; want 1", r.Passed)
	}
	if !strings.Contains(r.RawOut, "build failure") {
		t.Fatalf("RawOut should preserve stderr; got %q", r.RawOut)
	}
}

func TestParseFirstFileLine(t *testing.T) {
	cases := []struct {
		in   string
		file string
		line int
	}{
		{"    foo_test.go:42: expected 5", "foo_test.go", 42},
		{"foo_test.go:7: msg", "foo_test.go", 7},
		{"no assertion here", "", 0},
		{"    just_text.go: no line number after", "", 0},
	}
	for _, c := range cases {
		f, l := parseFirstFileLine(c.in)
		if f != c.file || l != c.line {
			t.Errorf("parseFirstFileLine(%q) = (%q,%d); want (%q,%d)",
				c.in, f, l, c.file, c.line)
		}
	}
}

func TestSummarizeFailures(t *testing.T) {
	r := &tierResult{
		Failures: []failureEntry{
			{Package: "pkg/a", Test: "T1"},
			{Package: "pkg/a", Test: "T2"},
			{Package: "pkg/b", Test: "T3"},
			{Package: "pkg/c", Test: "T4"},
		},
	}
	got := summarizeFailures(r)
	if !strings.HasPrefix(got, "4 failure(s):") {
		t.Fatalf("summary should lead with count: %q", got)
	}
	if !strings.Contains(got, "+1 more") {
		t.Fatalf("summary should truncate beyond max=3: %q", got)
	}
	if summarizeFailures(nil) != "" || summarizeFailures(&tierResult{}) != "" {
		t.Fatalf("summary on nil/empty should be empty string")
	}
}

func TestScanFileForSpecSections(t *testing.T) {
	body := `package x

// Some unrelated comment.
// spec: 11.7 (audit retention × compliance pairing matrix)
// diagnosis: Documented compatible pair rejected.
func TestPairing(t *testing.T) {}

// spec: 8.2, 8.3 (delegation cycle)
func TestCycle(t *testing.T) {}

// no annotations here
func TestPlain(t *testing.T) {}
`
	cases := []struct {
		test string
		want []string
	}{
		{"TestPairing", []string{"11.7"}},
		{"TestCycle", []string{"8.2", "8.3"}},
		{"TestPlain", nil},
		{"TestMissing", nil},
	}
	for _, c := range cases {
		got := scanFileForSpecSections(body, c.test)
		if len(got) != len(c.want) {
			t.Errorf("%s: got %v; want %v", c.test, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s[%d]: got %q; want %q", c.test, i, got[i], c.want[i])
			}
		}
	}
}

func TestRecordTierWithResultPropagatesCounts(t *testing.T) {
	v := newVerdict(selector{tier: "unit"})
	r := &tierResult{
		Total: 47, Passed: 45, Failed: 2, Skipped: 0,
		Failures: []failureEntry{
			{Package: "pkg/x", Test: "TestA"},
			{Package: "pkg/y", Test: "TestB"},
		},
	}
	v.recordTierWithResult("unit", "fail", 100, "two failures", r)
	stat := v.Tiers["unit"]
	if stat.Total != 47 || stat.Passed != 45 || stat.Failed != 2 {
		t.Fatalf("counts not propagated: %+v", stat)
	}
	if len(stat.Failures) != 2 {
		t.Fatalf("failures not propagated: %d", len(stat.Failures))
	}
}
