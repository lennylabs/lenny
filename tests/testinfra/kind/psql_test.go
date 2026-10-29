// SPDX-License-Identifier: MIT

package kind

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// spec: 11.7 (Audit Logging), 25.9 (Audit Log Query API)
//
// The end marker separates a lost `kubectl run --attach` output from an
// empty result set: StripPsqlEndMarker removes the marker line and reports
// whether it was present, keeping every other line in order.
func TestStripPsqlEndMarkerSeparatesLostOutputFromEmptyResult(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		want  string
		found bool
	}{
		{name: "lost output", in: "", want: "", found: false},
		{name: "empty result set", in: PsqlEndMarker + "\n", want: "", found: true},
		{name: "rows then marker", in: "1\n2\n" + PsqlEndMarker + "\n", want: "1\n2\n", found: true},
		{name: "marker with padding", in: "6\n  " + PsqlEndMarker + "  \n", want: "6\n", found: true},
		{name: "rows without marker", in: "1\n2\n", want: "1\n2\n", found: false},
		{name: "marker as substring is not the marker", in: "x" + PsqlEndMarker + "\n", want: "x" + PsqlEndMarker + "\n", found: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, found := StripPsqlEndMarker(tc.in)
			if got != tc.want || found != tc.found {
				t.Errorf("StripPsqlEndMarker(%q) = (%q, %v), want (%q, %v)", tc.in, got, found, tc.want, tc.found)
			}
		})
	}
}

// spec: 11.7 (Audit Logging), 25.9 (Audit Log Query API)
//
// withPsqlEndMarker terminates the caller's statement and appends the
// marker SELECT, so the marker prints only after the query ran.
func TestWithPsqlEndMarkerTerminatesTheQuery(t *testing.T) {
	for in, want := range map[string]string{
		"SELECT 1":           "SELECT 1; SELECT '" + PsqlEndMarker + "';",
		"SELECT 1;\n":        "SELECT 1; SELECT '" + PsqlEndMarker + "';",
		"  SELECT 1;  ":      "SELECT 1; SELECT '" + PsqlEndMarker + "';",
		"SELECT 1; SELECT 2": "SELECT 1; SELECT 2; SELECT '" + PsqlEndMarker + "';",
	} {
		if got := withPsqlEndMarker(in); got != want {
			t.Errorf("withPsqlEndMarker(%q) = %q, want %q", in, got, want)
		}
	}
}

// spec: 11.7 (Audit Logging), 25.9 (Audit Log Query API)
//
// runUntilMarked reruns a query whose output lost the marker, returns the
// first marked output without the marker, stops on a run error without
// retrying, and reports errPsqlMarkerMissing after the last attempt.
func TestRunUntilMarkedRetriesOnlyLostOutput(t *testing.T) {
	runErr := errors.New("exit status 3")
	cases := []struct {
		name      string
		outputs   []string
		errs      []error
		wantOut   string
		wantErr   error
		wantCalls int
		wantRetry []int
	}{
		{
			name:      "first attempt marked",
			outputs:   []string{"6\n" + PsqlEndMarker + "\n"},
			wantOut:   "6\n",
			wantCalls: 1,
		},
		{
			name:      "lost output then marked",
			outputs:   []string{"", "", "6\n" + PsqlEndMarker + "\n"},
			wantOut:   "6\n",
			wantCalls: 3,
			wantRetry: []int{1, 2},
		},
		{
			name:      "every attempt lost",
			outputs:   []string{"", "", ""},
			wantOut:   "",
			wantErr:   errPsqlMarkerMissing,
			wantCalls: 3,
			wantRetry: []int{1, 2},
		},
		{
			name:      "run error is not retried",
			outputs:   []string{"ERROR: relation does not exist"},
			errs:      []error{runErr},
			wantOut:   "ERROR: relation does not exist",
			wantErr:   runErr,
			wantCalls: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			var retries []int
			out, err := runUntilMarked(3, func() (string, error) {
				i := calls
				calls++
				var e error
				if i < len(tc.errs) {
					e = tc.errs[i]
				}
				return tc.outputs[i], e
			}, func(attempt int) { retries = append(retries, attempt) })
			if out != tc.wantOut {
				t.Errorf("output = %q, want %q", out, tc.wantOut)
			}
			if !errors.Is(err, tc.wantErr) || (tc.wantErr == nil && err != nil) {
				t.Errorf("error = %v, want %v", err, tc.wantErr)
			}
			if calls != tc.wantCalls {
				t.Errorf("run called %d times, want %d", calls, tc.wantCalls)
			}
			if !reflect.DeepEqual(retries, tc.wantRetry) {
				t.Errorf("retries = %v, want %v", retries, tc.wantRetry)
			}
		})
	}
}

// spec: 11.7 (Audit Logging)
//
// The one-shot psql pod connects to the Postgres pod IP with the e2e
// credentials and passes the caller's psql flags last.
func TestPsqlRunArgsTargetThePodIP(t *testing.T) {
	args := psqlRunArgs("lenny-system", "t9-read", "10.0.0.7", "-tA", "-c", "SELECT 1")
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"-n lenny-system run t9-read",
		"--image=" + PsqlImage,
		"--attach --rm --quiet",
		"psql -h 10.0.0.7 -U lenny -d lenny -tA -c SELECT 1",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("psqlRunArgs = %q, missing %q", joined, want)
		}
	}
}
