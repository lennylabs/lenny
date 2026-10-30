// SPDX-License-Identifier: MIT

package stack

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1alpha1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
)

// controllerDeploymentName and opsDeploymentName name the controller and the
// mandatory lenny-ops Deployments in the embedded manifests, alongside
// gatewayDeploymentName. The §17.4 control plane runs as these three in-cluster
// Deployments, so lenny status reads their readiness, lenny logs streams their
// pods, and lenny restart rolls them. They match the names the embedded
// manifests carry. spec: §17.4 (the in-cluster control plane Deployments).
const (
	controllerDeploymentName = "lenny-controller"
	opsDeploymentName        = "lenny-ops"
)

// componentDeployment maps a §24.19 restartable / loggable component name to
// the Deployment it names in the control-plane namespace. The pod-backed
// components are the gateway, controller, and ops Deployments (proposal 0017
// C5); k3s and the runtime pods are handled on their own paths. The returned
// ok is false for a name that is not a control-plane Deployment.
func componentDeployment(component string) (name string, ok bool) {
	switch component {
	case "gateway":
		return gatewayDeploymentName, true
	case "controller":
		return controllerDeploymentName, true
	case "ops":
		return opsDeploymentName, true
	default:
		return "", false
	}
}

// clusterClientFn builds a typed Kubernetes clientset from the embedded k3s
// admin kubeconfig at kubeconfigPath. It is a package-level var so the
// cluster-backed status, logs, and restart paths are unit-testable with an
// injected fake clientset (the seam stands in for a real API server), mirroring
// the bring-up seams. spec: §17.4 (the cluster commands reach the in-cluster
// control plane through the embedded kubeconfig).
var clusterClientFn = newClusterClient

// newClusterClient loads the kubeconfig at kubeconfigPath and builds a typed
// clientset addressing the embedded k3s API server. It is the default
// clusterClientFn the cluster-backed commands resolve their connection through.
func newClusterClient(kubeconfigPath string) (kubernetes.Interface, error) {
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("embedded cluster: load kubeconfig %s: %w", kubeconfigPath, err)
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("embedded cluster: build core client: %w", err)
	}
	return client, nil
}

// poolReadyFn reports whether the seeded echo warm pool has at least one ready
// idle pod. It is a package-level var so lenny status can distinguish
// "gateway up" from "pool ready" in a unit test without a live cluster. The
// default reads the SandboxWarmPool status.readyCount through the kubeconfig.
// spec: §17.4 (lenny status distinguishes gateway-up from pool-ready), §5.2
// (the hot pool's readyCount is its claimable-idle count).
var poolReadyFn = echoPoolReady

// echoPoolReady reads the seeded echo SandboxWarmPool's status.readyCount from
// the embedded cluster and reports whether at least one warm pod has reached a
// claimable ready state. A missing pool, an unreachable cluster, or a
// zero readyCount all report not-ready, so lenny status reports "pool warming"
// until the WarmPoolController has a ready idle pod. spec: §5.2 (readyCount is
// the claimable-idle count), §17.4 (the echo pool warms after the gateway is
// up).
func echoPoolReady(ctx context.Context, kubeconfigPath string) bool {
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return false
	}
	return echoPoolReadyFromConfig(ctx, cfg)
}

// echoPoolReadyFromConfig reads the echo pool's readyCount against an
// already-resolved rest config. It is split from echoPoolReady so a tier-2
// envtest drives the pool-readiness read against a real kube-apiserver with the
// lenny.dev CRDs installed, without writing a kubeconfig file. spec: §5.2,
// §17.4.
func echoPoolReadyFromConfig(ctx context.Context, cfg *rest.Config) bool {
	cl, err := lennyTypedClient(cfg)
	if err != nil {
		return false
	}
	var pool lennyv1alpha1.SandboxWarmPool
	if err := cl.Get(ctx, ctrlclient.ObjectKey{Namespace: agentNamespace, Name: EchoPoolName}, &pool); err != nil {
		return false
	}
	return pool.Status.ReadyCount >= 1
}

// lennyTypedClient builds a controller-runtime client carrying the lenny.dev
// scheme for cfg, so the cluster-backed status read can Get the echo
// SandboxWarmPool by name. It mirrors the scheme-building client the echo seed
// and the runtime-apply verb use rather than adding a second construction
// idiom. spec: §5.1 (the lenny.dev CRDs).
func lennyTypedClient(cfg *rest.Config) (ctrlclient.Client, error) {
	scheme := runtime.NewScheme()
	utilruntime.Must(lennyv1alpha1.AddToScheme(scheme))
	cl, err := ctrlclient.New(cfg, ctrlclient.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("embedded cluster: build lenny client: %w", err)
	}
	return cl, nil
}
