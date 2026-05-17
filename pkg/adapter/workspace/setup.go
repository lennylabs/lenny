// SPDX-License-Identifier: MIT

package workspace

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// SetupTimeoutDefault bounds a setup command that does not declare its
// own timeout.
const SetupTimeoutDefault = 5 * time.Minute

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
	// (proceed to runtime start despite the unfinished setup phase).
	FailOnAggregateTimeout bool
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
		if err := runSetupCommand(ctx, workdir, c); err != nil {
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
func aggregateTimeoutResult(opts SetupOptions, i int) error {
	if opts.FailOnAggregateTimeout {
		return fmt.Errorf("setup phase exceeded the aggregate cap of %s at command %d",
			opts.AggregateTimeout, i)
	}
	return nil
}

func runSetupCommand(ctx context.Context, workdir string, c *adapterv1.SetupCommand) error {
	if c.GetCmd() == "" {
		return errors.New("command is empty")
	}
	timeout := SetupTimeoutDefault
	if c.GetTimeoutSeconds() > 0 {
		timeout = time.Duration(c.GetTimeoutSeconds()) * time.Second
	}

	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, "/bin/sh", "-c", c.GetCmd())
	cmd.Dir = workdir
	out, err := cmd.CombinedOutput()

	if cctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("timed out after %s", timeout)
	}
	if err != nil {
		return fmt.Errorf("exited with error: %w (output: %s)", err, out)
	}
	return nil
}
