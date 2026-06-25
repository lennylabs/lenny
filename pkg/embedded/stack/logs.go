// SPDX-License-Identifier: MIT

package stack

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"

	"golang.org/x/sync/errgroup"

	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// logComponents lists the §17.4 / §24.19 components lenny logs can filter to
// by exact name. The §17.4 control plane runs as in-cluster pods, so the
// component logs stream from the pods through the embedded kubeconfig rather
// than from host log files: gateway, controller, and ops name their
// Deployments, and runtime-<name> names the agent pods of a runtime. The
// host-process components the in-cluster topology removes (postgres, redis,
// kms, oidc, supervisor) are dropped. The substrate (k3s) keeps its host log
// file path. spec: §17.4 line 179, §24.19 line 263.
var logComponents = []string{
	"gateway",
	"controller",
	"ops",
	"k3s",
}

// LogsOptions configures the lenny logs command.
type LogsOptions struct {
	// Root is the Embedded Mode state directory.
	Root string
	// Component, when set, filters output to one component. An empty
	// string merges every component log. The literal "runtime" expands
	// to every runtime-<name> with running agent pods; an explicit
	// "runtime-<name>" selects exactly one runtime's pods.
	Component string
	// Follow tails the selected logs in §24.19 line 263 `--follow` mode:
	// RunLogs blocks, streaming new lines, until ctx is cancelled.
	Follow bool
	// Out receives the log output.
	Out io.Writer
}

// RunLogs implements the `lenny logs [<component>] [--follow]` command.
// §17.4 / §24.19: it prints merged logs, or filters to one component. The
// gateway, controller, ops, and runtime-<name> components stream from the
// in-cluster pods through the embedded kubeconfig; the k3s substrate streams
// from its host log file. A component that names no running pod (the
// Deployment has not scheduled, or no agent pod of that runtime is up) is
// reported and skipped rather than failing the whole command.
//
// spec: §17.4 line 179, §24.19 line 263.
func RunLogs(ctx context.Context, opts LogsOptions) error {
	out := orDiscard(opts.Out)
	if ctx == nil {
		ctx = context.Background()
	}
	root, err := resolveRoot(opts.Root)
	if err != nil {
		return err
	}
	paths := NewPaths(root)
	st, ok, err := readRunningState(paths.StateFile())
	if err != nil {
		return err
	}
	if !ok {
		return ErrNoRunningStack
	}

	sources, err := resolveLogSources(ctx, paths, st, opts.Component)
	if err != nil {
		return err
	}
	merging := len(sources) > 1
	if !opts.Follow {
		for _, s := range sources {
			if err := s.stream(ctx, out, merging, false); err != nil {
				return err
			}
		}
		return nil
	}
	// Follow mode streams every source concurrently so a long-lived pod-log
	// follow on one source does not starve the others; the merged output
	// interleaves the prefixed lines. Each source honors ctx cancellation.
	return followSources(ctx, out, sources, merging)
}

// followSources streams every source concurrently until ctx is cancelled,
// returning the first source error. The writes are serialized through a mutex
// so concurrent sources do not interleave within a line.
func followSources(ctx context.Context, out io.Writer, sources []logSource, merging bool) error {
	var mu sync.Mutex
	safe := writerFunc(func(p []byte) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		return out.Write(p)
	})
	g, gctx := errgroup.WithContext(ctx)
	for _, s := range sources {
		s := s
		g.Go(func() error { return s.stream(gctx, safe, merging, true) })
	}
	err := g.Wait()
	// A cancelled context is the normal follow exit (the caller cancelled),
	// not a failure.
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// writerFunc adapts a function to io.Writer so the follow path can serialize
// writes from concurrent sources without a named type.
type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// logSource is one resolved log source RunLogs streams from: either a host log
// file (the k3s substrate) or a set of in-cluster pods (a control-plane
// Deployment or a runtime's agent pods). Exactly one of filePath or the
// cluster fields is set.
type logSource struct {
	// name is the component label prefixed onto each line when merging.
	name string
	// filePath, when set, names a host log file (the k3s substrate log).
	filePath string
	// client, namespace, and selector, when client is set, select the pods
	// whose logs this source streams.
	client    kubernetes.Interface
	namespace string
	selector  labels.Selector
}

// resolveLogSources expands the user-supplied component filter into the
// concrete set of log sources to stream. An empty filter merges every
// canonical component (the control-plane Deployments, the k3s substrate, and
// every runtime with running agent pods). The `runtime` alias expands to every
// runtime with running pods; an explicit runtime-<name> resolves to that
// runtime's agent pods. The cluster sources share one kube client built from
// the running stack's kubeconfig.
func resolveLogSources(ctx context.Context, paths Paths, st State, requested string) ([]logSource, error) {
	client, err := logsClusterClient(st)
	if err != nil {
		return nil, err
	}
	switch {
	case requested == "":
		return mergedLogSources(ctx, paths, client)
	case requested == "k3s":
		return []logSource{k3sLogSource(paths)}, nil
	case requested == "runtime":
		sources, err := runtimeLogSources(ctx, client)
		if err != nil {
			return nil, err
		}
		if len(sources) == 0 {
			return nil, fmt.Errorf("lenny logs: no runtime agent pods are running in %s", agentNamespace)
		}
		return sources, nil
	case strings.HasPrefix(requested, "runtime-") && len(requested) > len("runtime-"):
		runtimeName := strings.TrimPrefix(requested, "runtime-")
		return []logSource{runtimeLogSource(client, runtimeName)}, nil
	case knownLogComponent(requested):
		return []logSource{deploymentLogSource(client, requested)}, nil
	default:
		return nil, fmt.Errorf("lenny logs: unknown component %q (known: %v or runtime-<name>)", requested, logComponents)
	}
}

// mergedLogSources builds the full merged set: the control-plane Deployments,
// the k3s substrate log, and every runtime with running agent pods.
func mergedLogSources(ctx context.Context, paths Paths, client kubernetes.Interface) ([]logSource, error) {
	sources := []logSource{
		deploymentLogSource(client, "gateway"),
		deploymentLogSource(client, "controller"),
		deploymentLogSource(client, "ops"),
		k3sLogSource(paths),
	}
	runtimes, err := runtimeLogSources(ctx, client)
	if err != nil {
		return nil, err
	}
	sources = append(sources, runtimes...)
	return sources, nil
}

// logsClusterClient builds the kube client the pod-log sources stream through.
// A running stack records the embedded kubeconfig; a stack state with no
// kubeconfig (the substrate did not come up) fails closed so logs reports the
// missing substrate rather than silently streaming nothing.
func logsClusterClient(st State) (kubernetes.Interface, error) {
	if st.KubeconfigPath == "" {
		return nil, fmt.Errorf("lenny logs: stack state has no kubeconfigPath; the embedded substrate did not come up")
	}
	return clusterClientFn(st.KubeconfigPath)
}

// k3sLogSource builds the host-file log source for the k3s substrate.
func k3sLogSource(paths Paths) logSource {
	return logSource{name: "k3s", filePath: filepath.Join(paths.K3s, "k3s.log")}
}

// deploymentLogSource builds the pod-log source for a control-plane component
// (gateway, controller, ops). The pods are selected by the component label the
// chart stamps on the Deployment's pod template, so the selector matches the
// Deployment regardless of its label scheme. spec: §17.4 (the control-plane
// components run as in-cluster Deployments).
func deploymentLogSource(client kubernetes.Interface, component string) logSource {
	return logSource{
		name:      component,
		client:    client,
		namespace: controlPlaneNamespace,
		selector:  labels.SelectorFromSet(labels.Set{componentLabel: component}),
	}
}

// componentLabel is the pod label the chart stamps the control-plane component
// name on (gateway, controller, ops). It matches the gateway and controller
// Deployment selectors in the embedded manifests, so listing pods by it
// reaches the right component's pods. spec: §17.4.
const componentLabel = "lenny.dev/component"

// runtimeLogSource builds the pod-log source for one runtime's agent pods,
// selected by the §6.2 runtime-name label the Sandbox controller stamps on
// every agent pod (state.LabelRuntime). spec: §6.2 (the runtime-name pod
// label), §17.4 (runtime-<name> streams the runtime's agent pods).
func runtimeLogSource(client kubernetes.Interface, runtimeName string) logSource {
	return logSource{
		name:      "runtime-" + runtimeName,
		client:    client,
		namespace: agentNamespace,
		selector:  labels.SelectorFromSet(labels.Set{state.LabelRuntime: runtimeName}),
	}
}

// runtimeLogSources lists the distinct runtime names with running agent pods
// in the agent namespace and builds one log source per runtime, so the merged
// and `runtime`-alias paths stream every runtime currently placed. A list
// failure is returned so the caller surfaces it rather than silently dropping
// the runtime logs.
func runtimeLogSources(ctx context.Context, client kubernetes.Interface) ([]logSource, error) {
	pods, err := client.CoreV1().Pods(agentNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("lenny logs: list agent pods in %s: %w", agentNamespace, err)
	}
	seen := map[string]struct{}{}
	var names []string
	for i := range pods.Items {
		if name := pods.Items[i].Labels[state.LabelRuntime]; name != "" {
			if _, ok := seen[name]; !ok {
				seen[name] = struct{}{}
				names = append(names, name)
			}
		}
	}
	sort.Strings(names)
	sources := make([]logSource, 0, len(names))
	for _, name := range names {
		sources = append(sources, runtimeLogSource(client, name))
	}
	return sources, nil
}

// stream emits the source's log content to out, prefixing each line with the
// component name when merging. A file source reads the host log file; a
// cluster source lists the selected pods and streams each pod's log. Follow
// mode streams new lines until ctx is cancelled.
func (s logSource) stream(ctx context.Context, out io.Writer, merging, follow bool) error {
	if s.filePath != "" {
		return streamFile(ctx, s.filePath, s.name, out, merging, follow)
	}
	return s.streamPods(ctx, out, merging, follow)
}

// streamFile reads a host log file and emits each line with the component
// prefix when merging. A missing file is not an error: a substrate that has
// not yet written its log is skipped rather than failing the command. In
// follow mode it keeps reading, polling for new lines on the substrate file
// until ctx is cancelled, since the substrate log is a host file with no API
// follow channel.
func streamFile(ctx context.Context, path, name string, out io.Writer, merging, follow bool) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("lenny logs: open %s log: %w", name, err)
	}
	defer func() { _ = f.Close() }()
	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			writeLogLine(out, name, strings.TrimRight(line, "\n"), merging)
		}
		if err == nil {
			continue
		}
		if !errors.Is(err, io.EOF) {
			return fmt.Errorf("lenny logs: read %s log: %w", name, err)
		}
		if !follow {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(followInterval):
		}
	}
}

// streamPods lists the pods the source selects and streams each pod's log
// through the embedded kubeconfig. A selector matching no running pod is
// reported (the Deployment has not scheduled, or no agent pod of that runtime
// is up) and skipped rather than failing the command. Follow mode streams new
// lines until ctx is cancelled.
func (s logSource) streamPods(ctx context.Context, out io.Writer, merging, follow bool) error {
	pods, err := s.client.CoreV1().Pods(s.namespace).List(ctx, metav1.ListOptions{LabelSelector: s.selector.String()})
	if err != nil {
		return fmt.Errorf("lenny logs: list %s pods: %w", s.name, err)
	}
	if len(pods.Items) == 0 {
		fmt.Fprintf(out, "lenny logs: no running pods for %s yet\n", s.name)
		return nil
	}
	for i := range pods.Items {
		if err := s.streamPod(ctx, out, pods.Items[i].Name, merging, follow); err != nil {
			return err
		}
	}
	return nil
}

// streamPod streams one pod's log to out, prefixing each line with the source
// name when merging. Follow mode passes Follow:true to the pod-log request so
// the API server streams new lines until ctx is cancelled.
func (s logSource) streamPod(ctx context.Context, out io.Writer, podName string, merging, follow bool) error {
	req := s.client.CoreV1().Pods(s.namespace).GetLogs(podName, &corev1.PodLogOptions{Follow: follow})
	stream, err := req.Stream(ctx)
	if err != nil {
		return fmt.Errorf("lenny logs: stream %s pod %s: %w", s.name, podName, err)
	}
	defer func() { _ = stream.Close() }()
	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		writeLogLine(out, s.name, scanner.Text(), merging)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("lenny logs: read %s pod %s log: %w", s.name, podName, err)
	}
	return nil
}

// writeLogLine emits one log line, prefixing it with the component name when
// merging more than one source.
func writeLogLine(out io.Writer, name, line string, merging bool) {
	if merging {
		fmt.Fprintf(out, "%s | %s\n", name, line)
	} else {
		fmt.Fprintln(out, line)
	}
}

// knownLogComponent reports whether name matches a §24.19 line 263 component
// allow-list entry. The `runtime-<name>` form is handled by the caller via a
// prefix check rather than enumeration.
func knownLogComponent(name string) bool {
	for _, c := range logComponents {
		if c == name {
			return true
		}
	}
	return false
}

// followInterval is the poll cadence the substrate-file follow path re-reads
// the k3s log at after reaching EOF. Pod-log follow streams through the API
// server's own follow channel, so it needs no client-side poll; only the host
// file source polls. spec: §24.19 line 263 (`--follow`).
const followInterval = 250 * time.Millisecond
