// SPDX-License-Identifier: MIT

package localcli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/lennylabs/lenny/pkg/embedded/stack"
)

// runtimeUsage is the §17.4 / §24.19 `lenny runtime` command summary.
const runtimeUsage = `usage: lenny runtime <subcommand> [flags]

Subcommands:
  lenny runtime apply --file <path>   Apply a runtime's Runtime, SandboxTemplate,
                                      and SandboxWarmPool CRD set to the embedded
                                      cluster so the gateway resolves the runtime
                                      by name and the WarmPoolController warms a pod`

// applyRuntimeSetFn is the bring-up seam cmdRuntimeApply drives. It defaults to
// the real stack.RunRuntimeApply and is a package-level var so a unit test can
// substitute it to capture the (kubeconfig, file) cmdRuntimeApply resolves and
// passes, without reaching a real cluster. spec: §17.4 (the runtime-apply verb).
var applyRuntimeSetFn = stack.RunRuntimeApply

// cmdRuntime implements `lenny runtime ...`. The §17.4 custom-runtime
// walkthrough invokes `lenny runtime apply --file runtime-crds.yaml` to
// materialize a registered runtime's Runtime, SandboxTemplate, and
// SandboxWarmPool CRD set. Under the no-Postgres development profile no
// PoolScalingController projects a poolstore row into a SandboxWarmPool CRD,
// and `lenny-ctl runtime register` writes only the registry record, so this
// verb is the documented mechanism a custom runtime materializes its pool
// through, the same direct dynamic-apply path the echo seed uses.
//
// spec: §17.4 (S5 custom-runtime walkthrough verb), §4.6.2 (direct pool
// materialization without PoolScalingController).
func cmdRuntime(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny runtime: a subcommand is required")
		fmt.Fprintln(stderr, runtimeUsage)
		return 2
	}
	switch args[0] {
	case "apply":
		return cmdRuntimeApply(ctx, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "lenny runtime: unknown subcommand %q\n", args[0])
		fmt.Fprintln(stderr, runtimeUsage)
		return 2
	}
}

// cmdRuntimeApply implements `lenny runtime apply --file <path>`. It resolves
// the running stack's embedded kubeconfig and applies the runtime CRD set the
// file describes through the stack. A missing stack exits 3
// EMBEDDED_MODE_REQUIRED (the verb operates only against a running Embedded
// Mode cluster), matching `lenny token print` and the §24.9 / §24.19.1 exit
// convention. spec: §17.4 (the verb applies the CRD set to the embedded
// cluster), §4.6.2.
func cmdRuntimeApply(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	file, err := parseRuntimeApplyFlags(args)
	if err != nil {
		fmt.Fprintf(stderr, "lenny runtime apply: %v\n", err)
		fmt.Fprintln(stderr, runtimeUsage)
		return 2
	}

	kubeconfig, err := stack.RunningKubeconfig("")
	if err != nil {
		if errors.Is(err, stack.ErrNoRunningStack) {
			fmt.Fprintln(stderr, "lenny runtime apply: no embedded stack found; run 'lenny up' first (EMBEDDED_MODE_REQUIRED)")
			return exitEmbeddedModeRequired
		}
		fmt.Fprintf(stderr, "lenny runtime apply: %v\n", err)
		return 1
	}

	if err := applyRuntimeSetFn(ctx, kubeconfig, file); err != nil {
		fmt.Fprintf(stderr, "lenny runtime apply: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "applied runtime CRD set from %s\n", file)
	return 0
}

// parseRuntimeApplyFlags extracts the required --file value from args. An
// unknown flag or a missing --file value is an error so a typo fails fast
// rather than applying nothing silently.
func parseRuntimeApplyFlags(args []string) (string, error) {
	var file string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--file", "-f":
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s requires a path argument", args[i])
			}
			file = args[i+1]
			i++
		default:
			return "", fmt.Errorf("unknown argument %q", args[i])
		}
	}
	if file == "" {
		return "", errors.New("--file <path> is required")
	}
	return file, nil
}
