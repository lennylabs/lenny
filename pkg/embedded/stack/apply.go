// SPDX-License-Identifier: MIT

package stack

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"sort"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	memory "k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	k8syaml "sigs.k8s.io/yaml"

	"github.com/lennylabs/lenny/pkg/embedded/manifests"
)

// applyFieldManager is the server-side-apply field manager the embedded
// applier owns. A stable field manager lets a re-apply of the same
// manifest set reconcile the live objects in place (the §17.4 idempotent
// re-apply) rather than conflicting with a previous apply.
const applyFieldManager = "lenny-embedded"

// applyPhase selects which subset of the manifest set an apply pass submits.
// The bring-up fences the slow multi-image bundle import between the two
// phases: it applies the non-image objects (everything but the Deployments)
// concurrently with the import, then waits for the import to land, then
// applies the Deployments, so a scheduled pod never reaches the registry
// under IfNotPresent before its image is present in containerd (proposal
// 0017 C2: apply the Deployments after the import lands). spec: §17.4.
type applyPhase int

const (
	// applyPhaseAll applies every object in the manifest set in one pass. The
	// tier-2 envtest and any non-fenced caller use it; the bring-up uses the
	// split phases below.
	applyPhaseAll applyPhase = iota
	// applyPhaseNonDeployments applies every object except the Deployments
	// (namespaces, CRDs, RBAC, ConfigMaps/Secrets, Services, RuntimeClass).
	// The bring-up runs this phase concurrently with the image import.
	applyPhaseNonDeployments
	// applyPhaseDeployments applies only the Deployments. The bring-up runs
	// this phase after the image import has landed.
	applyPhaseDeployments
)

// ApplyManifests applies the embedded §17.4 control-plane manifest set
// (the gateway, controllers, RBAC, Services, RuntimeClass, and supporting
// objects rendered from the production chart under the development
// profile) to the embedded cluster reachable through kubeconfigPath. It is
// the in-cluster counterpart of InstallCRDs: where InstallCRDs applies only
// the lenny.dev CRDs through a typed apiextensions client, ApplyManifests
// applies the full rendered control plane through a dynamic client driven
// by a RESTMapper, so it needs no typed client per resource kind and no
// Helm SDK at bring-up.
//
// It is idempotent: each object is applied with server-side apply under a
// stable field manager, so a re-run of lenny up reconverges the live
// objects to the embedded manifests in place rather than failing on an
// AlreadyExists.
//
// spec: §17.4 (in-cluster control plane).
func ApplyManifests(ctx context.Context, kubeconfigPath string) error {
	return applyManifestsPhaseFromKubeconfig(ctx, kubeconfigPath, manifests.FS, applyPhaseAll)
}

// applyNonImageManifests applies every embedded object except the
// Deployments (the namespaces, CRDs, RBAC, ConfigMaps/Secrets, Services, and
// RuntimeClass) to the cluster at kubeconfigPath. The bring-up runs it
// concurrently with the image import: none of these objects pulls an image,
// so they apply while the bundle is still loading. spec: §17.4.
func applyNonImageManifests(ctx context.Context, kubeconfigPath string) error {
	return applyManifestsPhaseFromKubeconfig(ctx, kubeconfigPath, manifests.FS, applyPhaseNonDeployments)
}

// applyDeploymentManifests applies only the embedded Deployments to the
// cluster at kubeconfigPath. The bring-up runs it after the image import has
// landed, so a scheduled pod resolves its image locally under IfNotPresent
// rather than entering ImagePullBackOff. spec: §17.4.
func applyDeploymentManifests(ctx context.Context, kubeconfigPath string) error {
	return applyManifestsPhaseFromKubeconfig(ctx, kubeconfigPath, manifests.FS, applyPhaseDeployments)
}

// applyManifestsPhaseFromKubeconfig loads the kubeconfig at kubeconfigPath
// and applies the selected phase of fsys against the cluster it addresses.
// It is split from the phase entry points so the manifest source is
// injectable: the real entry points pass the embedded set, while a tier-2
// envtest passes a small representative set the test wrote a kubeconfig for,
// exercising the same kubeconfig-loading path. spec: §17.4 (in-cluster
// control plane).
func applyManifestsPhaseFromKubeconfig(ctx context.Context, kubeconfigPath string, fsys fs.FS, phase applyPhase) error {
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return fmt.Errorf("embedded apply: load kubeconfig %s: %w", kubeconfigPath, err)
	}
	return applyManifestsPhaseFromConfig(ctx, cfg, fsys, phase)
}

// applyManifestsFromConfig applies every object in fsys to the cluster
// addressed by cfg in one pass. It is the envtest entry point: a tier-2
// envtest drives the full apply against a real kube-apiserver from an
// already-resolved config without writing a kubeconfig file. The manifest
// source is injected so the test can supply a small representative set
// rather than the full embedded render, which references images and CRDs
// envtest does not carry.
//
// spec: §17.4 (in-cluster control plane).
func applyManifestsFromConfig(ctx context.Context, cfg *rest.Config, fsys fs.FS) error {
	return applyManifestsPhaseFromConfig(ctx, cfg, fsys, applyPhaseAll)
}

// applyManifestsPhaseFromConfig applies the objects in fsys selected by
// phase to the cluster addressed by cfg. The objects are still sorted into
// the §17.4 dependency order within the phase, so a non-Deployment pass
// applies namespaces before the namespaced objects they hold. spec: §17.4.
func applyManifestsPhaseFromConfig(ctx context.Context, cfg *rest.Config, fsys fs.FS, phase applyPhase) error {
	objs, err := decodeManifests(fsys)
	if err != nil {
		return err
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("embedded apply: build dynamic client: %w", err)
	}
	mapper, err := newRESTMapper(cfg)
	if err != nil {
		return err
	}
	// Apply in dependency order so a later object's prerequisites already
	// exist: namespaces hold the namespaced objects, CRDs register the kinds
	// custom resources use, RBAC grants the workloads their permissions, the
	// config/secret material and Services are referenced by the Deployments,
	// and the Deployments come last so their pods do not start before their
	// namespace, config, and Services exist.
	sortByApplyOrder(objs)
	for i := range objs {
		if !phaseSelects(phase, objs[i].GetKind()) {
			continue
		}
		if err := applyObject(ctx, dyn, mapper, &objs[i]); err != nil {
			return err
		}
	}
	return nil
}

// phaseSelects reports whether the apply phase submits an object of the
// given kind. The bring-up's two-phase fence rests on this split: the
// non-Deployment phase skips every Deployment so the Deployments are
// withheld until the image import lands, and the Deployment phase applies
// only the Deployments. spec: §17.4 (apply the Deployments after the import
// lands).
func phaseSelects(phase applyPhase, kind string) bool {
	switch phase {
	case applyPhaseAll:
		return true
	case applyPhaseNonDeployments:
		return kind != "Deployment"
	case applyPhaseDeployments:
		return kind == "Deployment"
	default:
		return false
	}
}

// decodeManifests reads every *.yaml file in fsys, splits each into its
// constituent YAML documents, and decodes each non-empty document into an
// unstructured.Unstructured. Comment-only and empty documents (the
// generated render header, the leading separators) are skipped.
func decodeManifests(fsys fs.FS) ([]unstructured.Unstructured, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("embedded apply: list manifests: %w", err)
	}
	var out []unstructured.Unstructured
	for _, e := range entries {
		if e.IsDir() || !hasYAMLExt(e.Name()) {
			continue
		}
		raw, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, fmt.Errorf("embedded apply: read %s: %w", e.Name(), err)
		}
		docs, err := decodeStream(e.Name(), raw)
		if err != nil {
			return nil, err
		}
		out = append(out, docs...)
	}
	return out, nil
}

// decodeStream splits a multi-document YAML stream on the "\n---" document
// separator and decodes each non-empty document into an Unstructured. A
// document that decodes to no kind (a stray comment block between
// separators) is skipped rather than applied.
func decodeStream(name string, raw []byte) ([]unstructured.Unstructured, error) {
	var out []unstructured.Unstructured
	for _, chunk := range bytes.Split(raw, []byte("\n---")) {
		trimmed := bytes.TrimSpace(chunk)
		if len(trimmed) == 0 {
			continue
		}
		var obj unstructured.Unstructured
		if err := k8syaml.Unmarshal(trimmed, &obj.Object); err != nil {
			return nil, fmt.Errorf("embedded apply: decode document in %s: %w", name, err)
		}
		if obj.GetKind() == "" {
			continue
		}
		out = append(out, obj)
	}
	return out, nil
}

// newRESTMapper builds a discovery-backed RESTMapper that resolves each
// object's GroupVersionKind to the GroupVersionResource the dynamic client
// addresses. The discovery results are cached in memory so the mapper does
// not re-query the API server for every object in the manifest set.
func newRESTMapper(cfg *rest.Config) (meta.RESTMapper, error) {
	dc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("embedded apply: build discovery client: %w", err)
	}
	cached := memory.NewMemCacheClient(dc)
	return restmapper.NewDeferredDiscoveryRESTMapper(cached), nil
}

// applyObject server-side-applies one object through the dynamic client,
// resolving its GVR via the RESTMapper and routing namespaced objects
// through their namespace. The apply uses types.ApplyPatchType under a
// stable field manager with Force set, mirroring the controller's
// RawPatch(ApplyPatchType) server-side apply, so a re-apply reconverges the
// live object in place and takes ownership of any field a prior embedded
// apply set. spec: §17.4 (in-cluster control plane).
func applyObject(ctx context.Context, dyn dynamic.Interface, mapper meta.RESTMapper, obj *unstructured.Unstructured) error {
	gvk := obj.GroupVersionKind()
	mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return fmt.Errorf("embedded apply: resolve %s %s/%s: %w", gvk.Kind, obj.GetNamespace(), obj.GetName(), err)
	}
	body, err := obj.MarshalJSON()
	if err != nil {
		return fmt.Errorf("embedded apply: marshal %s %s: %w", gvk.Kind, obj.GetName(), err)
	}
	resource := dyn.Resource(mapping.Resource)
	var ri dynamic.ResourceInterface = resource
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		ri = resource.Namespace(obj.GetNamespace())
	}
	force := true
	_, err = ri.Patch(ctx, obj.GetName(), types.ApplyPatchType, body, metav1.PatchOptions{
		FieldManager: applyFieldManager,
		Force:        &force,
	})
	if err != nil {
		return fmt.Errorf("embedded apply: apply %s %s/%s: %w", gvk.Kind, obj.GetNamespace(), obj.GetName(), err)
	}
	return nil
}

// applyOrder ranks an object's kind by the apply phase it belongs to so the
// manifest set is applied prerequisites-first. Lower ranks apply earlier.
// The §17.4 order is namespaces, CRDs, RBAC, config and secret material,
// Services, then Deployments; every other kind lands between the Services
// and the Deployments, which keeps cluster-scoped supporting objects (the
// runc RuntimeClass, ServiceAccounts) ahead of the workloads that reference
// them without enumerating each one.
func applyOrder(kind string) int {
	switch kind {
	case "Namespace":
		return 0
	case "CustomResourceDefinition":
		return 1
	case "ServiceAccount", "Role", "RoleBinding", "ClusterRole", "ClusterRoleBinding":
		return 2
	case "ConfigMap", "Secret":
		return 3
	case "Service":
		return 4
	case "Deployment":
		return 6
	default:
		return 5
	}
}

// sortByApplyOrder orders objs by their apply phase, stably so objects in
// the same phase keep their render order (which preserves any
// within-phase ordering the chart already established).
func sortByApplyOrder(objs []unstructured.Unstructured) {
	sort.SliceStable(objs, func(i, j int) bool {
		return applyOrder(objs[i].GetKind()) < applyOrder(objs[j].GetKind())
	})
}

// hasYAMLExt reports whether name ends in a YAML file extension.
func hasYAMLExt(name string) bool {
	return len(name) >= 5 && name[len(name)-5:] == ".yaml" ||
		len(name) >= 4 && name[len(name)-4:] == ".yml"
}
