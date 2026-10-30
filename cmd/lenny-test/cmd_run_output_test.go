// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/cmd/lenny-test/verdictstatus"
)

// The machine-readable emitters turn the verdict document into what a
// CI system gates on. A tier that reached no conclusion must not read
// as a passing testcase in any of them, because the process exit code
// for that same run is non-zero and the two would disagree.

// captureStdout runs fn with os.Stdout replaced by a pipe and returns
// everything fn printed. The emitters write through fmt.Printf, so the
// test reads them the way a CI consumer does.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("open pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	defer func() {
		os.Stdout = orig
		_ = r.Close()
	}()
	fn()
	if err := w.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}
	return <-done
}

// unverifiedVerdict builds a finalized verdict whose static tier could
// not reach a conclusion and whose unit tier passed.
func unverifiedVerdict(t *testing.T) *verdict {
	t.Helper()
	v := newVerdict(selector{maxTier: "unit"})
	v.recordTier(tierStatic, verdictstatus.Unverified, 5*time.Millisecond, "protoc not on PATH")
	v.recordTier(tierUnit, verdictstatus.Pass, 5*time.Millisecond, "")
	v.finalize()
	if v.Verdict != verdictstatus.VerdictUnverified {
		t.Fatalf("fixture verdict is %q; want %q", v.Verdict, verdictstatus.VerdictUnverified)
	}
	return v
}

// TestPrintJUnitReportsUnverifiedTierAsNonPassing pins the JUnit
// emitter. A <testcase> with no child element is a pass to every JUnit
// consumer, so the unverified tier needs an <error> child and a count
// in the suite header.
func TestPrintJUnitReportsUnverifiedTierAsNonPassing(t *testing.T) {
	v := unverifiedVerdict(t)
	out := captureStdout(t, func() { printJUnit(v) })

	if !strings.Contains(out, `errors="1"`) {
		t.Errorf("suite header does not count the unverified tier:\n%s", out)
	}
	block, ok := junitTestcase(out, tierStatic)
	if !ok {
		t.Fatalf("no testcase for the %s tier:\n%s", tierStatic, out)
	}
	if !strings.Contains(block, "<error") {
		t.Errorf("the %s testcase carries no <error> child, so it reads as passing:\n%s", tierStatic, block)
	}
	if !strings.Contains(block, "unverified") {
		t.Errorf("the %s testcase does not name the unverified state:\n%s", tierStatic, block)
	}
	passing, ok := junitTestcase(out, tierUnit)
	if !ok {
		t.Fatalf("no testcase for the %s tier:\n%s", tierUnit, out)
	}
	if strings.Contains(passing, "<error") || strings.Contains(passing, "<failure") {
		t.Errorf("the passing %s tier gained a failure child:\n%s", tierUnit, passing)
	}
}

// junitTestcase returns the <testcase> block named `name` from a JUnit
// document.
func junitTestcase(doc, name string) (string, bool) {
	_, rest, ok := strings.Cut(doc, `name="`+name+`"`)
	if !ok {
		return "", false
	}
	block, _, ok := strings.Cut(rest, "</testcase>")
	if !ok {
		return "", false
	}
	return block, true
}

// TestPrintTAPReportsUnverifiedTierAsNotOk pins the TAP emitter. TAP
// treats every `ok` line as a passing point, including one carrying a
// trailing comment.
func TestPrintTAPReportsUnverifiedTierAsNotOk(t *testing.T) {
	v := unverifiedVerdict(t)
	out := captureStdout(t, func() { printTAP(v, v.Verdict) })

	line, ok := lineContaining(out, tierStatic)
	if !ok {
		t.Fatalf("no TAP point for the %s tier:\n%s", tierStatic, out)
	}
	if !strings.HasPrefix(line, "not ok ") {
		t.Errorf("the unverified %s tier reports as a passing TAP point: %q", tierStatic, line)
	}
	if !strings.Contains(line, "unverified") {
		t.Errorf("the %s TAP point does not name the unverified state: %q", tierStatic, line)
	}
	if !strings.Contains(line, "protoc not on PATH") {
		t.Errorf("the %s TAP point drops the reason: %q", tierStatic, line)
	}
}

// TestPrintGitHubAnnotationsCountsUnverifiedTier pins the annotation
// emitter. Without an annotation and a counter, the summary line of a
// run that proved nothing is indistinguishable from a clean one.
func TestPrintGitHubAnnotationsCountsUnverifiedTier(t *testing.T) {
	v := unverifiedVerdict(t)
	out := captureStdout(t, func() { printGitHubAnnotations(v) })

	line, ok := lineContaining(out, "tier "+tierStatic+"::")
	if !ok {
		t.Fatalf("no annotation for the unverified %s tier:\n%s", tierStatic, out)
	}
	if !strings.HasPrefix(line, "::error") {
		t.Errorf("the unverified %s tier is not annotated as an error: %q", tierStatic, line)
	}
	summary, ok := lineContaining(out, "summary::")
	if !ok {
		t.Fatalf("no summary line:\n%s", out)
	}
	if !strings.Contains(summary, "unverified=1") {
		t.Errorf("the summary line does not count the unverified tier: %q", summary)
	}
	if !strings.Contains(summary, "verdict="+verdictstatus.VerdictUnverified) {
		t.Errorf("the summary line does not carry the overall verdict: %q", summary)
	}
}

// lineContaining returns the first line of out containing sub.
func lineContaining(out, sub string) (string, bool) {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, sub) {
			return line, true
		}
	}
	return "", false
}
