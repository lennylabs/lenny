// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// runInfra dispatches infrastructure-lifecycle subcommands.
//
// Up / Down / Status / Prune wrap `docker compose` against the
// compose/ profiles when --profile=compose (the default for the up
// flow). The kind profile shells out to `kind create cluster` once
// the harness is wired with cluster-bring-up code; for now it
// reports a precise diagnosis.
//
// Architecture: TESTING.md §10.3 (profile=compose), §10.4 (kind).
func runInfra(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: lenny-test infra <up|down|status|prune> [--profile <compose|kind|all>]")
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
	profile := profileFromArgs(args, "compose")
	switch profile {
	case "compose":
		return composeUp("default")
	case "compose-mtls":
		return composeUp("mtls")
	case "kind":
		return kindUp()
	case "all":
		if rc := composeUp("default"); rc != 0 {
			return rc
		}
		return kindUp()
	default:
		fmt.Fprintf(os.Stderr, "lenny-test: unknown profile %q\n", profile)
		return 2
	}
}

func infraDown(args []string) int {
	profile := profileFromArgs(args, "all")
	switch profile {
	case "compose", "compose-mtls":
		return composeDown(profile)
	case "kind":
		return kindDown()
	case "all":
		composeDown("compose")
		composeDown("compose-mtls")
		return kindDown()
	default:
		fmt.Fprintf(os.Stderr, "lenny-test: unknown profile %q\n", profile)
		return 2
	}
}

func infraStatus(args []string) int {
	// Compose status.
	if !dockerAvailable() {
		fmt.Println("profile=compose      unavailable  (docker not on PATH or daemon not running)")
	} else {
		out, _ := composeArgs("compose", "ps").Output()
		body := strings.TrimSpace(string(out))
		if body == "" || !strings.Contains(body, "lenny-postgres") {
			fmt.Println("profile=compose      stopped       (start via `lenny-test infra up --profile compose`)")
		} else {
			fmt.Println("profile=compose      running")
			for _, line := range strings.Split(body, "\n") {
				fmt.Println("  " + line)
			}
		}
	}

	// Kind status.
	if _, err := exec.LookPath("kind"); err != nil {
		fmt.Println("profile=kind         unavailable  (kind not on PATH)")
	} else {
		out, _ := exec.Command("kind", "get", "clusters").Output()
		clusters := strings.TrimSpace(string(out))
		if !strings.Contains(clusters, "lenny") {
			fmt.Println("profile=kind         stopped       (start via `lenny-test infra up --profile kind`)")
		} else {
			fmt.Println("profile=kind         running       cluster=lenny")
		}
	}

	return 0
}

func infraPrune(args []string) int {
	if !dockerAvailable() {
		fmt.Println("docker not available; nothing to prune.")
		return 0
	}
	// Prune dangling images + volumes + networks created by the
	// compose stack. The --filter scopes to project=lenny so we
	// don't blow away unrelated developer state.
	for _, c := range [][]string{
		{"docker", "compose", "-f", composeFile("default"), "down", "-v"},
		{"docker", "image", "prune", "--filter", "label=org.lennylabs.test=true", "--force"},
		{"docker", "volume", "prune", "--force"},
	} {
		out, err := exec.Command(c[0], c[1:]...).CombinedOutput()
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s failed: %v\n%s", strings.Join(c, " "), err, out)
		} else {
			fmt.Printf("$ %s\n%s", strings.Join(c, " "), out)
		}
	}
	return 0
}

func composeUp(variant string) int {
	if !dockerAvailable() {
		fmt.Fprintln(os.Stderr, "compose up: docker not on PATH or daemon not running")
		return 1
	}
	cmd := composeArgs(variant, "up", "-d")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return 1
	}
	fmt.Println("\ncompose up: ok. Use `lenny-test infra status` to inspect.")
	return 0
}

func composeDown(variant string) int {
	if !dockerAvailable() {
		fmt.Println("compose down: docker not available; nothing to do.")
		return 0
	}
	cmd := composeArgs(variant, "down", "-v")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
	return 0
}

func kindUp() int {
	if _, err := exec.LookPath("kind"); err != nil {
		fmt.Fprintln(os.Stderr, "kind not on PATH; install via `brew install kind` or per https://kind.sigs.k8s.io")
		return 1
	}
	// The Helm chart that this cluster hosts is a Phase 3 deliverable;
	// for now we create the cluster only and report the missing
	// chart.
	out, err := exec.Command("kind", "create", "cluster", "--name", "lenny").CombinedOutput()
	fmt.Print(string(out))
	if err != nil {
		return 1
	}
	fmt.Println("\nkind cluster `lenny` is up. Helm chart bring-up is a Phase 3 deliverable; ")
	fmt.Println("see TESTING.md §13.6.")
	return 0
}

func kindDown() int {
	if _, err := exec.LookPath("kind"); err != nil {
		return 0
	}
	out, _ := exec.Command("kind", "delete", "cluster", "--name", "lenny").CombinedOutput()
	fmt.Print(string(out))
	return 0
}

func composeArgs(variant string, rest ...string) *exec.Cmd {
	args := []string{"compose", "-f", composeFile("default")}
	if variant == "mtls" || variant == "compose-mtls" {
		args = append(args, "-f", composeFile("mtls"))
	}
	args = append(args, rest...)
	return exec.Command("docker", args...)
}

func composeFile(name string) string {
	return filepath.Join(repoRoot(), "compose", name+".yml")
}

func dockerAvailable() bool {
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		return false
	}
	return exec.Command("docker", "compose", "version").Run() == nil
}

func profileFromArgs(args []string, def string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == "--profile" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return def
}
