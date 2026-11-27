// SPDX-License-Identifier: MIT

package containers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// The tests in this file verify test-harness retry logic, which implements
// no specification requirement, so they carry no spec annotation.

// testStaleReaperRetry is a short policy so the deadline cases finish fast.
var testStaleReaperRetry = staleReaperRetry{
	wait:       200 * time.Millisecond,
	firstDelay: 2 * time.Millisecond,
	maxDelay:   20 * time.Millisecond,
}

// errReaperRemoving is the error a Postgres start returned in both
// attempts of gate-main run 46051812584, while the session's reaper was
// being removed.
var errReaperRemoving = errors.New(`run postgres container: run postgres: generic container: create container: reaper: from container "5a1c5f46": wait for reaper 5a1c5f46: detect internal port: check target: retries: 0, last err: unexpected container status "removing"`)

// flakyStart returns a start function that fails with failErr on its first
// n calls (every call when n is negative) and then returns "started", and a
// pointer to its call count. Each call records whether its context carried
// a deadline.
func flakyStart(n int, failErr error) (func(context.Context) (string, error), *int) {
	calls := 0
	return func(ctx context.Context) (string, error) {
		calls++
		if _, ok := ctx.Deadline(); !ok {
			return "", errors.New("start called without a deadline")
		}
		if n < 0 || calls <= n {
			return "", failErr
		}
		return "started", nil
	}, &calls
}

func TestStartContainerRetriesWhileTheReaperIsNotRunning(t *testing.T) {
	t.Parallel()
	start, calls := flakyStart(3, errReaperRemoving)
	got, err := startContainer(testStaleReaperRetry, time.Second, start)
	if err != nil {
		t.Fatalf("startContainer: %v", err)
	}
	if got != "started" {
		t.Fatalf("value = %q, want started", got)
	}
	if *calls != 4 {
		t.Fatalf("start called %d times, want 4 (3 failures then a success)", *calls)
	}
}

func TestStartContainerGivesEachAttemptAFreshContext(t *testing.T) {
	t.Parallel()
	var deadlines []time.Time
	calls := 0
	start := func(ctx context.Context) (string, error) {
		calls++
		d, _ := ctx.Deadline()
		deadlines = append(deadlines, d)
		if calls == 1 {
			// The first attempt outlives a quarter of its timeout; the
			// second must still get the whole timeout.
			time.Sleep(30 * time.Millisecond)
			return "", errReaperRemoving
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return "started", nil
	}
	if _, err := startContainer(testStaleReaperRetry, 100*time.Millisecond, start); err != nil {
		t.Fatalf("startContainer: %v", err)
	}
	if len(deadlines) != 2 || !deadlines[1].After(deadlines[0]) {
		t.Fatalf("attempt deadlines = %v, want a later deadline for the second attempt", deadlines)
	}
}

func TestStartContainerStopsAtTheDeadline(t *testing.T) {
	t.Parallel()
	start, calls := flakyStart(-1, errReaperRemoving)
	began := time.Now()
	_, err := startContainer(testStaleReaperRetry, time.Second, start)
	elapsed := time.Since(began)
	if err == nil {
		t.Fatal("startContainer succeeded with a reaper that never stops failing")
	}
	if !errors.Is(err, errReaperRemoving) {
		t.Fatalf("error %v does not wrap the last start error", err)
	}
	if !strings.Contains(err.Error(), "still not running") {
		t.Fatalf("error %q does not say the reaper was still not running", err)
	}
	if *calls < 2 {
		t.Fatalf("start called %d times, want at least 2", *calls)
	}
	if elapsed > testStaleReaperRetry.wait+100*time.Millisecond {
		t.Fatalf("startContainer ran %s, past its %s bound", elapsed, testStaleReaperRetry.wait)
	}
}

func TestStartContainerReturnsOtherErrorsAtOnce(t *testing.T) {
	t.Parallel()
	other := errors.New(`wait until ready: "database system is ready to accept connections" matched 0 times`)
	start, calls := flakyStart(-1, other)
	_, err := startContainer(testStaleReaperRetry, time.Second, start)
	if !errors.Is(err, other) {
		t.Fatalf("error = %v, want %v", err, other)
	}
	if *calls != 1 {
		t.Fatalf("start called %d times, want 1", *calls)
	}
}

func TestStartContainerReturnsTheFirstSuccess(t *testing.T) {
	t.Parallel()
	start, calls := flakyStart(0, errReaperRemoving)
	if _, err := startContainer(testStaleReaperRetry, time.Second, start); err != nil {
		t.Fatalf("startContainer: %v", err)
	}
	if *calls != 1 {
		t.Fatalf("start called %d times, want 1", *calls)
	}
}

func TestIsStaleReaperMatchesOnlyAReaperThatIsNotRunning(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"removing", errReaperRemoving, true},
		{"removing, wrapped", fmt.Errorf("StartPostgres: %w", errReaperRemoving), true},
		{"created", errors.New(`create container: reaper: from container "e58c2d02": wait for reaper e58c2d02: detect internal port: check target: retries: 0, last err: unexpected container status "created"`), true},
		{"exited", errors.New(`create container: reaper: from container "1ffc6707": wait for reaper 1ffc6707: mapped port: check target: retries: 2, port: "", last err: container exited with code 0`), true},
		{"the test's own container exited", errors.New(`generic container: start container: started hook: wait until ready: container exited with code 1`), false},
		{"the test's own container is removing", errors.New(`wait until ready: check target: unexpected container status "removing"`), false},
		{"reaper deadline", errors.New(`create container: reaper: from container "1ffc6707": wait for reaper 1ffc6707: context deadline exceeded`), false},
		{"startup timeout", errors.New(`wait until ready: "database system is ready to accept connections" matched 0 times, expected 2`), false},
	}
	for _, tc := range cases {
		if got := isStaleReaper(tc.err); got != tc.want {
			t.Errorf("%s: isStaleReaper = %v, want %v", tc.name, got, tc.want)
		}
	}
}
