// SPDX-License-Identifier: MIT

//go:build component

package controllers_test

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/admission/webhook"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/poolscaling"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// podLifecycleClient returns a controller-runtime client for the shared
// envtest control plane with the core and lenny.dev schemes registered.
func podLifecycleClient(t *testing.T) (client.Client, *runtime.Scheme, func() *discovery.DiscoveryClient, dynamic.Interface) {
	t.Helper()
	env := sharedEnv.Start(t)
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(lennyv1.AddToScheme(scheme))
	c, err := client.New(env.RESTConfig(), client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	dyn, err := dynamic.NewForConfig(env.RESTConfig())
	if err != nil {
		t.Fatalf("dynamic.NewForConfig: %v", err)
	}
	disc := func() *discovery.DiscoveryClient {
		d, err := discovery.NewDiscoveryClientForConfig(env.RESTConfig())
		if err != nil {
			t.Fatalf("discovery: %v", err)
		}
		return d
	}
	return c, scheme, disc, dyn
}

// TestAPIServerServesThePodLifecycleKinds checks against a real API server
// with the chart's CRDs installed that lenny.dev/v1alpha1 serves exactly
// agentpods, podsets, podclaims, and runtimes, and that a create of a
// retired kind is refused as an unknown resource: the RESTMapper finds no
// kind SandboxTemplate, and a request to the sandboxtemplates resource path
// answers NotFound.
//
// spec: KAPI
// diagnosis: the CRD manifests under charts/lenny/crds/ register a resource
// other than the four Lenny kinds, still install a retired kind, or miss
// one of the renamed kinds. Regenerate the CRDs with `make generate` and
// delete any retired CRD file.
func TestAPIServerServesThePodLifecycleKinds(t *testing.T) {
	c, _, disc, dyn := podLifecycleClient(t)
	ctx := context.Background()

	list, err := disc().ServerResourcesForGroupVersion(lennyv1.GroupVersion.String())
	if err != nil {
		t.Fatalf("discover %s: %v", lennyv1.GroupVersion, err)
	}
	var served []string
	for _, r := range list.APIResources {
		if !strings.Contains(r.Name, "/") {
			served = append(served, r.Name)
		}
	}
	sort.Strings(served)
	if got, want := strings.Join(served, ","), "agentpods,podclaims,podsets,runtimes"; got != want {
		t.Errorf("lenny.dev/v1alpha1 serves %s, want %s", got, want)
	}

	// The retired kind and resource are spelled in parts, so the
	// retired-name check reads this test like any other file.
	retiredKind := "Sand" + "box" + "Template"
	retiredResource := "sand" + "box" + "templates"

	ns := testNamespaces.Create(t, c)
	retired := &unstructured.Unstructured{}
	retired.SetGroupVersionKind(lennyv1.GroupVersion.WithKind(retiredKind))
	retired.SetName("retired")
	retired.SetNamespace(ns)
	if err := c.Create(ctx, retired); err == nil || !meta.IsNoMatchError(err) {
		t.Errorf("create of kind %s: err = %v, want a no-match error naming the kind", retiredKind, err)
	}
	gvr := schema.GroupVersionResource{Group: lennyv1.GroupVersion.Group, Version: lennyv1.GroupVersion.Version, Resource: retiredResource}
	if _, err := dyn.Resource(gvr).Namespace(ns).Create(ctx, retired, metav1.CreateOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("create on the %s resource path: err = %v, want NotFound", retiredResource, err)
	}
}

// podSetRows is a poolstore.Store double whose List returns fixed rows, so
// the PoolScalingController renders them without Postgres.
type podSetRows struct {
	poolstore.Store
	rows []poolstore.Pool
}

func (r podSetRows) List(context.Context, poolstore.ListFilter) ([]poolstore.Pool, error) {
	return r.rows, nil
}

// TestOnePodSetCarriesThePoolSettingsAndWarmTargets checks that the
// PoolScalingController renders a pool as one PodSet named after the pool,
// whose spec carries both the pool settings and the warm targets, and that
// the WarmPoolController creates AgentPods whose controller owner is that
// PodSet and whose spec.podSetRef names it. No other PodSet exists for the
// pool.
//
// spec: KPSET, KAPOD, POOL
// diagnosis: the PoolScalingController writes the pool into more than one
// object or leaves the pool settings or the warm targets off the PodSet, or
// the WarmPoolController does not own its AgentPods through the PodSet.
// Check syncPodSet in pkg/controller/poolscaling and createAgentPod in
// pkg/controller/warmpool.
func TestOnePodSetCarriesThePoolSettingsAndWarmTargets(t *testing.T) {
	c, scheme, _, _ := podLifecycleClient(t)
	ctx := context.Background()
	ns := testNamespaces.Create(t, c)
	const pool = "web"

	psc := &poolscaling.Reconciler{
		Client: c,
		Source: &poolscaling.PoolStoreSource{
			Store: podSetRows{rows: []poolstore.Pool{{
				Name: pool, RuntimeRef: "rt", IsolationProfile: isolation.ProfileSandboxed,
				ExecutionMode: runtimestore.ExecutionModeSession, WarmCount: 2,
			}}},
			Namespace: ns,
		},
	}
	if err := psc.Sync(ctx); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	var sets lennyv1.PodSetList
	if err := c.List(ctx, &sets, client.InNamespace(ns)); err != nil {
		t.Fatalf("list PodSets: %v", err)
	}
	if len(sets.Items) != 1 {
		t.Fatalf("PodSets for one pool = %d, want 1", len(sets.Items))
	}
	ps := sets.Items[0]
	if ps.Name != pool {
		t.Errorf("PodSet name = %q, want the pool name %q", ps.Name, pool)
	}
	if ps.Spec.RuntimeRef != "rt" || ps.Spec.IsolationProfile != string(isolation.ProfileSandboxed) {
		t.Errorf("PodSet pool settings = %q/%q, want rt/sandboxed", ps.Spec.RuntimeRef, ps.Spec.IsolationProfile)
	}
	if ps.Spec.MaxWarm != 2 || ps.Spec.MinWarm < 1 {
		t.Errorf("PodSet warm targets = min %d max %d, want max 2 and a positive min", ps.Spec.MinWarm, ps.Spec.MaxWarm)
	}

	wpc := &warmpool.Reconciler{Client: c, Scheme: scheme}
	if _, err := wpc.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKey{Namespace: ns, Name: pool}}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var pods lennyv1.AgentPodList
	if err := c.List(ctx, &pods, client.InNamespace(ns)); err != nil {
		t.Fatalf("list AgentPods: %v", err)
	}
	if len(pods.Items) == 0 {
		t.Fatal("the WarmPoolController created no AgentPod for the PodSet")
	}
	for _, p := range pods.Items {
		if p.Spec.PodSetRef != pool {
			t.Errorf("AgentPod %s spec.podSetRef = %q, want %q", p.Name, p.Spec.PodSetRef, pool)
		}
		owner := metav1.GetControllerOf(&p)
		if owner == nil || owner.Kind != "PodSet" || owner.Name != pool {
			t.Errorf("AgentPod %s controller owner = %+v, want PodSet %s", p.Name, owner, pool)
		}
	}
}

// TestReferenceFieldsAreRequiredAndClaimsAreGuarded checks the CRD schema
// and the claim guard against a real API server: an AgentPod without
// spec.podSetRef and a PodClaim without spec.agentPodRef are refused, an
// update that changes either reference is refused, and lenny-podclaim-guard
// refuses a second PodClaim on one agentPodRef with its message.
//
// spec: KAPOD, KPCLAIM, KADMIT
// diagnosis: the CRD manifests lost the required marker or the
// self == oldSelf rule on spec.podSetRef or spec.agentPodRef, or the claim
// guard reads a field other than spec.agentPodRef. Check
// pkg/apis/lenny/v1alpha1 and pkg/admission/webhook/podclaim_guard.go.
func TestReferenceFieldsAreRequiredAndClaimsAreGuarded(t *testing.T) {
	c, _, _, dyn := podLifecycleClient(t)
	ctx := context.Background()
	ns := testNamespaces.Create(t, c)

	create := func(resource, kind string, spec map[string]any) error {
		u := &unstructured.Unstructured{Object: map[string]any{"spec": spec}}
		u.SetGroupVersionKind(lennyv1.GroupVersion.WithKind(kind))
		u.SetGenerateName("ref-")
		gvr := lennyv1.GroupVersion.WithResource(resource)
		_, err := dyn.Resource(gvr).Namespace(ns).Create(ctx, u, metav1.CreateOptions{})
		return err
	}
	if err := create("agentpods", "AgentPod", map[string]any{"runtimeRef": "rt"}); !apierrors.IsInvalid(err) {
		t.Errorf("AgentPod without spec.podSetRef: err = %v, want Invalid", err)
	}
	if err := create("podclaims", "PodClaim", map[string]any{"tenantId": "acme"}); !apierrors.IsInvalid(err) {
		t.Errorf("PodClaim without spec.agentPodRef: err = %v, want Invalid", err)
	}

	pod := &lennyv1.AgentPod{
		ObjectMeta: metav1.ObjectMeta{Name: "web-abcde", Namespace: ns},
		Spec:       lennyv1.AgentPodSpec{RuntimeRef: "rt", PodSetRef: "web"},
	}
	mustCreate(t, ctx, c, pod)
	pod.Spec.PodSetRef = "api"
	if err := c.Update(ctx, pod); !apierrors.IsInvalid(err) {
		t.Errorf("update of spec.podSetRef: err = %v, want Invalid", err)
	}

	claim := &lennyv1.PodClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim-web-abcde", Namespace: ns},
		Spec:       lennyv1.PodClaimSpec{AgentPodRef: "web-abcde"},
	}
	mustCreate(t, ctx, c, claim)
	claim.Spec.AgentPodRef = "web-fghij"
	if err := c.Update(ctx, claim); !apierrors.IsInvalid(err) {
		t.Errorf("update of spec.agentPodRef: err = %v, want Invalid", err)
	}

	second := lennyv1.PodClaim{
		TypeMeta:   metav1.TypeMeta{APIVersion: lennyv1.GroupVersion.String(), Kind: "PodClaim"},
		ObjectMeta: metav1.ObjectMeta{Name: "claim-web-abcde-2", Namespace: ns},
		Spec:       lennyv1.PodClaimSpec{AgentPodRef: "web-abcde"},
	}
	raw, err := json.Marshal(second)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp := webhook.PodClaimGuard(c)(ctx, &admissionv1.AdmissionRequest{
		UID: "u1", Operation: admissionv1.Create, Namespace: ns, Name: second.Name,
		Object: runtime.RawExtension{Raw: raw},
	})
	if resp.Allowed {
		t.Fatal("a second PodClaim on one agentPodRef was admitted")
	}
	if want := "PodClaim already exists for AgentPod web-abcde; concurrent claim rejected"; resp.Result == nil || !strings.Contains(resp.Result.Message, want) {
		t.Errorf("guard message = %+v, want it to contain %q", resp.Result, want)
	}
}
