// SPDX-License-Identifier: MIT

package sandbox

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
)

const (
	supNS       = "lenny-agents"
	supPool     = "pool-a"
	supTemplate = "tmpl-a"
	supRuntime  = "agent-rt"
	supImage    = "ghcr.io/lennylabs/lenny-supervisor@sha256:aaaa"
)

// supRuntimeCR is a sidecar type: agent session-mode runtime with a command
// and env, which is restartable.
func supRuntimeCR(mut func(*lennyv1.RuntimeSpec)) *lennyv1.Runtime {
	rt := &lennyv1.Runtime{
		ObjectMeta: metav1.ObjectMeta{Name: supRuntime},
		Spec: lennyv1.RuntimeSpec{
			Type:    "agent",
			Image:   "ghcr.io/acme/agent@sha256:bbbb",
			Command: []string{"/usr/local/bin/agent"},
			Env:     []lennyv1.RuntimeEnvVar{{Name: "VENV", Value: "/opt/venv"}},
		},
	}
	if mut != nil {
		mut(&rt.Spec)
	}
	return rt
}

func supTemplateCR(runtimeProcess string) *lennyv1.SandboxTemplate {
	t := &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: supTemplate, Namespace: supNS},
		Spec:       lennyv1.SandboxTemplateSpec{RuntimeRef: supRuntime},
	}
	if runtimeProcess != "" {
		t.Spec.SessionPolicy = &lennyv1.SessionPolicy{Recycle: &lennyv1.RecyclePolicy{RuntimeProcess: runtimeProcess}}
	}
	return t
}

func supPoolCR(sdkWarmDisabled bool) *lennyv1.SandboxWarmPool {
	return &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: supPool, Namespace: supNS},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: supTemplate, SDKWarmDisabled: sdkWarmDisabled},
	}
}

func supSandbox(executionMode string) *lennyv1.Sandbox {
	return &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sb-1", Namespace: supNS, UID: "uid-1"},
		Spec: lennyv1.SandboxSpec{
			RuntimeRef: supRuntime, PoolRef: supPool, IsolationProfile: "standard", ExecutionMode: executionMode,
		},
	}
}

// supCreate runs createPod against a fake client seeded with objs and
// returns the created pod, or the createPod error.
func supCreate(t *testing.T, sb *lennyv1.Sandbox, funcs *interceptor.Funcs, objs ...client.Object) (*corev1.Pod, error) {
	t.Helper()
	scheme := sdkWarmScheme(t)
	b := fake.NewClientBuilder().WithScheme(scheme).WithObjects(append(objs, sb)...)
	if funcs != nil {
		b = b.WithInterceptorFuncs(*funcs)
	}
	cl := b.Build()
	r := &Reconciler{
		Client: cl, Scheme: scheme, AdapterImage: "ghcr.io/lennylabs/lenny-adapter:v1",
		SupervisorImage: supImage,
		SuperviseTiming: podspec.SuperviseTiming{PingInterval: time.Second, PingTimeout: 2 * time.Second, WaitTimeout: time.Minute, WarmUpTimeout: 30 * time.Second},
	}
	if err := r.createPod(context.Background(), sb); err != nil {
		var pods corev1.PodList
		if lerr := cl.List(context.Background(), &pods); lerr != nil {
			t.Fatalf("list pods: %v", lerr)
		}
		if len(pods.Items) != 0 {
			t.Fatalf("createPod returned %v but created %d pods", err, len(pods.Items))
		}
		return nil, err
	}
	var pod corev1.Pod
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: supNS, Name: sb.Name}, &pod); err != nil {
		t.Fatalf("get pod: %v", err)
	}
	return &pod, nil
}

func adapterArgs(t *testing.T, pod *corev1.Pod) []string {
	t.Helper()
	for _, c := range pod.Spec.Containers {
		if c.Name == podspec.AdapterContainerName {
			return c.Args
		}
	}
	t.Fatal("no adapter container")
	return nil
}

// spec: 5.2 (Runtime-process lifetime), 5.1 (command), 4.7.10 (Runtime supervisor)
func TestCreatePodRefusesRestartOnNonRestartableRuntime_spec_5_2(t *testing.T) {
	cases := map[string]func(*lennyv1.RuntimeSpec){
		"service-mode runtime": func(s *lennyv1.RuntimeSpec) { s.ExecutionMode = "service" },
		"preConnect runtime":   func(s *lennyv1.RuntimeSpec) { s.Capabilities = &lennyv1.RuntimeCapabilitiesCRD{PreConnect: true} },
		"runtime without command": func(s *lennyv1.RuntimeSpec) {
			s.Command = nil
			s.DeploymentModel = string(podspec.DeploymentEmbedded)
			s.Env = nil
		},
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := supCreate(t, supSandbox(""), nil, supRuntimeCR(mut), supPoolCR(false), supTemplateCR("restart"))
			if !errors.Is(err, errRestartNotRestartable) {
				t.Fatalf("createPod err = %v, want errRestartNotRestartable", err)
			}
		})
	}
}

// spec: 5.1 (command), 4.7.10 (Runtime supervisor)
func TestCreatePodRefusesAgentSidecarRuntimeWithoutCommand_spec_5_1(t *testing.T) {
	rt := supRuntimeCR(func(s *lennyv1.RuntimeSpec) { s.Command, s.Env = nil, nil })
	_, err := supCreate(t, supSandbox(""), nil, rt, supPoolCR(false), supTemplateCR(""))
	if !errors.Is(err, errAgentSidecarWithoutCommand) {
		t.Fatalf("createPod err = %v, want errAgentSidecarWithoutCommand", err)
	}
}

// spec: 5.2 (Runtime-process lifetime), 4.7.10 (Runtime supervisor; Runtime supervisor, Environment), 17.2 (item 5)
func TestCreatePodRendersSupervisedRestartPodForRestartableRuntime_spec_5_2(t *testing.T) {
	pod, err := supCreate(t, supSandbox(""), nil, supRuntimeCR(nil), supPoolCR(false), supTemplateCR("restart"))
	if err != nil {
		t.Fatalf("createPod: %v", err)
	}
	if got := pod.Labels["lenny.dev/runtime-process"]; got != "restart" {
		t.Errorf("label = %q, want restart", got)
	}
	args := adapterArgs(t, pod)
	for _, f := range []string{"--runtime-process=restart", "--launch-mode=session", "--supervise-socket=@lenny-supervise", "--supervise-ping-interval=1s", "--supervise-ping-timeout=2s", "--supervise-wait-timeout=1m0s", "--warm-up-timeout=30s"} {
		if !slices.Contains(args, f) {
			t.Errorf("adapter args %v lack %s", args, f)
		}
	}
	if len(pod.Spec.InitContainers) != 1 || pod.Spec.InitContainers[0].Image != supImage {
		t.Errorf("init containers = %+v, want the stage container on the supervisor image", pod.Spec.InitContainers)
	}
	var rtCmd []string
	for _, c := range pod.Spec.Containers {
		if c.Name == podspec.RuntimeContainerName {
			rtCmd = c.Command
		}
	}
	if !slices.Contains(rtCmd, "VENV=/opt/venv") || rtCmd[len(rtCmd)-1] != "/usr/local/bin/agent" {
		t.Errorf("runtime command = %v, want the runtime env and command", rtCmd)
	}
}

// spec: 4.7.10 (Runtime supervisor, Launch; Runtime supervisor, SDK-warm), 6.1 (Circuit-breaker for SDK-warm pools)
func TestCreatePodLaunchModeFromExecutionModePreConnectAndBreaker_spec_4_7_10(t *testing.T) {
	preConnect := func(s *lennyv1.RuntimeSpec) { s.Capabilities = &lennyv1.RuntimeCapabilitiesCRD{PreConnect: true} }
	serviceRuntime := func(s *lennyv1.RuntimeSpec) { s.ExecutionMode = "service" }
	cases := []struct {
		name     string
		sbMode   string
		mut      func(*lennyv1.RuntimeSpec)
		disabled bool
		want     string
	}{
		{"session", "", nil, false, "session"},
		// The Sandbox's execution mode is the pool's; it governs even when
		// the Runtime declares none.
		{"service sandbox, runtime declares no mode", "service", nil, false, "service"},
		// A session Sandbox renders no service launch even when the Runtime
		// declares service.
		{"session sandbox, service runtime", "session", serviceRuntime, false, "session"},
		{"preConnect, breaker closed", "", preConnect, false, "preconnect-sdk-warm"},
		{"preConnect, breaker open", "", preConnect, true, "preconnect-pod-warm"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pod, err := supCreate(t, supSandbox(c.sbMode), nil, supRuntimeCR(c.mut), supPoolCR(c.disabled), supTemplateCR(""))
			if err != nil {
				t.Fatalf("createPod: %v", err)
			}
			if args := adapterArgs(t, pod); !slices.Contains(args, "--launch-mode="+c.want) {
				t.Errorf("adapter args %v lack --launch-mode=%s", args, c.want)
			}
			if pod.Labels["lenny.dev/runtime-process"] != "keep" {
				t.Errorf("label = %q, want keep", pod.Labels["lenny.dev/runtime-process"])
			}
		})
	}
}

// spec: 4.7.10 (Runtime supervisor, SDK-warm), 6.1 (Circuit-breaker for SDK-warm pools)
func TestCreatePodFailedPoolReadOnPreConnectCreatesNoPod_spec_6_1(t *testing.T) {
	preConnect := supRuntimeCR(func(s *lennyv1.RuntimeSpec) { s.Capabilities = &lennyv1.RuntimeCapabilitiesCRD{PreConnect: true} })
	// A pool that does not exist fails the breaker read.
	if _, err := supCreate(t, supSandbox(""), nil, preConnect); err == nil {
		t.Error("preConnect pod created with no readable pool")
	}
	// A transient pool read error fails it too.
	boom := errors.New("apiserver unavailable")
	funcs := &interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
		if _, ok := obj.(*lennyv1.SandboxWarmPool); ok {
			return boom
		}
		return c.Get(ctx, key, obj, opts...)
	}}
	if _, err := supCreate(t, supSandbox(""), funcs, preConnect, supPoolCR(false), supTemplateCR("")); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the pool read error", err)
	}
}

// spec: 5.2 (Runtime-process lifetime)
func TestCreatePodLifetimeReadFailsClosedOnTransientError_spec_5_2(t *testing.T) {
	boom := errors.New("apiserver unavailable")
	funcs := &interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
		if _, ok := obj.(*lennyv1.SandboxTemplate); ok {
			return boom
		}
		return c.Get(ctx, key, obj, opts...)
	}}
	if _, err := supCreate(t, supSandbox(""), funcs, supRuntimeCR(nil), supPoolCR(false), supTemplateCR("restart")); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the template read error", err)
	}
	// A pool that is gone reads as a pool without the field: a keep pod.
	pod, err := supCreate(t, supSandbox(""), nil, supRuntimeCR(nil))
	if err != nil {
		t.Fatalf("createPod without a pool: %v", err)
	}
	if pod.Labels["lenny.dev/runtime-process"] != "keep" {
		t.Errorf("label = %q, want keep", pod.Labels["lenny.dev/runtime-process"])
	}
}

// spec: 4.7.10 (Runtime supervisor), 5.2 (Runtime-process lifetime)
func TestCreatePodUnsupervisedRuntimesCarryOnlyTheLifetimeLabel_spec_4_7_10(t *testing.T) {
	cases := map[string]*lennyv1.Runtime{
		"type mcp": supRuntimeCR(func(s *lennyv1.RuntimeSpec) { s.Type, s.Command, s.Env = "mcp", nil, nil }),
		"embedded": supRuntimeCR(func(s *lennyv1.RuntimeSpec) {
			s.DeploymentModel, s.Command, s.Env = string(podspec.DeploymentEmbedded), nil, nil
		}),
	}
	for name, rt := range cases {
		t.Run(name, func(t *testing.T) {
			pod, err := supCreate(t, supSandbox(""), nil, rt, supPoolCR(false), supTemplateCR("keep"))
			if err != nil {
				t.Fatalf("createPod: %v", err)
			}
			if len(pod.Spec.InitContainers) != 0 {
				t.Errorf("unsupervised pod has init containers %+v", pod.Spec.InitContainers)
			}
			if pod.Labels["lenny.dev/runtime-process"] != "keep" {
				t.Errorf("label = %q, want keep", pod.Labels["lenny.dev/runtime-process"])
			}
		})
	}
}

// spec: 4.7.10 (Runtime supervisor), 13.1 (Runtime supervisor volume)
func TestCreatePodSupervisedWithoutSupervisorImageCreatesNoPod_spec_4_7_10(t *testing.T) {
	scheme := sdkWarmScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(supRuntimeCR(nil), supPoolCR(false), supTemplateCR(""), supSandbox("")).Build()
	r := &Reconciler{Client: cl, Scheme: scheme, AdapterImage: "ghcr.io/lennylabs/lenny-adapter:v1"}
	if err := r.createPod(context.Background(), supSandbox("")); err == nil {
		t.Fatal("createPod rendered a supervised pod with no supervisor image")
	}
	err := cl.Get(context.Background(), client.ObjectKey{Namespace: supNS, Name: "sb-1"}, &corev1.Pod{})
	if !apierrors.IsNotFound(err) {
		t.Errorf("pod read err = %v, want NotFound", err)
	}
}
