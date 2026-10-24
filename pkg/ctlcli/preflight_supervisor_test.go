// SPDX-License-Identifier: MIT

package ctlcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/lennylabs/lenny/pkg/admission/webhook"
	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
	"github.com/lennylabs/lenny/pkg/podsecurity"
)

const (
	selftestNS    = "lenny-agents"
	selftestImage = "ghcr.io/lennylabs/lenny-supervisor@sha256:3333333333333333333333333333333333333333333333333333333333333333"
	passReport    = "nondumpable pass\nkillReach pass\ninitSigkillImmune pass\ninitSigstopImmune pass\n"
)

// selftestValues is a values file with the chart's default identities.
func selftestValues() preflightValues {
	var v preflightValues
	v.AgentNamespaces = append(v.AgentNamespaces, struct {
		Name string `json:"name"`
	}{Name: selftestNS}, struct {
		Name string `json:"name"`
	}{Name: "lenny-agents-kata"})
	v.Security.PodUIDs.Adapter = podspec.AdapterUID
	v.Security.PodUIDs.Agent = podspec.AgentUID
	v.Security.PodUIDs.CredReadersGID = podspec.CredReadersGID
	v.Controller.SupervisorImage = selftestImage
	return v
}

func builtSelftestPod(t *testing.T, runtimeClass string) *corev1.Pod {
	t.Helper()
	spec, err := selftestPodSpecFromValues(selftestValues(), runtimeClass, selftestPodPrefix+"test")
	if err != nil {
		t.Fatalf("selftestPodSpecFromValues: %v", err)
	}
	return buildSelftestPod(spec)
}

func decideSelftestPod(t *testing.T, pod *corev1.Pod, credReadersGID int64) *admissionv1.AdmissionResponse {
	t.Helper()
	raw, err := json.Marshal(pod)
	if err != nil {
		t.Fatalf("marshal pod: %v", err)
	}
	policy := podsecurity.RuntimeClassPolicy{GVisorRuntimeClass: "gvisor", KataRuntimeClass: "kata"}
	return webhook.PodSecurity(podspec.AdapterUID, podspec.AgentUID, credReadersGID,
		podspec.CredVolumeName, podspec.SupervisorVolumeName, policy)(
		context.Background(), &admissionv1.AdmissionRequest{
			UID: "preflight-supervisor", Operation: admissionv1.Create,
			Kind: metav1.GroupVersionKind{Kind: "Pod"}, Object: runtime.RawExtension{Raw: raw},
		},
	)
}

// spec: 24.2 (Preflight), 13.1 (Pod Security and Runtime Isolation, Container identity)
func TestPreflightSupervisorPodAdmittedByPodSecurity_spec_24_2(t *testing.T) {
	for _, rc := range []string{"runc", "gvisor", "kata"} {
		t.Run(rc, func(t *testing.T) {
			pod := builtSelftestPod(t, rc)
			if resp := decideSelftestPod(t, pod, podspec.CredReadersGID); !resp.Allowed {
				t.Fatalf("lenny-pod-security refused the self-test pod: %s", resp.Result.Message)
			}
		})
	}
	// The decider checks the fsGroup, so an install with another
	// lenny-cred-readers GID refuses a pod built for the default one.
	if resp := decideSelftestPod(t, builtSelftestPod(t, "runc"), podspec.CredReadersGID+1); resp.Allowed {
		t.Fatal("lenny-pod-security admitted a pod whose fsGroup is not the install's lenny-cred-readers GID")
	}
}

// spec: 24.2 (Preflight), 4.7.11 (Adapter-Agent Security Boundary, Runtime supervisor, Property self-test)
func TestPreflightSupervisorPodRendering_spec_24_2(t *testing.T) {
	pod := builtSelftestPod(t, "gvisor")
	if pod.Namespace != selftestNS {
		t.Errorf("namespace = %q, want the first agent namespace %q", pod.Namespace, selftestNS)
	}
	if pod.Spec.RuntimeClassName == nil || *pod.Spec.RuntimeClassName != "gvisor" {
		t.Errorf("runtimeClassName = %v, want gvisor", pod.Spec.RuntimeClassName)
	}
	if pod.Spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("restartPolicy = %q, want Never", pod.Spec.RestartPolicy)
	}
	sc := pod.Spec.SecurityContext
	if sc == nil || sc.FSGroup == nil || *sc.FSGroup != podspec.CredReadersGID ||
		len(sc.SupplementalGroups) != 1 || sc.SupplementalGroups[0] != podspec.CredReadersGID {
		t.Errorf("pod securityContext = %+v, want fsGroup and supplementalGroups of the lenny-cred-readers GID", sc)
	}
	if len(pod.Spec.Containers) != 1 || len(pod.Spec.InitContainers) != 0 || len(pod.Spec.Volumes) != 0 {
		t.Fatalf("pod carries %d containers, %d init containers, %d volumes; want one container and nothing else",
			len(pod.Spec.Containers), len(pod.Spec.InitContainers), len(pod.Spec.Volumes))
	}
	c := pod.Spec.Containers[0]
	if c.Image != selftestImage || len(c.Args) != 1 || c.Args[0] != "selftest" || len(c.Command) != 0 {
		t.Errorf("container image %q args %v command %v, want the supervisor image's entrypoint in selftest mode", c.Image, c.Args, c.Command)
	}
	if uid := *c.SecurityContext.RunAsUser; uid != podspec.SupervisorStageUID {
		t.Errorf("runAsUser = %d, want the supervisor stage UID %d", uid, podspec.SupervisorStageUID)
	}
	if _, ok := c.Resources.Requests[corev1.ResourceEphemeralStorage]; !ok {
		t.Error("container declares no ephemeral-storage request, which the agent-namespace ResourceQuota counts")
	}
}

// spec: 24.2 (Preflight)
func TestPreflightSupervisorRefusesIncompleteValues_spec_24_2(t *testing.T) {
	cases := map[string]struct {
		mutate       func(*preflightValues)
		runtimeClass string
	}{
		"no runtime class":         {func(*preflightValues) {}, ""},
		"unnamed agent namespace":  {func(v *preflightValues) { v.AgentNamespaces[0].Name = "" }, "runc"},
		"no supervisor image":      {func(v *preflightValues) { v.Controller.SupervisorImage = "" }, "runc"},
		"adapter UID is stage UID": {func(v *preflightValues) { v.Security.PodUIDs.Adapter = podspec.SupervisorStageUID }, "runc"},
		"agent UID is stage UID":   {func(v *preflightValues) { v.Security.PodUIDs.Agent = podspec.SupervisorStageUID }, "runc"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			v := selftestValues()
			tc.mutate(&v)
			if _, err := selftestPodSpecFromValues(v, tc.runtimeClass, "p"); !errors.Is(err, errSelftestPodInput) {
				t.Fatalf("err = %v, want errSelftestPodInput", err)
			}
		})
	}
}

// An install values file that sets only overrides leaves agentNamespaces and
// security.podUIDs to the chart defaults. The self-test pod resolves them to
// the same defaults the controller and lenny-pod-security apply, so it lands
// in the chart's first agent namespace with the default lenny-cred-readers
// GID and is admitted.
// spec: 24.2 (Preflight), 13.1 (Pod Security and Runtime Isolation, Container identity)
func TestPreflightSupervisorOverrideOnlyValuesUseChartDefaults_spec_24_2(t *testing.T) {
	path := filepath.Join(t.TempDir(), "values.yaml")
	if err := os.WriteFile(path, []byte("controller:\n  supervisorImage: "+selftestImage+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	v, err := readPreflightValues(path)
	if err != nil {
		t.Fatalf("readPreflightValues: %v", err)
	}
	spec, err := selftestPodSpecFromValues(v, "runc", selftestPodPrefix+"test")
	if err != nil {
		t.Fatalf("selftestPodSpecFromValues refused an override-only values file: %v", err)
	}
	pod := buildSelftestPod(spec)
	if pod.Namespace != "lenny-agents" {
		t.Errorf("namespace = %q, want the chart's first agent namespace lenny-agents", pod.Namespace)
	}
	sc := pod.Spec.SecurityContext
	if *sc.FSGroup != podspec.CredReadersGID || sc.SupplementalGroups[0] != podspec.CredReadersGID {
		t.Errorf("fsGroup %d supplementalGroups %v, want the default lenny-cred-readers GID %d", *sc.FSGroup, sc.SupplementalGroups, podspec.CredReadersGID)
	}
	if resp := decideSelftestPod(t, pod, podspec.CredReadersGID); !resp.Allowed {
		t.Fatalf("lenny-pod-security refused the self-test pod built from chart defaults: %s", resp.Result.Message)
	}
}

// Values the file does set win over the chart defaults.
// spec: 24.2 (Preflight), 13.1 (Pod Security and Runtime Isolation, Container identity)
func TestPreflightSupervisorValuesOverrideChartDefaults_spec_24_2(t *testing.T) {
	const gid int64 = 4242
	v := selftestValues()
	v.AgentNamespaces[0].Name = "team-agents"
	v.Security.PodUIDs.CredReadersGID = gid
	spec, err := selftestPodSpecFromValues(v, "runc", "p")
	if err != nil {
		t.Fatalf("selftestPodSpecFromValues: %v", err)
	}
	if spec.Namespace != "team-agents" || spec.CredReadersGID != gid {
		t.Errorf("namespace %q GID %d, want team-agents and %d", spec.Namespace, spec.CredReadersGID, gid)
	}
	if resp := decideSelftestPod(t, buildSelftestPod(spec), gid); !resp.Allowed {
		t.Fatalf("lenny-pod-security refused the self-test pod for an overridden GID: %s", resp.Result.Message)
	}
}

// podRequest is one request the recording fake received.
type podRequest struct {
	verb, resource, name string
}

// recordingPods is a selftestPodClient that stores at most one pod, records
// every request, and lets a case script each verb's outcome.
type recordingPods struct {
	mu       sync.Mutex
	pods     map[string]*corev1.Pod
	requests []podRequest

	createStoresThenFails error
	createErr             error
	phases                []corev1.PodPhase // phase each Get reports; the last repeats
	getErr                error
	logs                  string
	logsErr               error
	deleteErr             error
	gets                  int
}

func newRecordingPods() *recordingPods {
	return &recordingPods{pods: map[string]*corev1.Pod{}, phases: []corev1.PodPhase{corev1.PodPending, corev1.PodSucceeded}, logs: passReport}
}

func (f *recordingPods) record(verb, resource, name string) {
	f.requests = append(f.requests, podRequest{verb, resource, name})
}

func (f *recordingPods) Create(_ context.Context, pod *corev1.Pod) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("create", "pods", pod.Name)
	if f.createErr != nil {
		return f.createErr
	}
	f.pods[pod.Name] = pod.DeepCopy()
	return f.createStoresThenFails
}

func (f *recordingPods) Get(_ context.Context, name string) (*corev1.Pod, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("get", "pods", name)
	if f.getErr != nil {
		return nil, f.getErr
	}
	pod, ok := f.pods[name]
	if !ok {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "pods"}, name)
	}
	out := pod.DeepCopy()
	out.Status.Phase = f.phases[min(f.gets, len(f.phases)-1)]
	if out.Status.Phase == corev1.PodPending {
		out.Status.ContainerStatuses = []corev1.ContainerStatus{{
			Name: podspec.SelftestContainerName, State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"}},
		}}
	}
	f.gets++
	return out, nil
}

func (f *recordingPods) Delete(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("delete", "pods", name)
	if f.deleteErr != nil {
		return f.deleteErr
	}
	delete(f.pods, name)
	return nil
}

func (f *recordingPods) Logs(_ context.Context, name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("get", "pods/log", name)
	return f.logs, f.logsErr
}

// permissionCell is the §24.2 permission cell of `lenny-ctl preflight
// supervisor`: create, get, and delete on pods, and get on pods/log.
var permissionCell = map[[2]string]bool{
	{"create", "pods"}: true, {"get", "pods"}: true, {"delete", "pods"}: true, {"get", "pods/log"}: true,
}

func runSelftest(t *testing.T, f *recordingPods, wait time.Duration) (int, string, string, *corev1.Pod) {
	t.Helper()
	pod := builtSelftestPod(t, "runc")
	var out, errb bytes.Buffer
	r := selftestRun{pods: f, poll: time.Millisecond, wait: wait, deleteTimeout: time.Second}
	code := r.run(context.Background(), pod, &out, &errb)
	return code, out.String(), errb.String(), pod
}

// spec: 24.2 (Preflight), 4.7.11 (Adapter-Agent Security Boundary, Runtime supervisor, Property self-test)
func TestPreflightSupervisorRunOutcomes_spec_24_2(t *testing.T) {
	cases := map[string]struct {
		setup    func(*recordingPods)
		wait     time.Duration
		wantCode int
		wantOut  []string
		wantErr  string
		wantLogs bool
	}{
		"every property passes": {
			setup: func(*recordingPods) {}, wantCode: 0, wantLogs: true,
			wantOut: []string{"PASS nondumpable: gates a restart pod", "PASS killReach: gates a restart pod", "PASS initSigkillImmune: reported; gates no pod", "PASS initSigstopImmune: reported; gates no pod"},
		},
		"non-dumpable mark fails": {
			setup: func(f *recordingPods) {
				f.logs = strings.Replace(passReport, "nondumpable pass", "nondumpable fail", 1)
				f.phases = []corev1.PodPhase{corev1.PodFailed}
			},
			wantCode: 1, wantLogs: true, wantOut: []string{"FAIL nondumpable: gates a restart pod"}, wantErr: "gates a restart pod failed",
		},
		"kill reach fails": {
			setup: func(f *recordingPods) {
				f.logs = strings.Replace(passReport, "killReach pass", "killReach fail", 1)
				f.phases = []corev1.PodPhase{corev1.PodFailed}
			},
			wantCode: 1, wantLogs: true, wantOut: []string{"FAIL killReach: gates a restart pod"}, wantErr: "gates a restart pod failed",
		},
		"gating failure reported with a zero exit still fails": {
			setup:    func(f *recordingPods) { f.logs = strings.Replace(passReport, "killReach pass", "killReach fail", 1) },
			wantCode: 1, wantLogs: true, wantErr: "gates a restart pod failed",
		},
		"init immunity fails and gates nothing": {
			setup: func(f *recordingPods) {
				f.logs = "nondumpable pass\nkillReach pass\ninitSigkillImmune fail\ninitSigstopImmune fail\n"
			},
			wantCode: 0, wantLogs: true, wantOut: []string{"FAIL initSigkillImmune: reported; gates no pod", "FAIL initSigstopImmune: reported; gates no pod"},
		},
		"non-zero exit with every gating property passing": {
			setup:    func(f *recordingPods) { f.phases = []corev1.PodPhase{corev1.PodFailed} },
			wantCode: 1, wantLogs: true, wantErr: "ended in phase Failed",
		},
		"missing line": {
			setup:    func(f *recordingPods) { f.logs = "nondumpable pass\nkillReach pass\ninitSigkillImmune pass\n" },
			wantCode: 1, wantLogs: true, wantErr: "property initSigstopImmune is missing",
		},
		"malformed line": {
			setup:    func(f *recordingPods) { f.logs = "nondumpable: yes\n" },
			wantCode: 1, wantLogs: true, wantErr: "malformed",
		},
		"create error after creation": {
			setup:    func(f *recordingPods) { f.createStoresThenFails = errors.New("stream closed after the write") },
			wantCode: 1, wantErr: "create pod",
		},
		"create refused": {
			setup: func(f *recordingPods) {
				f.createErr = apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "x", errors.New("admission denied"))
			},
			wantCode: 1, wantErr: "create pod",
		},
		"get error": {
			setup:    func(f *recordingPods) { f.getErr = errors.New("connection refused") },
			wantCode: 1, wantErr: "get pod",
		},
		"log read error": {
			setup:    func(f *recordingPods) { f.logsErr = errors.New("pods/log forbidden") },
			wantCode: 1, wantLogs: true, wantErr: "read the self-test report",
		},
		"timeout": {
			setup:    func(f *recordingPods) { f.phases = []corev1.PodPhase{corev1.PodPending} },
			wait:     20 * time.Millisecond,
			wantCode: 1, wantErr: "ImagePullBackOff",
		},
		"delete error after a passing run": {
			setup:    func(f *recordingPods) { f.deleteErr = errors.New("etcd unavailable") },
			wantCode: 1, wantLogs: true, wantErr: "delete pod",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newRecordingPods()
			tc.setup(f)
			wait := tc.wait
			if wait == 0 {
				wait = 5 * time.Second
			}
			code, out, errOut, pod := runSelftest(t, f, wait)
			if code != tc.wantCode {
				t.Fatalf("exit = %d, want %d (stdout %q, stderr %q)", code, tc.wantCode, out, errOut)
			}
			for _, w := range tc.wantOut {
				if !strings.Contains(out, w) {
					t.Errorf("stdout %q does not report %q", out, w)
				}
			}
			if tc.wantErr != "" && !strings.Contains(errOut, tc.wantErr) {
				t.Errorf("stderr %q does not contain %q", errOut, tc.wantErr)
			}
			assertDeletedOnEveryPath(t, f, pod.Name, tc.wantLogs)
		})
	}
}

// assertDeletedOnEveryPath checks that the last request deletes the pod,
// that the logs were or were not read, and that every request is inside the
// §24.2 permission cell and names the run's pod.
func assertDeletedOnEveryPath(t *testing.T, f *recordingPods, name string, wantLogs bool) {
	t.Helper()
	last := f.requests[len(f.requests)-1]
	if last.verb != "delete" || last.resource != "pods" || last.name != name {
		t.Errorf("last request = %+v, want the delete of pod %s", last, name)
	}
	readLogs := false
	for _, r := range f.requests {
		if !permissionCell[[2]string{r.verb, r.resource}] {
			t.Errorf("request %s %s is outside the §24.2 permission cell", r.verb, r.resource)
		}
		if r.name != name {
			t.Errorf("request %+v names a pod other than %s", r, name)
		}
		readLogs = readLogs || r.resource == "pods/log"
	}
	if readLogs != wantLogs {
		t.Errorf("logs read = %v, want %v", readLogs, wantLogs)
	}
	if f.deleteErr == nil && len(f.pods) != 0 {
		t.Errorf("pods left after the run: %d", len(f.pods))
	}
}

// spec: 24.2 (Preflight)
func TestPreflightSupervisorDeletesAfterCancellation_spec_24_2(t *testing.T) {
	f := newRecordingPods()
	f.phases = []corev1.PodPhase{corev1.PodPending}
	ctx, cancel := context.WithCancel(context.Background())
	pod := builtSelftestPod(t, "runc")
	r := selftestRun{pods: cancelOnGet{f, cancel}, poll: time.Millisecond, wait: time.Minute, deleteTimeout: time.Second}
	var out, errb bytes.Buffer
	if code := r.run(ctx, pod, &out, &errb); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	assertDeletedOnEveryPath(t, f, pod.Name, false)
}

// cancelOnGet cancels the command's context on the first completion read,
// as an interrupt during the wait does.
type cancelOnGet struct {
	*recordingPods
	cancel context.CancelFunc
}

func (c cancelOnGet) Get(ctx context.Context, name string) (*corev1.Pod, error) {
	c.cancel()
	return c.recordingPods.Get(ctx, name)
}

// spec: 24.2 (Preflight)
// An interrupt or termination signal delivered to the process during the
// completion wait cancels the run, and the command still deletes its pod.
// Without the signal-cancelled context the default handler would end the
// test process with the pod left behind.
func TestPreflightSupervisorDeletesPodOnSignal_spec_24_2(t *testing.T) {
	for name, sig := range map[string]syscall.Signal{"interrupt": syscall.SIGINT, "terminate": syscall.SIGTERM} {
		t.Run(name, func(t *testing.T) {
			values := filepath.Join(t.TempDir(), "values.yaml")
			doc := "agentNamespaces:\n  - name: " + selftestNS + "\ncontroller:\n  supervisorImage: " + selftestImage + "\n"
			if err := os.WriteFile(values, []byte(doc), 0o600); err != nil {
				t.Fatal(err)
			}
			f := newRecordingPods()
			f.phases = []corev1.PodPhase{corev1.PodPending}
			cmd := preflightSupervisor{
				newPods:       func(string) (selftestPodClient, error) { return &signalOnGet{recordingPods: f, sig: sig}, nil },
				poll:          time.Millisecond,
				deleteTimeout: time.Second,
			}
			var out, errb bytes.Buffer
			args := []string{"--runtime-class", "runc", "--config", values, "--wait-timeout", "1m"}
			if code := cmd.run(context.Background(), args, &out, &errb); code != 1 {
				t.Fatalf("exit = %d, want 1 (stderr %q)", code, errb.String())
			}
			if !strings.Contains(errb.String(), context.Canceled.Error()) {
				t.Errorf("stderr %q, want the wait ended by the signal's cancellation", errb.String())
			}
			assertDeletedOnEveryPath(t, f, f.requests[0].name, false)
			if len(f.pods) != 0 {
				t.Errorf("pods left after the signal: %d", len(f.pods))
			}
		})
	}
}

// signalOnGet sends a signal to the test process on the first completion
// read, as an operator pressing Ctrl-C or a SIGTERM during the wait does.
type signalOnGet struct {
	*recordingPods
	sig  syscall.Signal
	once sync.Once
}

func (s *signalOnGet) Get(ctx context.Context, name string) (*corev1.Pod, error) {
	var err error
	s.once.Do(func() { err = syscall.Kill(os.Getpid(), s.sig) })
	if err != nil {
		return nil, err
	}
	return s.recordingPods.Get(ctx, name)
}

// spec: 24.2 (Preflight)
// The production client goes through the typed pod client. Its requests,
// the completion wait included, are create, get, and delete on pods and get
// on pods/log in the agent namespace, with no watch or list.
func TestPreflightSupervisorClientRequestsWithinPermissionCell_spec_24_2(t *testing.T) {
	cs := k8sfake.NewSimpleClientset()
	gets := 0
	cs.PrependReactor("get", "pods", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if a.GetSubresource() != "" {
			return false, nil, nil
		}
		gets++
		phase := corev1.PodPending
		if gets > 1 {
			phase = corev1.PodSucceeded
		}
		name := a.(k8stesting.GetAction).GetName()
		return true, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: selftestNS}, Status: corev1.PodStatus{Phase: phase}}, nil
	})
	pod := builtSelftestPod(t, "runc")
	r := selftestRun{pods: clientsetPods{pods: cs.CoreV1().Pods(selftestNS)}, poll: time.Millisecond, wait: 5 * time.Second, deleteTimeout: time.Second}
	var out, errb bytes.Buffer
	// The fake clientset's log body is not a self-test report, so the run
	// fails on the report after reading it, which still exercises every verb.
	if code := r.run(context.Background(), pod, &out, &errb); code != 1 || !strings.Contains(errb.String(), "malformed") {
		t.Fatalf("exit = %d stderr %q, want 1 with a malformed report", code, errb.String())
	}
	seen := map[[2]string]bool{}
	for _, a := range cs.Actions() {
		res := a.GetResource().Resource
		if a.GetSubresource() != "" {
			res += "/" + a.GetSubresource()
		}
		key := [2]string{a.GetVerb(), res}
		if !permissionCell[key] {
			t.Errorf("request %s %s is outside the §24.2 permission cell", key[0], key[1])
		}
		if a.GetNamespace() != selftestNS {
			t.Errorf("request %s %s in namespace %q, want %q", key[0], key[1], a.GetNamespace(), selftestNS)
		}
		seen[key] = true
	}
	for key := range permissionCell {
		if !seen[key] {
			t.Errorf("no %s %s request was made", key[0], key[1])
		}
	}
	if gets < 2 {
		t.Errorf("completion reads = %d, want the wait to poll get until the pod ends", gets)
	}
	if _, err := cs.Tracker().Get(corev1.SchemeGroupVersion.WithResource("pods"), selftestNS, pod.Name); !apierrors.IsNotFound(err) {
		t.Errorf("pod still present after the run: %v", err)
	}
}

// spec: 24.2 (Preflight)
func TestPreflightSupervisorCommandRefusesBadInvocation_spec_24_2(t *testing.T) {
	dir := t.TempDir()
	incomplete := filepath.Join(dir, "values.yaml")
	if err := os.WriteFile(incomplete, []byte("agentNamespaces:\n  - name: lenny-agents\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		args     []string
		wantCode int
		wantErr  string
	}{
		"no flags":             {[]string{"supervisor"}, 2, "required"},
		"no config":            {[]string{"supervisor", "--runtime-class", "runc"}, 2, "required"},
		"non-positive wait":    {[]string{"supervisor", "--runtime-class", "runc", "--config", incomplete, "--wait-timeout", "0s"}, 2, "positive"},
		"unknown flag":         {[]string{"supervisor", "--watch"}, 2, ""},
		"unreadable values":    {[]string{"supervisor", "--runtime-class", "runc", "--config", filepath.Join(dir, "absent.yaml")}, 1, "read --config"},
		"values with no image": {[]string{"supervisor", "--runtime-class", "runc", "--config", incomplete}, 1, "controller.supervisorImage"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var out, errb bytes.Buffer
			if code := cmdPreflight(context.Background(), nil, tc.args, &out, &errb); code != tc.wantCode {
				t.Fatalf("exit = %d, want %d (stderr %q)", code, tc.wantCode, errb.String())
			}
			if !strings.Contains(errb.String(), tc.wantErr) {
				t.Errorf("stderr %q does not contain %q", errb.String(), tc.wantErr)
			}
		})
	}
}

// spec: 24.2 (Preflight)
func TestPreflightValuesDecodeSupervisorKeys_spec_24_2(t *testing.T) {
	path := filepath.Join(t.TempDir(), "values.yaml")
	doc := "agentNamespaces:\n  - name: ns-a\n  - name: ns-b\nsecurity:\n  podUIDs:\n    adapter: 1001\n    agent: 1002\n    credReadersGID: 1003\ncontroller:\n  supervisorImage: " + selftestImage + "\n"
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	pod, err := selftestPodFromFlags(path, "runc")
	if err != nil {
		t.Fatalf("selftestPodFromFlags: %v", err)
	}
	if pod.Namespace != "ns-a" || *pod.Spec.SecurityContext.FSGroup != 1003 || pod.Spec.Containers[0].Image != selftestImage {
		t.Errorf("pod namespace %q fsGroup %d image %q, want ns-a, 1003, and the values image", pod.Namespace, *pod.Spec.SecurityContext.FSGroup, pod.Spec.Containers[0].Image)
	}
	if !strings.HasPrefix(pod.Name, selftestPodPrefix) || pod.Name == selftestPodPrefix {
		t.Errorf("pod name %q, want a minted name under %q", pod.Name, selftestPodPrefix)
	}
}

// spec: 24.2 (Preflight)
func TestPreflightSupervisorBuildsClientFromKubeconfig_spec_24_2(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kubeconfig")
	doc := `apiVersion: v1
kind: Config
clusters:
- name: c
  cluster:
    server: https://127.0.0.1:6443
contexts:
- name: x
  context:
    cluster: c
    user: u
current-context: x
users:
- name: u
  user:
    token: t
`
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", path)
	pods, err := kubeSelftestPods(selftestNS)
	if err != nil || pods == nil {
		t.Fatalf("kubeSelftestPods = %v, %v; want a client from the current context", pods, err)
	}
}
