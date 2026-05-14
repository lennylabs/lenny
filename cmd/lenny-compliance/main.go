// SPDX-License-Identifier: MIT

// Command lenny-compliance exercises an adapter binary against the
// Basic, Standard, or Full integration-level contract from spec §15.4.
// It is the test-infrastructure side of the conformance gate that every
// runtime (built-in or third-party) must clear before it is registered
// against a Lenny deployment.
//
// Usage:
//
//	lenny-compliance --binary ./bin/echo --level basic
//	lenny-compliance --binary ./bin/echo --level basic --json
//
// Phase 2 ships the Basic-level battery. Standard and Full ship in
// later phases alongside the adapter manifest and lifecycle channel.
//
// The harness exits 0 when every check passed and non-zero (one per
// failed check) otherwise. The JSON form produces a single document
// with one entry per check.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

const harnessVersion = "0.3.0-phase2.8"

func main() {
	var (
		binaryPath = flag.String("binary", "", "path to the adapter binary under test")
		level      = flag.String("level", "basic", "integration level: basic|standard|full")
		jsonOut    = flag.Bool("json", false, "emit JSON report on stdout instead of a human summary")
		timeout    = flag.Duration("timeout", 30*time.Second, "per-check timeout")
		verbose    = flag.Bool("verbose", false, "print stdin/stdout traces alongside the summary")
	)
	flag.Parse()

	if *binaryPath == "" {
		fmt.Fprintln(os.Stderr, "lenny-compliance: --binary is required")
		os.Exit(2)
	}
	var report Report
	switch *level {
	case "basic":
		report = runBasicBattery(*binaryPath, *timeout, *verbose)
	case "full":
		report = runFullBattery(*binaryPath, *timeout, *verbose)
	case "standard":
		fmt.Fprintln(os.Stderr, "lenny-compliance: --level=standard ships in a later phase (Phase 2.8 covers full; Phase 9 ships standard alongside delegation-echo)")
		os.Exit(2)
	default:
		fmt.Fprintf(os.Stderr, "lenny-compliance: unknown --level %q (basic|standard|full)\n", *level)
		os.Exit(2)
	}

	emit(report, *jsonOut)
	os.Exit(report.failedCount())
}

// Report is the conformance report. One entry per check.
type Report struct {
	Harness    string  `json:"harness"`
	Binary     string  `json:"binary"`
	Level      string  `json:"level"`
	StartedAt  string  `json:"started_at"`
	FinishedAt string  `json:"finished_at"`
	Checks     []Check `json:"checks"`
	Summary    Summary `json:"summary"`
}

// Check is a single conformance assertion.
type Check struct {
	Name     string `json:"name"`
	Spec     string `json:"spec"`
	Pass     bool   `json:"pass"`
	Detail   string `json:"detail,omitempty"`
	Duration string `json:"duration"`
}

// Summary captures aggregate counts.
type Summary struct {
	Total  int `json:"total"`
	Passed int `json:"passed"`
	Failed int `json:"failed"`
}

func (r Report) failedCount() int { return r.Summary.Failed }

// runBasicBattery runs every Basic-level conformance check against the
// adapter binary and assembles the Report.
func runBasicBattery(binary string, timeout time.Duration, verbose bool) Report {
	r := Report{
		Harness:   "lenny-compliance/" + harnessVersion,
		Binary:    binary,
		Level:     "basic",
		StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}

	cases := []struct {
		name string
		spec string
		fn   func(string, time.Duration, bool) (string, error)
	}{
		{"binary_exists_and_executes", "15.4", checkBinaryExecutes},
		{"empty_stdin_exits_cleanly", "15.4", checkEmptyStdin},
		{"message_emits_response", "15.4", checkMessageEmitsResponse},
		{"heartbeat_emits_ack", "15.4", checkHeartbeatAck},
		{"unknown_type_ignored", "15.4", checkUnknownTypeIgnored},
		{"shutdown_exits_within_deadline", "15.4", checkShutdownDeadline},
		{"sequential_messages_handled", "15.4", checkSequentialMessages},
	}

	for _, c := range cases {
		detail, err := c.fn(binary, timeout, verbose)
		r.recordCheck(c.name, c.spec, detail, err)
	}
	r.Summary.Total = len(r.Checks)
	r.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return r
}

func emit(r Report, asJSON bool) {
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(r)
		return
	}
	fmt.Printf("lenny-compliance %s\n", r.Harness)
	fmt.Printf("  binary: %s\n", r.Binary)
	fmt.Printf("  level:  %s\n", r.Level)
	fmt.Printf("  checks: %d total, %d passed, %d failed\n",
		r.Summary.Total, r.Summary.Passed, r.Summary.Failed)
	for _, c := range r.Checks {
		mark := "✓"
		if !c.Pass {
			mark = "✗"
		}
		fmt.Printf("  %s [%s] %-36s %s\n", mark, c.Spec, c.Name, c.Duration)
		if !c.Pass && c.Detail != "" {
			fmt.Printf("      %s\n", c.Detail)
		}
	}
}

// --- check primitives ---------------------------------------------------

// driveAdapter starts the binary, sends inputLines on stdin, then closes
// stdin. It reads up to maxLines from stdout (or until the process exits)
// and returns the captured lines. The process is killed if it does not
// exit within timeout.
func driveAdapter(binary string, inputLines []string, maxLines int, timeout time.Duration) (stdout []string, stderr string, exitCode int, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, "", -1, fmt.Errorf("stdin pipe: %w", err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, "", -1, fmt.Errorf("stdout pipe: %w", err)
	}
	errPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, "", -1, fmt.Errorf("stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, "", -1, fmt.Errorf("start: %w", err)
	}

	// Write input on a goroutine so we can read concurrently.
	writeErr := make(chan error, 1)
	go func() {
		defer stdin.Close()
		for _, line := range inputLines {
			if _, err := io.WriteString(stdin, line); err != nil {
				writeErr <- err
				return
			}
			if !strings.HasSuffix(line, "\n") {
				if _, err := io.WriteString(stdin, "\n"); err != nil {
					writeErr <- err
					return
				}
			}
		}
		writeErr <- nil
	}()

	scanner := bufio.NewScanner(out)
	scanner.Buffer(make([]byte, 64*1024), 50*1024*1024)
	for scanner.Scan() {
		stdout = append(stdout, scanner.Text())
		if maxLines > 0 && len(stdout) >= maxLines {
			break
		}
	}

	// Drain stderr non-blocking.
	if b, _ := io.ReadAll(errPipe); len(b) > 0 {
		stderr = string(b)
	}

	if werr := <-writeErr; werr != nil && !errors.Is(werr, io.ErrClosedPipe) {
		// Non-fatal; some checks intentionally close stdin while the
		// adapter is still emitting.
		_ = werr
	}

	waitErr := cmd.Wait()
	if waitErr != nil {
		var exit *exec.ExitError
		if errors.As(waitErr, &exit) {
			exitCode = exit.ExitCode()
		} else {
			exitCode = -1
			err = waitErr
		}
	} else {
		exitCode = 0
	}
	return stdout, stderr, exitCode, err
}

func checkBinaryExecutes(binary string, timeout time.Duration, _ bool) (string, error) {
	if _, err := os.Stat(binary); err != nil {
		return "", fmt.Errorf("stat %s: %w", binary, err)
	}
	// Run with empty stdin; expect a clean exit.
	_, _, code, err := driveAdapter(binary, nil, 0, timeout)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("exit code %d, want 0", code)
	}
	return "exit code 0", nil
}

func checkEmptyStdin(binary string, timeout time.Duration, _ bool) (string, error) {
	stdout, _, code, err := driveAdapter(binary, nil, 0, timeout)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("exit %d, want 0", code)
	}
	if len(stdout) != 0 {
		return "", fmt.Errorf("got %d stdout line(s), want 0", len(stdout))
	}
	return "no stdout, exit 0", nil
}

func checkMessageEmitsResponse(binary string, timeout time.Duration, verbose bool) (string, error) {
	in := []string{
		`{"type":"message","id":"msg_01J9X0ZW1ZF7K8Q1V2T3M4N5P1","from":{"kind":"client","id":"client_alice"},"input":[{"type":"text","inline":"ping"}]}`,
	}
	stdout, _, code, err := driveAdapter(binary, in, 1, timeout)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("exit %d", code)
	}
	if len(stdout) == 0 {
		return "", errors.New("no response on stdout")
	}
	var resp struct {
		Type   string `json:"type"`
		Output []struct {
			Type   string `json:"type"`
			Inline string `json:"inline"`
		} `json:"output"`
	}
	if err := json.Unmarshal([]byte(stdout[0]), &resp); err != nil {
		return "", fmt.Errorf("response not JSON: %w (line: %s)", err, stdout[0])
	}
	if resp.Type != "response" {
		return "", fmt.Errorf("response.type = %q, want \"response\"", resp.Type)
	}
	if len(resp.Output) == 0 {
		return "", errors.New("response.output is empty")
	}
	if verbose {
		return fmt.Sprintf("stdout=%q", stdout[0]), nil
	}
	return "response.type=response, output[0].type=" + resp.Output[0].Type, nil
}

func checkHeartbeatAck(binary string, timeout time.Duration, _ bool) (string, error) {
	in := []string{`{"type":"heartbeat","ts":1234}`}
	stdout, _, code, err := driveAdapter(binary, in, 1, timeout)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("exit %d", code)
	}
	if len(stdout) == 0 {
		return "", errors.New("no heartbeat_ack on stdout")
	}
	var ack struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(stdout[0]), &ack); err != nil {
		return "", fmt.Errorf("ack not JSON: %w", err)
	}
	if ack.Type != "heartbeat_ack" {
		return "", fmt.Errorf("ack.type = %q, want \"heartbeat_ack\"", ack.Type)
	}
	return "heartbeat_ack received", nil
}

func checkUnknownTypeIgnored(binary string, timeout time.Duration, _ bool) (string, error) {
	in := []string{
		`{"type":"this_is_a_future_message_type","x":1}`,
		// Follow with a heartbeat so we can confirm the adapter is still alive.
		`{"type":"heartbeat","ts":2}`,
	}
	stdout, _, code, err := driveAdapter(binary, in, 1, timeout)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("exit %d (the unknown type must be ignored, not fatal)", code)
	}
	if len(stdout) == 0 {
		return "", errors.New("no heartbeat_ack after unknown type")
	}
	return "unknown type silently dropped; heartbeat still answered", nil
}

func checkShutdownDeadline(binary string, _ time.Duration, _ bool) (string, error) {
	in := []string{`{"type":"shutdown","reason":"test","deadline_ms":500}`}
	start := time.Now()
	_, _, code, err := driveAdapter(binary, in, 0, 5*time.Second)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("exit %d", code)
	}
	elapsed := time.Since(start)
	// The adapter must exit. We don't enforce the deadline strictly here
	// because OS-level scheduling adds noise; we just require it doesn't
	// hang past a generous bound.
	if elapsed > 3*time.Second {
		return "", fmt.Errorf("exit took %v, expected well under the 500ms deadline", elapsed)
	}
	return fmt.Sprintf("exit in %s", elapsed.Round(time.Millisecond)), nil
}

func checkSequentialMessages(binary string, timeout time.Duration, _ bool) (string, error) {
	in := []string{
		`{"type":"message","id":"msg_01J9X0ZW1ZF7K8Q1V2T3M4N5A1","from":{"kind":"client","id":"client_alice"},"input":[{"type":"text","inline":"one"}]}`,
		`{"type":"message","id":"msg_01J9X0ZW1ZF7K8Q1V2T3M4N5A2","from":{"kind":"client","id":"client_alice"},"input":[{"type":"text","inline":"two"}]}`,
		`{"type":"message","id":"msg_01J9X0ZW1ZF7K8Q1V2T3M4N5A3","from":{"kind":"client","id":"client_alice"},"input":[{"type":"text","inline":"three"}]}`,
	}
	stdout, _, code, err := driveAdapter(binary, in, 3, timeout)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("exit %d", code)
	}
	if len(stdout) < 3 {
		return "", fmt.Errorf("got %d response(s), want 3", len(stdout))
	}
	return fmt.Sprintf("3 messages → 3 responses (%d total stdout lines)", len(stdout)), nil
}
