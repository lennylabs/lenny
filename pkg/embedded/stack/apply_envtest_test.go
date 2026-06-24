// SPDX-License-Identifier: MIT

package stack_test

import (
	"context"
	"path/filepath"
	"testing"
	"testing/fstest"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/lennylabs/lenny/pkg/embedded/stack"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"

	"k8s.io/client-go/tools/clientcmd"
)

// controlPlaneFixture is a minimal cross-phase manifest set covering the
// §17.4 apply order: a Namespace, the RBAC ServiceAccount, a ConfigMap, a
// Service, and a Deployment, with the documents deliberately rendered out of
// apply order (the Deployment first) so the test exercises the applier's
// reordering. Every object lands in the same namespace, so a Deployment
// applied before its Namespace would be rejected by the API server; the test
// passing proves the applier sequenced the Namespace first.
const controlPlaneFixture = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: lenny-gateway
  namespace: lenny-embed
spec:
  replicas: 1
  selector:
    matchLabels:
      app: lenny-gateway
  template:
    metadata:
      labels:
        app: lenny-gateway
    spec:
      serviceAccountName: lenny-gateway
      containers:
        - name: gateway
          image: ghcr.io/lennylabs/lenny-gateway:dev
---
apiVersion: v1
kind: Service
metadata:
  name: lenny-gateway
  namespace: lenny-embed
spec:
  type: NodePort
  selector:
    app: lenny-gateway
  ports:
    - name: http
      port: 8059
      targetPort: 8059
      nodePort: 30080
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: lenny-gateway-config
  namespace: lenny-embed
data:
  mode: embedded
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: lenny-gateway
  namespace: lenny-embed
---
apiVersion: v1
kind: Namespace
metadata:
  name: lenny-embed
`

func fixtureFS() fstest.MapFS {
	return fstest.MapFS{
		"manifests.yaml": &fstest.MapFile{Data: []byte(controlPlaneFixture)},
	}
}

// TestApplyManifestsCreatesControlPlaneAcrossPhases_spec_17_4 covers the
// §17.4 dynamic apply against a real kube-apiserver: the applier decodes the
// rendered manifest set, resolves each object's GVR through the RESTMapper,
// and server-side-applies it in dependency order, so the Namespace, the RBAC
// ServiceAccount, the ConfigMap, the Service, and the Deployment all exist
// after one apply even though the documents are rendered with the Deployment
// first. A namespaced object applied before its Namespace would be rejected,
// so the objects existing proves the applier sequenced the Namespace ahead
// of them.
//
// diagnosis: a failure means the embedded bring-up cannot apply the
// rendered control plane through the dynamic client, so Embedded Mode brings
// up no in-cluster gateway/controller and §4.7 placement never runs.
//
// spec: §17.4 (in-cluster control plane apply path and order).
func TestApplyManifestsCreatesControlPlaneAcrossPhases_spec_17_4(t *testing.T) {
	env := envtest.Start(t)
	ctx := context.Background()
	cfg := env.RESTConfig()

	if err := stack.ApplyManifestsFromConfigForTest(ctx, cfg, fixtureFS()); err != nil {
		t.Fatalf("apply manifests: %v", err)
	}

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("build core client: %v", err)
	}
	const ns = "lenny-embed"
	if _, err := cs.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{}); err != nil {
		t.Fatalf("namespace not created: %v", err)
	}
	if _, err := cs.CoreV1().ServiceAccounts(ns).Get(ctx, "lenny-gateway", metav1.GetOptions{}); err != nil {
		t.Errorf("ServiceAccount not created: %v", err)
	}
	if _, err := cs.CoreV1().ConfigMaps(ns).Get(ctx, "lenny-gateway-config", metav1.GetOptions{}); err != nil {
		t.Errorf("ConfigMap not created: %v", err)
	}
	svc, err := cs.CoreV1().Services(ns).Get(ctx, "lenny-gateway", metav1.GetOptions{})
	if err != nil {
		t.Errorf("Service not created: %v", err)
	} else if svc.Spec.Type != corev1.ServiceTypeNodePort {
		t.Errorf("Service type = %q, want NodePort", svc.Spec.Type)
	}
	dep, err := cs.AppsV1().Deployments(ns).Get(ctx, "lenny-gateway", metav1.GetOptions{})
	if err != nil {
		t.Errorf("Deployment not created: %v", err)
	} else if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 1 {
		t.Errorf("Deployment replicas = %v, want 1", dep.Spec.Replicas)
	}
}

// TestApplyManifestsIsIdempotentOnReapply_spec_17_4 covers the §17.4
// idempotent-re-apply requirement: applying the same manifest set twice does
// not fail on AlreadyExists and leaves a single object per name with the
// applier's field manager owning it (server-side apply reconverges in place).
// This is the warm-`lenny up` path, which re-applies the embedded manifests
// every bring-up.
//
// diagnosis: a failure means a re-run of lenny up errors on the existing
// control-plane objects, so a warm bring-up cannot reconverge the embedded
// manifests and Embedded Mode is not restartable.
//
// spec: §17.4 (idempotent re-apply of the in-cluster control plane).
func TestApplyManifestsIsIdempotentOnReapply_spec_17_4(t *testing.T) {
	env := envtest.Start(t)
	ctx := context.Background()
	cfg := env.RESTConfig()

	if err := stack.ApplyManifestsFromConfigForTest(ctx, cfg, fixtureFS()); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	// The second apply must reconverge in place rather than fail on the
	// objects the first apply created.
	if err := stack.ApplyManifestsFromConfigForTest(ctx, cfg, fixtureFS()); err != nil {
		t.Fatalf("re-apply must be idempotent, got: %v", err)
	}

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("build core client: %v", err)
	}
	const ns = "lenny-embed"
	deps, err := cs.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list deployments after re-apply: %v", err)
	}
	var gateways int
	var managed bool
	for i := range deps.Items {
		d := &deps.Items[i]
		if d.Name != "lenny-gateway" {
			continue
		}
		gateways++
		for _, mf := range d.ManagedFields {
			if mf.Manager == "lenny-embedded" {
				managed = true
			}
		}
	}
	if gateways != 1 {
		t.Errorf("after re-apply there are %d lenny-gateway Deployments, want exactly 1", gateways)
	}
	if !managed {
		t.Errorf("the re-applied Deployment is not owned by the lenny-embedded field manager; managed fields: %+v", managedManagers(deps))
	}
}

// TestApplyManifestsRejectsUnknownGVK_spec_17_4 covers the fail-closed
// behavior when a rendered object names a kind the cluster does not know:
// the RESTMapper cannot resolve the GVR, so the apply surfaces an error
// naming the object rather than silently skipping it. A drifted or
// hand-edited embedded render that references an absent CRD must fail the
// bring-up loudly rather than bring up a partial control plane.
//
// diagnosis: a failure means the applier swallowed an unresolvable object,
// so a manifest set referencing a missing CRD would bring up an incomplete
// control plane without surfacing the gap.
//
// spec: §17.4 (in-cluster control plane apply path).
func TestApplyManifestsRejectsUnknownGVK_spec_17_4(t *testing.T) {
	env := envtest.Start(t)
	ctx := context.Background()
	cfg := env.RESTConfig()

	const unknown = `apiVersion: example.com/v1
kind: NoSuchKind
metadata:
  name: orphan
  namespace: default
`
	fsys := fstest.MapFS{"manifests.yaml": &fstest.MapFile{Data: []byte(unknown)}}
	err := stack.ApplyManifestsFromConfigForTest(ctx, cfg, fsys)
	if err == nil {
		t.Fatal("apply accepted an object whose kind the cluster does not know, want a resolve error")
	}
	// The unknown kind has no REST mapping, which is the expected failure.
	if apierrors.IsAlreadyExists(err) {
		t.Errorf("error = %v, want an unresolved-GVR error rather than AlreadyExists", err)
	}
}

// TestApplyManifestsFromKubeconfigEntryPoint_spec_17_4 covers the public
// ApplyManifests entry point, which loads a kubeconfig file before applying
// (the path lenny up takes with the launcher's host-rewritten kubeconfig).
// It writes a kubeconfig from the envtest config, then applies the fixture
// set through the kubeconfig-loading wrapper and asserts the Deployment
// landed, so the kubeconfig-resolution leg is exercised, not only the
// already-resolved-config path.
//
// diagnosis: a failure means the bring-up cannot load the launcher's
// kubeconfig to apply the embedded control plane, so lenny up brings up no
// in-cluster gateway/controller.
//
// spec: §17.4 (in-cluster control plane apply path).
func TestApplyManifestsFromKubeconfigEntryPoint_spec_17_4(t *testing.T) {
	env := envtest.Start(t)
	ctx := context.Background()
	cfg := env.RESTConfig()

	kubeconfigPath := writeKubeconfig(t, cfg)
	if err := stack.ApplyManifestsFromKubeconfigForTest(ctx, kubeconfigPath, fixtureFS()); err != nil {
		t.Fatalf("apply via kubeconfig: %v", err)
	}

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("build core client: %v", err)
	}
	if _, err := cs.AppsV1().Deployments("lenny-embed").Get(ctx, "lenny-gateway", metav1.GetOptions{}); err != nil {
		t.Errorf("Deployment not applied through the kubeconfig entry point: %v", err)
	}
}

// writeKubeconfig serializes a kubeconfig file addressing the envtest API
// server from cfg's TLS client-certificate material, so the ApplyManifests
// kubeconfig-loading entry point can be exercised against the test control
// plane. It returns the written file path.
func writeKubeconfig(t *testing.T, cfg *rest.Config) string {
	t.Helper()
	const name = "envtest"
	kc := clientcmdapi.NewConfig()
	kc.Clusters[name] = &clientcmdapi.Cluster{
		Server:                   cfg.Host,
		CertificateAuthorityData: cfg.CAData,
	}
	kc.AuthInfos[name] = &clientcmdapi.AuthInfo{
		ClientCertificateData: cfg.CertData,
		ClientKeyData:         cfg.KeyData,
	}
	kc.Contexts[name] = &clientcmdapi.Context{Cluster: name, AuthInfo: name}
	kc.CurrentContext = name

	path := filepath.Join(t.TempDir(), "kubeconfig.yaml")
	if err := clientcmd.WriteToFile(*kc, path); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}
	return path
}

// managedManagers projects the field-manager names across the listed
// Deployments for a failure message.
func managedManagers(deps *appsv1.DeploymentList) []string {
	var out []string
	for i := range deps.Items {
		for _, mf := range deps.Items[i].ManagedFields {
			out = append(out, mf.Manager)
		}
	}
	return out
}
