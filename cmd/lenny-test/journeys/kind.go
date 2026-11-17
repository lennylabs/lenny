// SPDX-License-Identifier: MIT

package journeys

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
	// KindNetwork is the journey cluster's own Docker network, which the
	// harness creates with KindNetworkSubnet and hands to kind through
	// KIND_EXPERIMENTAL_DOCKER_NETWORK. The agent's egress proxy joins it
	// to reach the cluster's API server. No other cluster's nodes are on
	// it.
	KindNetwork = "lenny-journeys"
	// KindNetworkSubnet lies inside 172.16.0.0/12, the Docker bridge range
	// the e2e install's API server egress policy and kubeApiServerCIDR
	// admit.
	KindNetworkSubnet = "172.29.0.0/16"
	// The journey cluster's pod and service networks differ from the e2e
	// cluster's (10.245.0.0/16 and 10.97.0.0/16 in
	// tests/testinfra/kind/cluster.yaml). The service network stays inside
	// 10.96.0.0/12, which the chart's policies name for the kubernetes
	// Service.
	JourneyPodSubnet     = "10.246.0.0/16"
	JourneyServiceSubnet = "10.98.0.0/16"
	networkLabel         = "lenny.dev/journeys-network"
	// Node labels that record which run created the cluster, so a run
	// never deletes or creates over a cluster another live run owns.
	ownerRunLabel = "lenny.dev/journeys-run"
	ownerPIDLabel = "lenny.dev/journeys-pid"
)

// CommandRunner runs a command with an environment, writing its combined
// output to log. KindTarget uses it so its tests substitute a fake.
type CommandRunner func(ctx context.Context, log io.Writer, env []string, dir, name string, args ...string) (string, error)

// ExecRunner runs commands with os/exec. env is appended to the
// process's own environment. It returns standard output alone, so a
// warning on standard error never corrupts a kubeconfig or a label the
// caller parses; standard error goes to log and into the error.
func ExecRunner(ctx context.Context, log io.Writer, env []string, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var out, errOut strings.Builder
	stdout, stderr := io.Writer(&out), io.Writer(&errOut)
	if log != nil {
		// os/exec copies the two streams on separate goroutines, so the
		// log they share is serialized.
		shared := &lockedWriter{w: log}
		stdout, stderr = io.MultiWriter(&out, shared), io.MultiWriter(&errOut, shared)
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = 10 * time.Second
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, clip(errOut.String()))
	}
	return out.String(), nil
}

// lockedWriter serializes writes to w.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// KindTarget creates the journey cluster with the Kind install script the
// e2e_kind tier uses, under the name lenny-journeys, with the echo
// reference runtime (decision 7).
type KindTarget struct {
	// RepoRoot is the repository checkout the script and the cluster
	// configuration are read from.
	RepoRoot string
	// Dir holds the generated cluster configuration, the generated
	// install files, and the kubeconfig.
	Dir string
	// Cluster is the Kind cluster name.
	Cluster string
	// RunID and PID identify the run that owns the cluster. Create labels
	// every node with them.
	RunID string
	PID   int
	// Run runs commands.
	Run CommandRunner
	// Alive reports whether a process runs; nil uses processAlive.
	Alive func(pid int) bool

	// created records that this target started creating the cluster, so
	// its own teardown deletes it even when its labels cannot be read.
	created bool
}

// NewKindTarget returns the Kind target factory for one run.
func NewKindTarget(repoRoot, runID string) TargetFactory {
	return func(dir string) Target {
		return &KindTarget{RepoRoot: repoRoot, Dir: dir, Cluster: KindClusterName, RunID: runID, PID: os.Getpid(), Run: ExecRunner}
	}
}

// Name implements Target.
func (k *KindTarget) Name() string { return TargetKind }

// Kubeconfig implements Target. install.sh and kind export the cluster's
// kubeconfig into this file rather than the user's own.
func (k *KindTarget) Kubeconfig() string { return filepath.Join(k.Dir, "kubeconfig") }

// CostUSD implements Target. A local cluster costs nothing to record.
func (k *KindTarget) CostUSD() float64 { return 0 }

// MaxCostUSD implements Target.
func (k *KindTarget) MaxCostUSD() float64 { return 0 }

// Facts implements Target: the values install.sh resolved, such as the
// digest-pinned echo runtime image, read from its facts file.
func (k *KindTarget) Facts() map[string]string {
	out := map[string]string{}
	f, err := os.Open(k.factsPath())
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if name, value, ok := strings.Cut(strings.TrimSpace(sc.Text()), "="); ok && name != "" {
			out[name] = value
		}
	}
	return out
}

func (k *KindTarget) factsPath() string { return filepath.Join(k.Dir, "install-facts") }

// AgentEndpoint implements Target: the journey cluster's network and its
// internal kubeconfig, whose server is the control-plane node's name on
// that network.
func (k *KindTarget) AgentEndpoint(ctx context.Context) (AgentEndpoint, error) {
	out, err := k.Run(ctx, nil, nil, k.RepoRoot, "kind", "get", "kubeconfig", "--internal", "--name", k.Cluster)
	if err != nil {
		return AgentEndpoint{}, err
	}
	server, err := KubeconfigServer([]byte(out))
	if err != nil {
		return AgentEndpoint{}, err
	}
	return AgentEndpoint{Network: KindNetwork, Kubeconfig: []byte(out), APIServer: server}, nil
}

// Create implements Target. It deletes a cluster an earlier, dead run
// left under the same name and refuses to touch one a live run owns,
// writes a cluster configuration without the host port mappings of the
// e2e configuration (so the journey cluster runs beside lenny-e2e) and
// with the owner labels on every node, and then either creates the bare
// cluster (SetupCluster) or runs install.sh, stopping after the data
// stores for SetupPrerequisites. install.sh writes its generated files
// under Dir rather than into the checkout.
func (k *KindTarget) Create(ctx context.Context, setup string, log io.Writer) error {
	if err := os.MkdirAll(k.Dir, 0o700); err != nil {
		return err
	}
	if err := k.clearLeftover(ctx, log); err != nil {
		return err
	}
	if setup != SetupCluster && setup != SetupPrerequisites && setup != SetupLenny {
		return fmt.Errorf("setup %q is unknown", setup)
	}
	if err := k.ensureNetwork(ctx, log); err != nil {
		return err
	}
	base, err := os.ReadFile(filepath.Join(k.RepoRoot, "tests", "testinfra", "kind", "cluster.yaml"))
	if err != nil {
		return err
	}
	cfg, err := JourneyClusterConfig(base, k.Cluster, map[string]string{ownerRunLabel: k.RunID, ownerPIDLabel: strconv.Itoa(k.PID)})
	if err != nil {
		return err
	}
	cfgPath := filepath.Join(k.Dir, "kind-cluster.yaml")
	if err := os.WriteFile(cfgPath, cfg, 0o600); err != nil {
		return err
	}
	k.created = true
	if setup == SetupCluster {
		_, err := k.Run(ctx, log, []string{"KIND_EXPERIMENTAL_DOCKER_NETWORK=" + KindNetwork}, k.RepoRoot,
			"kind", "create", "cluster", "--name", k.Cluster, "--config", cfgPath, "--kubeconfig", k.Kubeconfig())
		return err
	}
	env, err := k.installEnv(setup, cfgPath)
	if err != nil {
		return err
	}
	_, err = k.Run(ctx, log, env, k.RepoRoot, "bash", filepath.Join("tests", "testinfra", "kind", "install.sh"))
	return err
}

// ensureNetwork creates the journey cluster's network with its subnet
// when it does not exist, and refuses one that exists with another
// subnet.
func (k *KindTarget) ensureNetwork(ctx context.Context, log io.Writer) error {
	out, err := k.Run(ctx, nil, nil, k.RepoRoot, "docker", "network", "inspect", "--format", "{{range .IPAM.Config}}{{.Subnet}} {{end}}", KindNetwork)
	if err == nil {
		if !strings.Contains(" "+out+" ", " "+KindNetworkSubnet+" ") {
			return fmt.Errorf("the Docker network %s exists with subnet %q, not %s; remove it with `docker network rm %s`", KindNetwork, strings.TrimSpace(out), KindNetworkSubnet, KindNetwork)
		}
		return nil
	}
	fmt.Fprintf(log, "==> creating the Docker network %s (%s)\n", KindNetwork, KindNetworkSubnet)
	_, err = k.Run(ctx, log, nil, k.RepoRoot, "docker", "network", "create",
		"--subnet", KindNetworkSubnet, "--label", networkLabel+"=true",
		"--opt", "com.docker.network.bridge.enable_ip_masquerade=true", KindNetwork)
	if err != nil {
		return fmt.Errorf("create the Docker network %s: %w", KindNetwork, err)
	}
	return nil
}

// removeNetworkIfUnused removes the journey network when no container is
// attached to it.
func (k *KindTarget) removeNetworkIfUnused(ctx context.Context, log io.Writer) error {
	out, err := k.Run(ctx, nil, nil, k.RepoRoot, "docker", "network", "inspect", "--format", "{{len .Containers}}", KindNetwork)
	if err != nil {
		return nil // already gone
	}
	if n := strings.TrimSpace(out); n != "0" {
		return fmt.Errorf("the Docker network %s still has %s container(s) attached", KindNetwork, n)
	}
	_, err = k.Run(ctx, log, nil, k.RepoRoot, "docker", "network", "rm", KindNetwork)
	return err
}

// KubeconfigServer returns the host:port of the first cluster's server in
// a kubeconfig.
func KubeconfigServer(kubeconfig []byte) (string, error) {
	var doc struct {
		Clusters []struct {
			Cluster struct {
				Server string `yaml:"server"`
			} `yaml:"cluster"`
		} `yaml:"clusters"`
	}
	if err := yaml.Unmarshal(kubeconfig, &doc); err != nil {
		return "", fmt.Errorf("kubeconfig: %w", err)
	}
	if len(doc.Clusters) == 0 || doc.Clusters[0].Cluster.Server == "" {
		return "", errors.New("the kubeconfig names no cluster server")
	}
	u, err := url.Parse(doc.Clusters[0].Cluster.Server)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("the kubeconfig server %q is not a URL", doc.Clusters[0].Cluster.Server)
	}
	if u.Port() == "" {
		return net.JoinHostPort(u.Hostname(), "443"), nil
	}
	return u.Host, nil
}

// clearLeftover deletes a cluster of the journey name that a dead run
// left, and refuses when a live run owns it or its owner cannot be read.
func (k *KindTarget) clearLeftover(ctx context.Context, log io.Writer) error {
	exists, err := k.exists(ctx)
	if err != nil || !exists {
		return err
	}
	if err := k.checkNotOwnedByOtherLiveRun(ctx); err != nil {
		return err
	}
	fmt.Fprintf(log, "==> deleting the %s cluster an earlier run left\n", k.Cluster)
	if err := k.deleteCluster(ctx, log); err != nil {
		return err
	}
	return k.removeNetworkIfUnused(ctx, log)
}

func (k *KindTarget) installEnv(setup, cfgPath string) ([]string, error) {
	env := []string{
		"LENNY_KIND_CLUSTER=" + k.Cluster,
		"LENNY_CLUSTER_CONFIG=" + cfgPath,
		"KUBECONFIG=" + k.Kubeconfig(),
		"LENNY_ALIAS_TAG=" + KindAliasTag,
		"LENNY_INSTALL_GEN_DIR=" + filepath.Join(k.Dir, "gen"),
		"LENNY_INSTALL_FACTS_FILE=" + k.factsPath(),
		"LENNY_EXTRA_VALUES=" + filepath.Join(k.Dir, "journey-values.yaml"),
		"KIND_EXPERIMENTAL_DOCKER_NETWORK=" + KindNetwork,
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
	if err := os.WriteFile(filepath.Join(k.Dir, "journey-values.yaml"), []byte(journeyValues), 0o600); err != nil {
		return nil, err
	}
	return env, nil
}

// journeyValues follow the journey cluster's pod network: the e2e values
// admit the object store's egress on the e2e pod network.
const journeyValues = "# Generated by lenny-test --tier journeys.\nobjectStorage:\n  egressCIDRs:\n    - \"" + JourneyPodSubnet + "\"\n"

// Delete implements Target. It deletes the cluster this target created,
// refuses one another live run owns or whose owner cannot be read, and
// removes the journey network once no container uses it.
func (k *KindTarget) Delete(ctx context.Context, log io.Writer) error {
	exists, err := k.exists(ctx)
	if err != nil {
		return err
	}
	if exists {
		if !k.created {
			if err := k.checkNotOwnedByOtherLiveRun(ctx); err != nil {
				return err
			}
		}
		if err := k.deleteCluster(ctx, log); err != nil {
			return err
		}
	}
	return k.removeNetworkIfUnused(ctx, log)
}

func (k *KindTarget) deleteCluster(ctx context.Context, log io.Writer) error {
	_, err := k.Run(ctx, log, []string{"KUBECONFIG=" + k.Kubeconfig()}, k.RepoRoot, "kind", "delete", "cluster", "--name", k.Cluster)
	return err
}

// ErrOwnedByLiveRun is returned when the journey cluster belongs to
// another run whose process is alive.
var ErrOwnedByLiveRun = errors.New("the journey cluster belongs to another live run")

// ErrOwnerUnknown is returned when the journey cluster's owner labels
// cannot be read.
var ErrOwnerUnknown = errors.New("the journey cluster's owner cannot be read")

// checkNotOwnedByOtherLiveRun reads the owner labels of the existing
// cluster's nodes. A cluster whose labels name another run and a live
// process is refused, and so is one whose labels cannot be read: it might
// belong to a run on another lock, and deleting it is the owner's call.
func (k *KindTarget) checkNotOwnedByOtherLiveRun(ctx context.Context) error {
	run, pid, ok := k.owner(ctx)
	if !ok {
		return fmt.Errorf("%w: cluster %s has no readable %s and %s labels; delete it with `kind delete cluster --name %s` if no run uses it",
			ErrOwnerUnknown, k.Cluster, ownerRunLabel, ownerPIDLabel, k.Cluster)
	}
	if run == k.RunID {
		return nil
	}
	alive := k.Alive
	if alive == nil {
		alive = processAlive
	}
	if pid != k.PID && alive(pid) {
		return fmt.Errorf("%w: cluster %s was created by run %s, process %d, which is still running", ErrOwnedByLiveRun, k.Cluster, run, pid)
	}
	return nil
}

func (k *KindTarget) owner(ctx context.Context) (string, int, bool) {
	kc, err := k.Run(ctx, nil, nil, k.RepoRoot, "kind", "get", "kubeconfig", "--name", k.Cluster)
	if err != nil {
		return "", 0, false
	}
	path := filepath.Join(k.Dir, "owner-kubeconfig")
	if err := os.MkdirAll(k.Dir, 0o700); err != nil {
		return "", 0, false
	}
	if err := os.WriteFile(path, []byte(kc), 0o600); err != nil {
		return "", 0, false
	}
	defer os.Remove(path)
	jsonpath := fmt.Sprintf(`jsonpath={.items[0].metadata.labels.%s} {.items[0].metadata.labels.%s}`,
		strings.ReplaceAll(ownerRunLabel, ".", `\.`), strings.ReplaceAll(ownerPIDLabel, ".", `\.`))
	out, err := k.Run(ctx, nil, nil, k.RepoRoot, "kubectl", "--kubeconfig", path, "--request-timeout", "20s", "get", "nodes", "-o", jsonpath)
	if err != nil {
		return "", 0, false
	}
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return "", 0, false
	}
	pid, err := strconv.Atoi(fields[1])
	if err != nil {
		return "", 0, false
	}
	return fields[0], pid, true
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
	if _, err := k.Run(ctx, nil, nil, k.RepoRoot, "docker", "network", "inspect", "--format", "{{.Name}}", KindNetwork); err == nil {
		errs = append(errs, fmt.Errorf("the Docker network %s still exists", KindNetwork))
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
// from the e2e configuration: the same nodes, images, and labels, under
// the journey cluster's name, with labels added to every node, with the
// journey pod and service networks, and without host port mappings, which
// would collide with a running lenny-e2e cluster.
func JourneyClusterConfig(base []byte, name string, labels map[string]string) ([]byte, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(base, &doc); err != nil {
		return nil, fmt.Errorf("kind cluster configuration: %w", err)
	}
	if doc == nil {
		return nil, errors.New("kind cluster configuration is empty")
	}
	doc["name"] = name
	networking, _ := doc["networking"].(map[string]any)
	if networking == nil {
		networking = map[string]any{}
	}
	networking["podSubnet"] = JourneyPodSubnet
	networking["serviceSubnet"] = JourneyServiceSubnet
	doc["networking"] = networking
	nodes, _ := doc["nodes"].([]any)
	if len(nodes) == 0 {
		return nil, errors.New("kind cluster configuration has no nodes")
	}
	for _, n := range nodes {
		node, ok := n.(map[string]any)
		if !ok {
			continue
		}
		delete(node, "extraPortMappings")
		if len(labels) == 0 {
			continue
		}
		nl, _ := node["labels"].(map[string]any)
		if nl == nil {
			nl = map[string]any{}
		}
		for k, v := range labels {
			nl[k] = v
		}
		node["labels"] = nl
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return append([]byte("# Generated by lenny-test --tier journeys from tests/testinfra/kind/cluster.yaml.\n"), out...), nil
}
