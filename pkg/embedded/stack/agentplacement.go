// SPDX-License-Identifier: MIT

package stack

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	nodev1 "k8s.io/api/node/v1"
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

// runcRuntimeClassName is the §5.3 RuntimeClass the `standard` isolation
// profile maps to (standard->runc, sandboxed->gvisor, microvm->kata). The
// seeded embedded echo pool uses the standard profile, so the WarmPoolController
// cannot render its pod until this RuntimeClass exists; its handler is
// k3s/containerd's built-in `runc`, so naming it needs no out-of-band runtime
// install. In production the §5.3 RuntimeClasses ship via the Helm chart
// (charts/lenny/templates/runtimeclasses.yaml); Embedded Mode applies the CRDs
// directly and installs this one RuntimeClass at bring-up so placement renders.
// spec: §5.3 (isolation profiles), §17.4 (Embedded Mode provisions the substrate).
const runcRuntimeClassName = "runc"

// The §17.4 echo warm-pool parameters the embedded bring-up applies as a
// SandboxTemplate/SandboxWarmPool pair. The pool is a §5.2 single-pod hot
// pool (warmCount 1, so minWarm = maxWarm = 1) named echo-pool-embedded,
// matching the working Kind precedent. It runs `standard` (runc) isolation
// under the §17.4 local-fidelity disclosure, the `restricted` egress
// profile, the `small` resource class, and the §13.2 cluster-default DNS
// opt-out the embedded substrate requires because it runs no dedicated
// lenny-system CoreDNS. No Postgres-backed PoolScalingController materializes
// these in the development profile, so the bring-up applies them directly.
// spec: §5.2 (single-pod hot pool), §13.2 (cluster-default DNS opt-out),
// §17.4 (Embedded Mode seed).
const (
	// EchoPoolName is the name of the §17.4 echo warm pool the embedded
	// bring-up applies (the SandboxTemplate and SandboxWarmPool share it).
	// It matches the Kind precedent echo-pool-embedded, so the gateway's
	// ResolvePool resolves the same pool the embedded stack materializes.
	EchoPoolName = "echo-pool-embedded"

	echoPoolWarmCount        = 1
	echoPoolResourceClass    = "small"
	echoPoolEgressProfile    = "restricted"
	echoPoolIsolationProfile = "standard"
	echoPoolDNSPolicy        = "cluster-default"
)

// applyEchoPool applies the echo SandboxTemplate/SandboxWarmPool pair to the
// cluster reachable through kubeconfigPath. Under the §17.4 no-Postgres
// development profile no PoolScalingController runs, so the canonical
// poolstore→CRD projection (poolscaling.PoolStoreSource.toConfig) is
// reproduced here and applied directly, so the unconditionally-registered
// WarmPoolController pre-warms the echo pod. spec: §4.6.2, §5.2, §17.4.
func applyEchoPool(ctx context.Context, kubeconfigPath, namespace string) error {
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return fmt.Errorf("embedded echo pool: load kubeconfig %s: %w", kubeconfigPath, err)
	}
	return ApplyEchoPoolFromConfig(ctx, cfg, namespace)
}

// ApplyEchoPoolFromConfig applies the echo SandboxTemplate/SandboxWarmPool
// pair against an already-resolved rest config. It is the no-Postgres
// pool-materialization path the bring-up runs (applyEchoPool resolves the
// kubeconfig and calls it), exported so a tier-2 component test can drive the
// create and update-in-place reconvergence against a real kube-apiserver with
// the lenny.dev CRDs installed, without writing a kubeconfig file. spec:
// §4.6.2, §5.2.
func ApplyEchoPoolFromConfig(ctx context.Context, cfg *rest.Config, namespace string) error {
	scheme := runtime.NewScheme()
	utilruntime.Must(lennyv1alpha1.AddToScheme(scheme))
	cl, err := ctrlclient.New(cfg, ctrlclient.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("embedded echo pool: build cluster client: %w", err)
	}
	tmpl, pool := echoPoolObjects(namespace)
	if err := upsertSandboxTemplate(ctx, cl, tmpl); err != nil {
		return err
	}
	return upsertSandboxWarmPool(ctx, cl, pool)
}

// echoPoolObjects builds the echo SandboxTemplate and SandboxWarmPool in
// namespace ns. The field mapping reproduces poolscaling.PoolStoreSource.toConfig
// (pkg/controller/poolscaling/poolstoresource.go) for a single-pod hot pool:
// the SandboxTemplate carries the runtimeRef, isolation, egress, DNS-policy,
// and resource-class fields, and the SandboxWarmPool sets templateRef to the
// pool name with minWarm = maxWarm = warmCount, so the directly-applied pair
// matches what the PoolScalingController would have produced from the seed.
// spec: §4.6.2 (the poolstore→CRD projection), §5.2 (single-pod hot pool).
func echoPoolObjects(ns string) (*lennyv1alpha1.SandboxTemplate, *lennyv1alpha1.SandboxWarmPool) {
	tmpl := &lennyv1alpha1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: EchoPoolName, Namespace: ns},
		Spec: lennyv1alpha1.SandboxTemplateSpec{
			RuntimeRef:       EchoRuntimeName,
			IsolationProfile: echoPoolIsolationProfile,
			EgressProfile:    echoPoolEgressProfile,
			DNSPolicy:        echoPoolDNSPolicy,
			ResourceClass:    echoPoolResourceClass,
		},
	}
	pool := &lennyv1alpha1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: EchoPoolName, Namespace: ns},
		Spec: lennyv1alpha1.SandboxWarmPoolSpec{
			TemplateRef: EchoPoolName,
			MinWarm:     echoPoolWarmCount,
			MaxWarm:     echoPoolWarmCount,
		},
	}
	return tmpl, pool
}

// upsertSandboxTemplate creates the SandboxTemplate, or updates its spec in
// place when one of that name already exists, so a re-run of lenny up
// reconverges it rather than failing on AlreadyExists.
func upsertSandboxTemplate(ctx context.Context, cl ctrlclient.Client, tmpl *lennyv1alpha1.SandboxTemplate) error {
	var existing lennyv1alpha1.SandboxTemplate
	key := ctrlclient.ObjectKey{Name: tmpl.Name, Namespace: tmpl.Namespace}
	err := cl.Get(ctx, key, &existing)
	if apierrors.IsNotFound(err) {
		if err := cl.Create(ctx, tmpl); err != nil {
			return fmt.Errorf("embedded echo pool: create SandboxTemplate %s: %w", tmpl.Name, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("embedded echo pool: get SandboxTemplate %s: %w", tmpl.Name, err)
	}
	existing.Spec = tmpl.Spec
	if err := cl.Update(ctx, &existing); err != nil {
		return fmt.Errorf("embedded echo pool: update SandboxTemplate %s: %w", tmpl.Name, err)
	}
	return nil
}

// upsertSandboxWarmPool creates the SandboxWarmPool, or updates its spec in
// place when one of that name already exists, so a re-run of lenny up
// reconverges it. The WarmPoolController-owned status counts are not touched.
func upsertSandboxWarmPool(ctx context.Context, cl ctrlclient.Client, pool *lennyv1alpha1.SandboxWarmPool) error {
	var existing lennyv1alpha1.SandboxWarmPool
	key := ctrlclient.ObjectKey{Name: pool.Name, Namespace: pool.Namespace}
	err := cl.Get(ctx, key, &existing)
	if apierrors.IsNotFound(err) {
		if err := cl.Create(ctx, pool); err != nil {
			return fmt.Errorf("embedded echo pool: create SandboxWarmPool %s: %w", pool.Name, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("embedded echo pool: get SandboxWarmPool %s: %w", pool.Name, err)
	}
	existing.Spec = pool.Spec
	if err := cl.Update(ctx, &existing); err != nil {
		return fmt.Errorf("embedded echo pool: update SandboxWarmPool %s: %w", pool.Name, err)
	}
	return nil
}

// Substrate placement seams. They default to the real typed/controller-runtime
// clients and are package-level vars so a unit test can substitute fakes and
// assert the §4.7 activation sequence (namespace create, RuntimeClass install,
// Runtime-CR apply, echo-pool apply, digest injection) without a live API
// server, mirroring the existing substrate seams (newSubstrate,
// installSubstrateCRDs). spec: §4.7, §5.1, §4.6.2.
var (
	ensureAgentNamespaceFn = ensureAgentNamespace
	ensureRuntimeClassFn   = ensureRuntimeClass
	applyEchoRuntimeCRFn   = applyEchoRuntimeCR
	applyEchoPoolFn        = applyEchoPool
)

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

// ensureRuntimeClass creates the named §5.3 RuntimeClass (with the given
// containerd handler) in the embedded cluster reachable through kubeconfigPath,
// idempotently. The seeded echo pool's `standard` isolation profile resolves to
// the `runc` RuntimeClass, which does not exist in a bare k3s cluster, so the
// WarmPoolController fails its pod render ("runtimeclass \"runc\" not found")
// until this installs it. It is part of activating the §4.7 pod path in Embedded
// Mode, alongside ensureAgentNamespace. spec: §5.3 (isolation profiles), §17.4.
func ensureRuntimeClass(ctx context.Context, kubeconfigPath, name, handler string) error {
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return fmt.Errorf("embedded runtimeclass: load kubeconfig %s: %w", kubeconfigPath, err)
	}
	return ensureRuntimeClassFromConfig(ctx, cfg, name, handler)
}

// ensureRuntimeClassFromConfig creates the RuntimeClass against an
// already-resolved rest config, idempotently. It is split from ensureRuntimeClass
// so a tier-2 envtest can exercise the create and the AlreadyExists idempotency
// against a real kube-apiserver without writing a kubeconfig file. spec: §5.3.
func ensureRuntimeClassFromConfig(ctx context.Context, cfg *rest.Config, name, handler string) error {
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("embedded runtimeclass: build core client: %w", err)
	}
	rc := &nodev1.RuntimeClass{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Handler:    handler,
	}
	if _, err := client.NodeV1().RuntimeClasses().Create(ctx, rc, metav1.CreateOptions{}); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return nil
		}
		return fmt.Errorf("embedded runtimeclass: create %s: %w", name, err)
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
