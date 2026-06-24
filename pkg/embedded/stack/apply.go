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
	return applyManifestsFromKubeconfig(ctx, kubeconfigPath, manifests.FS)
}

// applyManifestsFromKubeconfig loads the kubeconfig at kubeconfigPath and
// applies fsys against the cluster it addresses. It is split from
// ApplyManifests so the manifest source is injectable: ApplyManifests
// passes the embedded set, while a tier-2 envtest passes a small
// representative set the test wrote a kubeconfig for, exercising the same
// kubeconfig-loading entry point. spec: §17.4 (in-cluster control plane).
func applyManifestsFromKubeconfig(ctx context.Context, kubeconfigPath string, fsys fs.FS) error {
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return fmt.Errorf("embedded apply: load kubeconfig %s: %w", kubeconfigPath, err)
	}
	return applyManifestsFromConfig(ctx, cfg, fsys)
}

// applyManifestsFromConfig applies every object in fsys to the cluster
// addressed by cfg. It is split from applyManifestsFromKubeconfig so a
// tier-2 envtest can drive the apply against a real kube-apiserver from an
// already-resolved config without writing a kubeconfig file. The manifest
// source is injected so the test can supply a small representative set
// rather than the full embedded render, which references images and CRDs
// envtest does not carry.
//
// spec: §17.4 (in-cluster control plane).
func applyManifestsFromConfig(ctx context.Context, cfg *rest.Config, fsys fs.FS) error {
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
		if err := applyObject(ctx, dyn, mapper, &objs[i]); err != nil {
			return err
		}
	}
	return nil
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
