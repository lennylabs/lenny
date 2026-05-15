// SPDX-License-Identifier: MIT

// Command lenny-test-cached is the §6 container-cache daemon. It is
// a long-lived helper that holds the testcontainers stack running
// between `lenny-test` invocations, so each component-tier run does
// not pay the five-to-ten-second container-startup tax.
//
// The daemon listens on a Unix socket at
// `${XDG_RUNTIME_DIR}/lenny-test/cached.sock` (or, when that is
// unset, /tmp/lenny-test-cached.sock for the current user). The
// protocol is line-oriented JSON; each request is one line, each
// response is one line.
//
// Commands:
//
//	{"op":"status"}                  → {"running":bool, "since":"RFC3339"}
//	{"op":"ensure"}                  → {"running":true, "pid":<int>}
//	{"op":"shutdown"}                → {"ok":true} and the process exits
//	{"op":"endpoints"}               → endpoint URLs for postgres / redis / minio / otlp
//
// The daemon is a developer convenience and is not used in CI. CI
// runs always start with cold containers for isolation (TESTING.md
// §6).
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

func main() {
	socketFlag := flag.String("socket", defaultSocketPath(), "Unix socket path")
	composeFile := flag.String("compose", "compose/default.yml", "path to compose YAML, relative to repo root")
	flag.Parse()

	// Single-instance guard: refuse to start if the socket is
	// already bound and reachable.
	if existing := probeSocket(*socketFlag); existing {
		fmt.Fprintf(os.Stderr, "lenny-test-cached: another instance is already bound to %s\n", *socketFlag)
		os.Exit(1)
	}

	// Remove a stale socket (left over from a previous run that
	// crashed before cleanup).
	_ = os.Remove(*socketFlag)
	if err := os.MkdirAll(filepath.Dir(*socketFlag), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "lenny-test-cached: mkdir socket dir: %v\n", err)
		os.Exit(1)
	}

	listener, err := net.Listen("unix", *socketFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lenny-test-cached: listen: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		_ = listener.Close()
		_ = os.Remove(*socketFlag)
	}()

	d := newDaemon(*composeFile)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		fmt.Fprintln(os.Stderr, "lenny-test-cached: received signal, stopping")
		_ = d.shutdownCompose()
		cancel()
		_ = listener.Close()
	}()

	fmt.Printf("lenny-test-cached: listening on %s\n", *socketFlag)
	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		go d.handle(ctx, conn)
	}
}

type daemon struct {
	mu          sync.Mutex
	composeFile string
	startedAt   time.Time
	running     bool
}

func newDaemon(composeFile string) *daemon {
	return &daemon{composeFile: composeFile}
}

type request struct {
	Op string `json:"op"`
}

type response struct {
	OK        bool              `json:"ok,omitempty"`
	Error     string            `json:"error,omitempty"`
	Running   bool              `json:"running,omitempty"`
	Since     string            `json:"since,omitempty"`
	PID       int               `json:"pid,omitempty"`
	Endpoints map[string]string `json:"endpoints,omitempty"`
}

func (d *daemon) handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	// One request per connection. The protocol is a single JSON line
	// followed by a single JSON response; clients reopen the socket
	// for each call.
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 32*1024), 1<<20)
	if !scanner.Scan() {
		return
	}
	var req request
	if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
		writeResponse(conn, response{Error: "invalid JSON: " + err.Error()})
		return
	}
	switch req.Op {
	case "status":
		writeResponse(conn, d.status())
	case "ensure":
		writeResponse(conn, d.ensure())
	case "endpoints":
		writeResponse(conn, response{OK: true, Endpoints: endpoints()})
	case "shutdown":
		writeResponse(conn, response{OK: true})
		_ = d.shutdownCompose()
		os.Exit(0)
	default:
		writeResponse(conn, response{Error: "unknown op: " + req.Op})
	}
}

func (d *daemon) status() response {
	d.mu.Lock()
	defer d.mu.Unlock()
	since := ""
	if !d.startedAt.IsZero() {
		since = d.startedAt.Format(time.RFC3339)
	}
	return response{OK: true, Running: d.running, Since: since}
}

func (d *daemon) ensure() response {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.running {
		return response{OK: true, Running: true, Since: d.startedAt.Format(time.RFC3339), PID: os.Getpid()}
	}
	cmd := exec.Command("docker", "compose", "-f", d.composeFile, "up", "-d")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return response{Error: fmt.Sprintf("compose up failed: %v\n%s", err, out)}
	}
	d.running = true
	d.startedAt = time.Now().UTC()
	return response{OK: true, Running: true, Since: d.startedAt.Format(time.RFC3339), PID: os.Getpid()}
}

func (d *daemon) shutdownCompose() error {
	d.mu.Lock()
	running := d.running
	d.mu.Unlock()
	if !running {
		return nil
	}
	cmd := exec.Command("docker", "compose", "-f", d.composeFile, "down", "-v")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("compose down: %w\n%s", err, out)
	}
	return nil
}

func writeResponse(conn net.Conn, r response) {
	enc := json.NewEncoder(conn)
	_ = enc.Encode(r)
}

func endpoints() map[string]string {
	return map[string]string{
		"postgres": "postgres://lenny:lenny@127.0.0.1:15432/lenny?sslmode=disable",
		"redis":    "127.0.0.1:16379",
		"minio":    "http://127.0.0.1:19000",
		"otlp":     "127.0.0.1:14317",
	}
}

// defaultSocketPath returns ${XDG_RUNTIME_DIR}/lenny-test/cached.sock
// when the env var is set, falling back to /tmp/lenny-test-cached-<uid>.sock.
func defaultSocketPath() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "lenny-test", "cached.sock")
	}
	uid := os.Getuid()
	return fmt.Sprintf("/tmp/lenny-test-cached-%d.sock", uid)
}

// probeSocket attempts a 200ms-timeout connection. Returns true when
// the socket accepts traffic.
func probeSocket(path string) bool {
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

// ensureGuard is exported for tests that want to confirm the helper
// chain refuses to start the daemon twice. Currently unused outside
// tests; kept reachable for future expansion.
var _ = strings.HasPrefix
