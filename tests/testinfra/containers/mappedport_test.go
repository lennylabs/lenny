// SPDX-License-Identifier: MIT

package containers

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// The tests in this file verify test-harness retry logic, which implements
// no specification requirement, so they carry no spec annotation.

// testPortMappingRetry is a short policy so the deadline cases finish fast.
var testPortMappingRetry = portMappingRetry{
	wait:       200 * time.Millisecond,
	firstDelay: 2 * time.Millisecond,
	maxDelay:   20 * time.Millisecond,
}

// errPortPending is the error testcontainers' MappedPort returns while
// Docker has not yet published the port, wrapped as a caller sees it.
var errPortPending = fmt.Errorf("dsn: %w", errors.New(`port "5432/tcp" not found`))

// flakyPortOp returns an op that fails with failErr on its first n calls and
// then returns "127.0.0.1:55432", and a pointer to its call count.
func flakyPortOp(n int, failErr error) (func(context.Context) (string, error), *int) {
	calls := 0
	return func(context.Context) (string, error) {
		calls++
		if n < 0 || calls <= n {
			return "", failErr
		}
		return "127.0.0.1:55432", nil
	}, &calls
}

func TestRetryPortMappingSucceedsOnceThePortIsPublished(t *testing.T) {
	t.Parallel()
	op, calls := flakyPortOp(3, errPortPending)
	got, err := retryPortMapping(context.Background(), testPortMappingRetry, op)
	if err != nil {
		t.Fatalf("retryPortMapping: %v", err)
	}
	if got != "127.0.0.1:55432" {
		t.Fatalf("endpoint = %q, want 127.0.0.1:55432", got)
	}
	if *calls != 4 {
		t.Fatalf("op called %d times, want 4 (3 failures then a success)", *calls)
	}
}

func TestRetryPortMappingRetriesTheNoPortMappingError(t *testing.T) {
	t.Parallel()
	op, calls := flakyPortOp(1, errors.New("container abc: no port mapping for 6379/tcp"))
	if _, err := retryPortMapping(context.Background(), testPortMappingRetry, op); err != nil {
		t.Fatalf("retryPortMapping: %v", err)
	}
	if *calls != 2 {
		t.Fatalf("op called %d times, want 2", *calls)
	}
}

func TestRetryPortMappingStopsAtTheDeadline(t *testing.T) {
	t.Parallel()
	op, calls := flakyPortOp(-1, errPortPending)
	start := time.Now()
	_, err := retryPortMapping(context.Background(), testPortMappingRetry, op)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("retryPortMapping succeeded, want an error at the deadline")
	}
	if !errors.Is(err, errPortPending) {
		t.Fatalf("error %v does not wrap the last op error", err)
	}
	if elapsed < testPortMappingRetry.wait {
		t.Fatalf("gave up after %s, before the %s deadline", elapsed, testPortMappingRetry.wait)
	}
	// One pause past the deadline is the most the loop can overrun; the
	// bound is generous so a loaded host does not flake the test.
	if elapsed > testPortMappingRetry.wait+2*time.Second {
		t.Fatalf("gave up after %s, long past the %s deadline", elapsed, testPortMappingRetry.wait)
	}
	if *calls < 2 {
		t.Fatalf("op called %d times, want at least 2 attempts before the deadline", *calls)
	}
}

func TestRetryPortMappingReturnsOtherErrorsAtOnce(t *testing.T) {
	t.Parallel()
	errOther := errors.New(`inspect: container 3f2a not found`)
	op, calls := flakyPortOp(-1, errOther)
	_, err := retryPortMapping(context.Background(), testPortMappingRetry, op)
	if err != errOther {
		t.Fatalf("error = %v, want the op error returned as is", err)
	}
	if *calls != 1 {
		t.Fatalf("op called %d times, want 1", *calls)
	}
}

func TestRetryPortMappingHonoursTheCallersContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	op := func(context.Context) (string, error) {
		calls++
		if calls == 2 {
			cancel()
		}
		return "", errPortPending
	}
	policy := testPortMappingRetry
	policy.wait = time.Minute
	start := time.Now()
	_, err := retryPortMapping(ctx, policy, op)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want it to wrap context.Canceled", err)
	}
	if !errors.Is(err, errPortPending) {
		t.Fatalf("error = %v, want it to wrap the last op error", err)
	}
	if calls != 2 {
		t.Fatalf("op called %d times, want 2 (stop once ctx is cancelled)", calls)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("returned after %s, want promptly after cancellation", elapsed)
	}
}

func TestIsPortMappingPendingMatchesOnlyAnUnpublishedPort(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"port not found", errors.New(`port "5432/tcp" not found`), true},
		{"wrapped port not found", fmt.Errorf("dsn: %w", errors.New(`port "9000/tcp" not found`)), true},
		{"no port mapping", errors.New("no port mapping for 6379/tcp"), true},
		{"no port mapping upper case", errors.New("No Port Mapping"), true},
		{"container not found", errors.New("container 3f2a not found"), false},
		{"connection refused", errors.New("dial tcp 127.0.0.1:5432: connect: connection refused"), false},
		{"deadline", context.DeadlineExceeded, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isPortMappingPending(tc.err); got != tc.want {
				t.Fatalf("isPortMappingPending(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
