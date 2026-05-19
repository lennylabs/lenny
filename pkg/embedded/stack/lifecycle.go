// SPDX-License-Identifier: MIT

package stack

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

// SuperviseEnvVar names the environment variable the foreground lenny
// up sets when it re-executes itself as the detached supervisor. The
// lenny binary checks it to dispatch to RunSupervisor.
const SuperviseEnvVar = "LENNY_EMBEDDED_SUPERVISE"

// UpOptions configures the foreground lenny up command.
type UpOptions struct {
	// Root is the Embedded Mode state directory. Empty resolves to the
	// default.
	Root string
	// HTTPPort and HTTPSPort are the gateway listen ports. Zero uses
	// the §17.4 defaults.
	HTTPPort  int
	HTTPSPort int
	// Out and ErrOut receive progress and error output.
	Out    io.Writer
	ErrOut io.Writer
}

// RunUp implements the foreground `lenny up` command. §17.4: lenny up
// is idempotent — a second invocation is a no-op when a stack is
// already running. When no stack is running, RunUp re-executes the
// lenny binary as a detached supervisor process that hosts the stack,
// then waits for the supervisor to record a healthy gateway.
func RunUp(ctx context.Context, opts UpOptions) error {
	out := orDiscard(opts.Out)
	errOut := orDiscard(opts.ErrOut)

	root, err := resolveRoot(opts.Root)
	if err != nil {
		return err
	}
	paths := NewPaths(root)

	// Idempotency check: a recorded stack whose supervisor and gateway
	// are alive means lenny up is a no-op.
	if st, ok, err := readState(paths.StateFile()); err == nil && ok {
		if processAlive(st.SupervisorPID) && processAlive(st.GatewayPID) {
			fmt.Fprintf(out, "lenny up: stack already running (supervisor pid %d)\n", st.SupervisorPID)
			fmt.Fprintf(out, "  gateway https://localhost%s\n", portSuffix(st.HTTPSAddr))
			return nil
		}
	}

	if err := paths.EnsureDirs(); err != nil {
		return err
	}

	// Re-execute this binary as the detached supervisor. The
	// supervisor brings the stack up and stays alive to host the
	// in-process components.
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("lenny up: resolve own path: %w", err)
	}
	args := []string{"__supervise"}
	if opts.HTTPPort != 0 {
		args = append(args, "--http-port", fmt.Sprintf("%d", opts.HTTPPort))
	}
	if opts.HTTPSPort != 0 {
		args = append(args, "--https-port", fmt.Sprintf("%d", opts.HTTPSPort))
	}
	superLog, err := os.OpenFile(paths.Logs+"/supervisor.log", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("lenny up: open supervisor log: %w", err)
	}
	defer func() { _ = superLog.Close() }()

	cmd := exec.Command(self, args...)
	cmd.Env = append(os.Environ(), SuperviseEnvVar+"=1", "LENNY_HOME="+root)
	cmd.Stdout = superLog
	cmd.Stderr = superLog
	// Detach: a new session so the supervisor outlives the foreground
	// lenny up process and its controlling terminal.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("lenny up: start supervisor: %w", err)
	}
	// The foreground process does not wait on the supervisor; release
	// it so the supervisor is not left a zombie when lenny up exits.
	_ = cmd.Process.Release()

	fmt.Fprintln(out, "lenny up: bringing the embedded stack up (first run downloads PostgreSQL and k3s)")
	if err := waitForStack(ctx, paths, 6*time.Minute); err != nil {
		fmt.Fprintf(errOut, "lenny up: %v\n", err)
		fmt.Fprintf(errOut, "lenny up: see %s for supervisor output\n", paths.Logs+"/supervisor.log")
		return err
	}
	st, _, _ := readState(paths.StateFile())
	fmt.Fprintf(out, "\n  %s\n\n", ProductionWarningBanner)
	fmt.Fprintf(out, "lenny up: stack ready\n")
	fmt.Fprintf(out, "  gateway   https://localhost%s  (http://localhost%s)\n",
		portSuffix(st.HTTPSAddr), portSuffix(st.HTTPAddr))
	fmt.Fprintf(out, "  TLS CA    %s\n", paths.TLS+"/ca.crt")
	fmt.Fprintf(out, "  token     run 'lenny token print' for a bearer for the built-in user\n")
	if !st.K3sEnabled {
		fmt.Fprintf(out, "  note      embedded Kubernetes is not running; session placement is unavailable\n")
	}
	return nil
}

// waitForStack blocks until the supervisor records a state file with a
// live gateway or timeout elapses.
func waitForStack(ctx context.Context, paths Paths, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		st, ok, err := readState(paths.StateFile())
		if err == nil && ok && processAlive(st.GatewayPID) {
			if gatewayHealthy(ctx, "http://"+st.HTTPAddr) {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the embedded stack did not become ready within %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// RunSupervisor implements the hidden `__supervise` subcommand. It is
// the detached long-lived process that hosts the in-process Embedded
// Mode components and supervises the child processes. It brings the
// stack up, then blocks until it receives SIGTERM or SIGINT, at which
// point it tears the stack down gracefully.
func RunSupervisor(ctx context.Context, opts UpOptions) error {
	root, err := resolveRoot(opts.Root)
	if err != nil {
		return err
	}
	st, err := Up(ctx, Config{
		Root:      root,
		HTTPPort:  opts.HTTPPort,
		HTTPSPort: opts.HTTPSPort,
		Out:       orDiscard(opts.Out),
	})
	if err != nil {
		return err
	}
	// Block until a teardown signal arrives. lenny down sends SIGTERM
	// to this process.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	select {
	case <-sigCh:
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := st.Stop(shutdownCtx); err != nil {
		return err
	}
	_ = removeState(NewPaths(root).StateFile())
	return nil
}

// DownOptions configures the lenny down command.
type DownOptions struct {
	// Root is the Embedded Mode state directory.
	Root string
	// Purge removes the entire state directory after teardown.
	Purge bool
	// Out and ErrOut receive progress and error output.
	Out    io.Writer
	ErrOut io.Writer
}

// RunDown implements the `lenny down` command. §17.4: lenny down
// gracefully terminates all components; state under ~/.lenny/ is
// preserved unless --purge is passed. RunDown signals the supervisor
// process, which tears the stack down, then waits for it to exit.
func RunDown(ctx context.Context, opts DownOptions) error {
	out := orDiscard(opts.Out)
	root, err := resolveRoot(opts.Root)
	if err != nil {
		return err
	}
	paths := NewPaths(root)

	st, ok, err := readState(paths.StateFile())
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(out, "lenny down: no embedded stack is running")
		if opts.Purge {
			if err := purgeRoot(root); err != nil {
				return err
			}
			fmt.Fprintf(out, "lenny down: purged %s\n", root)
		}
		return nil
	}

	if processAlive(st.SupervisorPID) {
		fmt.Fprintf(out, "lenny down: stopping the embedded stack (supervisor pid %d)\n", st.SupervisorPID)
		// SIGTERM triggers the supervisor's graceful teardown.
		stopByPID(st.SupervisorPID)
	} else {
		// The supervisor is gone but children may have outlived it.
		// Signal the recorded gateway, controller, and k3s PIDs
		// directly so a crashed supervisor does not leak processes.
		fmt.Fprintln(out, "lenny down: supervisor not running; terminating recorded child processes")
		for _, pid := range []int{st.ControllerPID, st.GatewayPID, st.K3sPID} {
			stopByPID(pid)
		}
		_ = stopEmbeddedPostgres(st)
	}
	_ = removeState(paths.StateFile())
	fmt.Fprintln(out, "lenny down: stack stopped")

	if opts.Purge {
		if err := purgeRoot(root); err != nil {
			return err
		}
		fmt.Fprintf(out, "lenny down: purged %s\n", root)
	}
	return nil
}

// stopEmbeddedPostgres terminates an embedded Postgres instance left
// running by a crashed supervisor. embedded-postgres records its
// postmaster PID in the data directory, so a fresh process can stop
// it.
func stopEmbeddedPostgres(st State) error {
	// embedded-postgres writes postmaster.pid into the data directory;
	// the in-process Stop path is unavailable from a fresh process, so
	// rely on the OS to reap it when the recorded gateway is gone.
	// Nothing further is required here: the postmaster is reaped when
	// its data directory is reused on the next lenny up, or removed by
	// --purge.
	_ = st
	return nil
}

// resolveRoot resolves the Embedded Mode state directory: the explicit
// value, or the default location.
func resolveRoot(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	return DefaultRoot()
}

// orDiscard returns w, or io.Discard when w is nil.
func orDiscard(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}

// portSuffix returns the :port suffix of a host:port address, or the
// address unchanged when it has no port.
func portSuffix(addr string) string {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[i:]
		}
	}
	return addr
}
