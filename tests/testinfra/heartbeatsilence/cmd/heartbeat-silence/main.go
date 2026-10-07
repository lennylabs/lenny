// SPDX-License-Identifier: MIT

// Command heartbeat-silence is the entrypoint of the Kind overlay's
// heartbeat-silence fixture images. It takes the path of a reference
// runtime binary as its one argument, opens the §4.7 runtime transport the
// way the reference runtimes do, and runs that binary behind the
// heartbeatsilence filter. It exits with the child's exit code, so the
// §15.4 exit codes the adapter reads are the child's own.
//
// It is a test fixture: no product image ships it.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/lennylabs/lenny/pkg/runtimekit"
	"github.com/lennylabs/lenny/tests/testinfra/heartbeatsilence"
)

// exitRuntimeError is the §15.4 runtime-error exit code, used when the
// fixture itself cannot start the child.
const exitRuntimeError = 1

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: heartbeat-silence <runtime-binary>")
		os.Exit(exitRuntimeError)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	transport, err := runtimekit.Open(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(exitRuntimeError)
	}
	err = heartbeatsilence.Supervise(ctx, heartbeatsilence.Child{Path: os.Args[1], Env: os.Environ()},
		transport.Reader, transport.Writer, os.Stderr)
	_ = transport.Close()
	os.Exit(exitCode(err))
}

// exitCode maps the child's exit error to the fixture's exit code.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() >= 0 {
		return exitErr.ExitCode()
	}
	fmt.Fprintln(os.Stderr, err)
	return exitRuntimeError
}
