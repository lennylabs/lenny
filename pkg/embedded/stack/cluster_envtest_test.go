// SPDX-License-Identifier: MIT

package stack_test

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1alpha1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/embedded/stack"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// controlPlaneDeploymentFixture builds a minimal control-plane Deployment in
// the default namespace for the envtest, so the cluster-backed status read and
// the rollout-restart have a real Deployment object to act on.
func controlPlaneDeploymentFixture(name, component string) *appsv1.Deployment {
	replicas := int32(1)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"lenny.dev/component": component}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"lenny.dev/component": component}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: component, Image: "ghcr.io/lennylabs/lenny-" + component + ":dev"}},
				},
			},
		},
	}
}

// opsPodFixture builds a lenny-ops pod in the default namespace carrying the
// app: lenny-ops label the chart's ops-deployment.yaml stamps (the §13.2
// NET-051 pod-label exception), so the envtest can assert the logs selector
// matches the real ops label scheme rather than a fabricated
// lenny.dev/component=ops label.
func opsPodFixture(name, opsDeployment string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			Labels:    map[string]string{"app": opsDeployment},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "ops", Image: "ghcr.io/lennylabs/lenny-ops:dev"}},
		},
	}
}

// TestLogsOpsSelectorMatchesRealOpsLabel_spec_17_4 covers the §13.2 lenny-ops
// pod-label exception on the logs path against a real kube-apiserver: the
// lenny-ops Deployment stamps app: lenny-ops on its pods rather than the
// lenny.dev/component label the gateway and controller use, so the lenny logs
// ops selector must list pods by app: lenny-ops. The test creates an ops pod
// with the real label and asserts the resolved logs selector lists it, while a
// uniform lenny.dev/component=ops selector lists nothing.
//
// diagnosis: a failure means lenny logs ops selects zero pods against the real
// ops Deployment label scheme and prints "no running pods for ops" even when the
// mandatory lenny-ops Deployment is running, so an operator cannot read ops logs.
//
// spec: §17.4 line 179 (ops streams from the in-cluster pods), §13.2 (the
// lenny-ops pod-label exception).
func TestLogsOpsSelectorMatchesRealOpsLabel_spec_17_4(t *testing.T) {
	env := envtest.Start(t)
	ctx := context.Background()
	cfg := env.RESTConfig()
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("build core client: %v", err)
	}
	_, _, ops := stack.ControlPlaneDeploymentNamesForTest()
	if _, err := cs.CoreV1().Pods("default").Create(ctx, opsPodFixture("lenny-ops-xyz", ops), metav1.CreateOptions{}); err != nil {
		t.Fatalf("create ops pod: %v", err)
	}

	// The logs selector for ops must list the app: lenny-ops pod.
	opsSelector := stack.DeploymentPodSelectorForTest("ops")
	matched, err := cs.CoreV1().Pods("default").List(ctx, metav1.ListOptions{LabelSelector: opsSelector})
	if err != nil {
		t.Fatalf("list ops pods by logs selector %q: %v", opsSelector, err)
	}
	if len(matched.Items) != 1 {
		t.Errorf("logs selector %q matched %d pods, want the lenny-ops pod; the selector does not track the real ops label scheme",
			opsSelector, len(matched.Items))
	}

	// A uniform lenny.dev/component=ops selector (the divergence the fix
	// corrects) would list zero pods, since the ops Deployment carries no such
	// label. This guards against a regression back to the uniform selector.
	uniform, err := cs.CoreV1().Pods("default").List(ctx, metav1.ListOptions{LabelSelector: "lenny.dev/component=ops"})
	if err != nil {
		t.Fatalf("list ops pods by uniform selector: %v", err)
	}
	if len(uniform.Items) != 0 {
		t.Errorf("a lenny.dev/component=ops selector matched %d pods, want 0 (the ops Deployment carries no such label)", len(uniform.Items))
	}
}

// TestStatusReadsDeploymentReadiness_spec_17_4 covers the §17.4 cluster-backed
// status against a real kube-apiserver: the gateway, controller, and ops rows
// report their Deployment readiness. A Deployment whose status carries a ready
// replica reports healthy; one that does not reports down. The status
// subresource is set directly because envtest runs no Deployment controller.
//
// diagnosis: a failure means lenny status misreports the in-cluster control
// plane: either a ready Deployment reads as down or an un-ready one reads as up,
// so an operator cannot trust the readiness lenny status reports.
//
// spec: §17.4 (status reads the in-cluster control-plane Deployment readiness).
func TestStatusReadsDeploymentReadiness_spec_17_4(t *testing.T) {
	env := envtest.Start(t)
	ctx := context.Background()
	cfg := env.RESTConfig()
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("build core client: %v", err)
	}
	gw, ctl, ops := stack.ControlPlaneDeploymentNamesForTest()

	if _, err := cs.AppsV1().Deployments("default").Create(ctx, controlPlaneDeploymentFixture(gw, "gateway"), metav1.CreateOptions{}); err != nil {
		t.Fatalf("create gateway deployment: %v", err)
	}
	if _, err := cs.AppsV1().Deployments("default").Create(ctx, controlPlaneDeploymentFixture(ctl, "controller"), metav1.CreateOptions{}); err != nil {
		t.Fatalf("create controller deployment: %v", err)
	}
	if _, err := cs.AppsV1().Deployments("default").Create(ctx, controlPlaneDeploymentFixture(ops, "ops"), metav1.CreateOptions{}); err != nil {
		t.Fatalf("create ops deployment: %v", err)
	}

	// Drive gateway and controller to ready; leave ops not ready.
	markDeploymentReady(t, cs, gw)
	markDeploymentReady(t, cs, ctl)

	rows := stack.CollectClusterComponentsFromClientForTest(ctx, cs)
	byName := map[string]stack.ComponentStatus{}
	for _, r := range rows {
		byName[r.Name] = r
	}
	if c := byName["gateway"]; !c.Healthy {
		t.Errorf("gateway row = %+v, want healthy for a ready Deployment", c)
	}
	if c := byName["controller"]; !c.Healthy {
		t.Errorf("controller row = %+v, want healthy for a ready Deployment", c)
	}
	if c := byName["ops"]; c.Healthy {
		t.Errorf("ops row = %+v, want down for a not-ready Deployment", c)
	}
}

// markDeploymentReady advances the named Deployment's status to one ready
// replica caught up to its generation, so deploymentReady reads it healthy.
func markDeploymentReady(t *testing.T, cs kubernetes.Interface, name string) {
	t.Helper()
	ctx := context.Background()
	live, err := cs.AppsV1().Deployments("default").Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment %s: %v", name, err)
	}
	live.Status.ObservedGeneration = live.Generation
	live.Status.Replicas = 1
	live.Status.ReadyReplicas = 1
	if _, err := cs.AppsV1().Deployments("default").UpdateStatus(ctx, live, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update %s status: %v", name, err)
	}
}

// TestRolloutRestartBumpsDeploymentTemplate_spec_24_19 covers the §24.19/§17.4
// rollout-restart against a real kube-apiserver: rolling a control-plane
// Deployment stamps the restartedAt annotation onto its pod template (the same
// change kubectl rollout restart makes) and increments the Deployment's
// generation, so the API server schedules a fresh rollout, without changing the
// desired replica count.
//
// diagnosis: a failure means lenny restart does not roll the in-cluster
// Deployment: either the pod-template annotation was not stamped or the
// Deployment's desired spec changed, so a restart either does nothing or
// mutates the component.
//
// spec: §24.19 line 264 (the restart is a Deployment rollout-restart), §17.4.
func TestRolloutRestartBumpsDeploymentTemplate_spec_24_19(t *testing.T) {
	env := envtest.Start(t)
	ctx := context.Background()
	cfg := env.RESTConfig()
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("build core client: %v", err)
	}
	gw, _, _ := stack.ControlPlaneDeploymentNamesForTest()
	created, err := cs.AppsV1().Deployments("default").Create(ctx, controlPlaneDeploymentFixture(gw, "gateway"), metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create gateway deployment: %v", err)
	}
	beforeGen := created.Generation
	beforeReplicas := *created.Spec.Replicas

	if err := stack.RolloutRestartDeploymentForTest(ctx, cs, "default", gw); err != nil {
		t.Fatalf("rollout restart: %v", err)
	}

	rolled, err := cs.AppsV1().Deployments("default").Get(ctx, gw, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get rolled deployment: %v", err)
	}
	if _, ok := rolled.Spec.Template.Annotations[stack.RestartedAtAnnotationForTest()]; !ok {
		t.Errorf("rolled Deployment carries no %s annotation; template annotations: %v",
			stack.RestartedAtAnnotationForTest(), rolled.Spec.Template.Annotations)
	}
	if rolled.Generation <= beforeGen {
		t.Errorf("rolled Deployment generation = %d, want > %d (a pod-template change triggers a rollout)", rolled.Generation, beforeGen)
	}
	if *rolled.Spec.Replicas != beforeReplicas {
		t.Errorf("rolled Deployment replicas = %d, want unchanged %d", *rolled.Spec.Replicas, beforeReplicas)
	}
}

// TestEchoPoolReadyReflectsReadyCount_spec_17_4 covers the §17.4/§5.2 echo-pool
// readiness read against a real kube-apiserver with the lenny.dev CRDs
// installed: the seeded echo SandboxWarmPool reads not-ready until its
// status.readyCount reports a claimable idle pod, then ready. This is the
// "pool ready" signal lenny status reports independently of "gateway up". The
// status subresource is set directly because envtest runs no WarmPoolController.
//
// diagnosis: a failure means lenny status cannot tell a still-warming echo pool
// from a ready one, so it reports the pool ready before any pod is claimable and
// the first session start surprises the operator with a pool-warming response.
//
// spec: §5.2 (readyCount is the claimable-idle count), §17.4 (status
// distinguishes gateway-up from pool-ready).
func TestEchoPoolReadyReflectsReadyCount_spec_17_4(t *testing.T) {
	env := envtest.Start(t)
	ctx := context.Background()
	cfg := env.RESTConfig()

	const ns = "lenny-agents"
	if err := stack.EnsureAgentNamespaceFromConfigForTest(ctx, cfg, ns); err != nil {
		t.Fatalf("ensure agent namespace: %v", err)
	}
	if err := stack.ApplyEchoPoolFromConfig(ctx, cfg, ns); err != nil {
		t.Fatalf("apply echo pool: %v", err)
	}

	// Before the WarmPoolController reports a ready idle pod, the pool reads
	// not ready.
	if stack.EchoPoolReadyFromConfigForTest(ctx, cfg) {
		t.Error("echo pool ready before any readyCount is set, want not ready")
	}

	// Drive the pool status to one ready idle pod.
	cl := lennyClient(t, cfg)
	var pool lennyv1alpha1.SandboxWarmPool
	if err := cl.Get(ctx, ctrlclient.ObjectKey{Namespace: ns, Name: stack.EchoPoolName}, &pool); err != nil {
		t.Fatalf("get echo pool: %v", err)
	}
	pool.Status.ReadyCount = 1
	if err := cl.Status().Update(ctx, &pool); err != nil {
		t.Fatalf("update echo pool status: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for !stack.EchoPoolReadyFromConfigForTest(ctx, cfg) {
		if time.Now().After(deadline) {
			t.Fatal("echo pool not reported ready after readyCount reached 1")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
