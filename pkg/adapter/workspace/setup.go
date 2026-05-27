// SPDX-License-Identifier: MIT

package workspace

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// ErrSetupAggregateTimeoutWarn is the §5.1 setupPolicy.onTimeout = warn
// signal: it is returned by RunSetup when the aggregate cap fires under
// the warn disposition. Callers MUST treat it as a non-fatal warning
// (the spec phrasing is "Gateway logs a warning, proceeds to runtime
// start") — the recommended pattern is `errors.Is(err, ErrSetupAggregate
// TimeoutWarn)` followed by a structured log line and a metric / audit
// emission, then returning nil to the RPC. Returning it (rather than
// silently swallowing the warn case) is what gives the §5.1 disposition
// an operationally observable surface. spec: §5.1 lines 89-91 — F-7.5.13.
var ErrSetupAggregateTimeoutWarn = errors.New("setup phase exceeded the aggregate cap (warn disposition)")

// SetupOptions carries the §5.1 runtime setupPolicy that bounds the
// whole setup phase. The zero value applies no aggregate cap, so only
// the per-command timeouts constrain execution.
type SetupOptions struct {
	// AggregateTimeout is the §5.1 setupPolicy.timeoutSeconds cap on the
	// entire setup phase. Zero means the runtime declares no aggregate
	// cap.
	AggregateTimeout time.Duration

	// FailOnAggregateTimeout selects the §5.1 setupPolicy.onTimeout
	// disposition: true is `fail` (abort pod startup), false is `warn`
	// (proceed to runtime start despite the unfinished setup phase). When
	// false RunSetup returns ErrSetupAggregateTimeoutWarn (non-fatal) on
	// cap-exceed so the caller can emit the §5.1 warn observability
	// before unwrapping to RPC success. spec: §5.1 lines 89-91 — F-7.5.13.
	FailOnAggregateTimeout bool

	// Env, when non-nil, replaces the inherited process environment for
	// each setup command. Callers wire the §7.5 line 479 minimal whitelist
	// via DefaultSetupEnv so a setup command does not see arbitrary
	// adapter-process state (gateway gRPC addresses, the platform MCP
	// socket path, OTLP endpoints, etc.). spec: §7.5 line 479 — F-7.5.8.
	// A nil value preserves the legacy "inherit os.Environ()" behaviour
	// for tests that have not been updated yet.
	Env []string
}

// DefaultSetupEnv returns the §7.5 line 479 minimal env whitelist a
// setup command runs with: PATH, HOME, USER, LANG, LC_ALL, TMPDIR, and
// PWD seeded to workdir. The list excludes platform-internal variables
// (gateway addresses, manifest paths, OTLP endpoints, the runtime
// nonce) that the adapter inherits at pod start so a setup command
// cannot reach them. spec: §7.5 line 479 — F-7.5.8.
func DefaultSetupEnv(workdir string) []string {
	env := []string{
		"PATH=/usr/local/sbin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=/home/agent",
		"USER=agent",
		"LANG=C.UTF-8",
		"LC_ALL=C.UTF-8",
	}
	if workdir != "" {
		env = append(env, "PWD="+workdir, "TMPDIR=/tmp")
	}
	return env
}

// RunSetup executes the WorkspacePlan setup commands in order, each one
// in workdir under a bounded timeout. Execution stops at the first
// command that exits non-zero or exceeds its timeout, and that
// command's error is returned. The §4.7 adapter calls RunSetup from
// StartSession after the workspace is materialized and before the
// runtime binary is launched.
//
// When opts.AggregateTimeout is set, the whole setup phase is bounded
// by that §5.1 cap. Exceeding it aborts with an error under the `fail`
// disposition and returns nil (proceed to runtime start) under `warn`.
//
// Setup commands are deployer-supplied and run inside the agent pod as
// the agent user; the pod sandbox is the isolation boundary. RunSetup
// bounds each command in wall-clock time so a hung command cannot pin
// a warm pod indefinitely.
func RunSetup(ctx context.Context, workdir string, cmds []*adapterv1.SetupCommand, opts SetupOptions) error {
	if opts.AggregateTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.AggregateTimeout)
		defer cancel()
	}
	for i, c := range cmds {
		// §5.1 aggregate cap tripped between commands.
		if opts.AggregateTimeout > 0 && ctx.Err() != nil {
			return aggregateTimeoutResult(opts, i)
		}
		if err := runSetupCommand(ctx, workdir, c, opts); err != nil {
			// §5.1 aggregate cap tripped during this command — the
			// derived per-command context inherits the expired deadline.
			if opts.AggregateTimeout > 0 && ctx.Err() == context.DeadlineExceeded {
				return aggregateTimeoutResult(opts, i)
			}
			return fmt.Errorf("setup command %d (%q): %w", i, c.GetCmd(), err)
		}
	}
	return nil
}

// aggregateTimeoutResult applies the §5.1 onTimeout disposition when
// the setup phase exceeds its aggregate cap before command index i.
// fail returns a fatal error; warn returns ErrSetupAggregateTimeoutWarn
// wrapped with the cap + command-index so callers can both detect the
// warn case (errors.Is) and surface the diagnostic context in their
// observability emit. spec: §5.1 lines 89-91 — F-7.5.13.
func aggregateTimeoutResult(opts SetupOptions, i int) error {
	if opts.FailOnAggregateTimeout {
		return fmt.Errorf("setup phase exceeded the aggregate cap of %s at command %d",
			opts.AggregateTimeout, i)
	}
	return fmt.Errorf("setup phase exceeded the aggregate cap of %s at command %d: %w",
		opts.AggregateTimeout, i, ErrSetupAggregateTimeoutWarn)
}

func runSetupCommand(ctx context.Context, workdir string, c *adapterv1.SetupCommand, opts SetupOptions) error {
	if c.GetCmd() == "" {
		return errors.New("command is empty")
	}
	// spec: §14 line 99 — an omitted per-command timeoutSeconds carries
	// no independent time limit; the §5.1 setupPolicy.timeoutSeconds
	// aggregate cap (encoded in ctx by RunSetup) is the only bound.
	// F-7.5.6.
	cctx := ctx
	var (
		cancel  context.CancelFunc
		timeout time.Duration
	)
	if c.GetTimeoutSeconds() > 0 {
		timeout = time.Duration(c.GetTimeoutSeconds()) * time.Second
		cctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(cctx, "/bin/sh", "-c", c.GetCmd())
	cmd.Dir = workdir
	if opts.Env != nil {
		cmd.Env = opts.Env
	}
	// spec: §7.5 line 477 ("Time-bounded") — each command runs in its
	// own process group so a per-command or aggregate-cap kill reaches
	// every descendant (background jobs, nohup'd processes, deep forks)
	// rather than only the shell. F-7.5.7.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// cmd.Cancel runs when cctx fires. It signals the whole process
	// group rather than just the shell so the kill reaches descendants;
	// without this override exec.CommandContext only signals the
	// immediate process. cmd.WaitDelay bounds how long Wait waits for
	// I/O drain after the SIGKILL before returning. spec: §7.5 line 477.
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second

	out, err := cmd.CombinedOutput()

	if cctx.Err() == context.DeadlineExceeded {
		if timeout > 0 {
			return fmt.Errorf("timed out after %s", timeout)
		}
		return errors.New("timed out under the setup phase aggregate cap")
	}
	if err != nil {
		return fmt.Errorf("exited with error: %w (output: %s)", err, out)
	}
	return nil
}
