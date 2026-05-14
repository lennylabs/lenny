// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// cachedSocketPath returns the path the daemon listens on. Mirrors
// the logic in cmd/lenny-test-cached/main.go.
func cachedSocketPath() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "lenny-test", "cached.sock")
	}
	return fmt.Sprintf("/tmp/lenny-test-cached-%d.sock", os.Getuid())
}

// cachedProbe returns true when the daemon's socket accepts traffic.
func cachedProbe() bool {
	path := cachedSocketPath()
	if _, err := os.Stat(path); err != nil {
		return false
	}
	conn, err := net.DialTimeout("unix", path, 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// cachedSpawnIfMissing starts the daemon in the background when the
// socket is absent. Returns once `status` returns Running, or an
// error if the daemon failed to come up within 5 seconds.
func cachedSpawnIfMissing() error {
	if cachedProbe() {
		return nil
	}

	// Locate the binary: prefer $GOPATH/bin/lenny-test-cached,
	// fall back to a freshly-built binary alongside lenny-test.
	path := "lenny-test-cached"
	if p, err := exec.LookPath(path); err == nil {
		path = p
	} else if self, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(self), "lenny-test-cached")
		if _, err := os.Stat(candidate); err == nil {
			path = candidate
		}
	}
	cmd := exec.Command(path)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawn daemon: %w", err)
	}
	// Detach.
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("detach daemon: %w", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cachedProbe() {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("cached daemon did not become reachable within 5s")
}

// cachedRequest sends a one-line JSON request and decodes the
// one-line JSON response.
func cachedRequest(op string) (map[string]any, error) {
	conn, err := net.DialTimeout("unix", cachedSocketPath(), 500*time.Millisecond)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	payload, _ := json.Marshal(map[string]string{"op": op})
	if _, err := conn.Write(append(payload, '\n')); err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 32*1024), 1<<20)
	if !scanner.Scan() {
		return nil, errors.New("daemon closed without responding")
	}
	var out map[string]any
	if err := json.Unmarshal(scanner.Bytes(), &out); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return out, nil
}

// cachedEnsure starts the daemon (if absent) and asks it to bring
// the compose stack up. Returns the endpoints map.
func cachedEnsure() (map[string]any, error) {
	if err := cachedSpawnIfMissing(); err != nil {
		return nil, err
	}
	if _, err := cachedRequest("ensure"); err != nil {
		return nil, fmt.Errorf("ensure: %w", err)
	}
	return cachedRequest("endpoints")
}
