// SPDX-License-Identifier: MIT

package tier0_static

import (
	"context"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/helm"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// This file ties the chart's rendered control-plane controller argv to the
// flags each controller binary declares. The deployment-boundary gate
// (deployment_boundary_test.go) covers the agent-pod binaries the controller's
// pod builder renders. The controller Deployments are rendered by the chart
// instead, so a flag the chart renders and the binary does not define (a
// rename that changes only one side) makes the container exit with status 2
// from the flag package at startup, and a flag the binary gates a subsystem
// on that the chart never renders leaves that subsystem off in every install.
// The second class is how the §4.6.1 orphan-claim GC stayed unregistered
// (F-4.6.30).

// chartControllerBinary is one chart-rendered Deployment container and the
// cmd/ binary its image runs.
type chartControllerBinary struct {
	Deployment, Container, Dir string
}

// chartControllerBinaries are the control-plane controller Deployments whose
// argv this file checks.
var chartControllerBinaries = []chartControllerBinary{
	{Deployment: "lenny-controller", Container: "controller", Dir: "cmd/lenny-controller"},
	{Deployment: "lenny-pool-scaling-controller", Container: "pool-scaling-controller", Dir: "cmd/lenny-pool-scaling-controller"},
}

// chartFlagRenders are the value sets the check renders. "maximal" turns on
// every conditional controller argument the chart template carries, so a
// rename in a rarely-set branch is checked too. postgres.dsn is set in every
// case because the pool-scaling-controller Deployment renders only with it.
// The "postgres" case otherwise keeps the chart's default controller values.
var chartFlagRenders = []struct {
	Name string
	Set  []string
}{
	{Name: "postgres", Set: []string{
		"postgres.dsn=postgres://lenny:lenny@db.acme.svc:5432/lenny",
		// The chart refuses to render agent namespaces without the dedicated
		// CoreDNS ClusterIP (§13.2 K8S-033).
		"coredns.clusterIP=10.96.0.53",
	}},
	{Name: "maximal", Set: []string{
		"postgres.dsn=postgres://lenny:lenny@db.acme.svc:5432/lenny",
		"controller.adapterImage=lenny-adapter:acme",
		"controller.egressCaptureImage=lenny-egress-capture:acme",
		"global.devMode=true",
		"global.saTokenAudience=lenny-gateway-acme",
		"coredns.deploy=true",
		"coredns.clusterIP=10.96.0.53",
		"isolation.runtimeClassNames.standard=runc-acme",
		"isolation.runtimeClassNames.sandboxed=runsc",
		"isolation.runtimeClassNames.microvm=kata-qemu",
		"controller.resourceClasses.small.requests.cpu=50m",
		"controller.resourceClasses.small.requests.memory=128Mi",
		"controller.resourceClasses.small.limits.cpu=500m",
		"controller.resourceClasses.small.limits.memory=640Mi",
		"controller.tuning.podCreateQPS=20",
		"controller.tuning.podCreateBurst=40",
		"controller.tuning.statusUpdateQPS=30",
		"controller.tuning.statusUpdateBurst=60",
		"controller.tuning.maxConcurrentReconciles=4",
		"controller.tuning.workqueueMaxDepth=2000",
		"controller.tuning.statusUpdateDedupWindow=1s",
		"controller.tuning.initialFillGracePeriod=3m",
	}},
}

var (
	controllerFlagsOnce    sync.Once
	controllerFlagsRes     map[string]binaryInputs
	controllerFlagsDefects []string
	controllerFlagsErr     error
)

// thirdPartyFlagBinding reports whether a scan defect names a line that binds
// controller-runtime's zap logger flags onto the command line. Those flags are
// declared by a third-party package the scan does not model; the chart renders
// none of them, and a rendered --zap-* argument would still fail the argv
// parse as undefined, so admitting the binding loosens nothing.
func thirdPartyFlagBinding(root, defect string) bool {
	file, rest, ok := strings.Cut(defect, ":")
	if !ok {
		return false
	}
	lineText, _, ok := strings.Cut(rest, ":")
	if !ok {
		return false
	}
	line, err := strconv.Atoi(lineText)
	if err != nil {
		return false
	}
	body, err := os.ReadFile(filepath.Join(root, file))
	if err != nil {
		return false
	}
	lines := strings.Split(string(body), "\n")
	if line < 1 || line > len(lines) {
		return false
	}
	src := strings.TrimSpace(lines[line-1])
	return src == "zapOpts.BindFlags(flag.CommandLine)" ||
		src == "f.zapOpts.BindFlags(flag.CommandLine)"
}

// controllerBinaryInputs scans each controller binary's in-module closure
// once and returns the flags and environment reads it declares, keyed by
// cmd/ directory. It reuses the deployment-boundary scanner, so a declaration
// the scan cannot resolve fails the caller rather than being skipped.
func controllerBinaryInputs(t *testing.T) map[string]binaryInputs {
	t.Helper()
	controllerFlagsOnce.Do(func() {
		controllerFlagsRes, controllerFlagsDefects, controllerFlagsErr = scanControllerBinaries(context.Background())
	})
	if controllerFlagsErr != nil {
		t.Fatalf("scan controller binaries: %v", controllerFlagsErr)
	}
	for _, d := range controllerFlagsDefects {
		t.Errorf("the flag scan cannot resolve a declaration: %s", d)
	}
	return controllerFlagsRes
}

// scanControllerBinaries performs the scan controllerBinaryInputs caches.
func scanControllerBinaries(ctx context.Context) (map[string]binaryInputs, []string, error) {
	root, err := schematest.RepoRootCwd()
	if err != nil {
		return nil, nil, err
	}
	fset := token.NewFileSet()
	res := map[string]binaryInputs{}
	var defects []string
	for _, b := range chartControllerBinaries {
		srcs, err := listClosure(ctx, root, b.Dir)
		if err != nil {
			return nil, nil, err
		}
		files, err := parseSourceFiles(fset, srcs, nil)
		if err != nil {
			return nil, nil, err
		}
		in, scanDefects := scanInputs(fset, files, collectStringConsts(files))
		for _, d := range scanDefects {
			if !thirdPartyFlagBinding(root, d) {
				defects = append(defects, d)
			}
		}
		res[b.Dir] = in
	}
	return res, defects, nil
}

// containerSpec returns the named container of a rendered Deployment.
func containerSpec(t *testing.T, m helm.Manifests, deployment, container string) map[string]any {
	t.Helper()
	dep := m.MustFind(t, "Deployment", deployment)
	for _, c := range podContainers(dep) {
		cm, _ := c.(map[string]any)
		if name, _ := cm["name"].(string); name == container {
			return cm
		}
	}
	t.Fatalf("Deployment %s has no %q container", deployment, container)
	return nil
}

// namedContainerArgv returns a container's argv strings.
func namedContainerArgv(cm map[string]any) []string {
	raw, _ := cm["args"].([]any)
	out := make([]string, 0, len(raw))
	for _, a := range raw {
		if s, ok := a.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// containerEnvNames returns the names of a container's env entries.
func containerEnvNames(cm map[string]any) map[string]bool {
	raw, _ := cm["env"].([]any)
	out := map[string]bool{}
	for _, e := range raw {
		em, _ := e.(map[string]any)
		if name, _ := em["name"].(string); name != "" {
			out[name] = true
		}
	}
	return out
}

func renderForFlags(t *testing.T, set []string) helm.Manifests {
	t.Helper()
	helm.SkipUnlessAvailable(t)
	return helm.Render(t, helm.Options{
		Chart:     "../../charts/lenny",
		Release:   "lenny",
		Namespace: "lenny-system",
		Set:       set,
	})
}

// spec: 4.6.1 (Warm Pool Controller (Pod Lifecycle)), 4.6.2
// (PoolScalingController (Pool Configuration))
//
// TestChartControllerArgsParseAgainstTheirBinaries renders the chart with the
// default and the maximal controller values and parses each controller
// Deployment's argv against the flags its binary declares, with the declared
// types. A rendered flag the binary does not define, a value the declared type
// rejects, a flag rendered twice (the flag package keeps only the last value),
// or a positional argument fails the test. A failure means the chart template
// and the binary's flag set have drifted: the container would exit at startup.
func TestChartControllerArgsParseAgainstTheirBinaries(t *testing.T) {
	inputs := controllerBinaryInputs(t)
	for _, r := range chartFlagRenders {
		t.Run(r.Name, func(t *testing.T) {
			m := renderForFlags(t, r.Set)
			for _, b := range chartControllerBinaries {
				args := namedContainerArgv(containerSpec(t, m, b.Deployment, b.Container))
				if len(args) == 0 {
					t.Errorf("%s: rendered no args", b.Deployment)
					continue
				}
				_, defects := parseArgv(inputs[b.Dir].Flags, argvUse{
					Case: r.Name, Site: b.Deployment + "/" + b.Container, Binary: b.Dir, Args: args,
				})
				for _, d := range defects {
					t.Error(d)
				}
			}
		})
	}
}

// spec: 4.6.1 (Warm Pool Controller (Pod Lifecycle), "Orphaned SandboxClaim
// detection")
//
// TestChartRendersTheOrphanClaimGCInputs pins the inputs lenny-controller
// gates the orphan-claim GarbageCollect loop and the agent_pod_state mirror
// reconciler on. The binary registers both only when --agent-namespaces names a
// namespace and the Postgres DSN is set (--postgres-dsn, defaulted from
// LENNY_POSTGRES_DSN). The check reads the flag and variable names from the
// binary's declarations, so renaming either on one side fails here. A failure
// means a chart install with Postgres runs no orphan-claim GC (F-4.6.30).
func TestChartRendersTheOrphanClaimGCInputs(t *testing.T) {
	inputs := controllerBinaryInputs(t)["cmd/lenny-controller"]
	declared := map[string]bool{}
	for _, f := range inputs.Flags {
		if f.Set == "" {
			declared[f.Name] = true
		}
	}
	for _, name := range []string{"agent-namespaces", "postgres-dsn"} {
		if !declared[name] {
			t.Fatalf("cmd/lenny-controller declares no --%s flag; update this gate and the chart together", name)
		}
	}
	dsnEnvBound := false
	for _, e := range inputs.Env {
		if e.Name == "LENNY_POSTGRES_DSN" && e.BoundTo == "--postgres-dsn" {
			dsnEnvBound = true
		}
	}
	if !dsnEnvBound {
		t.Fatal("cmd/lenny-controller no longer defaults --postgres-dsn from LENNY_POSTGRES_DSN; the chart wires the DSN through that variable")
	}

	m := renderForFlags(t, chartFlagRenders[0].Set)
	cm := containerSpec(t, m, "lenny-controller", "controller")
	var nsArgs []string
	for _, a := range namedContainerArgv(cm) {
		if strings.HasPrefix(a, "--agent-namespaces=") {
			nsArgs = append(nsArgs, a)
		}
	}
	if len(nsArgs) != 1 || nsArgs[0] != "--agent-namespaces=lenny-agents,lenny-agents-kata" {
		t.Errorf("lenny-controller --agent-namespaces args = %v, want exactly [--agent-namespaces=lenny-agents,lenny-agents-kata] from the default agentNamespaces", nsArgs)
	}
	if !containerEnvNames(cm)["LENNY_POSTGRES_DSN"] {
		t.Error("lenny-controller carries no LENNY_POSTGRES_DSN env with postgres.dsn set; the GC's active-session lookup has no Postgres")
	}
}

// spec: 4.6.1 (Warm Pool Controller (Pod Lifecycle))
//
// TestChartControllerArgsGateReportsARenamedFlag is the negative control for
// TestChartControllerArgsParseAgainstTheirBinaries: it feeds the parse the
// argv a one-sided rename produces and asserts the parse reports it. Without
// it, a parse that admitted every argument would pass the gate vacuously.
func TestChartControllerArgsGateReportsARenamedFlag(t *testing.T) {
	inputs := controllerBinaryInputs(t)["cmd/lenny-controller"]
	cases := map[string][]string{
		"undeclared flag":   {"--leader-elect", "--agent-namespace=lenny-agents"},
		"flag set twice":    {"--agent-namespaces=acme-agents", "--agent-namespaces=globex-agents"},
		"mistyped value":    {"--create-qps=fast"},
		"positional argval": {"--leader-elect", "lenny-agents"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			_, defects := parseArgv(inputs.Flags, argvUse{
				Case: "negative", Site: "lenny-controller/controller", Binary: "cmd/lenny-controller", Args: args,
			})
			if len(defects) == 0 {
				t.Errorf("parse admitted %v; the gate must report it", args)
			}
		})
	}
}
