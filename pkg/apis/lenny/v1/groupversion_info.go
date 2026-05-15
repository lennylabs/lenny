// SPDX-License-Identifier: MIT

// Package v1 contains the lenny.dev/v1 CustomResourceDefinition API
// types — the Kubernetes-native declarations the Lenny controllers
// reconcile. The §4 control plane is CRD-driven: a Runtime declares a
// registered agent runtime, a SandboxWarmPool declares a pool of
// pre-warmed pods, a SandboxClaim requests a pod for a session, and a
// Sandbox is the per-pod lifecycle record.
//
// controller-gen generates the runtime.Object DeepCopy methods
// (zz_generated.deepcopy.go) and the CRD manifests from these types.
//
// +kubebuilder:object:generate=true
// +groupName=lenny.dev
package v1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

// GroupVersion is the lenny.dev/v1 API group-version that every Lenny
// custom resource belongs to.
var GroupVersion = schema.GroupVersion{Group: "lenny.dev", Version: "v1"}

// SchemeBuilder registers the lenny.dev/v1 types onto a runtime.Scheme.
var SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

// AddToScheme adds the lenny.dev/v1 types to a runtime.Scheme. The
// controller manager and the gateway's typed client call it during
// scheme setup.
var AddToScheme = SchemeBuilder.AddToScheme
