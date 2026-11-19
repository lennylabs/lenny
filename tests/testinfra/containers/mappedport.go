// SPDX-License-Identifier: MIT

package containers

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// portMappingRetry bounds how long a read of a container's published host
// port is retried while Docker has not yet published it.
//
// testcontainers can report a container as started before the Docker daemon
// has written the container's port bindings into its inspect record. A
// MappedPort, PortEndpoint, or ConnectionString call made in that window
// fails with `port "5432/tcp" not found`, and a rerun passes. The window is
// normally well under a second; the bound leaves room for a loaded gate host
// while still failing a container whose port never appears.
type portMappingRetry struct {
	// wait is the longest the retry runs, measured from the first attempt.
	wait time.Duration
	// firstDelay is the pause after the first failed attempt.
	firstDelay time.Duration
	// maxDelay caps the pause, which doubles after each failed attempt.
	maxDelay time.Duration
}

// defaultPortMappingRetry is the policy every starter in this package uses.
var defaultPortMappingRetry = portMappingRetry{
	wait:       10 * time.Second,
	firstDelay: 50 * time.Millisecond,
	maxDelay:   500 * time.Millisecond,
}

// portNotFound matches the error testcontainers' MappedPort returns while a
// port has no published binding (`port "5432/tcp" not found`). It requires
// the word "port" with a quoted name immediately before "not found", so a
// `container <id> not found` error, which a retry cannot cure, does not
// match.
var portNotFound = regexp.MustCompile(`(?i)\bport "[^"]*" not found`)

// isPortMappingPending reports whether err is the error a container returns
// while Docker has not yet published the requested port. Every other error is
// final and is returned to the caller at once.
func isPortMappingPending(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return portNotFound.MatchString(msg) || strings.Contains(strings.ToLower(msg), "no port mapping")
}

// retryPortMapping calls op until it succeeds, returns an error other than a
// pending port mapping, ctx ends, or p.wait passes since the first attempt.
// The pause between attempts starts at p.firstDelay and doubles up to
// p.maxDelay. When the retry gives up, the returned error wraps op's last
// error and, when ctx ended, ctx's error.
func retryPortMapping[T any](ctx context.Context, p portMappingRetry, op func(context.Context) (T, error)) (T, error) {
	var zero T
	deadline := time.NewTimer(p.wait)
	defer deadline.Stop()
	delay := p.firstDelay
	for attempt := 1; ; attempt++ {
		v, err := op(ctx)
		if !isPortMappingPending(err) {
			return v, err
		}
		pause := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			pause.Stop()
			return zero, fmt.Errorf("port mapping not published after %d attempts: %w (%w)", attempt, err, ctx.Err())
		case <-deadline.C:
			pause.Stop()
			return zero, fmt.Errorf("port mapping not published within %s after %d attempts: %w", p.wait, attempt, err)
		case <-pause.C:
		}
		delay = min(2*delay, p.maxDelay)
	}
}

// mappedEndpoint returns the host:port at which c publishes port (for
// example "6379/tcp"), retrying while the binding has not been published.
func mappedEndpoint(ctx context.Context, c testcontainers.Container, port string) (string, error) {
	return retryPortMapping(ctx, defaultPortMappingRetry, func(ctx context.Context) (string, error) {
		return c.PortEndpoint(ctx, port, "")
	})
}

// postgresDSN returns c's connection string with sslmode=disable, retrying
// while the 5432/tcp binding has not been published.
func postgresDSN(ctx context.Context, c *tcpostgres.PostgresContainer) (string, error) {
	return retryPortMapping(ctx, defaultPortMappingRetry, func(ctx context.Context) (string, error) {
		return c.ConnectionString(ctx, "sslmode=disable")
	})
}
