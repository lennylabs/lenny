// SPDX-License-Identifier: MIT

package main

import (
	"flag"
	"os"
	"strings"
	"testing"
)

// TestRedisClusterAddrsHelpDescribesTheDeploymentCluster_spec_12_4 pins
// the --redis-cluster-addrs help text to the §12.4 topology: a Redis
// Cluster is the one Redis deployment serving every concern at Tier 3,
// reached by the whole-deployment cutover of the migration pre-plan. The
// text must not describe the retired per-instance migration of the Quota
// / Rate Limiting instance. It runs on a fresh flag set so it neither
// redefines a flag on the global set nor triggers a second flag.Parse.
//
// spec: 12.4 (Redis HA and Failure Modes: "Shared Redis topology." and
// "Redis Cluster migration pre-plan (Tier 2→3 transition)")
func TestRedisClusterAddrsHelpDescribesTheDeploymentCluster_spec_12_4(t *testing.T) {
	savedCmdLine := flag.CommandLine
	savedArgs := os.Args
	t.Cleanup(func() {
		flag.CommandLine = savedCmdLine
		os.Args = savedArgs
	})
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	os.Args = []string{savedArgs[0]}

	f := &gatewayFlags{}
	f.registerCoreFlags()
	fl := flag.CommandLine.Lookup("redis-cluster-addrs")
	if fl == nil {
		t.Fatal("registerCoreFlags did not bind --redis-cluster-addrs")
	}
	help := fl.Usage
	for _, retired := range []string{"Quota/Rate Limiting instance", "Quota / Rate Limiting instance", "migration topology"} {
		if strings.Contains(help, retired) {
			t.Errorf("--redis-cluster-addrs help names the retired per-instance migration (%q): %s", retired, help)
		}
	}
	for _, want := range []string{"every concern", "Tier 3", "whole-deployment cutover"} {
		if !strings.Contains(help, want) {
			t.Errorf("--redis-cluster-addrs help does not state %q: %s", want, help)
		}
	}
}
