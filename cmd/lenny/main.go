// SPDX-License-Identifier: MIT

// Command lenny is the §17.4 Embedded Mode entry point: a single
// binary that brings up a complete Lenny stack on localhost with no
// pre-existing Kubernetes cluster, Postgres, Redis, or OIDC provider.
//
// Command surface (§17.4):
//
//	lenny up                Start the embedded stack. Idempotent.
//	lenny down [--purge]    Tear the stack down. --purge removes ~/.lenny.
//	lenny status            Print component health and active session count.
//	lenny logs [<comp>]     Tail merged logs, or filter to one component.
//	lenny token print       Emit a bearer token for the built-in user.
//
// The embedded stack runs the production gateway, controllers, CRDs,
// and storage interfaces. Embedded Mode signals the mode=embedded
// platform flag through the gateway's standard configuration surface;
// there are no mode-dependent code splits in platform business logic.
//
// The lenny binary is the same executable as lenny-ctl (§24) under a
// short name. When invoked as lenny it defaults to Embedded Mode and
// targets the local stack; when invoked as lenny-ctl it targets a
// remote gateway. The Embedded Mode state directory is ~/.lenny/,
// overridable with LENNY_HOME.
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/lennylabs/lenny/pkg/embedded/stack"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run dispatches one lenny invocation and returns a process exit code.
// It is split out from main so tests can drive it directly.
func run(args []string, stdout, stderr io.Writer) int {
	// The hidden __supervise subcommand is the detached long-lived
	// process lenny up re-executes itself as. It is checked before
	// argument parsing so it is never reachable as a user command.
	if os.Getenv(stack.SuperviseEnvVar) == "1" {
		return runSupervise(args, stdout, stderr)
	}
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	ctx := context.Background()
	switch args[0] {
	case "up":
		return cmdUp(ctx, args[1:], stdout, stderr)
	case "down":
		return cmdDown(ctx, args[1:], stdout, stderr)
	case "status":
		return cmdStatus(ctx, args[1:], stdout, stderr)
	case "logs":
		return cmdLogs(args[1:], stdout, stderr)
	case "token":
		return cmdToken(args[1:], stdout, stderr)
	case "__supervise":
		// Reachable only via the env-gated path above; a direct
		// invocation is rejected.
		fmt.Fprintln(stderr, "lenny: __supervise is an internal subcommand")
		return 2
	case "help", "-h", "--help":
		fmt.Fprintln(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "lenny: unknown command %q\n\n%s\n", args[0], usage)
		return 2
	}
}

const usage = `lenny — Embedded Mode: a complete Lenny stack on localhost

Usage:
  lenny <command> [flags]

Embedded Mode commands (§17.4):
  up                       Start the embedded stack (idempotent)
  down [--purge]           Stop the stack; --purge removes ~/.lenny
  status [--json]          Print component health and active session count
  logs [<component>]       Tail merged logs, or one of: gateway, controller,
                           k3s, supervisor
  token print [--ttl <d>]  Print a bearer token for the built-in user

Flags:
  up    --http-port <n>    Gateway plaintext port (default 8080)
        --https-port <n>   Gateway TLS port (default 8443)

State lives under ~/.lenny/ (override with LENNY_HOME). The embedded
stack is for local development only; lenny up prints a non-suppressible
production-warning banner.`

// runSupervise dispatches the hidden detached supervisor process.
func runSupervise(args []string, stdout, stderr io.Writer) int {
	httpPort, httpsPort := parsePortFlags(args)
	err := stack.RunSupervisor(context.Background(), stack.UpOptions{
		HTTPPort:  httpPort,
		HTTPSPort: httpsPort,
		Out:       stdout,
		ErrOut:    stderr,
	})
	if err != nil {
		fmt.Fprintf(stderr, "lenny supervisor: %v\n", err)
		return 1
	}
	return 0
}

// cmdUp implements `lenny up`.
func cmdUp(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	httpPort, httpsPort := parsePortFlags(args)
	err := stack.RunUp(ctx, stack.UpOptions{
		HTTPPort:  httpPort,
		HTTPSPort: httpsPort,
		Out:       stdout,
		ErrOut:    stderr,
	})
	if err != nil {
		fmt.Fprintf(stderr, "lenny up: %v\n", err)
		return 1
	}
	return 0
}

// cmdDown implements `lenny down`.
func cmdDown(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	purge := false
	for _, a := range args {
		if a == "--purge" {
			purge = true
		}
	}
	err := stack.RunDown(ctx, stack.DownOptions{
		Purge:  purge,
		Out:    stdout,
		ErrOut: stderr,
	})
	if err != nil {
		fmt.Fprintf(stderr, "lenny down: %v\n", err)
		return 1
	}
	return 0
}

// cmdStatus implements `lenny status`.
func cmdStatus(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	asJSON := false
	for _, a := range args {
		if a == "--json" {
			asJSON = true
		}
	}
	st, err := stack.CollectStatus(ctx, stack.StatusOptions{})
	if err != nil {
		fmt.Fprintf(stderr, "lenny status: %v\n", err)
		return 1
	}
	if asJSON {
		stack.WriteStatusJSON(stdout, st)
	} else {
		stack.WriteStatus(stdout, st)
	}
	// A recorded-but-unhealthy stack exits non-zero so scripts can
	// gate on it.
	if st.Running {
		for _, c := range st.Components {
			if c.Name == "gateway" && !c.Healthy {
				return 1
			}
		}
	}
	return 0
}

// cmdLogs implements `lenny logs`.
func cmdLogs(args []string, stdout, stderr io.Writer) int {
	component := ""
	for _, a := range args {
		if a != "" && a[0] != '-' {
			component = a
			break
		}
	}
	err := stack.RunLogs(stack.LogsOptions{Component: component, Out: stdout})
	if err != nil {
		fmt.Fprintf(stderr, "lenny logs: %v\n", err)
		return 1
	}
	return 0
}
