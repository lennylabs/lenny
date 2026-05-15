// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// runPreflightSubcommand checks a cluster's readiness to host Lenny
// per the §6 command surface:
//
//	lenny-test preflight --cluster <kubeconfig>
//
// The check verifies the cluster has the required versions, the
// required CRDs / operators, and the storage / KMS / observability
// adapters. Today it dispatches to scripts/preflight.sh; in a later
// phase it grows native checks for the deeper invariants.
func runPreflightSubcommand(args []string) int {
	fs := flag.NewFlagSet("preflight", flag.ExitOnError)
	cluster := fs.String("cluster", "", "path to a kubeconfig (default: in-cluster or $KUBECONFIG)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	script := filepath.Join(repoRoot(), "scripts", "preflight.sh")
	if _, err := os.Stat(script); err != nil {
		fmt.Fprintf(os.Stderr, "preflight: %s not present: %v\n", script, err)
		return 1
	}

	env := append([]string{}, os.Environ()...)
	if *cluster != "" {
		env = append(env, "KUBECONFIG="+*cluster)
	}
	// Preflight should be quick; a hung script means the
	// cluster's API server stopped responding. Cap at 5 minutes
	// so the gate fails clearly rather than blocking the workflow.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", script)
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		fmt.Fprintln(os.Stderr, "preflight: timed out after 5 minutes")
		return 1
	}
	if err != nil {
		return 1
	}
	return 0
}
