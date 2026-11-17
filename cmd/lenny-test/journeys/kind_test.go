// SPDX-License-Identifier: MIT

package journeys

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// These tests exercise contributor tooling and carry no spec annotation.

func TestJourneyClusterConfigDropsHostPortsRenamesAndLabels(t *testing.T) {
	base, err := os.ReadFile(filepath.Join("..", "..", "..", "tests", "testinfra", "kind", "cluster.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(base), "extraPortMappings") {
		t.Fatal("the e2e configuration no longer maps host ports; this test checks nothing")
	}
	out, err := JourneyClusterConfig(base, KindClusterName, map[string]string{ownerRunLabel: "run1", ownerPIDLabel: "42"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "extraPortMappings") || strings.Contains(string(out), "hostPort") {
		t.Errorf("the journey configuration keeps host port mappings:\n%s", out)
	}
	var doc struct {
		Name  string `yaml:"name"`
		Nodes []struct {
			Labels map[string]string `yaml:"labels"`
		} `yaml:"nodes"`
	}
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Name != KindClusterName || len(doc.Nodes) < 2 {
		t.Fatalf("name %q with %d nodes", doc.Name, len(doc.Nodes))
	}
	for i, n := range doc.Nodes {
		if n.Labels[ownerRunLabel] != "run1" || n.Labels[ownerPIDLabel] != "42" {
			t.Errorf("node %d labels = %v, want the owner labels", i, n.Labels)
		}
	}
	if doc.Nodes[1].Labels["lenny.dev/pool"] == "" {
		t.Error("the e2e node labels were dropped")
	}
	if _, err := JourneyClusterConfig([]byte("kind: Cluster\n"), "x", nil); err == nil {
		t.Error("a configuration without nodes was accepted")
	}
	if _, err := JourneyClusterConfig([]byte(""), "x", nil); err == nil {
		t.Error("an empty configuration was accepted")
	}
}

// fakeCommands records the commands a KindTarget runs and answers them.
type fakeCommands struct {
	calls    []string
	envs     [][]string
	clusters string
	docker   string
	owner    string // "run pid" printed by the node-label query
	failOn   string
}

func (f *fakeCommands) run(_ context.Context, _ io.Writer, env []string, _, name string, args ...string) (string, error) {
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	f.envs = append(f.envs, env)
	if f.failOn != "" && strings.HasPrefix(call, f.failOn) {
		return "", errors.New("failed: " + call)
	}
	switch {
	case call == "kind get clusters":
		return f.clusters, nil
	case strings.HasPrefix(call, "kind delete cluster"):
		f.clusters = strings.ReplaceAll(f.clusters, KindClusterName, "")
		return "", nil
	case strings.HasPrefix(call, "kind get kubeconfig --internal"):
		return "apiVersion: v1\nclusters:\n- cluster: {server: https://lenny-journeys-control-plane:6443}\n", nil
	case strings.HasPrefix(call, "kind get kubeconfig"):
		return "apiVersion: v1\n", nil
	case strings.HasPrefix(call, "kubectl --kubeconfig") && strings.Contains(call, "get nodes"):
		return f.owner, nil
	case strings.HasPrefix(call, "docker ps"):
		return f.docker, nil
	}
	return "", nil
}

func (f *fakeCommands) ran(prefix string) bool {
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func kindTarget(t *testing.T, f *fakeCommands, alive ...int) *KindTarget {
	t.Helper()
	live := map[int]bool{}
	for _, p := range alive {
		live[p] = true
	}
	return &KindTarget{
		RepoRoot: filepath.Join("..", "..", ".."), Dir: t.TempDir(), Cluster: KindClusterName,
		RunID: "run-mine", PID: 100, Run: f.run, Alive: func(pid int) bool { return live[pid] },
	}
}

func TestKindTargetCreateDeletesADeadRunsClusterAndStopsAfterTheDataStores(t *testing.T) {
	f := &fakeCommands{clusters: "lenny-e2e\n" + KindClusterName + "\n", owner: "run-old 200"}
	k := kindTarget(t, f)
	if err := k.Create(context.Background(), SetupPrerequisites, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !f.ran("kind delete cluster --name "+KindClusterName) || !f.ran("bash tests/testinfra/kind/install.sh") {
		t.Fatalf("calls = %q", f.calls)
	}
	env := strings.Join(f.envs[len(f.envs)-1], " ")
	for _, w := range []string{
		"LENNY_KIND_CLUSTER=" + KindClusterName, "LENNY_INSTALL_STOP_AFTER=datastores", "LENNY_ALIAS_TAG=journeys",
		"LENNY_SKIP_PRUNE=1", "KUBECONFIG=" + k.Kubeconfig(), "LENNY_CLUSTER_CONFIG=",
		"LENNY_INSTALL_GEN_DIR=" + filepath.Join(k.Dir, "gen"), "LENNY_INSTALL_FACTS_FILE=" + filepath.Join(k.Dir, "install-facts"),
	} {
		if !strings.Contains(env, w) {
			t.Errorf("install env lacks %q: %s", w, env)
		}
	}
	cfg, err := os.ReadFile(filepath.Join(k.Dir, "kind-cluster.yaml"))
	if err != nil || !strings.Contains(string(cfg), "run-mine") {
		t.Errorf("the cluster configuration lacks the owner label: %v", err)
	}
}

func TestKindTargetRefusesAClusterAnotherLiveRunOwns(t *testing.T) {
	f := &fakeCommands{clusters: KindClusterName + "\n", owner: "run-other 300"}
	k := kindTarget(t, f, 300)
	err := k.Create(context.Background(), SetupLenny, io.Discard)
	if !errors.Is(err, ErrOwnedByLiveRun) {
		t.Fatalf("create over a live run's cluster: %v", err)
	}
	if err := k.Delete(context.Background(), io.Discard); !errors.Is(err, ErrOwnedByLiveRun) {
		t.Fatalf("delete a live run's cluster: %v", err)
	}
	if f.ran("kind delete") || f.ran("bash") {
		t.Fatalf("the target touched a live run's cluster: %q", f.calls)
	}
	// The run's own cluster, and one with no readable owner, are deleted.
	f.owner = "run-mine 100"
	if err := k.Delete(context.Background(), io.Discard); err != nil || !f.ran("kind delete") {
		t.Fatalf("delete the run's own cluster: %v %q", err, f.calls)
	}
	f.clusters, f.owner, f.calls = KindClusterName+"\n", "", nil
	if err := k.Delete(context.Background(), io.Discard); err != nil || !f.ran("kind delete") {
		t.Fatalf("delete an unlabelled leftover: %v %q", err, f.calls)
	}
	f.calls = nil
	if err := k.Delete(context.Background(), io.Discard); err != nil || f.ran("kind delete") {
		t.Fatalf("deleting a cluster that does not exist: %v %q", err, f.calls)
	}
}

func TestKindTargetCreatesABareClusterForTheProbe(t *testing.T) {
	f := &fakeCommands{clusters: "lenny-e2e\n"}
	k := kindTarget(t, f)
	if err := k.Create(context.Background(), SetupCluster, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !f.ran("kind create cluster --name "+KindClusterName) || f.ran("bash") {
		t.Fatalf("calls = %q", f.calls)
	}
}

func TestKindTargetCreateInstallsLennyForTheLennySetup(t *testing.T) {
	f := &fakeCommands{clusters: "lenny-e2e\n"}
	k := kindTarget(t, f)
	if err := k.Create(context.Background(), SetupLenny, io.Discard); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(f.envs[len(f.envs)-1], " "), "LENNY_INSTALL_STOP_AFTER") {
		t.Fatalf("the lenny setup stopped after the data stores: %q", f.envs)
	}
	if err := k.Create(context.Background(), "cloud", io.Discard); err == nil {
		t.Error("an unknown setup was accepted")
	}
	f.failOn = "bash"
	if err := k.Create(context.Background(), SetupLenny, io.Discard); err == nil {
		t.Error("a failed install was not reported")
	}
}

func TestKindTargetReadsFactsAndTheInternalEndpoint(t *testing.T) {
	f := &fakeCommands{}
	k := kindTarget(t, f)
	if len(k.Facts()) != 0 {
		t.Error("facts before the install")
	}
	if err := os.WriteFile(filepath.Join(k.Dir, "install-facts"), []byte("echoImage=lenny-runtime-echo:src-1@sha256:ab\n\nbad line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := k.Facts(); got["echoImage"] != "lenny-runtime-echo:src-1@sha256:ab" || len(got) != 1 {
		t.Errorf("facts = %v", got)
	}
	ep, err := k.AgentEndpoint(context.Background())
	if err != nil || ep.Network != KindNetwork || !strings.Contains(string(ep.Kubeconfig), "lenny-journeys-control-plane") {
		t.Errorf("endpoint = %+v, %v", ep, err)
	}
}

func TestKindTargetVerifyDeletedFindsLeftovers(t *testing.T) {
	f := &fakeCommands{clusters: "lenny-e2e\n"}
	k := kindTarget(t, f)
	if err := k.VerifyDeleted(context.Background()); err != nil {
		t.Fatalf("a deleted cluster: %v", err)
	}
	f.clusters = KindClusterName + "\n"
	f.docker = "abc123\ndef456\n"
	err := k.VerifyDeleted(context.Background())
	if err == nil || !strings.Contains(err.Error(), "still lists") || !strings.Contains(err.Error(), "2 container(s)") {
		t.Fatalf("leftovers: %v", err)
	}
	f.failOn = "kind get"
	if err := k.VerifyDeleted(context.Background()); err == nil {
		t.Error("a failed listing verified the deletion")
	}
	if k.Name() != TargetKind || k.CostUSD() != 0 || k.MaxCostUSD() != 0 {
		t.Error("the Kind target reports a name or a cost it should not")
	}
}

func TestTargetsBuildTheNamedTarget(t *testing.T) {
	ts := Targets{TargetKind: NewKindTarget("/repo", "run1")}
	got, err := ts.New(TargetKind, "/scratch/target")
	if err != nil || got.Name() != TargetKind || got.Kubeconfig() != filepath.Join("/scratch/target", "kubeconfig") {
		t.Fatalf("New(kind) = %v, %v", got, err)
	}
	if _, err := ts.New("aws", "/x"); err == nil || !strings.Contains(err.Error(), "the targets are kind") {
		t.Errorf("an unknown target: %v", err)
	}
}
