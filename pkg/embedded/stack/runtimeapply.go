// SPDX-License-Identifier: MIT

package stack

import (
	"context"
	"fmt"
	"os"
	"regexp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	k8syaml "sigs.k8s.io/yaml"

	lennyv1alpha1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
)

// runtimeImageDigestPattern mirrors the §5.3 supply-chain pattern the Runtime
// CRD enforces on spec.image at the embedded API server
// (+kubebuilder:validation:Pattern=`@sha256:[A-Fa-f0-9]{64}$` in
// pkg/apis/lenny/v1alpha1/runtime_types.go; embedded copy
// pkg/embedded/crds/lenny.dev_runtimes.yaml). The pattern is reproduced here so
// the verb fails closed with a clear, actionable message before apply when a
// runtime file carries a tag-based image (the docker-build output a runtime
// author starts from, for example `my-agent:dev`), rather than letting the
// write reach the API server and surface a raw OpenAPI pattern rejection. The
// CRD pattern is unanchored at the start and anchored at the end ($), the same
// match the API server applies, so any reference this gate accepts the API
// server also accepts and any it rejects the API server also rejects. spec: §5.3
// (digest-pinned image references), §17.4 (the runtime-apply verb).
var runtimeImageDigestPattern = regexp.MustCompile(`@sha256:[A-Fa-f0-9]{64}$`)

// The §4.6.2 pool defaults the runtime-apply verb stamps onto the derived
// SandboxTemplate/SandboxWarmPool when the runtime file does not pin them.
// They mirror the echo seed's poolstore→CRD field mapping
// (poolscaling.PoolStoreSource.toConfig: MinWarm = MaxWarm = warmCount, the
// isolation profile, the egress profile, the DNS policy, and the resource
// class) so a runtime materialized through the verb warms a pod the same way
// the echo seed does, with no PoolScalingController. The DNS policy is the
// §13.2 cluster-default opt-out the embedded substrate requires because it
// runs no dedicated lenny-system CoreDNS. spec: §4.6.2 (the poolstore→CRD
// projection), §5.2 (single-pod hot pool), §13.2 (cluster-default DNS opt-out).
const (
	// runtimeApplyWarmCount is the §5.2 single-pod hot-pool floor the verb
	// applies (MinWarm = MaxWarm = 1). The §17.4 walkthrough invokes the verb
	// with no warm-count flag, so a custom runtime warms one pod by default,
	// matching the echo seed.
	runtimeApplyWarmCount = 1
	// runtimeApplyIsolationDefault is the §5.3 isolation profile applied when
	// the runtime file pins none. The embedded single-node cluster renders
	// only the `runc` RuntimeClass the `standard` profile maps to, so a
	// runtime that does not pin an isolation profile defaults to the one the
	// substrate can warm. spec: §5.3 (standard→runc), §17.4 (local fidelity).
	runtimeApplyIsolationDefault = "standard"
	runtimeApplyEgressProfile    = echoPoolEgressProfile
	runtimeApplyDNSPolicy        = echoPoolDNSPolicy
	runtimeApplyResourceClass    = echoPoolResourceClass
)

// RunRuntimeApply parses the runtime file at path, assembles the runtime's
// Runtime, SandboxTemplate, and SandboxWarmPool CRD set, and applies the set
// to the embedded cluster reachable through kubeconfigPath. It is the
// generalized, runtime-agnostic counterpart of the echo seed's direct pool
// materialization: under the §17.4 no-Postgres development profile no
// PoolScalingController runs to project a poolstore row into a SandboxWarmPool
// CRD, and `lenny-ctl runtime register` writes only the runtime-registry
// record, so without this set a custom runtime registered through the
// walkthrough has no Runtime CRD and no pool and ResolvePool returns
// ErrNoMatchingPool. The verb applies the set so the Sandbox controller
// resolves the runtime by name and the unconditionally-registered
// WarmPoolController reconciles the pool to a warm pod.
//
// The SandboxTemplate/SandboxWarmPool pair is applied through the C1
// dynamic-apply path (applyObjects), and the Runtime CR through the same
// typed-client upsert the echo seed uses (upsertRuntimeCR), so the verb reuses
// both established apply mechanisms rather than adding a parallel one. The
// whole set is idempotent: a re-run reconverges the live objects in place.
//
// spec: §17.4 (the runtime-apply verb materializes the CRD set without a
// PoolScalingController), §5.2 (ResolvePool lists the applied SandboxWarmPool),
// §4.6.2 (direct pool materialization).
func RunRuntimeApply(ctx context.Context, kubeconfigPath, path string) error {
	rt, err := loadRuntimeFile(path)
	if err != nil {
		return err
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return fmt.Errorf("embedded runtime apply: load kubeconfig %s: %w", kubeconfigPath, err)
	}
	return ApplyRuntimeSetFromConfig(ctx, cfg, rt)
}

// loadRuntimeFile reads path and decodes the single Runtime resource it
// carries. The §17.4 walkthrough's runtime-crds.yaml is a one-document
// Runtime; a missing name, an empty image, or a tag-based (non-digest-pinned)
// image is rejected so the verb fails closed rather than applying an
// unresolvable CRD set the API server would reject partway through. The image
// check mirrors the §5.3 supply-chain pattern the Runtime CRD enforces at the
// API server, so a runtime author who starts from a tag-based docker-build
// reference (for example `my-agent:dev`) gets an actionable message naming the
// digest-pinned form rather than a raw OpenAPI pattern rejection deep in the
// apply. spec: §5.1 (the Runtime declarative record), §5.3 (digest-pinned
// images).
func loadRuntimeFile(path string) (*lennyv1alpha1.Runtime, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("embedded runtime apply: read %s: %w", path, err)
	}
	var rt lennyv1alpha1.Runtime
	if err := k8syaml.Unmarshal(raw, &rt); err != nil {
		return nil, fmt.Errorf("embedded runtime apply: decode Runtime in %s: %w", path, err)
	}
	if rt.Name == "" {
		return nil, fmt.Errorf("embedded runtime apply: %s carries no metadata.name", path)
	}
	if rt.Spec.Image == "" {
		return nil, fmt.Errorf("embedded runtime apply: %s carries no spec.image", path)
	}
	if !runtimeImageDigestPattern.MatchString(rt.Spec.Image) {
		return nil, fmt.Errorf(
			"embedded runtime apply: %s spec.image %q must be digest-pinned (@sha256:<64-hex>); "+
				"a tag-based reference is rejected by the Runtime CRD's §5.3 supply-chain pattern. "+
				"Use the digest from 'lenny image import' output, e.g. my-agent@sha256:<digest>",
			path, rt.Spec.Image,
		)
	}
	return &rt, nil
}

// ApplyRuntimeSetFromConfig assembles the runtime's CRD set from rt and applies
// it against an already-resolved rest config through the C1 dynamic-apply path
// (the SandboxTemplate/SandboxWarmPool pair) plus the echo seed's typed-client
// upsert (the Runtime CR). It is split from RunRuntimeApply so a tier-2 envtest
// drives the same apply path against a real kube-apiserver with the lenny.dev
// CRDs installed, without writing a kubeconfig file, the way
// ApplyEchoPoolFromConfig exposes the echo seed's apply. The Runtime CR is
// applied first (the SandboxTemplate's runtimeRef points at it), then the
// SandboxTemplate and SandboxWarmPool pair. spec: §17.4, §4.6.2.
func ApplyRuntimeSetFromConfig(ctx context.Context, cfg *rest.Config, rt *lennyv1alpha1.Runtime) error {
	scheme := runtime.NewScheme()
	utilruntime.Must(lennyv1alpha1.AddToScheme(scheme))
	cl, err := ctrlclient.New(cfg, ctrlclient.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("embedded runtime apply: build cluster client: %w", err)
	}
	cr := runtimeCRFromFile(rt)
	if err := upsertRuntimeCR(ctx, cl, cr); err != nil {
		return fmt.Errorf("embedded runtime apply: apply Runtime %s: %w", cr.Name, err)
	}
	objs, err := runtimePoolUnstructured(cr)
	if err != nil {
		return err
	}
	if err := applyObjects(ctx, cfg, objs); err != nil {
		return fmt.Errorf("embedded runtime apply: apply pool CRDs for %s: %w", cr.Name, err)
	}
	return nil
}

// runtimeCRFromFile normalizes the runtime parsed from the file into the
// cluster-scoped Runtime CR the verb applies. It defaults the §5.1 fields the
// Sandbox controller and the CRD validation require but the walkthrough file
// may omit (type → agent, executionMode → session, isolationProfile →
// standard, deploymentModel → sidecar, the §17.4 walkthrough default), so a
// minimal runtime-crds.yaml carrying only name, image, and integrationLevel
// applies cleanly. The image is passed through as written: loadRuntimeFile has
// already rejected any non-digest-pinned reference against the §5.3
// supply-chain pattern the CRD enforces, so the image reaching here is
// digest-pinned and the API server accepts it. spec: §5.1 (Runtime defaults),
// §4.7 (sidecar default deployment model).
func runtimeCRFromFile(rt *lennyv1alpha1.Runtime) *lennyv1alpha1.Runtime {
	spec := rt.Spec
	if spec.Type == "" {
		spec.Type = "agent"
	}
	if spec.ExecutionMode == "" {
		spec.ExecutionMode = "session"
	}
	if spec.IsolationProfile == "" {
		spec.IsolationProfile = runtimeApplyIsolationDefault
	}
	if spec.DeploymentModel == "" {
		spec.DeploymentModel = "sidecar"
	}
	return &lennyv1alpha1.Runtime{
		TypeMeta:   metav1.TypeMeta{APIVersion: lennyv1alpha1.GroupVersion.String(), Kind: "Runtime"},
		ObjectMeta: metav1.ObjectMeta{Name: rt.Name},
		Spec:       spec,
	}
}

// runtimePoolObjects builds the SandboxTemplate and SandboxWarmPool for the
// runtime cr in the agent namespace. The field mapping reproduces the echo
// seed's poolstore→CRD projection (echoPoolObjects, in turn reproducing
// poolscaling.PoolStoreSource.toConfig): the SandboxTemplate carries the
// runtimeRef, the runtime's isolation profile, and the embedded egress, DNS,
// and resource-class defaults, and the SandboxWarmPool sets templateRef to the
// runtime name with MinWarm = MaxWarm = warmCount, so the directly-applied
// pair matches what the PoolScalingController would have produced. The pool is
// named for the runtime so multiple runtimes applied through the verb each get
// their own pool. spec: §4.6.2 (the poolstore→CRD projection), §5.2 (hot pool).
func runtimePoolObjects(cr *lennyv1alpha1.Runtime) (*lennyv1alpha1.SandboxTemplate, *lennyv1alpha1.SandboxWarmPool) {
	poolName := RuntimePoolName(cr.Name)
	tmpl := &lennyv1alpha1.SandboxTemplate{
		TypeMeta:   metav1.TypeMeta{APIVersion: lennyv1alpha1.GroupVersion.String(), Kind: "SandboxTemplate"},
		ObjectMeta: metav1.ObjectMeta{Name: poolName, Namespace: agentNamespace},
		Spec: lennyv1alpha1.SandboxTemplateSpec{
			RuntimeRef:       cr.Name,
			IsolationProfile: cr.Spec.IsolationProfile,
			EgressProfile:    runtimeApplyEgressProfile,
			DNSPolicy:        runtimeApplyDNSPolicy,
			ResourceClass:    runtimeApplyResourceClass,
		},
	}
	pool := &lennyv1alpha1.SandboxWarmPool{
		TypeMeta:   metav1.TypeMeta{APIVersion: lennyv1alpha1.GroupVersion.String(), Kind: "SandboxWarmPool"},
		ObjectMeta: metav1.ObjectMeta{Name: poolName, Namespace: agentNamespace},
		Spec: lennyv1alpha1.SandboxWarmPoolSpec{
			TemplateRef: poolName,
			MinWarm:     runtimeApplyWarmCount,
			MaxWarm:     runtimeApplyWarmCount,
		},
	}
	return tmpl, pool
}

// RuntimePoolName returns the SandboxTemplate/SandboxWarmPool name the
// runtime-apply verb derives for a runtime. The pool is named for the runtime
// (runtime-pool) so multiple runtimes applied through the verb each get a
// distinct pool the WarmPoolController reconciles independently.
func RuntimePoolName(runtimeName string) string {
	return runtimeName + "-pool"
}

// runtimePoolUnstructured builds the runtime's SandboxTemplate/SandboxWarmPool
// pair and encodes each as an unstructured.Unstructured carrying its lenny.dev
// GVK, so the C1 applier (applyObjects) resolves the GVR via the RESTMapper and
// server-side-applies it, the same transport the echo seed uses. spec: §4.6.2.
func runtimePoolUnstructured(cr *lennyv1alpha1.Runtime) ([]unstructured.Unstructured, error) {
	tmpl, pool := runtimePoolObjects(cr)
	tmplU, err := toUnstructured(tmpl)
	if err != nil {
		return nil, fmt.Errorf("embedded runtime apply: encode SandboxTemplate %s: %w", tmpl.Name, err)
	}
	poolU, err := toUnstructured(pool)
	if err != nil {
		return nil, fmt.Errorf("embedded runtime apply: encode SandboxWarmPool %s: %w", pool.Name, err)
	}
	return []unstructured.Unstructured{*tmplU, *poolU}, nil
}
