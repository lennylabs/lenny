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
// The CRD carries the registration subset documented in §5.1 and the
// §26 catalog template (type, image, integration level, execution mode,
// isolation profile, allowed resource classes, supported providers, and
// the credential-capabilities block). The richer §5.1 fields
// (capabilities, limits, the runtime options schema, the agent
// interface) are configured through the admin API after the CRD
// registers the base record; the reconciler's update path mirrors only
// the CRD-owned fields and leaves the admin-configured fields intact.
//
// Deletion is handled through a finalizer: removing the Runtime CRD
// soft-deletes the mirrored registry row (the gateway then refuses new
// pod registrations against it but keeps the row for audit, per §15.1)
// before the object leaves etcd.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/controller/controllermetrics"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
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
// soft-delete so a re-applied CRD reactivates the registration. Fields
// the CRD does not model (capabilities, limits, agentInterface, the
// runtime options schema, setup policy) are left untouched so an
// admin-API configuration layered on top of the CRD base survives a
// re-reconcile.
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
	dst.Labels = domainLabels(rt.Labels)
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
