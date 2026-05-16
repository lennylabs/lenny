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

// RunSetup executes the WorkspacePlan setup commands in order, each one
// in workdir under a bounded timeout. Execution stops at the first
// command that exits non-zero or exceeds its timeout, and that
// command's error is returned. The §4.7 adapter calls RunSetup from
// StartSession after the workspace is materialized and before the
// runtime binary is launched.
//
// Setup commands are deployer-supplied and run inside the agent pod as
// the agent user; the pod sandbox is the isolation boundary. RunSetup
// bounds each command in wall-clock time so a hung command cannot pin
// a warm pod indefinitely.
func RunSetup(ctx context.Context, workdir string, cmds []*adapterv1.SetupCommand) error {
	for i, c := range cmds {
		if err := runSetupCommand(ctx, workdir, c); err != nil {
			return fmt.Errorf("setup command %d (%q): %w", i, c.GetCmd(), err)
		}
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
