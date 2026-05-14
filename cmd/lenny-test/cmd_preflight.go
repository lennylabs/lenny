// SPDX-License-Identifier: MIT

package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
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

	script := repoRoot() + "/scripts/preflight.sh"
	if _, err := os.Stat(script); err != nil {
		fmt.Fprintf(os.Stderr, "preflight: scripts/preflight.sh not present in %s\n", script)
		return 1
	}

	env := append([]string{}, os.Environ()...)
	if *cluster != "" {
		env = append(env, "KUBECONFIG="+*cluster)
	}
	cmd := exec.Command("bash", script)
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return 1
	}
	return 0
}
