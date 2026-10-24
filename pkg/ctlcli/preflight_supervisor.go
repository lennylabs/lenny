// SPDX-License-Identifier: MIT

package ctlcli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
	"github.com/lennylabs/lenny/pkg/supervisorselftest"
)

const (
	// defaultSelftestWait bounds the wait for the self-test pod to end. It
	// covers the supervisor image pull on a node that has not cached it.
	// Operator-tunable through --wait-timeout.
	defaultSelftestWait = 5 * time.Minute
	// selftestPollInterval is the interval between completion reads.
	selftestPollInterval = 2 * time.Second
	// selftestDeleteTimeout bounds the delete that runs on every path, on a
	// context detached from the command's, so an interrupted or timed-out
	// command still removes its pod.
	selftestDeleteTimeout = 30 * time.Second
	// selftestLogLimit caps the report read from the pod log. The report is
	// one short line per property.
	selftestLogLimit int64 = 64 << 10
)

// selftestPodClient is the set of pod requests the command makes. Each maps
// to one verb of the §24.2 permission cell: create, get, and delete on pods
// and get on pods/log, in the agent namespace. The completion wait polls
// Get, because the cell grants no watch or list.
type selftestPodClient interface {
	Create(ctx context.Context, pod *corev1.Pod) error
	Get(ctx context.Context, name string) (*corev1.Pod, error)
	Delete(ctx context.Context, name string) error
	Logs(ctx context.Context, name string) (string, error)
}

// cmdPreflightSupervisor implements `lenny-ctl preflight supervisor
// --runtime-class <name> --config <values.yaml>`. It runs in standalone mode
// only, through the current kubeconfig context: it creates a one-off pod
// from the supervisor image under the named RuntimeClass, runs the
// supervisor's property self-test, reports each property, and deletes the
// pod. It exits non-zero when a property that gates a `restart` pod fails.
// spec: §24.2; §4.7.11 (Runtime supervisor, Property self-test).
func cmdPreflightSupervisor(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("preflight supervisor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	runtimeClass := fs.String("runtime-class", "", "RuntimeClass the self-test pod runs under (required)")
	configPath := fs.String("config", "", "path to the Helm values.yaml the install uses (required)")
	wait := fs.Duration("wait-timeout", defaultSelftestWait, "bound on the wait for the self-test pod to end")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *configPath == "" || *runtimeClass == "" || *wait <= 0 {
		fmt.Fprintln(stderr, "lenny-ctl preflight supervisor: --runtime-class and --config are required, and --wait-timeout must be positive")
		return 2
	}
	pod, err := selftestPodFromFlags(*configPath, *runtimeClass)
	if err != nil {
		fmt.Fprintf(stderr, "lenny-ctl preflight supervisor: %v\n", err)
		return 1
	}
	pods, err := kubeSelftestPods(pod.Namespace)
	if err != nil {
		fmt.Fprintf(stderr, "lenny-ctl preflight supervisor: %v\n", err)
		return 1
	}
	r := selftestRun{pods: pods, poll: selftestPollInterval, wait: *wait, deleteTimeout: selftestDeleteTimeout}
	return r.run(ctx, pod, stdout, stderr)
}

// selftestPodFromFlags reads the values file and builds the self-test pod.
func selftestPodFromFlags(configPath, runtimeClass string) (*corev1.Pod, error) {
	v, err := readPreflightValues(configPath)
	if err != nil {
		return nil, fmt.Errorf("read --config: %w", err)
	}
	name, err := newSelftestPodName()
	if err != nil {
		return nil, err
	}
	spec, err := selftestPodSpecFromValues(v, runtimeClass, name)
	if err != nil {
		return nil, err
	}
	return buildSelftestPod(spec), nil
}

// selftestRun drives one self-test pod from creation to deletion.
type selftestRun struct {
	pods          selftestPodClient
	poll          time.Duration
	wait          time.Duration
	deleteTimeout time.Duration
}

// run creates the pod, waits for it to end, reads and reports its result
// lines, and deletes the pod on every path, a failed create included,
// because a create that fails after the API server stored the pod still
// leaves one. A failed delete makes the run fail, naming the pod.
func (r selftestRun) run(ctx context.Context, pod *corev1.Pod, stdout, stderr io.Writer) (code int) {
	defer func() {
		if err := r.deletePod(ctx, pod.Name); err != nil {
			fmt.Fprintf(stderr, "lenny-ctl preflight supervisor: delete pod %s/%s: %v\n", pod.Namespace, pod.Name, err)
			code = 1
		}
	}()
	results, phase, err := r.execute(ctx, pod)
	if err != nil {
		fmt.Fprintf(stderr, "lenny-ctl preflight supervisor: %v\n", err)
		return 1
	}
	return reportSelftest(results, phase, stdout, stderr)
}

// execute creates the pod, waits for its terminal phase, and parses its
// report.
func (r selftestRun) execute(ctx context.Context, pod *corev1.Pod) (map[string]bool, corev1.PodPhase, error) {
	if err := r.pods.Create(ctx, pod); err != nil {
		return nil, "", fmt.Errorf("create pod %s/%s: %w", pod.Namespace, pod.Name, err)
	}
	phase, err := r.awaitCompletion(ctx, pod.Name)
	if err != nil {
		return nil, "", err
	}
	report, err := r.pods.Logs(ctx, pod.Name)
	if err != nil {
		return nil, "", fmt.Errorf("read the self-test report of pod %s: %w", pod.Name, err)
	}
	results, err := supervisorselftest.ParseReport(report)
	if err != nil {
		return nil, "", fmt.Errorf("pod %s: %w", pod.Name, err)
	}
	return results, phase, nil
}

// errSelftestTimeout reports a self-test pod that did not end within the
// wait bound.
var errSelftestTimeout = errors.New("the self-test pod did not end before the wait ended")

// awaitCompletion polls the pod with get until it reaches Succeeded or
// Failed, or the wait bound passes.
func (r selftestRun) awaitCompletion(ctx context.Context, name string) (corev1.PodPhase, error) {
	ctx, cancel := context.WithTimeout(ctx, r.wait)
	defer cancel()
	for {
		pod, err := r.pods.Get(ctx, name)
		if err != nil {
			return "", fmt.Errorf("get pod %s: %w", name, err)
		}
		if p := pod.Status.Phase; p == corev1.PodSucceeded || p == corev1.PodFailed {
			return p, nil
		}
		timer := time.NewTimer(r.poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", fmt.Errorf("%w (pod %s: %s): %w", errSelftestTimeout, name, describePodWait(pod), ctx.Err())
		case <-timer.C:
		}
	}
}

// describePodWait names the last observed phase and, when a container is
// waiting, its reason, such as an image pull failure.
func describePodWait(pod *corev1.Pod) string {
	desc := "phase " + string(pod.Status.Phase)
	for _, cs := range pod.Status.ContainerStatuses {
		if w := cs.State.Waiting; w != nil && w.Reason != "" {
			desc += ", container " + cs.Name + " waiting: " + w.Reason
		}
	}
	return desc
}

// deletePod deletes the pod on a context detached from the command's, so a
// cancelled or timed-out command still removes it. A pod already gone is
// not an error.
func (r selftestRun) deletePod(ctx context.Context, name string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.deleteTimeout)
	defer cancel()
	return r.pods.Delete(ctx, name)
}

// reportSelftest prints one line per property and returns the exit code: 1
// when a gating property failed, or when the supervisor exited non-zero
// although its report shows every gating property passed, because the
// report and the exit then disagree and neither can be trusted.
// spec: §24.2; §4.7.11 (Runtime supervisor, Property self-test).
func reportSelftest(results map[string]bool, phase corev1.PodPhase, stdout, stderr io.Writer) int {
	gateFailed := false
	for _, name := range supervisorselftest.Properties {
		passed := results[name]
		reason := "reported; gates no pod"
		if supervisorselftest.Gates(name) {
			reason = "gates a restart pod"
			gateFailed = gateFailed || !passed
		}
		printPreflightCheck(stdout, name, passed, reason)
	}
	switch {
	case gateFailed:
		fmt.Fprintln(stderr, "lenny-ctl preflight supervisor: a property that gates a restart pod failed")
		return 1
	case phase != corev1.PodSucceeded:
		fmt.Fprintf(stderr, "lenny-ctl preflight supervisor: the supervisor self-test ended in phase %s although every gating property passed\n", phase)
		return 1
	}
	fmt.Fprintln(stdout, "lenny-ctl preflight supervisor: every property that gates a restart pod passed")
	return 0
}

// kubeSelftestPods builds the pod client from the current kubeconfig
// context. spec: §24.2.
func kubeSelftestPods(namespace string) (selftestPodClient, error) {
	cfg, err := ctrl.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("load the kubeconfig context: %w", err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("build the Kubernetes client: %w", err)
	}
	return clientsetPods{pods: cs.CoreV1().Pods(namespace)}, nil
}

// clientsetPods is the selftestPodClient over the typed pod client of one
// namespace.
type clientsetPods struct {
	pods typedcorev1.PodInterface
}

func (c clientsetPods) Create(ctx context.Context, pod *corev1.Pod) error {
	_, err := c.pods.Create(ctx, pod, metav1.CreateOptions{})
	return err
}

func (c clientsetPods) Get(ctx context.Context, name string) (*corev1.Pod, error) {
	return c.pods.Get(ctx, name, metav1.GetOptions{})
}

func (c clientsetPods) Delete(ctx context.Context, name string) error {
	err := c.pods.Delete(ctx, name, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func (c clientsetPods) Logs(ctx context.Context, name string) (string, error) {
	limit := selftestLogLimit
	raw, err := c.pods.GetLogs(name, &corev1.PodLogOptions{Container: podspec.SelftestContainerName, LimitBytes: &limit}).DoRaw(ctx)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
