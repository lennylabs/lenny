// SPDX-License-Identifier: MIT

package stack

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1alpha1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
)

// agentNamespace is the §5 namespace the embedded controller materializes the
// seeded warm-pool/SandboxTemplate CRDs into and the gateway resolves the warm
// pool from for §4.7 placement. It matches the PoolScalingController target
// (agentNamespaces[0] in cmd/lenny-controller/main.go) and the Kind e2e
// precedent (tests/testinfra/kind, the lenny-agents agent namespace). The
// gateway's -agent-namespace and the controller's --agent-namespaces are both
// pointed at it so the registry-to-CRD projection and the placement resolution
// agree on one namespace. spec: §4.6.2, §5.1, §17.4.
const agentNamespace = "lenny-agents"

// Substrate placement seams. They default to the real typed/controller-runtime
// clients and are package-level vars so a unit test can substitute fakes and
// assert the §4.7 activation sequence (namespace create, Runtime-CR apply,
// digest injection) without a live API server, mirroring the existing
// substrate seams (newSubstrate, installSubstrateCRDs). spec: §4.7, §5.1.
var (
	ensureAgentNamespaceFn = ensureAgentNamespace
	applyEchoRuntimeCRFn   = applyEchoRuntimeCR
)

// gatewayAgentNamespace returns the §4.7 agent namespace the embedded gateway
// places into, or the empty string to keep the gateway on the in-process echo
// executor. Placement is activated only when both the substrate is up
// (k3sEnabled) and the echo-embedded image import resolved a digest
// (echoImageRef != ""). Gating on the resolved image as well as the substrate
// makes the import-failure edge fail closed: when k3s is up but the import
// resolved no digest, no echo Runtime CR is applied and the seed keeps its
// sentinel placeholder, so routing the session through the §4.7 pod path would
// fail rather than start; leaving the namespace unset keeps the gateway on the
// in-process echo executor, the same degraded path the substrate being down
// takes. spec: §17.4 (Embedded Mode degrades to the in-process echo executor
// when placement cannot run), §4.7 (the pod path needs a runnable digest-pinned
// image and an applied Runtime CR).
func gatewayAgentNamespace(k3sEnabled bool, echoImageRef string) string {
	if k3sEnabled && echoImageRef != "" {
		return agentNamespace
	}
	return ""
}

// ensureAgentNamespace creates the agent namespace in the embedded cluster
// reachable through kubeconfigPath, idempotently: an already-present namespace
// is treated as success so a re-run of lenny up does not fail. The gateway
// resolves the warm pool from this namespace and the PoolScalingController
// materializes the seeded pool CRDs into it, so the namespace must exist before
// either places. It is the first half of activating the §4.7 pod path in
// Embedded Mode.
//
// spec: §4.6.2 (the pool CRDs materialize in the agent namespace), §5.1
// (platform-global pools materialize per agent namespace), §17.4 (Embedded
// Mode provisions the substrate per host).
func ensureAgentNamespace(ctx context.Context, kubeconfigPath, namespace string) error {
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return fmt.Errorf("embedded agent-namespace: load kubeconfig %s: %w", kubeconfigPath, err)
	}
	return ensureAgentNamespaceFromConfig(ctx, cfg, namespace)
}

// ensureAgentNamespaceFromConfig creates the agent namespace against an
// already-resolved rest config, idempotently. It is split from
// ensureAgentNamespace so a tier-2 envtest can exercise the create and the
// AlreadyExists idempotency against a real kube-apiserver without writing a
// kubeconfig file. spec: §4.6.2, §5.1.
func ensureAgentNamespaceFromConfig(ctx context.Context, cfg *rest.Config, namespace string) error {
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("embedded agent-namespace: build core client: %w", err)
	}
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
	if _, err := client.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{}); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return nil
		}
		return fmt.Errorf("embedded agent-namespace: create %s: %w", namespace, err)
	}
	return nil
}

// applyEchoRuntimeCR applies the cluster-scoped echo Runtime custom resource to
// the embedded cluster reachable through kubeconfigPath, carrying the
// import-time-resolved echo-embedded image digest and deploymentModel:
// embedded. The Sandbox controller resolves the runtime from a Runtime CR by
// name (it reads spec.image and spec.deploymentModel off the CR), so the
// registry-only bootstrap seed alone produces no idle pod; this CR is the
// missing artifact that lets the §4.7 single-container embedded model render.
// It is created when absent and its spec is updated in place when it already
// exists, so a re-run of lenny up is idempotent.
//
// The digest the CR carries must equal the digest of the image present in the
// embedded containerd and the digest the bootstrap seed registers, because the
// Sandbox pod is digest-pinned with the default IfNotPresent pull policy;
// resolving all three from the same imported image is what guarantees that
// equality.
//
// spec: §4.7 (embedded deployment model), §5.1 (Runtime CR is the declarative
// source the Sandbox controller resolves by name).
func applyEchoRuntimeCR(ctx context.Context, kubeconfigPath, image string) error {
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return fmt.Errorf("embedded echo runtime: load kubeconfig %s: %w", kubeconfigPath, err)
	}
	return applyEchoRuntimeCRFromConfig(ctx, cfg, image)
}

// applyEchoRuntimeCRFromConfig applies the echo Runtime CR against an
// already-resolved rest config. It is split from applyEchoRuntimeCR so a
// tier-2 envtest can exercise the create and the update-in-place reconvergence
// against a real kube-apiserver with the lenny.dev CRDs installed, without
// writing a kubeconfig file. spec: §4.7, §5.1.
func applyEchoRuntimeCRFromConfig(ctx context.Context, cfg *rest.Config, image string) error {
	scheme := runtime.NewScheme()
	utilruntime.Must(lennyv1alpha1.AddToScheme(scheme))
	cl, err := ctrlclient.New(cfg, ctrlclient.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("embedded echo runtime: build cluster client: %w", err)
	}
	return upsertRuntimeCR(ctx, cl, echoRuntimeCR(image))
}

// echoRuntimeCR builds the cluster-scoped echo Runtime custom resource the
// embedded bring-up applies. It mirrors the Kind precedent
// echo-runtime-embedded (tests/testinfra/kind/agent-workload.yaml): the §4.7
// embedded deployment model, Basic integration level, session execution mode,
// and the locally-runnable standard isolation profile the embedded single-node
// cluster degrades sandboxed/microvm to (§17.4 local fidelity). The Runtime is
// cluster-scoped, so it carries only metadata.name. The runtime name matches
// EchoRuntimeName so the seeded pool's runtimeRef and the gateway's
// --runtime echo both resolve to it. spec: §4.7, §5.1.
func echoRuntimeCR(image string) *lennyv1alpha1.Runtime {
	return &lennyv1alpha1.Runtime{
		ObjectMeta: metav1.ObjectMeta{Name: EchoRuntimeName},
		Spec: lennyv1alpha1.RuntimeSpec{
			Type:             "agent",
			Image:            image,
			IntegrationLevel: "basic",
			ExecutionMode:    "session",
			IsolationProfile: "standard",
			DeploymentModel:  "embedded",
		},
	}
}

// upsertRuntimeCR creates rt, or updates the existing Runtime's spec in place
// when a Runtime of that name is already registered, so a re-run of lenny up
// reconverges the CR to the import-time-resolved digest rather than failing on
// an AlreadyExists.
func upsertRuntimeCR(ctx context.Context, cl ctrlclient.Client, rt *lennyv1alpha1.Runtime) error {
	var existing lennyv1alpha1.Runtime
	err := cl.Get(ctx, ctrlclient.ObjectKey{Name: rt.Name}, &existing)
	if apierrors.IsNotFound(err) {
		if err := cl.Create(ctx, rt); err != nil {
			return fmt.Errorf("embedded echo runtime: create Runtime %s: %w", rt.Name, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("embedded echo runtime: get Runtime %s: %w", rt.Name, err)
	}
	existing.Spec = rt.Spec
	if err := cl.Update(ctx, &existing); err != nil {
		return fmt.Errorf("embedded echo runtime: update Runtime %s: %w", rt.Name, err)
	}
	return nil
}
