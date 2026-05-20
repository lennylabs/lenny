// SPDX-License-Identifier: MIT

// Package compose wraps the §10.3 docker-compose profile. It exposes
// Up / Down / WaitReady / Endpoints helpers so tier-3 contract and
// tier-4 integration tests can ensure the backing stores are
// running without each test orchestrating docker compose itself.
//
// The Go helper shells out to `docker compose` rather than a SDK so
// the tooling matches what a developer runs interactively. The
// helper is idempotent: Up returns immediately if the stack is
// already running.
//
// Usage:
//
//	if compose.Available(t) {
//	    stack := compose.Up(t)
//	    pg := stack.PostgresDSN()
//	    // ...
//	}
//
// On hosts without docker or docker compose, Available reports false
// and the test should t.Skip with a diagnosis.
package compose

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// Profile selects a compose overlay.
type Profile string

const (
	ProfileDefault Profile = "default"
	ProfileMTLS    Profile = "mtls"
)

// Stack represents a running compose stack. The endpoints are
// canonical per compose/default.yml.
type Stack struct {
	root    string
	profile Profile
}

// SkipUnlessAvailable t.Skips with a precise reason when docker or
// docker compose is unreachable. Matches the convention used by
// chaos.SkipUnlessAvailable, envtest.SkipUnlessAvailable, and
// kind.PrerequisitesAvailable so callers can write one line:
//
//	compose.SkipUnlessAvailable(t)
//	stack := compose.Up(t, compose.ProfileDefault)
func SkipUnlessAvailable(t testing.TB) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("compose.SkipUnlessAvailable: docker not on PATH: %v", err)
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skipf("compose.SkipUnlessAvailable: docker daemon not reachable: %v", err)
	}
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skipf("compose.SkipUnlessAvailable: docker compose v2 plugin not present: %v", err)
	}
}

// Available reports whether docker and docker compose are reachable
// on this host. Callers usually prefer SkipUnlessAvailable above;
// Available is kept for code paths that need a non-skipping check.
func Available(t testing.TB) bool {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		return false
	}
	// `docker compose version` exits 0 when the v2 plugin is present.
	return exec.Command("docker", "compose", "version").Run() == nil
}

// Up brings the stack up with the named profile. Registers a
// t.Cleanup that brings it down on test exit. Returns a *Stack
// pointing at the running services. Idempotent: when the stack is
// already up the call simply returns the existing handle.
func Up(t testing.TB, profile Profile) *Stack {
	t.Helper()
	if !Available(t) {
		t.Skipf("compose.Up: docker compose not available on this host")
	}
	root := schematest.RepoRoot(t)
	s := &Stack{root: root, profile: profile}
	args := s.composeArgs("up", "-d")
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("compose up: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		down := s.composeArgs("down", "-v")
		out, err := exec.Command("docker", down...).CombinedOutput()
		if err != nil {
			t.Logf("compose down (cleanup): %v\n%s", err, out)
		}
	})
	if err := s.WaitReady(60 * time.Second); err != nil {
		t.Fatalf("compose up did not become ready: %v", err)
	}
	return s
}

// WaitReady polls each service's healthcheck until all report
// healthy or the deadline expires.
func (s *Stack) WaitReady(timeout time.Duration) error {
	services := []string{
		"lenny-postgres", "lenny-redis", "lenny-minio",
		"lenny-pgbouncer", "lenny-redis-replica",
	}
	deadline := time.Now().Add(timeout)
	lastStatus := map[string]string{}
	for {
		allHealthy := true
		for _, svc := range services {
			out, err := exec.Command("docker", "inspect", "--format", "{{.State.Health.Status}}", svc).Output()
			if err != nil {
				lastStatus[svc] = fmt.Sprintf("inspect error: %v", err)
				allHealthy = false
				break
			}
			status := strings.TrimSpace(string(out))
			lastStatus[svc] = status
			if status != "healthy" {
				allHealthy = false
				break
			}
		}
		if allHealthy {
			return nil
		}
		if time.Now().After(deadline) {
			parts := make([]string, 0, len(services))
			for _, svc := range services {
				st := lastStatus[svc]
				if st == "" {
					st = "(never reported)"
				}
				parts = append(parts, fmt.Sprintf("%s=%s", svc, st))
			}
			return fmt.Errorf("compose: services did not reach healthy state within %s: %s",
				timeout, strings.Join(parts, ", "))
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// Down brings the stack down. Normally invoked via the t.Cleanup Up
// registered, but exposed for tests that want explicit teardown.
func (s *Stack) Down(t testing.TB) {
	t.Helper()
	out, err := exec.Command("docker", s.composeArgs("down", "-v")...).CombinedOutput()
	if err != nil {
		t.Logf("compose down: %v\n%s", err, out)
	}
}

// PostgresDSN returns a connection string suitable for pgx.Connect.
func (s *Stack) PostgresDSN() string {
	return "postgres://lenny:lenny@127.0.0.1:15432/lenny?sslmode=disable"
}

// PgBouncerDSN returns a connection string that points at the
// PgBouncer connection-pooler in session pooling mode. The §12.2.2
// connection-reuse leak test reaches Postgres through this endpoint
// so a returned connection retains its SET LOCAL state, exposing any
// gateway-side failure to reset the tenant context before reuse.
func (s *Stack) PgBouncerDSN() string {
	return "postgres://lenny:lenny@127.0.0.1:16432/lenny?sslmode=disable"
}

// RedisAddr returns the host:port for the Redis master node.
func (s *Stack) RedisAddr() string {
	return "127.0.0.1:16379"
}

// RedisSentinelAddrs returns the three Sentinel host:port pairs the
// §12.8 TestRedisSentinelFailover chaos scaffold consumes. The
// quorum is 2; a client should connect via the redis Sentinel
// protocol against any of the three addresses.
func (s *Stack) RedisSentinelAddrs() []string {
	return []string{"127.0.0.1:26379", "127.0.0.1:26380", "127.0.0.1:26381"}
}

// RedisSentinelMasterName returns the master name the Sentinel
// quorum monitors. Matches the `sentinel monitor lenny-master`
// directive rendered into the Sentinel config.
func (s *Stack) RedisSentinelMasterName() string {
	return "lenny-master"
}

// MinIOEndpoint returns the S3-compatible endpoint URL.
func (s *Stack) MinIOEndpoint() string {
	return "http://127.0.0.1:19000"
}

// MinIOCredentials returns access + secret keys.
func (s *Stack) MinIOCredentials() (access, secret string) {
	return "lenny", "lenny-secret"
}

// OTLPEndpoint returns the OTLP gRPC endpoint URL.
func (s *Stack) OTLPEndpoint() string {
	return "127.0.0.1:14317"
}

// composeArgs builds `docker compose -f <file> ...` argument list.
// When profile is mtls, the mtls overlay is layered on top.
func (s *Stack) composeArgs(rest ...string) []string {
	base := []string{"compose", "-f", filepath.Join(s.root, "compose", "default.yml")}
	if s.profile == ProfileMTLS {
		base = append(base, "-f", filepath.Join(s.root, "compose", "mtls.yml"))
	}
	return append(base, rest...)
}

// Status reports the docker-compose ps output. Used by `lenny-test
// infra status`.
func Status(profile Profile) (string, error) {
	root, err := schematest.RepoRootCwd()
	if err != nil {
		return "", err
	}
	s := &Stack{root: root, profile: profile}
	out, err := exec.Command("docker", s.composeArgs("ps")...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("compose ps: %w", err)
	}
	return string(out), nil
}
