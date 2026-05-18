// SPDX-License-Identifier: MIT

package runbooks_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/runbooks"
)

const stepBody = "### Step 1: Check pool status\n" +
	"\n" +
	"<!-- access: lenny-ctl -->\n" +
	"```bash\n" +
	"lenny-ctl diagnose pool <pool-name>\n" +
	"```\n" +
	"\n" +
	"<!-- access: api method=GET path=/v1/admin/diagnostics/pools/{name} -->\n" +
	"```\n" +
	"GET /v1/admin/diagnostics/pools/<pool-name>\n" +
	"```\n" +
	"\n" +
	"<!-- access: kubectl requires=cluster-access -->\n" +
	"```bash\n" +
	"kubectl get sandboxes -n lenny-agents -l pool=<pool-name>\n" +
	"kubectl describe sandbox <pod-name> -n lenny-agents\n" +
	"```\n" +
	"\n" +
	"### Decision\n" +
	"\n" +
	"If the bottleneck is quota exhaustion, escalate to a cluster admin.\n" +
	"\n" +
	"### Step 2: Scale the pool\n" +
	"\n" +
	"<!-- access: api method=PUT path=/v1/admin/pools/{name}/warm-count -->\n" +
	"```\n" +
	"PUT /v1/admin/pools/<pool-name>/warm-count\n" +
	"```\n"

func TestParseSteps(t *testing.T) {
	steps := runbooks.ParseSteps([]byte(stepBody))

	// The "Decision" heading carries no access path and is skipped.
	if len(steps) != 2 {
		t.Fatalf("got %d steps, want 2 (Decision prose is skipped)", len(steps))
	}

	s1 := steps[0]
	if s1.ID != "step-1" || s1.Title != "Step 1: Check pool status" {
		t.Errorf("step 1 = %q/%q", s1.ID, s1.Title)
	}
	if len(s1.Paths) != 3 {
		t.Fatalf("step 1 has %d paths, want 3", len(s1.Paths))
	}
	if s1.Paths[0].Access != "lenny-ctl" || len(s1.Paths[0].Commands) != 1 {
		t.Errorf("path 0 = %+v, want one lenny-ctl command", s1.Paths[0])
	}
	api := s1.Paths[1]
	if api.Access != "api" || api.Method != "GET" || api.Path != "/v1/admin/diagnostics/pools/{name}" {
		t.Errorf("api path = %+v, want GET /v1/admin/diagnostics/pools/{name}", api)
	}
	kube := s1.Paths[2]
	if kube.Access != "kubectl" || kube.Requires != "cluster-access" || len(kube.Commands) != 2 {
		t.Errorf("kubectl path = %+v, want requires cluster-access and 2 commands", kube)
	}

	if steps[1].ID != "step-2" || len(steps[1].Paths) != 1 {
		t.Errorf("step 2 = %q with %d paths, want step-2 with 1", steps[1].ID, len(steps[1].Paths))
	}
}

func TestParseStepsEmptyWithoutAccessMarkers(t *testing.T) {
	body := "### Overview\n\nThis runbook is all prose, no structured steps.\n"
	if steps := runbooks.ParseSteps([]byte(body)); len(steps) != 0 {
		t.Errorf("got %d steps, want 0 for a prose-only runbook", len(steps))
	}
}
