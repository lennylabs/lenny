// SPDX-License-Identifier: MIT

package containers

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"time"

	"github.com/testcontainers/testcontainers-go"
)

// staleReaperRetry bounds how long a container start is retried while the
// testcontainers reaper of the test session exists and is not running.
//
// Every test binary that one `go test` invocation starts shares a
// testcontainers session, because the session ID derives from the PID and
// start time of the binary's parent, the go command. The binaries share
// one reaper container (Ryuk), named after the session and created by the
// first binary that starts a container. Ryuk exits once no binary has been
// connected to it for its reconnection timeout (10 seconds by default),
// which a gap between packages can reach, and Docker then removes the
// container, which it runs with auto-remove. A binary that starts a container in that window
// finds the reaper by its name and labels, and testcontainers-go v0.42.0
// waits on it as if it were running: the wait fails at once with
// `unexpected container status "removing"` (or "exited", or "created"),
// and testcontainers-go treats that as final. Every container start of
// the binary then fails the same way until Docker has removed the
// container. Once it is gone, the next start creates a new reaper.
//
// The fix upstream, testcontainers-go pull request 3868 (merged on
// 2026-11-09, in no release as of 2026-11-27), removes a stopped reaper
// and creates a new one. Until go.mod takes a release with it, the
// starters of this package retry the start themselves. GATE-I18 records
// the defect and the upgrade that retires this retry.
type staleReaperRetry struct {
	// wait is the longest the retry runs, measured from the first attempt.
	wait time.Duration
	// firstDelay is the pause after the first failed attempt.
	firstDelay time.Duration
	// maxDelay caps the pause, which doubles after each failed attempt.
	maxDelay time.Duration
}

// defaultStaleReaperRetry is the policy every starter in this package
// uses. Docker removes an exited reaper in well under a second on an idle
// host, and the reaper of gate-main run 46051812584 was still in the
// "removing" state across tests that ran over tens of seconds on the
// 4-vCPU runner, so the bound leaves room for a loaded host while still
// failing a start whose reaper never goes away, such as one left in the
// "created" state.
var defaultStaleReaperRetry = staleReaperRetry{
	wait:       2 * time.Minute,
	firstDelay: 250 * time.Millisecond,
	maxDelay:   5 * time.Second,
}

// staleReaper matches the error testcontainers-go returns when the reaper
// it found for the session is not running: `reaper: from container "<id>":
// wait for reaper <id>: ... unexpected container status "<status>"`, or
// `... container exited with code <n>`. It requires the reaper prefix, so
// a container of the test's own that stops is not retried.
var staleReaper = regexp.MustCompile(`reaper: from container "[0-9a-f]+": wait for reaper [0-9a-f]+: .*(unexpected container status "[a-z]+"|container exited with code -?[0-9]+)`)

// isStaleReaper reports whether err is the error a container start returns
// while the session's reaper exists and is not running. Every other error
// is final and is returned to the caller at once.
func isStaleReaper(err error) bool {
	return err != nil && staleReaper.MatchString(err.Error())
}

// startContainer runs start with a context bounded by timeout and, while
// start fails because the session's reaper is not running, runs it again
// with a fresh context, so the time a stopped reaper costs does not come
// out of the container's own startup timeout. The pause between attempts
// starts at p.firstDelay and doubles up to p.maxDelay, and the retry stops
// once p.wait has passed since the first attempt. testcontainers-go looks
// the reaper up before it creates the container, so a failed attempt
// leaves no container behind.
func startContainer[T any](p staleReaperRetry, timeout time.Duration, start func(context.Context) (T, error)) (T, error) {
	deadline := time.Now().Add(p.wait)
	delay := p.firstDelay
	for attempt := 1; ; attempt++ {
		v, err := startOnce(timeout, start)
		if !isStaleReaper(err) {
			return v, err
		}
		if time.Now().Add(delay).After(deadline) {
			var zero T
			return zero, fmt.Errorf("the session's reaper was still not running after %d attempts in %s: %w", attempt, p.wait, err)
		}
		log.Printf("containers: the session's reaper is not running; starting again in %s (attempt %d): %v", delay, attempt, err)
		time.Sleep(delay)
		delay = min(2*delay, p.maxDelay)
	}
}

// startOnce runs start with a context bounded by timeout.
func startOnce[T any](timeout time.Duration, start func(context.Context) (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return start(ctx)
}

// genericContainer starts req with testcontainers.GenericContainer, under
// startContainer with p and timeout.
func genericContainer(p staleReaperRetry, timeout time.Duration, req testcontainers.GenericContainerRequest) (testcontainers.Container, error) {
	return startContainer(p, timeout, func(ctx context.Context) (testcontainers.Container, error) {
		return testcontainers.GenericContainer(ctx, req)
	})
}
