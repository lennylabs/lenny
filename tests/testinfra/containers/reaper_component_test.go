//go:build component

// SPDX-License-Identifier: MIT

package containers_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// reaperChildEnv selects what a child process of
// TestContainerStartsAfterTheSessionReaperStopped does: "session" prints
// the child's testcontainers session ID, and "start" starts a Redis
// container and pings it.
const reaperChildEnv = "LENNY_TEST_REAPER_CHILD"

// ryukImage is the reaper image of the testcontainers-go version in go.mod
// (config.ReaperDefaultImage, which is internal to the module).
const ryukImage = "testcontainers/ryuk:0.13.0"

// staleReaperLife is how long the test keeps the stopped reaper in place
// before removing it, as the Docker daemon removes an exited reaper that
// it runs with auto-remove.
const staleReaperLife = 3 * time.Second

// This test exercises the test harness rather than a requirement, so it
// carries no specification annotation.
//
// diagnosis: a container start in a test process failed because the
// testcontainers reaper of its session was not running, and the start was
// not retried until the stopped reaper was gone. Every test of a package
// that starts after the shared reaper stopped then fails with
// `reaper: from container "<id>": wait for reaper <id>: ... unexpected
// container status`. Check startContainer and isStaleReaper in
// tests/testinfra/containers/reaper.go, and whether the testcontainers-go
// version in go.mod changed the reaper's error text.
func TestContainerStartsAfterTheSessionReaperStopped(t *testing.T) {
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Skipf("blocked: the docker CLI is not on PATH: %v", err)
	}

	// The children of this process share one testcontainers session,
	// whose ID derives from their parent's PID and start time, so a
	// reaper this test plants under that session is the one a child
	// finds. The rest of the run uses this process's own session and
	// does not see it.
	session := childSessionID(t, runReaperChild(t, "session"))

	// A reaper container that exists and is not running is what another
	// test process of the session finds once Ryuk has exited and the
	// daemon has not yet removed it. A container that is created and never
	// started holds that state for as long as the test needs. It runs the
	// Ryuk image, as the reaper does: testcontainers-go connects any other
	// container it finds to the reaper, which deadlocks the lookup.
	name := "reaper_" + session
	create := exec.Command(docker, "create",
		"--name", name,
		"--label", "org.testcontainers.sessionId="+session,
		"--label", "org.testcontainers.reaper=true",
		"--label", "org.testcontainers.ryuk=true",
		ryukImage)
	if out, err := create.CombinedOutput(); err != nil {
		t.Fatalf("docker create %s: %v\n%s", name, err, out)
	}
	removed := make(chan error, 1)
	timer := time.AfterFunc(staleReaperLife, func() {
		removed <- exec.Command(docker, "rm", "-f", name).Run()
	})
	t.Cleanup(func() {
		timer.Stop()
		_ = exec.Command(docker, "rm", "-f", name).Run()
	})

	out := runReaperChild(t, "start")
	if !strings.Contains(out, "redis started") {
		t.Fatalf("the child did not start Redis:\n%s", out)
	}
	select {
	case err := <-removed:
		if err != nil {
			t.Fatalf("remove the stopped reaper: %v", err)
		}
	default:
		t.Fatalf("the child started Redis while the stopped reaper %s was still in place:\n%s", name, out)
	}
}

// runReaperChild runs this test binary again as a child that runs only
// TestReaperChild in the given mode, and returns its output. A child that
// fails fails the test.
func runReaperChild(t *testing.T, mode string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestReaperChild$", "-test.v", "-test.count=1")
	cmd.Env = append(os.Environ(), reaperChildEnv+"="+mode)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("child %q: %v\n%s", mode, err, out.String())
	}
	return out.String()
}

// childSessionID returns the session ID a "session" child printed.
func childSessionID(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if id, ok := strings.CutPrefix(strings.TrimSpace(line), "session="); ok && len(id) == 64 {
			return id
		}
	}
	t.Fatalf("the child printed no 64-character session ID:\n%s", out)
	return ""
}

// TestReaperChild is the child process of
// TestContainerStartsAfterTheSessionReaperStopped. Run without
// LENNY_TEST_REAPER_CHILD, as every run of the package does, it returns at
// once. It exercises the test harness, so it carries no specification
// annotation.
//
// diagnosis: the child half of TestContainerStartsAfterTheSessionReaperStopped
// failed; that test's diagnosis applies.
func TestReaperChild(t *testing.T) {
	switch os.Getenv(reaperChildEnv) {
	case "":
		return
	case "session":
		fmt.Println("session=" + testcontainers.SessionID())
	case "start":
		r := containers.StartRedis(t, containers.RedisOptions{})
		if err := r.Client.Ping(context.Background()).Err(); err != nil {
			t.Fatalf("ping: %v", err)
		}
		fmt.Println("redis started")
	default:
		t.Fatalf("unknown %s %q", reaperChildEnv, os.Getenv(reaperChildEnv))
	}
}
