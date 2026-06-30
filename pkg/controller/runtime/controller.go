// SPDX-License-Identifier: MIT

// Package runtime holds the §5.1 RuntimeReconciler. The Runtime CRD is
// the declarative registration source for platform-global agent and
// mcp runtimes; this controller watches Runtime resources and mirrors
// each one into the gateway's runtime registry (the runtime_definitions
// table the §15.1 admin CRUD endpoints and the session-creation
// runtimeRef resolver read). Once a runtime is mirrored the controller
// sets the §5.1 `Registered` status condition the CRD's kubebuilder
// annotation promises.
//
// The CRD carries the §5.1 registration fields and the §26 catalog
// template (type, image, integration level, execution mode, isolation
// profile, allowed resource classes, supported providers, the
// credential-capabilities block, capabilities, the runtimeOptionsSchema
// ref, limits, setupCommandPolicy, setupPolicy, defaultPoolConfig, and the
// agentInterface). The reconciler's update path mirrors the CRD-owned
// fields and leaves the few admin-only fields (publishedMetadata, tool
// capability overrides) intact.
//
// Deletion is handled through a finalizer: removing the Runtime CRD
// soft-deletes the mirrored registry row (the gateway then refuses new
// pod registrations against it but keeps the row for audit, per §15.1)
// before the object leaves etcd.
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/controllermetrics"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

const (
	// runtimeFinalizer guards the gateway-registry soft-delete: the
	// reconciler adds it before mirroring so a Runtime CRD deletion
	// soft-deletes the registry row before the object leaves etcd.
	runtimeFinalizer = "lenny.dev/runtime-registry"

	// conditionRegistered is the §5.1 status condition the controller
	// sets once the runtime is mirrored into the gateway registry.
	conditionRegistered = "Registered"

	// fieldOwner is the SSA field manager the reconciler writes the
	// Runtime status subresource under. It is distinct from the §4.6.3
	// pool-CRD field managers; the Runtime CRD status is owned solely by
	// this controller.
	fieldOwner = "lenny-runtime-controller"

	// reasonMirrored / reasonInvalid are the §5.1 Registered-condition
	// reasons. Mirrored is the success state; Invalid marks a permanent
	// registration error (e.g., a CRD name the §5.1 registry-name pattern
	// rejects) that no requeue can resolve.
	reasonMirrored = "Mirrored"
	reasonInvalid  = "InvalidRuntime"

	// deploymentModelSidecar is the §4.7 sidecar deployment model, the
	// only model the nonce-only requireSoPeercred opt-in is valid on. An
	// empty deploymentModel defaults to sidecar at registration time (the
	// Runtime CRD enum permits sidecar, embedded, or empty), so the guard
	// below treats empty as sidecar.
	deploymentModelSidecar = "sidecar"

	// errNonceOnlyEmbedded is the §4.7 message the RuntimeReconciler sets
	// on the Registered condition when an embedded runtime carries
	// requireSoPeercred: false. The embedded model is a single trusted
	// process with no adapter-agent socket boundary to fall back from.
	errNonceOnlyEmbedded = "requireSoPeercred: false is only valid on deploymentModel: sidecar runtimes"
)

// Reconciler mirrors §5.1 Runtime CRDs into the gateway runtime
// registry. Store is the same runtimestore.Store the gateway reads at
// session creation; in production it is the Postgres-backed pgstore
// pointed at the shared runtime_definitions table.
type Reconciler struct {
	Client client.Client
	Scheme *k8sruntime.Scheme

	// Store is the gateway runtime registry the CRD is mirrored into.
	Store runtimestore.Store

	// DevMode selects the §5.3 dev-mode isolation default (`standard`
	// rather than `sandboxed`) applied to a runtime that omits an
	// isolation profile, matching the gateway admin path's ApplyDefaults.
	DevMode bool

	MaxConcurrentReconciles int
	QueueFactory            controllermetrics.QueueFactory
}

// permanentError marks a mirror failure that no requeue can resolve, so
// Reconcile records it on the Registered condition and stops rather than
// tight-looping.
type permanentError struct{ err error }

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

// Reconcile mirrors a single Runtime CRD into the gateway registry and
// maintains its §5.1 Registered condition. A deleted CRD soft-deletes
// the registry row and drops the finalizer.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var rt lennyv1.Runtime
	if err := r.Client.Get(ctx, req.NamespacedName, &rt); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Deletion path: soft-delete the registry row, then release the
	// finalizer so the API server can remove the object.
	if !rt.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&rt, runtimeFinalizer) {
			if err := r.Store.SoftDelete(ctx, rt.Name, time.Now().UTC()); err != nil && !errors.Is(err, runtimestore.ErrNotFound) {
				return ctrl.Result{}, fmt.Errorf("soft-delete runtime %q: %w", rt.Name, err)
			}
			controllerutil.RemoveFinalizer(&rt, runtimeFinalizer)
			if err := r.Client.Update(ctx, &rt); err != nil {
				return ctrl.Result{}, fmt.Errorf("remove finalizer: %w", err)
			}
		}
		return ctrl.Result{}, nil
	}

	// Add the finalizer before the first mirror so a delete that races
	// the initial reconcile still soft-deletes. The Update re-enqueues
	// through the watch, so the mirror runs on the next pass.
	if !controllerutil.ContainsFinalizer(&rt, runtimeFinalizer) {
		controllerutil.AddFinalizer(&rt, runtimeFinalizer)
		if err := r.Client.Update(ctx, &rt); err != nil {
			return ctrl.Result{}, fmt.Errorf("add finalizer: %w", err)
		}
		return ctrl.Result{}, nil
	}

	if err := r.mirror(ctx, &rt); err != nil {
		var perm *permanentError
		if errors.As(err, &perm) {
			log.Error(err, "runtime registration is permanently invalid", "runtime", rt.Name)
			return ctrl.Result{}, r.setCondition(ctx, &rt, metav1.ConditionFalse, reasonInvalid, perm.Error())
		}
		return ctrl.Result{}, fmt.Errorf("mirror runtime %q: %w", rt.Name, err)
	}

	return ctrl.Result{}, r.setCondition(ctx, &rt, metav1.ConditionTrue, reasonMirrored,
		"runtime mirrored into the gateway registry")
}

// mirror upserts the CRD into the registry: it creates the row when
// absent and updates the CRD-owned fields when present. A registry name
// the §5.1 pattern rejects is a permanent error.
func (r *Reconciler) mirror(ctx context.Context, rt *lennyv1.Runtime) error {
	if err := runtimestore.ValidateName(rt.Name); err != nil {
		return &permanentError{err}
	}
	// spec: §4.7 — requireSoPeercred: false (nonce-only mode) weakens the
	// adapter-agent authentication boundary and is valid only on the
	// sidecar deployment model, which has the adapter-agent socket boundary
	// to fall back from. The embedded model is a single trusted process
	// with no such boundary, so an embedded runtime that sets the field is
	// a permanent registration error: it is marked Registered=False
	// (InvalidRuntime) and is not mirrored into the registry. A nil or
	// explicit true is identical to the default and is permitted on either
	// model. An empty deploymentModel defaults to sidecar.
	if rt.Spec.RequireSoPeercred != nil && !*rt.Spec.RequireSoPeercred &&
		rt.Spec.DeploymentModel != "" && rt.Spec.DeploymentModel != deploymentModelSidecar {
		return &permanentError{errors.New(errNonceOnlyEmbedded)}
	}

	_, err := r.Store.Get(ctx, rt.Name)
	switch {
	case errors.Is(err, runtimestore.ErrNotFound):
		desired := r.runtimeFromCRD(rt)
		runtimestore.ApplyDefaults(&desired, r.DevMode)
		if cerr := r.Store.Create(ctx, desired); cerr != nil {
			// A racing writer (the admin API or a concurrent reconcile)
			// may have created the row between Get and Create; fall through
			// to the update path so the CRD-owned fields still converge.
			if errors.Is(cerr, runtimestore.ErrAlreadyExists) {
				return r.updateExisting(ctx, rt)
			}
			return cerr
		}
		return nil
	case err != nil:
		return err
	default:
		return r.updateExisting(ctx, rt)
	}
}

// updateExisting overwrites only the CRD-owned fields on the stored
// runtime, re-applies the §5.1 defaults, and clears any prior
// soft-delete so a re-applied CRD reactivates the registration. The few
// admin-only fields the CRD does not model (publishedMetadata, tool
// capability overrides) are left untouched so an admin-API configuration
// layered on top of the CRD base survives a re-reconcile.
func (r *Reconciler) updateExisting(ctx context.Context, rt *lennyv1.Runtime) error {
	_, err := r.Store.Update(ctx, rt.Name, func(stored *runtimestore.Runtime) error {
		applyCRDFields(stored, rt)
		runtimestore.ApplyDefaults(stored, r.DevMode)
		stored.DeletedAt = time.Time{}
		return nil
	})
	return err
}

// runtimeFromCRD builds a fresh registry runtime from the CRD-owned
// field subset.
func (r *Reconciler) runtimeFromCRD(rt *lennyv1.Runtime) runtimestore.Runtime {
	var out runtimestore.Runtime
	applyCRDFields(&out, rt)
	return out
}

// applyCRDFields copies the §5.1 CRD-owned fields onto dst. The CRD
// (RuntimeSpec) carries type, image, integration level, execution mode,
// isolation profile, allowed resource classes, supported providers, and
// the credential-capabilities block; the object's domain labels become
// the runtime's §10.6 runtimeSelector label set. DeploymentModel is a
// CRD-only field with no registry counterpart and is intentionally not
// mirrored.
func applyCRDFields(dst *runtimestore.Runtime, rt *lennyv1.Runtime) {
	dst.Name = rt.Name
	dst.Type = runtimestore.RuntimeType(rt.Spec.Type)
	dst.Image = rt.Spec.Image
	dst.IntegrationLevel = runtimestore.IntegrationLevel(rt.Spec.IntegrationLevel)
	dst.ExecutionMode = runtimestore.ExecutionMode(rt.Spec.ExecutionMode)
	dst.IsolationProfile = isolation.Profile(rt.Spec.IsolationProfile)
	dst.AllowedResourceClasses = append([]string(nil), rt.Spec.AllowedResourceClasses...)
	dst.SupportedProviders = append([]string(nil), rt.Spec.SupportedProviders...)
	dst.CredentialCapabilities = credentialCapabilitiesFromCRD(rt.Spec.CredentialCapabilities)
	// spec: §12.9 line 1025 — workspaceTier is mirrored from the CRD into the
	// gateway registry so §5.2 cross-tenant-reuse rejection (line 396) and the
	// §6.4 T4 dedicated-node injection both see the same value the deployer
	// declared on the Runtime resource.
	dst.WorkspaceTier = runtimestore.WorkspaceTier(rt.Spec.WorkspaceTier)
	// spec: §4.7 — requireSoPeercred is mirrored from the CRD into the
	// gateway registry, where the §5.3 pool admission gate evaluates it. A
	// nil (unset) CRD field resolves to true so the persisted row matches
	// the migration column's NOT NULL DEFAULT true and the nonce-only gate
	// fails closed: only an explicit false reaches the store as false. The
	// embedded + false combination is rejected before mirroring (mirror),
	// so an embedded runtime never reaches this copy carrying false.
	dst.RequireSoPeercred = requireSoPeercredFromCRD(rt.Spec.RequireSoPeercred)
	// spec: §5.1 / §7.5 lines 481-490 — setupCommandPolicy is mirrored from
	// the CRD so the §7.5 gateway-side enforcement (F-7.5.1) reads the same
	// policy the deployer declared on the Runtime resource. F-7.5.10.
	dst.SetupCommandPolicy = setupCommandPolicyFromCRD(rt.Spec.SetupCommandPolicy)
	// spec: §7.4 lines 458, 462; §13.4 — archivePolicy is mirrored from
	// the CRD so the adapter's uploadArchive extractor reads the same
	// AllowSymlinks toggle the deployer declared on the Runtime resource.
	// F-7.4.4.
	dst.ArchivePolicy = archivePolicyFromCRD(rt.Spec.ArchivePolicy)
	// spec: §5.1 lines 60-64; §26.9 line 407; §26.10 line 432; §26.11 line
	// 466 — capabilities (interaction + injection.{supported, modes}) are
	// mirrored from the CRD so the gateway registry advertises the §15.4.6
	// conformance battery the runtime actually implements. F-26.9.2 /
	// F-26.10.2 / F-26.11.2.
	dst.Capabilities = capabilitiesFromCRD(rt.Spec.Capabilities)
	// spec: §5.1 line 92; §26.9 line 408; §26.10 line 434; §26.11 line 465
	// — runtimeOptionsSchemaRef is rendered as a JSON {"$ref": "<url>"}
	// schema fragment so the §14 validator resolves the v1 schema URL the
	// runtime advertises. An empty CRD field leaves the registry schema
	// nil. F-26.9.2 / F-26.10.2 / F-26.11.2.
	dst.RuntimeOptionsSchema = runtimeOptionsSchemaFromRef(rt.Spec.RuntimeOptionsSchemaRef)
	// spec: §5.1 line 68; §26.11 line 467 — delegationPolicyRef binds the
	// runtime to its §8.3 DelegationPolicy so the gateway resolves the
	// policy at session-creation time. §26.11 declares the field required
	// on the crewai runtime. F-26.11.2.
	dst.DelegationPolicyRef = rt.Spec.DelegationPolicyRef
	// spec: §5.1 lines 76-79; §26.2 lines 81-86; §26.3 lines 166-169 —
	// limits is mirrored from the CRD so the §11.3 / §6.2 session-age
	// watchdog, the upload cap, and the inter-agent request_input timeout
	// read the per-runtime caps the §26 coding-agent catalog declares.
	// F-26.2.1 / F-26.3.2.
	dst.Limits = limitsFromCRD(rt.Spec.Limits)
	// spec: §5.1 lines 90-92; §26.3 lines 174-176 — setupPolicy is mirrored
	// from the CRD so the §6.2 finalizing-state watchdog enforces the
	// runtime-declared setup timeout and onTimeout disposition. F-26.2.1 /
	// F-26.3.4.
	dst.SetupPolicy = setupPolicyFromCRD(rt.Spec.SetupPolicy)
	// spec: §5.1 lines 97-100; §26.3 lines 181-184 — defaultPoolConfig is
	// mirrored from the CRD so the §5.2 pool resolver consults the
	// runtime-declared warmCount / resourceClass / egressProfile before
	// falling back to platform defaults. F-26.2.1 / F-26.3.5.
	dst.DefaultPoolConfig = defaultPoolConfigFromCRD(rt.Spec.DefaultPoolConfig)
	// spec: §5.1 lines 102-130; §26.3 lines 185-199 — agentInterface is
	// mirrored from the CRD so the registered Runtime advertises the
	// agent's declared skills and the §15 A2A agent-card auto-generation
	// has a source. F-26.2.1 / F-26.3.5.
	dst.AgentInterface = agentInterfaceFromCRD(rt.Spec.AgentInterface)
	dst.Labels = domainLabels(rt.Labels)
}

// limitsFromCRD maps the §5.1 limits block. A nil CRD block mirrors to a
// nil registry block (the runtime declares no per-runtime caps and the
// gateway falls back to platform defaults). spec: §5.1 lines 76-79 —
// F-26.2.1 / F-26.3.2.
func limitsFromCRD(l *lennyv1.RuntimeLimits) *runtimestore.Limits {
	if l == nil {
		return nil
	}
	return &runtimestore.Limits{
		MaxSessionAgeSeconds:       l.MaxSessionAgeSeconds,
		MaxUploadSizeBytes:         l.MaxUploadSizeBytes,
		MaxRequestInputWaitSeconds: l.MaxRequestInputWaitSeconds,
		MaxElicitationWaitSeconds:  l.MaxElicitationWaitSeconds,
		MaxElicitationsPerSession:  l.MaxElicitationsPerSession,
	}
}

// setupPolicyFromCRD maps the §5.1 setupPolicy block. A nil CRD block
// mirrors to a nil registry block (no aggregate setup cap). spec: §5.1
// lines 90-92 — F-26.2.1 / F-26.3.4.
func setupPolicyFromCRD(p *lennyv1.SetupPolicy) *runtimestore.SetupPolicy {
	if p == nil {
		return nil
	}
	return &runtimestore.SetupPolicy{
		TimeoutSeconds: p.TimeoutSeconds,
		OnTimeout:      runtimestore.SetupTimeoutDisposition(p.OnTimeout),
	}
}

// defaultPoolConfigFromCRD maps the §5.1 defaultPoolConfig block. A nil CRD
// block mirrors to a nil registry block (the §5.2 pool resolver falls back
// to platform defaults). spec: §5.1 lines 97-100 — F-26.2.1 / F-26.3.5.
func defaultPoolConfigFromCRD(c *lennyv1.DefaultPoolConfig) *runtimestore.DefaultPoolConfig {
	if c == nil {
		return nil
	}
	return &runtimestore.DefaultPoolConfig{
		WarmCount:     c.WarmCount,
		ResourceClass: c.ResourceClass,
		EgressProfile: c.EgressProfile,
	}
}

// agentInterfaceFromCRD maps the §5.1 agentInterface descriptor. A nil CRD
// block mirrors to a nil registry block (the runtime declares no
// agentInterface). spec: §5.1 lines 102-130 — F-26.2.1 / F-26.3.5.
func agentInterfaceFromCRD(a *lennyv1.AgentInterface) *runtimestore.AgentInterface {
	if a == nil {
		return nil
	}
	out := &runtimestore.AgentInterface{
		Description:            a.Description,
		SupportsWorkspaceFiles: a.SupportsWorkspaceFiles,
	}
	for _, m := range a.InputModes {
		out.InputModes = append(out.InputModes, runtimestore.AgentInterfaceMode{Type: m.Type, Role: m.Role})
	}
	for _, m := range a.OutputModes {
		out.OutputModes = append(out.OutputModes, runtimestore.AgentInterfaceMode{Type: m.Type, Role: m.Role})
	}
	for _, s := range a.Skills {
		out.Skills = append(out.Skills, runtimestore.AgentInterfaceSkill{ID: s.ID, Name: s.Name, Description: s.Description})
	}
	return out
}

// credentialCapabilitiesFromCRD maps the §5.1 credentialCapabilities
// block. A nil CRD block mirrors to a nil registry block (no
// credentialCapabilities declared).
func credentialCapabilitiesFromCRD(c *lennyv1.CredentialCapabilities) *runtimestore.CredentialCapabilities {
	if c == nil {
		return nil
	}
	return &runtimestore.CredentialCapabilities{
		HotRotation:  c.HotRotation,
		ProxyDialect: append([]string(nil), c.ProxyDialect...),
	}
}

// setupCommandPolicyFromCRD maps the §5.1 / §7.5 setupCommandPolicy block.
// A nil CRD block mirrors to a nil registry block (no policy declared, the
// pre-F-7.5.1 admit-everything path remains). spec: §7.5 lines 481-490 —
// F-7.5.10.
func setupCommandPolicyFromCRD(p *lennyv1.SetupCommandPolicy) *runtimestore.SetupCommandPolicy {
	if p == nil {
		return nil
	}
	return &runtimestore.SetupCommandPolicy{
		Mode:        runtimestore.SetupCommandMode(p.Mode),
		Shell:       p.Shell,
		Allowlist:   append([]string(nil), p.Allowlist...),
		Blocklist:   append([]string(nil), p.Blocklist...),
		MaxCommands: int(p.MaxCommands),
	}
}

// archivePolicyFromCRD maps the §13.4 archivePolicy block. A nil CRD block
// mirrors to a nil registry block (no opt-in declared; symlinks rejected
// per §7.4 line 458). spec: §7.4 lines 458, 462; §13.4 — F-7.4.4.
func archivePolicyFromCRD(p *lennyv1.ArchivePolicy) *runtimestore.ArchivePolicy {
	if p == nil {
		return nil
	}
	return &runtimestore.ArchivePolicy{
		AllowSymlinks: p.AllowSymlinks,
	}
}

// requireSoPeercredFromCRD maps the §4.7 requireSoPeercred field from the
// CRD onto the registry runtime, resolving a nil (unset) pointer to an
// explicit true. The default of true keeps the SO_PEERCRED adapter-agent
// boundary self-test mandatory; only an explicit false activates
// nonce-only mode, and only then for a deploymentModel: sidecar runtime on
// an acknowledged pool. Returning an explicit pointer (rather than copying
// nil through) makes the registry row match the migration column's NOT NULL
// DEFAULT true and keeps the gate fail-closed. spec: §4.7.
func requireSoPeercredFromCRD(p *bool) *bool {
	if p == nil {
		t := true
		return &t
	}
	v := *p
	return &v
}

// capabilitiesFromCRD maps the §5.1 capabilities block. A nil CRD block
// mirrors to a nil registry block (no capabilities declared). The
// runtimestore enum types accept any string; ApplyDefaults at registry
// admission rejects invalid values with the spec-required error.
// spec: §5.1 lines 60-64 — F-26.9.2.
func capabilitiesFromCRD(c *lennyv1.RuntimeCapabilitiesCRD) *runtimestore.RuntimeCapabilities {
	if c == nil {
		return nil
	}
	out := &runtimestore.RuntimeCapabilities{
		Interaction: runtimestore.RuntimeInteraction(c.Interaction),
		PreConnect:  c.PreConnect,
	}
	if c.Injection != nil {
		out.Injection.Supported = c.Injection.Supported
		modes := make([]runtimestore.InjectionMode, 0, len(c.Injection.Modes))
		for _, m := range c.Injection.Modes {
			modes = append(modes, runtimestore.InjectionMode(m))
		}
		out.Injection.Modes = modes
	}
	return out
}

// runtimeOptionsSchemaFromRef converts the CRD's URL form of
// runtimeOptionsSchema into the gateway registry's JSON Schema fragment:
// a `{"$ref": "<url>"}` document the §14 validator dereferences. An empty
// ref returns nil. spec: §5.1 line 92; §26.9 line 408 — F-26.9.2.
func runtimeOptionsSchemaFromRef(ref string) json.RawMessage {
	if ref == "" {
		return nil
	}
	body, _ := json.Marshal(map[string]string{"$ref": ref})
	return body
}

// domainLabels returns the CRD object labels that are domain
// runtimeSelector labels, dropping the Kubernetes/Helm machinery labels
// (`app.kubernetes.io/*`, `helm.sh/*`) the chart's lenny.labels helper
// stamps on every object. The registry's §10.6 runtimeSelector matching
// then sees only meaningful labels. An empty result returns nil.
func domainLabels(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		if strings.HasPrefix(k, "app.kubernetes.io/") || strings.HasPrefix(k, "helm.sh/") {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// setCondition writes the §5.1 Registered condition (and the observed
// generation) onto the Runtime status via Server-Side Apply, re-reading
// on conflict. The Runtime status conditions are owned solely by this
// controller, so the apply asserts the full merged list.
func (r *Reconciler) setCondition(ctx context.Context, rt *lennyv1.Runtime, status metav1.ConditionStatus, reason, message string) error {
	key := client.ObjectKeyFromObject(rt)
	first := true
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var live lennyv1.Runtime
		if first {
			live = *rt
			first = false
		} else if err := r.Client.Get(ctx, key, &live); err != nil {
			return err
		}
		desired := metav1.Condition{
			Type:               conditionRegistered,
			Status:             status,
			Reason:             reason,
			Message:            message,
			ObservedGeneration: live.Generation,
		}
		merged := append([]metav1.Condition{}, live.Status.Conditions...)
		changed := meta.SetStatusCondition(&merged, desired)
		if live.Status.ObservedGeneration != live.Generation {
			changed = true
		}
		if !changed {
			return nil
		}
		patch := &lennyv1.Runtime{
			TypeMeta: metav1.TypeMeta{
				APIVersion: lennyv1.GroupVersion.String(),
				Kind:       "Runtime",
			},
			ObjectMeta: metav1.ObjectMeta{Name: live.Name},
		}
		patch.Status.Conditions = merged
		patch.Status.ObservedGeneration = live.Generation
		return r.Client.Status().Patch(ctx, patch, client.Apply, client.FieldOwner(fieldOwner))
	})
}

// SetupWithManager registers the reconciler on the manager, watching
// Runtime resources.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	opts := controller.Options{}
	if r.MaxConcurrentReconciles > 0 {
		opts.MaxConcurrentReconciles = r.MaxConcurrentReconciles
	}
	if r.QueueFactory != nil {
		opts.NewQueue = r.QueueFactory
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&lennyv1.Runtime{}).
		Named("runtime").
		WithOptions(opts).
		Complete(r)
}
