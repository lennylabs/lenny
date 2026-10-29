// SPDX-License-Identifier: MIT

// Command reinstall-sync is the Kind install script's entry point to
// package reinstall. It has two subcommands:
//
//	reinstall-sync bootstrap-job --name <job>
//
// reads `helm get hooks` output on standard input and prints the JSON
// manifest of a one-off copy of the bootstrap Job that runs
// `lenny-ctl bootstrap --force-update`.
//
//	reinstall-sync stale-pods [--warming] --pods <file> --runtimes <file> --sandboxes <file>
//
// reads `kubectl get -o json` lists of the agent pods, the Runtime CRs,
// and the Sandboxes, and prints one line per idle pod (per warming pod
// with --warming) whose runtime image differs from its Runtime CR:
//
//	<pod> TAB <resourceVersion> TAB <runtime> TAB <pod image> TAB <CR image>
//
// It is test infrastructure: no product path runs it.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	corev1 "k8s.io/api/core/v1"

	"github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/tests/testinfra/kind/reinstall"
)

func main() {
	if len(os.Args) < 2 {
		fail(fmt.Errorf("usage: reinstall-sync bootstrap-job|stale-pods [flags]"))
	}
	var err error
	switch os.Args[1] {
	case "bootstrap-job":
		err = bootstrapJob(os.Args[2:])
	case "stale-pods":
		err = stalePods(os.Args[2:])
	default:
		err = fmt.Errorf("unknown subcommand %q", os.Args[1])
	}
	if err != nil {
		fail(err)
	}
}

func bootstrapJob(args []string) error {
	fs := flag.NewFlagSet("bootstrap-job", flag.ExitOnError)
	name := fs.String("name", "", "name of the one-off force-update Job")
	if err := fs.Parse(args); err != nil {
		return err
	}
	b, err := reinstall.ForceUpdateBootstrapJob(os.Stdin, *name)
	if err != nil {
		return err
	}
	_, err = fmt.Println(string(b))
	return err
}

func stalePods(args []string) error {
	fs := flag.NewFlagSet("stale-pods", flag.ExitOnError)
	podsFile := fs.String("pods", "", "kubectl get pods -o json output")
	runtimesFile := fs.String("runtimes", "", "kubectl get runtimes -o json output")
	sandboxesFile := fs.String("sandboxes", "", "kubectl get sandboxes -o json output")
	warming := fs.Bool("warming", false, "print the warming pods on an old image instead of the idle ones")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var pods corev1.PodList
	var runtimes v1alpha1.RuntimeList
	var sandboxes v1alpha1.SandboxList
	for _, in := range []struct {
		path string
		into any
	}{{*podsFile, &pods}, {*runtimesFile, &runtimes}, {*sandboxesFile, &sandboxes}} {
		if err := readJSON(in.path, in.into); err != nil {
			return err
		}
	}
	selectPods := reinstall.StaleIdlePods
	if *warming {
		selectPods = reinstall.StaleWarmingPods
	}
	for _, p := range selectPods(&pods, &runtimes, &sandboxes) {
		if _, err := fmt.Printf("%s\t%s\t%s\t%s\t%s\n", p.Name, p.ResourceVersion, p.Runtime, p.Image, p.Want); err != nil {
			return err
		}
	}
	return nil
}

func readJSON(path string, into any) error {
	if path == "" {
		return fmt.Errorf("stale-pods needs --pods, --runtimes, and --sandboxes")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(b, into); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "reinstall-sync:", err)
	os.Exit(1)
}
