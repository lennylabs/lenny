// SPDX-License-Identifier: MIT

package adapter

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// supervisorHeader is the runtime supervisor's header, relative to this
// package, which defines the supervisor's timing constants.
var supervisorHeader = filepath.Join("..", "..", "supervisor", "supervisor.h")

// supervisorMillis returns the value of the named millisecond #define in the
// supervisor header. The value is a product of integer literals, optionally
// parenthesized and suffixed, such as (4ULL * 60 * 1000).
func supervisorMillis(t *testing.T, header, name string) time.Duration {
	t.Helper()
	m := regexp.MustCompile(`(?m)^#define\s+` + regexp.QuoteMeta(name) + `\s+(.+)$`).FindStringSubmatch(header)
	if m == nil {
		t.Fatalf("%s: no #define %s (renamed or removed?)", supervisorHeader, name)
	}
	expr := strings.Trim(strings.TrimSpace(m[1]), "()")
	product := int64(1)
	for _, f := range strings.Split(expr, "*") {
		lit := strings.TrimRight(strings.TrimSpace(f), "ULul")
		n, err := strconv.ParseInt(lit, 10, 64)
		if err != nil {
			t.Fatalf("%s: #define %s %q is not a product of integer literals: %v", supervisorHeader, name, m[1], err)
		}
		product *= n
	}
	return time.Duration(product) * time.Millisecond
}

// TestSupervisorMillisParsesEveryDefineForm pins the parser the bound check
// below relies on, so that check cannot pass by misreading the header.
//
// spec: 28.5.3 (CH-SUPERVISE, Timing)
func TestSupervisorMillisParsesEveryDefineForm(t *testing.T) {
	header := "#define A 5000\n#define B (10ULL * 60 * 1000)\n#define C   (4ull*60*1000)\n"
	for name, want := range map[string]time.Duration{"A": 5 * time.Second, "B": 10 * time.Minute, "C": 4 * time.Minute} {
		if got := supervisorMillis(t, header, name); got != want {
			t.Errorf("%s = %s, want %s", name, got, want)
		}
	}
}

// spec: 28.5.3 (CH-SUPERVISE, Timing), 4.7.10 (Runtime supervisor, Children;
// Runtime supervisor, Sweep), 5.2 (Supervised scrub sequence)
// diagnosis: the adapter's sweep request bound no longer exceeds the
// supervisor's latest sweep answer (its sweep worker backstop, its wait after
// the backstop SIGKILL, and its end pass bound). A sweep worker that runs
// past the adapter's bound then ends as a link failure: the scrub skips
// cleanupCommands and the second end and sweep, and the supervisor's
// sweep_failed answer for that worker is never read.
func TestSuperviseSweepBoundExceedsTheSupervisorSweepAnswer_spec_28_5_3(t *testing.T) {
	raw, err := os.ReadFile(supervisorHeader)
	if err != nil {
		t.Fatalf("read the supervisor header: %v", err)
	}
	header := string(raw)
	answer := supervisorMillis(t, header, "SV_SWEEP_BACKSTOP_MS") +
		supervisorMillis(t, header, "SV_WAIT_AFTER_KILL_MS") +
		supervisorMillis(t, header, "SV_END_KILL_BOUND_MS")
	if superviseSweepBound <= answer {
		t.Fatalf("superviseSweepBound = %s, want more than the supervisor's latest sweep answer %s", superviseSweepBound, answer)
	}
	if deadline := scrubDeadline(0); deadline <= 2*(superviseEndBound+superviseSweepBound) {
		t.Fatalf("scrubDeadline = %s, want more than two ends and two sweeps at their bounds", deadline)
	}
}
