// SPDX-License-Identifier: MIT

package journeys

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Kind target settings. The cluster name is fixed by decision 6 of
// change 0157; journeys never use the shared lenny-e2e cluster.
const (
	KindClusterName = "lenny-journeys"
	// KindAliasTag is the alias tag install.sh gives each image it loads
	// for a journey cluster. The e2e installs move the e2e alias, so the
	// journey cluster uses its own and leaves that alias where it is.
	KindAliasTag = "journeys"
)

// CommandRunner runs a command with an environment, writing its combined
// output to log. KindTarget uses it so its tests substitute a fake.
type CommandRunner func(ctx context.Context, log io.Writer, env []string, dir, name string, args ...string) (string, error)

// ExecRunner runs commands with os/exec. env is appended to the
// process's own environment.
func ExecRunner(ctx context.Context, log io.Writer, env []string, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var out strings.Builder
	w := io.Writer(&out)
	if log != nil {
		w = io.MultiWriter(&out, log)
	}
	cmd.Stdout = w
	cmd.Stderr = w
	cmd.WaitDelay = 10 * time.Second
	err := cmd.Run()
	if err != nil {
		return out.String(), fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return out.String(), nil
}

// KindTarget creates the journey cluster with the Kind install script the
// e2e_kind tier uses, under the name lenny-journeys, with the echo
// reference runtime (decision 7).
type KindTarget struct {
	// RepoRoot is the repository checkout the script and the cluster
	// configuration are read from.
	RepoRoot string
	// Dir holds the generated cluster configuration and the kubeconfig.
	Dir string
	// Cluster is the Kind cluster name.
	Cluster string
	// Run runs commands.
	Run CommandRunner
}

// NewKindTarget returns the Kind target for one trial.
func NewKindTarget(repoRoot string) TargetFactory {
	return func(dir string) Target {
		return &KindTarget{RepoRoot: repoRoot, Dir: dir, Cluster: KindClusterName, Run: ExecRunner}
	}
}

// Name implements Target.
func (k *KindTarget) Name() string { return TargetKind }

// Kubeconfig implements Target. install.sh exports the cluster's
// kubeconfig into this file rather than the user's own.
func (k *KindTarget) Kubeconfig() string { return filepath.Join(k.Dir, "kubeconfig") }

// CostUSD implements Target. A local cluster costs nothing to record.
func (k *KindTarget) CostUSD() float64 { return 0 }

// MaxCostUSD implements Target.
func (k *KindTarget) MaxCostUSD() float64 { return 0 }

// Create implements Target. It deletes a cluster an earlier, interrupted
// run left under the same name, writes a cluster configuration without
// the host port mappings of the e2e configuration (so the journey cluster
// runs beside lenny-e2e), and runs install.sh, stopping after the data
// stores for SetupPrerequisites.
func (k *KindTarget) Create(ctx context.Context, setup string, log io.Writer) error {
	if err := os.MkdirAll(k.Dir, 0o755); err != nil {
		return err
	}
	exists, err := k.exists(ctx)
	if err != nil {
		return err
	}
	if exists {
		fmt.Fprintf(log, "==> deleting the %s cluster an earlier run left\n", k.Cluster)
		if err := k.Delete(ctx, log); err != nil {
			return err
		}
	}
	base, err := os.ReadFile(filepath.Join(k.RepoRoot, "tests", "testinfra", "kind", "cluster.yaml"))
	if err != nil {
		return err
	}
	cfg, err := JourneyClusterConfig(base, k.Cluster)
	if err != nil {
		return err
	}
	cfgPath := filepath.Join(k.Dir, "kind-cluster.yaml")
	if err := os.WriteFile(cfgPath, cfg, 0o644); err != nil {
		return err
	}
	env, err := k.installEnv(setup, cfgPath)
	if err != nil {
		return err
	}
	_, err = k.Run(ctx, log, env, k.RepoRoot, "bash", filepath.Join("tests", "testinfra", "kind", "install.sh"))
	return err
}

func (k *KindTarget) installEnv(setup, cfgPath string) ([]string, error) {
	env := []string{
		"LENNY_KIND_CLUSTER=" + k.Cluster,
		"LENNY_CLUSTER_CONFIG=" + cfgPath,
		"KUBECONFIG=" + k.Kubeconfig(),
		"LENNY_ALIAS_TAG=" + KindAliasTag,
		// The host prune is a maintenance step of the shared e2e
		// environment; a journey run leaves the host cache alone.
		"LENNY_SKIP_PRUNE=1",
	}
	switch setup {
	case SetupPrerequisites:
		env = append(env, "LENNY_INSTALL_STOP_AFTER=datastores")
	case SetupLenny:
	default:
		return nil, fmt.Errorf("setup %q is unknown", setup)
	}
	return env, nil
}

// Delete implements Target.
func (k *KindTarget) Delete(ctx context.Context, log io.Writer) error {
	_, err := k.Run(ctx, log, []string{"KUBECONFIG=" + k.Kubeconfig()}, k.RepoRoot, "kind", "delete", "cluster", "--name", k.Cluster)
	return err
}

// VerifyDeleted implements Target. It checks that Kind lists no cluster
// of the name and that Docker holds no container labelled with it.
func (k *KindTarget) VerifyDeleted(ctx context.Context) error {
	exists, err := k.exists(ctx)
	if err != nil {
		return err
	}
	var errs []error
	if exists {
		errs = append(errs, fmt.Errorf("kind still lists the cluster %s", k.Cluster))
	}
	out, err := k.Run(ctx, nil, nil, k.RepoRoot, "docker", "ps", "-a", "-q", "--filter", "label=io.x-k8s.kind.cluster="+k.Cluster)
	if err != nil {
		errs = append(errs, err)
	} else if ids := strings.Fields(out); len(ids) > 0 {
		errs = append(errs, fmt.Errorf("docker still holds %d container(s) of the cluster %s: %s", len(ids), k.Cluster, strings.Join(ids, " ")))
	}
	return errors.Join(errs...)
}

func (k *KindTarget) exists(ctx context.Context) (bool, error) {
	out, err := k.Run(ctx, nil, nil, k.RepoRoot, "kind", "get", "clusters")
	if err != nil {
		return false, err
	}
	for _, name := range strings.Fields(out) {
		if name == k.Cluster {
			return true, nil
		}
	}
	return false, nil
}

// JourneyClusterConfig derives the journey cluster's Kind configuration
// from the e2e configuration: the same nodes, images, labels, and
// networking, under the journey cluster's name and without host port
// mappings, which would collide with a running lenny-e2e cluster.
func JourneyClusterConfig(base []byte, name string) ([]byte, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(base, &doc); err != nil {
		return nil, fmt.Errorf("kind cluster configuration: %w", err)
	}
	if doc == nil {
		return nil, errors.New("kind cluster configuration is empty")
	}
	doc["name"] = name
	nodes, _ := doc["nodes"].([]any)
	if len(nodes) == 0 {
		return nil, errors.New("kind cluster configuration has no nodes")
	}
	for _, n := range nodes {
		if node, ok := n.(map[string]any); ok {
			delete(node, "extraPortMappings")
		}
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return append([]byte("# Generated by lenny-test --tier journeys from tests/testinfra/kind/cluster.yaml.\n"), out...), nil
}
