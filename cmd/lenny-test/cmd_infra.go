// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
)

// runInfra dispatches infrastructure-lifecycle subcommands.
//
// Phase 0 implementation: every subcommand prints a "not yet implemented"
// notice. Phase 2 wires testcontainers-based provisioning; Phase 3 wires Kind.
func runInfra(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: lenny-test infra <up|down|status|prune> [--profile <containers|compose|kind|cloud|all>]")
		return 2
	}
	switch args[0] {
	case "up":
		return infraUp(args[1:])
	case "down":
		return infraDown(args[1:])
	case "status":
		return infraStatus(args[1:])
	case "prune":
		return infraPrune(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "lenny-test: unknown infra subcommand %q. Use up|down|status|prune.\n", args[0])
		return 2
	}
}

func infraUp(args []string) int {
	profile := profileFromArgs(args, "containers")
	fmt.Printf("lenny-test infra up (profile=%s): not yet implemented in Phase 0.\n", profile)
	fmt.Println("Tier 2 component tests use testcontainers-go directly. Tier 4 integration")
	fmt.Println("tests use docker-compose under tests/testinfra/compose/. Tier 5 e2e uses")
	fmt.Println("Kind via scripts/setup-cluster.sh.")
	return 0
}

func infraDown(args []string) int {
	profile := profileFromArgs(args, "all")
	fmt.Printf("lenny-test infra down (profile=%s): not yet implemented in Phase 0.\n", profile)
	return 0
}

func infraStatus(args []string) int {
	fmt.Println("profile=containers   stopped   (provisioned per test suite via testcontainers-go)")
	fmt.Println("profile=compose      stopped   (start via docker compose --profile <name> up)")
	fmt.Println("profile=kind         stopped   (start via scripts/setup-cluster.sh)")
	return 0
}

func infraPrune(args []string) int {
	fmt.Println("lenny-test infra prune: not yet implemented in Phase 0.")
	fmt.Println("To prune locally: `docker image prune --filter \"label=org.lennylabs.test=true\" --force`.")
	return 0
}

func profileFromArgs(args []string, def string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == "--profile" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return def
}
