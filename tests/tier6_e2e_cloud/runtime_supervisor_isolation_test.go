// SPDX-License-Identifier: MIT

//go:build e2e_cloud

// Tier-6 cases for the runtime supervisor under the sandboxed isolation
// runtimes Kind cannot reproduce: gVisor on each platform (ptrace and KVM),
// gVisor run with --oci-seccomp, and Kata. Each case runs per entry of
// sandboxedRuntimes against a supervised probe pod the test builds under
// that RuntimeClass (supervisedpod.SandboxedProbePod), and skips with a
// recorded reason only where the cluster carries no such RuntimeClass.
//
// The pods pull two images the operator pushes to a registry the cluster
// reaches: the supervisor image (LENNY_CLOUD_SUPERVISOR_IMAGE, built from
// Dockerfile.supervisor) and the probe image
// (LENNY_CLOUD_SUPERVISE_PROBE_IMAGE, built from
// cmd/runtimes/supervise-probe/Dockerfile). A cluster with a sandboxed
// RuntimeClass and no image named fails the case rather than skipping it.

package tier6_e2e_cloud_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	nodev1 "k8s.io/api/node/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/lennylabs/lenny/pkg/supervisorselftest"
	"github.com/lennylabs/lenny/tests/testinfra/supervisedpod"
	"github.com/lennylabs/lenny/tests/testinfra/superviseprobe"
)

// sandboxedRuntime is one sandboxed runtime the supervisor cases run under:
// the first of RuntimeClasses the cluster carries.
type sandboxedRuntime struct {
	Name           string
	RuntimeClasses []string
	// Provision is what an operator installs for the entry to run.
	Provision string
}

// sandboxedRuntimes are the runtimes of the sandboxed and microvm isolation
// profiles (§5.3) the supervisor must hold its properties under. The gVisor
// platform and --oci-seccomp are runsc handler options, so each is its own
// RuntimeClass.
var sandboxedRuntimes = []sandboxedRuntime{
	{
		Name: "gvisor-ptrace", RuntimeClasses: []string{"gvisor-ptrace"},
		Provision: "a RuntimeClass gvisor-ptrace whose handler runs runsc --platform=ptrace",
	},
	{
		Name: "gvisor-kvm", RuntimeClasses: []string{"gvisor-kvm"},
		Provision: "a RuntimeClass gvisor-kvm whose handler runs runsc --platform=kvm on nodes exposing /dev/kvm",
	},
	{
		Name: "gvisor-oci-seccomp", RuntimeClasses: []string{"gvisor-oci-seccomp"},
		Provision: "a RuntimeClass gvisor-oci-seccomp whose handler runs runsc --oci-seccomp",
	},
	{
		Name: "gvisor", RuntimeClasses: []string{"gvisor"},
		Provision: "the gvisor RuntimeClass of the sandboxed profile (scripts/cloud/<provider>/up-runtimeclass-pools.sh)",
	},
	{
		Name: "kata", RuntimeClasses: []string{"kata", "kata-containers"},
		Provision: "the kata RuntimeClass of the microvm profile on Kata nodes",
	},
}

// Environment variables naming the cloud install's inputs.
const (
	// supervisorValuesEnv names the values file of the cloud install, which
	// `lenny-ctl preflight supervisor --config` reads.
	supervisorValuesEnv = "LENNY_CLOUD_VALUES_FILE"
	supervisorImageEnv  = "LENNY_CLOUD_SUPERVISOR_IMAGE"
	probeImageEnv       = "LENNY_CLOUD_SUPERVISE_PROBE_IMAGE"
)

// The probe pod's CH-SUPERVISE ping bounds, and a window longer than their
// sum, after which a supervisor that stopped answering has missed a pong.
const (
	sandboxedPingInterval = "2s"
	sandboxedPingTimeout  = "2s"
	sandboxedPingWindow   = 10 * time.Second
)

// The fault signals, which the supervisor keeps at SIG_DFL and blocks
// (§4.7.10 Runtime supervisor, Signals). Their numbers are the same on
// amd64 and arm64.
var faultSignals = []int{4, 7, 8, 11} // SIGILL, SIGBUS, SIGFPE, SIGSEGV

// errNoRuntimeClass reports that the cluster carries none of a sandboxed
// runtime's RuntimeClasses, the one condition under which a case skips.
var errNoRuntimeClass = errors.New("no RuntimeClass found")

// resolveRuntimeClass returns the first RuntimeClass of rt the cluster
// carries. It skips the test only when every candidate is absent, and fails
// it on any other lookup error, so an API or RBAC failure on a cluster that
// does carry the RuntimeClass is not recorded as a missing one.
func resolveRuntimeClass(t *testing.T, cli kubernetes.Interface, rt sandboxedRuntime) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	rc, err := firstRuntimeClass(ctx, cli, rt.RuntimeClasses)
	switch {
	case errors.Is(err, errNoRuntimeClass):
		t.Skipf("blocked: the cluster has no RuntimeClass among %v; provision %s to run this case",
			rt.RuntimeClasses, rt.Provision)
	case err != nil:
		t.Fatalf("look up the RuntimeClasses %v: %v", rt.RuntimeClasses, err)
	}
	return rc
}

// firstRuntimeClass returns the first of names the cluster carries. It
// returns errNoRuntimeClass when every lookup reports NotFound, and the
// lookup error itself when any lookup fails for another reason.
func firstRuntimeClass(ctx context.Context, cli kubernetes.Interface, names []string) (string, error) {
	for _, rc := range names {
		_, err := cli.NodeV1().RuntimeClasses().Get(ctx, rc, metav1.GetOptions{})
		switch {
		case err == nil:
			return rc, nil
		case !apierrors.IsNotFound(err):
			return "", fmt.Errorf("get RuntimeClass %s: %w", rc, err)
		}
	}
	return "", fmt.Errorf("%w among %v", errNoRuntimeClass, names)
}

// sandboxedImages reads the supervisor and probe images, failing the test
// when either is unset, because the case is runnable on this cluster.
func sandboxedImages(t *testing.T) (supervisor, probe string) {
	t.Helper()
	supervisor, probe = os.Getenv(supervisorImageEnv), os.Getenv(probeImageEnv)
	if supervisor == "" || probe == "" {
		t.Fatalf("%s and %s must name the pushed supervisor and probe images to run the sandboxed supervisor cases",
			supervisorImageEnv, probeImageEnv)
	}
	return supervisor, probe
}

// sandboxedNamespace sweeps the namespaces an earlier run left, creates a
// fresh one outside the lenny-pod-security webhook's scope, and deletes it
// when the test ends.
func sandboxedNamespace(t *testing.T, cli *kubernetes.Clientset) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	sel := supervisedpod.TestLabel + "=true"
	if list, err := cli.CoreV1().Namespaces().List(ctx, metav1.ListOptions{LabelSelector: sel}); err != nil {
		t.Logf("sweep of %s namespaces failed (ignored): %v", sel, err)
	} else {
		for _, ns := range list.Items {
			if err := cli.CoreV1().Namespaces().Delete(ctx, ns.Name, metav1.DeleteOptions{}); err == nil {
				t.Logf("swept namespace %s an earlier run left", ns.Name)
			}
		}
	}
	name := fmt.Sprintf("lenny-supervisor-t6-%d", time.Now().UnixNano())
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{supervisedpod.TestLabel: "true"}}}
	if _, err := cli.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create namespace %s: %v", name, err)
	}
	t.Cleanup(func() {
		_ = cli.CoreV1().Namespaces().Delete(context.Background(), name, metav1.DeleteOptions{})
	})
	return name
}

// sandboxedEnv is one case's cluster, namespace, RuntimeClass, and images.
type sandboxedEnv struct {
	cli                     *kubernetes.Clientset
	namespace, rc           string
	supervisorImg, probeImg string
}

// newSandboxedEnv resolves rt and prepares a namespace, skipping when the
// cluster has no RuntimeClass for rt.
func newSandboxedEnv(t *testing.T, cli *kubernetes.Clientset, rt sandboxedRuntime) sandboxedEnv {
	t.Helper()
	rc := resolveRuntimeClass(t, cli, rt)
	sup, probe := sandboxedImages(t)
	return sandboxedEnv{cli: cli, namespace: sandboxedNamespace(t, cli), rc: rc, supervisorImg: sup, probeImg: probe}
}

// spec builds the pod inputs for a pod named name.
func (e sandboxedEnv) spec(name string) supervisedpod.SandboxedPodSpec {
	return supervisedpod.SandboxedPodSpec{
		Name: name, Namespace: e.namespace, RuntimeClassName: e.rc,
		SupervisorImage: e.supervisorImg, ProbeImage: e.probeImg,
		PingInterval: sandboxedPingInterval, PingTimeout: sandboxedPingTimeout,
	}
}

// create creates pod and deletes it when the test ends.
func (e sandboxedEnv) create(t *testing.T, pod *corev1.Pod) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := e.cli.CoreV1().Pods(e.namespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create pod %s under %s: %v", pod.Name, e.rc, err)
	}
	t.Cleanup(func() {
		_ = e.cli.CoreV1().Pods(e.namespace).Delete(context.Background(), pod.Name, metav1.DeleteOptions{})
	})
}

// startProbePod creates a supervised probe pod and waits for the peer to
// receive the supervisor's ready, returning the pod name and the self-test
// properties the ready reported.
func (e sandboxedEnv) startProbePod(t *testing.T, name string) (string, map[string]bool) {
	t.Helper()
	e.create(t, supervisedpod.SandboxedProbePod(e.spec(name)))
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		for _, ev := range superviseprobe.ParsePeerEvents(e.logs(name, "adapter")) {
			switch ev.Event {
			case superviseprobe.PeerReady:
				return name, ev.Properties
			case superviseprobe.PeerLinkFailed:
				t.Fatalf("pod %s under %s: the supervisor never sent ready: %s\n%s", name, e.rc, ev.Error, e.logs(name, "runtime"))
			}
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("pod %s under %s: no ready from the supervisor within 5m: %s", name, e.rc, e.describe(t, name))
	return "", nil
}

// logs returns a container's log, or "" when it cannot be read yet.
func (e sandboxedEnv) logs(pod, container string) string {
	out, _ := exec.Command("kubectl", "-n", e.namespace, "logs", pod, "-c", container).CombinedOutput()
	return string(out)
}

// attack runs one probe attack in pod's runtime container at the agent UID.
func (e sandboxedEnv) attack(t *testing.T, pod, attack string, args ...string) superviseprobe.AttackResult {
	t.Helper()
	argv := append([]string{
		"-n", e.namespace, "exec", pod, "-c", "runtime", "--",
		supervisedpod.ProbeExe, superviseprobe.SubAttack, attack,
	}, args...)
	out, err := exec.Command("kubectl", argv...).Output()
	if err != nil {
		t.Fatalf("run attack %s in %s under %s: %v\n%s", attack, pod, e.rc, err, out)
	}
	var r superviseprobe.AttackResult
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &r); err != nil {
		t.Fatalf("decode attack %s result %q: %v", attack, out, err)
	}
	return r
}

// containers reads pod's container states by name.
func (e sandboxedEnv) containers(t *testing.T, pod string) map[string]corev1.ContainerState {
	t.Helper()
	p, err := e.cli.CoreV1().Pods(e.namespace).Get(context.Background(), pod, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get pod %s: %v", pod, err)
	}
	out := map[string]corev1.ContainerState{}
	for _, cs := range p.Status.ContainerStatuses {
		out[cs.Name] = cs.State
	}
	return out
}

// describe renders pod's container states for a failure message.
func (e sandboxedEnv) describe(t *testing.T, pod string) string {
	t.Helper()
	b, _ := json.Marshal(e.containers(t, pod))
	return string(b)
}

// linkFailed reports whether the peer recorded a link failure.
func (e sandboxedEnv) linkFailed(pod string) bool {
	for _, ev := range superviseprobe.ParsePeerEvents(e.logs(pod, "adapter")) {
		if ev.Event == superviseprobe.PeerLinkFailed {
			return true
		}
	}
	return false
}

// requireAnswering asserts that pod's supervisor still answers ping: the
// peer is running and recorded no link failure, and the runtime container,
// whose first process is the supervisor, is running.
func (e sandboxedEnv) requireAnswering(t *testing.T, pod, after string) {
	t.Helper()
	cs := e.containers(t, pod)
	if cs["adapter"].Running == nil || cs["runtime"].Running == nil || e.linkFailed(pod) {
		t.Fatalf("pod %s under %s: the supervisor stopped answering ping after %s: %s\n%s",
			pod, e.rc, after, e.describe(t, pod), e.logs(pod, "adapter"))
	}
}

// requireRetired asserts that pod retires: the peer records the link failure
// and exits non-zero, as a service-mode adapter does on a missed pong.
func (e sandboxedEnv) requireRetired(t *testing.T, pod, after string) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) {
		if a := e.containers(t, pod)["adapter"].Terminated; a != nil && a.ExitCode != 0 && e.linkFailed(pod) {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("pod %s under %s: the pod did not retire after %s, where the namespace init is not immune: %s",
		pod, e.rc, after, e.describe(t, pod))
}

// spec: §4.7.11 item 8 (Runtime supervisor, Property self-test), §24.2
//
// diagnosis: under a sandboxed runtime `lenny-ctl preflight supervisor` did
// not report every self-test property, or its exit status disagreed with
// the gating properties it reported: it must exit non-zero exactly when the
// non-dumpable mark or kill reach fails. Whether a property holds is the
// runtime's answer rather than the test's; a gating property that fails
// means a restart pod on that RuntimeClass never becomes Ready, and the
// result is logged for the operator.
func TestSupervisorSelfTestUnderSandboxedRuntimes_spec_4_7_11(t *testing.T) {
	_ = requireCloud(t)
	cli := kube(t)
	values := os.Getenv(supervisorValuesEnv)
	bin := filepath.Join(t.TempDir(), "lenny-ctl")
	if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/lenny-ctl").CombinedOutput(); err != nil {
		t.Fatalf("build lenny-ctl: %v\n%s", err, out)
	}
	for _, rt := range sandboxedRuntimes {
		t.Run(rt.Name, func(t *testing.T) {
			rc := resolveRuntimeClass(t, cli, rt)
			if values == "" {
				t.Fatalf("%s must name the cloud install's values file, which the self-test pod is built from", supervisorValuesEnv)
			}
			out, err := exec.Command(bin, "preflight", "supervisor", "--runtime-class", rc, "--config", values,
				"--wait-timeout", "5m").CombinedOutput()
			t.Logf("preflight supervisor under %s:\n%s", rc, out)
			requirePreflightAgrees(t, rc, string(out), err)
		})
	}
}

// preflightLine is one property line of `lenny-ctl preflight supervisor`:
// PASS or FAIL, then the property name.
var preflightLine = regexp.MustCompile(`(?m)^\s*(PASS|FAIL) (\w+):`)

// gatingProperties are the properties that gate a restart pod.
// spec: §4.7.11 item 8.
var gatingProperties = map[string]bool{
	supervisorselftest.PropertyNondumpable: true,
	supervisorselftest.PropertyKillReach:   true,
}

// requirePreflightAgrees asserts that out reports every property and that
// the command failed exactly when a gating property failed.
func requirePreflightAgrees(t *testing.T, rc, out string, runErr error) {
	t.Helper()
	results := map[string]string{}
	for _, m := range preflightLine.FindAllStringSubmatch(out, -1) {
		results[m[2]] = m[1]
	}
	gateFailed := false
	for _, prop := range supervisorselftest.Properties {
		r, ok := results[prop]
		if !ok {
			t.Errorf("preflight under %s reports no %s property", rc, prop)
			continue
		}
		if r == "FAIL" && gatingProperties[prop] {
			gateFailed = true
		}
	}
	if gateFailed != (runErr != nil) {
		t.Errorf("preflight under %s: a gating property failed = %v, but the command error is %v", rc, gateFailed, runErr)
	}
	if gateFailed {
		t.Logf("under %s a gating property fails, so a restart pod on this RuntimeClass never becomes Ready", rc)
	}
}

// spec: §4.7.10 (Runtime supervisor, Signals; Liveness), §4.7.11 item 8
//
// diagnosis: under a sandboxed runtime, a fault signal the agent UID sent to
// the supervisor stopped it answering ping (the fault signals must stay
// blocked), a synchronous fault in a test build was not delivered through
// the block (the harness case failed), SIGKILL or SIGSTOP from the agent
// stopped an immune supervisor or did not retire a pod whose namespace init
// is not immune, or a fork bomb in the runtime container stopped the
// supervisor answering ping.
func TestSupervisorSignalsAndLimitsUnderSandboxedRuntimes_spec_4_7_10(t *testing.T) {
	_ = requireCloud(t)
	cli := kube(t)
	for _, rt := range sandboxedRuntimes {
		t.Run(rt.Name, func(t *testing.T) {
			e := newSandboxedEnv(t, cli, rt)
			t.Run("fault signals leave the supervisor answering ping", func(t *testing.T) {
				pod, _ := e.startProbePod(t, "faults")
				for _, sig := range faultSignals {
					e.attack(t, pod, superviseprobe.AttackSignalPID1, strconv.Itoa(sig))
				}
				time.Sleep(sandboxedPingWindow)
				e.requireAnswering(t, pod, "the fault signals")
			})
			t.Run("synchronous fault in a test build is delivered through the block", func(t *testing.T) {
				e.requireSyncFaultDelivered(t)
			})
			for _, c := range []struct {
				name, sig, immunity string
			}{
				{"SIGSTOP from the agent", "19", supervisorselftest.PropertyInitSigstopImmune},
				{"SIGKILL from the agent", "9", supervisorselftest.PropertyInitSigkillImmune},
			} {
				t.Run(c.name, func(t *testing.T) {
					pod, props := e.startProbePod(t, "sig-"+c.sig)
					e.attack(t, pod, superviseprobe.AttackSignalPID1, c.sig)
					if props[c.immunity] {
						time.Sleep(sandboxedPingWindow)
						e.requireAnswering(t, pod, c.name+" to an immune namespace init")
						return
					}
					e.requireRetired(t, pod, c.name)
				})
			}
			t.Run("fork bomb leaves the supervisor answering ping", func(t *testing.T) {
				pod, _ := e.startProbePod(t, "forkbomb")
				r := e.attack(t, pod, superviseprobe.AttackForkBomb)
				t.Logf("fork bomb under %s started %s children (stopped by %q)", e.rc, r.Detail, r.Errno)
				time.Sleep(sandboxedPingWindow)
				e.requireAnswering(t, pod, "the fork bomb")
			})
		})
	}
}

// requireSyncFaultDelivered runs the harness's synchronous-fault case under
// the RuntimeClass and asserts that it passed: the kernel delivered SIGSEGV
// through the supervisor's block and ended the process.
func (e sandboxedEnv) requireSyncFaultDelivered(t *testing.T) {
	t.Helper()
	pod := supervisedpod.SyncFaultPod(e.spec("sync-fault"))
	e.create(t, pod)
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		p, err := e.cli.CoreV1().Pods(e.namespace).Get(context.Background(), pod.Name, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("get pod %s: %v", pod.Name, err)
		}
		switch p.Status.Phase {
		case corev1.PodSucceeded:
			return
		case corev1.PodFailed:
			t.Fatalf("under %s the synchronous fault was not delivered through the block:\n%s", e.rc, e.logs(pod.Name, "sync-fault"))
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("the synchronous-fault pod under %s did not finish within 5m", e.rc)
}

// spec: §4.7.11 (Runtime supervisor, Property self-test)
// diagnosis: a failure means the sandboxed supervisor cases skip as "no
// RuntimeClass" on a lookup error other than NotFound, so an API or RBAC
// failure on a provisioned cluster hides the cases instead of failing them.
//
// TestRuntimeClassLookupSkipsOnlyOnAbsence_spec_4_7_11 pins the RuntimeClass lookup the
// sandboxed cases resolve before running: an absent candidate falls through
// to the next, every candidate absent is errNoRuntimeClass, and any other
// lookup error is returned as itself.
func TestRuntimeClassLookupSkipsOnlyOnAbsence_spec_4_7_11(t *testing.T) {
	gr := schema.GroupResource{Group: "node.k8s.io", Resource: "runtimeclasses"}
	cases := []struct {
		name      string
		present   []string
		failWith  error
		want      string
		wantAbsnt bool
		wantErr   bool
	}{
		{name: "second candidate present", present: []string{"kata-containers"}, want: "kata-containers"},
		{name: "all absent", wantAbsnt: true, wantErr: true},
		{name: "forbidden", failWith: apierrors.NewForbidden(gr, "kata", errors.New("rbac")), wantErr: true},
		{name: "timeout", failWith: apierrors.NewTimeoutError("slow", 1), wantErr: true},
		{name: "unreachable", failWith: errors.New("dial tcp: connection refused"), wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var objs []runtime.Object
			for _, n := range c.present {
				objs = append(objs, &nodev1.RuntimeClass{ObjectMeta: metav1.ObjectMeta{Name: n}, Handler: n})
			}
			cli := fake.NewSimpleClientset(objs...)
			if c.failWith != nil {
				cli.PrependReactor("get", "runtimeclasses", func(k8stesting.Action) (bool, runtime.Object, error) {
					return true, nil, c.failWith
				})
			}
			got, err := firstRuntimeClass(context.Background(), cli, []string{"kata", "kata-containers"})
			if got != c.want || (err != nil) != c.wantErr || errors.Is(err, errNoRuntimeClass) != c.wantAbsnt {
				t.Fatalf("firstRuntimeClass = %q, %v; want %q, error %v, absent %v", got, err, c.want, c.wantErr, c.wantAbsnt)
			}
		})
	}
}
