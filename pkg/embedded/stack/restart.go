// SPDX-License-Identifier: MIT

package stack

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"syscall"
	"time"
)

// RestartableComponents lists the §24.19 components `lenny restart` can
// cycle individually. The gateway and controller are separate child
// processes the supervisor owns, so they restart without tearing the
// rest of the stack down. The in-process components (Postgres, Redis,
// the OIDC provider, the TLS proxy) and the embedded k3s node share the
// supervisor's lifecycle and are restarted with `lenny down` + `lenny
// up`.
func RestartableComponents() []string {
	return []string{"gateway", "controller"}
}

// Restartable reports whether name is a §24.19 individually-restartable
// component.
func Restartable(name string) bool {
	for _, c := range RestartableComponents() {
		if c == name {
			return true
		}
	}
	return false
}

// RestartComponent stops and re-spawns a single child component from its
// retained spec, leaving the rest of the stack running. It runs inside
// the supervisor process, which owns the child processes. It updates the
// recorded PID in the state file so `lenny status` and `lenny down`
// track the new process.
//
// spec: §24.19 line 264 — restart a single embedded component without
// tearing down the rest of the stack.
func (s *Stack) RestartComponent(ctx context.Context, name string) error {
	switch name {
	case "gateway":
		if s.gwSpec.BinPath == "" {
			return fmt.Errorf("embedded: gateway spec is unavailable; restart the full stack with 'lenny down' && 'lenny up'")
		}
		if s.gateway != nil {
			_ = s.gateway.Stop()
		}
		gw, err := startGateway(s.gwSpec)
		if err != nil {
			return fmt.Errorf("embedded: restart gateway: %w", err)
		}
		s.gateway = gw
		if err := waitGatewayHealthy(ctx, "http://"+s.gwSpec.HTTPAddr, 60*time.Second); err != nil {
			return fmt.Errorf("embedded: gateway did not become healthy after restart: %w", err)
		}
		s.state.GatewayPID = gw.PID()
		return writeState(s.paths.StateFile(), s.state)
	case "controller":
		if s.ctlSpec.BinPath == "" {
			return fmt.Errorf("embedded: the controller is not running in this stack (embedded Kubernetes is unavailable)")
		}
		if s.control != nil {
			_ = s.control.Stop()
		}
		ctl, err := startController(s.ctlSpec)
		if err != nil {
			return fmt.Errorf("embedded: restart controller: %w", err)
		}
		s.control = ctl
		s.state.ControllerPID = ctl.PID()
		return writeState(s.paths.StateFile(), s.state)
	default:
		return fmt.Errorf("embedded: component %q cannot be restarted individually; restartable components are gateway and controller", name)
	}
}

// restartResult is the supervisor's reply to a restart request,
// serialized to Paths.RestartResultFile() so the separate `lenny
// restart` process can report the outcome.
type restartResult struct {
	Component string `json:"component"`
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
}

// handleRestartRequest is the supervisor's SIGHUP handler body. It reads
// the component name the CLI wrote to the request file, restarts that
// component, writes the result file, and clears the request. A missing
// or empty request file is ignored so a spurious SIGHUP is harmless.
func (s *Stack) handleRestartRequest(ctx context.Context, paths Paths) {
	reqPath := paths.RestartRequestFile()
	b, err := os.ReadFile(reqPath)
	if err != nil {
		return
	}
	component := string(b)
	// Trim surrounding whitespace the CLI may include.
	for len(component) > 0 && (component[len(component)-1] == '\n' || component[len(component)-1] == '\r' || component[len(component)-1] == ' ') {
		component = component[:len(component)-1]
	}
	if component == "" {
		_ = os.Remove(reqPath)
		return
	}
	res := restartResult{Component: component, OK: true}
	if err := s.RestartComponent(ctx, component); err != nil {
		res.OK = false
		res.Error = err.Error()
	}
	if data, mErr := json.Marshal(res); mErr == nil {
		_ = os.WriteFile(paths.RestartResultFile(), data, 0o600)
	}
	_ = os.Remove(reqPath)
}

// RestartOptions configures the `lenny restart` command.
type RestartOptions struct {
	// Root is the Embedded Mode state directory.
	Root string
	// Component is the component to restart (see RestartableComponents).
	Component string
	// Out receives progress output.
	Out io.Writer
}

// RunRestart implements the foreground `lenny restart <component>`
// command. It validates the request, signals the running supervisor to
// restart the named component, and waits for the supervisor's result.
// It runs as a short-lived process separate from the supervisor that
// owns the child processes, so it communicates through the §24.19
// restart request/result files plus a SIGHUP.
//
// spec: §24.19 line 264.
func RunRestart(ctx context.Context, opts RestartOptions) error {
	out := orDiscard(opts.Out)
	root, err := resolveRoot(opts.Root)
	if err != nil {
		return err
	}
	if opts.Component == "" {
		return fmt.Errorf("a <component> argument is required (one of: %v)", RestartableComponents())
	}
	if !Restartable(opts.Component) {
		return fmt.Errorf("component %q cannot be restarted individually; restartable components are %v", opts.Component, RestartableComponents())
	}
	paths := NewPaths(root)
	st, ok, err := readState(paths.StateFile())
	if err != nil {
		return err
	}
	if !ok {
		return ErrNoRunningStack
	}
	if !processAlive(st.SupervisorPID) {
		return fmt.Errorf("the embedded stack supervisor (pid %d) is not running; run 'lenny up' first", st.SupervisorPID)
	}

	// Clear any stale result, write the request, then signal the
	// supervisor. The result file is the supervisor's reply.
	_ = os.Remove(paths.RestartResultFile())
	if err := os.WriteFile(paths.RestartRequestFile(), []byte(opts.Component), 0o600); err != nil {
		return fmt.Errorf("write restart request: %w", err)
	}
	fmt.Fprintf(out, "lenny restart: restarting %s (supervisor pid %d)\n", opts.Component, st.SupervisorPID)
	if err := syscall.Kill(st.SupervisorPID, syscall.SIGHUP); err != nil {
		_ = os.Remove(paths.RestartRequestFile())
		return fmt.Errorf("signal supervisor: %w", err)
	}

	res, err := waitRestartResult(ctx, paths, 90*time.Second)
	if err != nil {
		return err
	}
	if !res.OK {
		return fmt.Errorf("restart %s: %s", opts.Component, res.Error)
	}
	fmt.Fprintf(out, "lenny restart: %s restarted\n", opts.Component)
	return nil
}

// waitRestartResult polls the restart-result file the supervisor writes
// until it appears or timeout elapses. It removes the file once read.
func waitRestartResult(ctx context.Context, paths Paths, timeout time.Duration) (restartResult, error) {
	deadline := time.Now().Add(timeout)
	for {
		b, err := os.ReadFile(paths.RestartResultFile())
		if err == nil {
			_ = os.Remove(paths.RestartResultFile())
			var res restartResult
			if jErr := json.Unmarshal(b, &res); jErr != nil {
				return restartResult{}, fmt.Errorf("parse restart result: %w", jErr)
			}
			return res, nil
		}
		if time.Now().After(deadline) {
			return restartResult{}, fmt.Errorf("the supervisor did not acknowledge the restart within %s", timeout)
		}
		select {
		case <-ctx.Done():
			return restartResult{}, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}
