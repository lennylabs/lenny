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

func TestJourneyClusterConfigDropsHostPortsAndRenames(t *testing.T) {
	base, err := os.ReadFile(filepath.Join("..", "..", "..", "tests", "testinfra", "kind", "cluster.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(base), "extraPortMappings") {
		t.Fatal("the e2e configuration no longer maps host ports; this test checks nothing")
	}
	out, err := JourneyClusterConfig(base, KindClusterName)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "extraPortMappings") || strings.Contains(string(out), "hostPort") {
		t.Errorf("the journey configuration keeps host port mappings:\n%s", out)
	}
	var doc struct {
		Name  string           `yaml:"name"`
		Nodes []map[string]any `yaml:"nodes"`
	}
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	var orig struct {
		Nodes []map[string]any `yaml:"nodes"`
	}
	if err := yaml.Unmarshal(base, &orig); err != nil {
		t.Fatal(err)
	}
	if doc.Name != KindClusterName || len(doc.Nodes) != len(orig.Nodes) {
		t.Errorf("name %q with %d nodes, want %s with %d", doc.Name, len(doc.Nodes), KindClusterName, len(orig.Nodes))
	}
	if _, err := JourneyClusterConfig([]byte("kind: Cluster\n"), "x"); err == nil {
		t.Error("a configuration without nodes was accepted")
	}
	if _, err := JourneyClusterConfig([]byte(""), "x"); err == nil {
		t.Error("an empty configuration was accepted")
	}
}

// fakeCommands records the commands a KindTarget runs and answers them.
type fakeCommands struct {
	calls    []string
	envs     [][]string
	clusters string
	docker   string
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
	case strings.HasPrefix(call, "docker ps"):
		return f.docker, nil
	}
	return "", nil
}

func kindTarget(t *testing.T, f *fakeCommands) *KindTarget {
	t.Helper()
	root := filepath.Join("..", "..", "..")
	return &KindTarget{RepoRoot: root, Dir: t.TempDir(), Cluster: KindClusterName, Run: f.run}
}

func TestKindTargetCreateDeletesALeftoverClusterAndStopsAfterTheDataStores(t *testing.T) {
	f := &fakeCommands{clusters: "lenny-e2e\n" + KindClusterName + "\n"}
	k := kindTarget(t, f)
	if err := k.Create(context.Background(), SetupPrerequisites, io.Discard); err != nil {
		t.Fatal(err)
	}
	want := []string{"kind get clusters", "kind delete cluster --name " + KindClusterName, "bash tests/testinfra/kind/install.sh"}
	if strings.Join(f.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("calls = %q, want %q", f.calls, want)
	}
	env := strings.Join(f.envs[2], " ")
	for _, w := range []string{"LENNY_KIND_CLUSTER=" + KindClusterName, "LENNY_INSTALL_STOP_AFTER=datastores", "LENNY_ALIAS_TAG=journeys", "LENNY_SKIP_PRUNE=1", "KUBECONFIG=" + k.Kubeconfig(), "LENNY_CLUSTER_CONFIG="} {
		if !strings.Contains(env, w) {
			t.Errorf("install env lacks %q: %s", w, env)
		}
	}
	if _, err := os.Stat(filepath.Join(k.Dir, "kind-cluster.yaml")); err != nil {
		t.Errorf("the cluster configuration was not written: %v", err)
	}
}

func TestKindTargetCreateInstallsLennyForTheLennySetup(t *testing.T) {
	f := &fakeCommands{clusters: "lenny-e2e\n"}
	k := kindTarget(t, f)
	if err := k.Create(context.Background(), SetupLenny, io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 2 || strings.Contains(strings.Join(f.envs[1], " "), "LENNY_INSTALL_STOP_AFTER") {
		t.Fatalf("calls = %q env = %q", f.calls, f.envs)
	}
	if err := k.Create(context.Background(), "cloud", io.Discard); err == nil {
		t.Error("an unknown setup was accepted")
	}
	f.failOn = "bash"
	if err := k.Create(context.Background(), SetupLenny, io.Discard); err == nil {
		t.Error("a failed install was not reported")
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
	ts := Targets{TargetKind: NewKindTarget("/repo")}
	got, err := ts.New(TargetKind, "/scratch/target")
	if err != nil || got.Name() != TargetKind || got.Kubeconfig() != filepath.Join("/scratch/target", "kubeconfig") {
		t.Fatalf("New(kind) = %v, %v", got, err)
	}
	if _, err := ts.New("aws", "/x"); err == nil || !strings.Contains(err.Error(), "the targets are kind") {
		t.Errorf("an unknown target: %v", err)
	}
}
