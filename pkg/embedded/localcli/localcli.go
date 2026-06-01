// SPDX-License-Identifier: MIT

// Package localcli implements the §24.19 Embedded Mode local commands
// (up, down, status, logs, restart, token, image, session). The logic
// lives in this importable package so the two binaries the §24 preamble
// describes as "one binary, two names" can share it: the short-name
// lenny binary (cmd/lenny) dispatches every command here, and the
// operator CLI lenny-ctl (cmd/lenny-ctl) delegates the same local
// commands so `lenny-ctl <command>` behaves identically to `lenny
// <command>` per §24.19 line 266 and §24.9 line 120.
package localcli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/lennylabs/lenny/pkg/embedded/stack"
)

// Exit codes shared across the Embedded Mode local commands. The
// §24.19.1 and §24.9 tables make these values normative so scripted
// callers can distinguish failure modes by exit code.
const (
	// exitInvalidImageRef is §24.19.1 line 282 INVALID_IMAGE_REFERENCE.
	exitInvalidImageRef = 2
	// exitEmbeddedModeRequired is the §24.9 line 120 / §24.19.1 line 282
	// EMBEDDED_MODE_REQUIRED code: a local command that requires a
	// running Embedded Mode stack was invoked without one.
	exitEmbeddedModeRequired = 3
	// exitK3sUnavailable is §24.19.1 line 282 K3S_UNAVAILABLE.
	exitK3sUnavailable = 4
)

// Supervising reports whether this process was launched as the detached
// Embedded Mode stack supervisor. `lenny up` re-executes os.Executable()
// with the LENNY_EMBEDDED_SUPERVISE gate set; because os.Executable()
// resolves to whichever name (lenny or lenny-ctl) ran `up`, both entry
// points consult this before normal command dispatch.
func Supervising() bool {
	return os.Getenv(stack.SuperviseEnvVar) == "1"
}

// RunSupervise runs the detached supervisor process that hosts the
// in-process Embedded Mode components and supervises the gateway and
// controller children. The precondition is Supervising().
func RunSupervise(args []string, stdout, stderr io.Writer) int {
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

// Local reports whether name is one of the §24.19 / §24.9 Embedded Mode
// local commands. lenny-ctl uses it to decide whether to delegate an
// invocation to the embedded stack (§24.19 line 266).
func Local(name string) bool {
	switch name {
	case "up", "down", "status", "logs", "restart", "token", "image", "session":
		return true
	default:
		return false
	}
}

// Run dispatches a single Embedded Mode local command and returns its
// process exit code. The precondition is Local(name). It is the entry
// point both lenny and lenny-ctl use for the §24.19 local-command
// surface.
func Run(ctx context.Context, name string, args []string, stdout, stderr io.Writer) int {
	switch name {
	case "up":
		return cmdUp(ctx, args, stdout, stderr)
	case "down":
		return cmdDown(ctx, args, stdout, stderr)
	case "status":
		return cmdStatus(ctx, args, stdout, stderr)
	case "logs":
		return cmdLogs(ctx, args, stdout, stderr)
	case "restart":
		return cmdRestart(ctx, args, stdout, stderr)
	case "token":
		return cmdToken(args, stdout, stderr)
	case "image":
		return cmdImage(args, stdout, stderr)
	case "session":
		return cmdSession(args, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "lenny: %q is not an Embedded Mode command\n", name)
		return 2
	}
}

// Main is the entry point for the short-name lenny binary. version is
// the build version cmd/lenny stamps via the release ldflag. It handles
// the supervisor gate, version, and help, then dispatches local
// commands through Run.
func Main(args []string, stdout, stderr io.Writer, version string) int {
	// The hidden __supervise re-exec is gated on the environment
	// variable before argument parsing so it is never reachable as a
	// user command.
	if Supervising() {
		return RunSupervise(args, stdout, stderr)
	}
	if len(args) == 0 {
		fmt.Fprintln(stderr, Usage)
		return 2
	}
	ctx := context.Background()
	switch args[0] {
	case "version", "--version", "-v":
		// Local CLI build version. Offline by design so it works before
		// `lenny up` brings up the embedded stack.
		// spec: §24.0 line 23, §17.6 line 360.
		fmt.Fprintf(stdout, "lenny %s\n", version)
		return 0
	case "__supervise":
		// Reachable only via the env-gated path above; a direct
		// invocation is rejected.
		fmt.Fprintln(stderr, "lenny: __supervise is an internal subcommand")
		return 2
	case "help", "-h", "--help":
		fmt.Fprintln(stdout, Usage)
		return 0
	default:
		if Local(args[0]) {
			return Run(ctx, args[0], args[1:], stdout, stderr)
		}
		fmt.Fprintf(stderr, "lenny: unknown command %q\n\n%s\n", args[0], Usage)
		return 2
	}
}

// Usage is the §17.4 / §24.19 command summary the lenny binary prints.
const Usage = `lenny — Embedded Mode: a complete Lenny stack on localhost

Usage:
  lenny <command> [flags]

Embedded Mode commands (§17.4, §24.19):
  up                       Start the embedded stack (idempotent)
  down [--purge]           Stop the stack; --purge removes ~/.lenny
  status [--json]          Print component health and active session count
  logs [<component>] [--follow]
                           Tail merged logs, or one of: gateway, controller,
                           ops, postgres, redis, kms, oidc, k3s, supervisor,
                           or runtime-<name>; --follow streams new lines
  restart <component>      Restart one component (gateway or controller)
                           without tearing down the rest of the stack
  token print [--ttl <d>]  Print a bearer token for the built-in user
  image <import|list|rm>   Manage images in the embedded containerd store
  session new --runtime <name>
                           Start a session against the running gateway
                           and print the new session id
  version                  Print the local CLI build version (offline)

Flags:
  up    --http-port <n>    Gateway plaintext port (default 8080)
        --https-port <n>   Gateway TLS port (default 8443)

State lives under ~/.lenny/ (override with LENNY_HOME). The embedded
stack is for local development only; lenny up prints a non-suppressible
production-warning banner.`

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

// cmdLogs implements `lenny logs [<component>] [--follow]`.
// spec: §17.4 line 179, §24.19 line 263.
func cmdLogs(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	component := ""
	follow := false
	for _, a := range args {
		switch a {
		case "--follow", "-f":
			follow = true
		default:
			if a != "" && a[0] != '-' && component == "" {
				component = a
			}
		}
	}
	err := stack.RunLogs(ctx, stack.LogsOptions{Component: component, Follow: follow, Out: stdout})
	if err != nil {
		fmt.Fprintf(stderr, "lenny logs: %v\n", err)
		return 1
	}
	return 0
}

// parsePortFlags extracts --http-port and --https-port from args. An
// unset or unparseable flag yields zero, which the stack resolves to
// the §17.4 default.
func parsePortFlags(args []string) (httpPort, httpsPort int) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--http-port":
			if i+1 < len(args) {
				if n, err := strconv.Atoi(args[i+1]); err == nil {
					httpPort = n
				}
				i++
			}
		case "--https-port":
			if i+1 < len(args) {
				if n, err := strconv.Atoi(args[i+1]); err == nil {
					httpsPort = n
				}
				i++
			}
		}
	}
	return httpPort, httpsPort
}
