// SPDX-License-Identifier: MIT

package tierpromotion

import (
	"context"
	"fmt"
	"strings"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Defaults for the well-known names the chart renders. Operators who
// override these set custom GatherOptions on the cluster gatherer.
const (
	// DefaultNamespace is the release namespace the chart installs into.
	DefaultNamespace = "lenny-system"
	// DefaultGatewayDeployment is the chart-rendered gateway Deployment
	// name.
	DefaultGatewayDeployment = "lenny-gateway"
	// DefaultControllerDeployment is the chart-rendered warm-pool
	// controller Deployment name.
	DefaultControllerDeployment = "lenny-controller"
	// DefaultOpsDeployment is the chart-rendered lenny-ops Deployment
	// name.
	DefaultOpsDeployment = "lenny-ops"
	// DefaultPostgresStatefulSet is the chart-rendered Postgres
	// StatefulSet name for self-managed installs.
	DefaultPostgresStatefulSet = "lenny-postgres"
	// DefaultRedisStatefulSet is the chart-rendered Redis StatefulSet
	// name for self-managed installs.
	DefaultRedisStatefulSet = "lenny-redis"
	// LennyWebhookPrefix is the prefix every Lenny-managed
	// ValidatingWebhookConfiguration carries.
	LennyWebhookPrefix = "lenny-"
)

// GatherOptions carry the per-deployment names the cluster gatherer
// looks up. Operators who diverge from the chart defaults set them on
// the ClusterGatherer's Options field.
type GatherOptions struct {
	// Namespace is the release namespace; defaults to DefaultNamespace.
	Namespace string
	// GatewayDeployment is the gateway Deployment's metadata.name;
	// defaults to DefaultGatewayDeployment.
	GatewayDeployment string
	// ControllerDeployment is the warm-pool controller Deployment's
	// metadata.name; defaults to DefaultControllerDeployment.
	ControllerDeployment string
	// OpsDeployment is the lenny-ops Deployment's metadata.name;
	// defaults to DefaultOpsDeployment.
	OpsDeployment string
	// PostgresStatefulSet is the self-managed Postgres StatefulSet's
	// metadata.name; defaults to DefaultPostgresStatefulSet. Ignored
	// when PostgresExternal is true.
	PostgresStatefulSet string
	// RedisStatefulSet is the self-managed Redis StatefulSet's
	// metadata.name; defaults to DefaultRedisStatefulSet. Ignored when
	// RedisExternal is true.
	RedisStatefulSet string

	// PostgresExternal reports whether the operator has wired Postgres
	// to an external managed endpoint (RDS, Cloud SQL, Azure Database).
	// When true, the gatherer treats Postgres as on persistent storage
	// without inspecting an in-cluster StatefulSet.
	PostgresExternal bool
	// RedisExternal reports whether the operator has wired Redis to an
	// external managed endpoint (ElastiCache, Memorystore, Azure
	// Cache). When true, the gatherer treats Redis as on persistent
	// storage without inspecting an in-cluster StatefulSet.
	RedisExternal bool

	// ChartValuesTier is the rendered chart's capacityPlanning.tier
	// value. The operator supplies this from the values file they are
	// applying; the gatherer cannot read it from the chart alone.
	ChartValuesTier Tier
	// AuditRetainDays is the rendered chart's
	// backups.retention.retainDays value, supplied by the operator.
	AuditRetainDays int
	// SecretEncryptionVerified is the operator's attestation that etcd
	// Secret encryption-at-rest is configured (§4.9).
	SecretEncryptionVerified bool
}

// ClusterGatherer implements Gatherer over a sigs.k8s.io/controller-
// runtime client.Reader. It populates Inputs from the deployed
// Deployments, the data-plane StatefulSets, and the
// ValidatingWebhookConfigurations.
type ClusterGatherer struct {
	// Reader is the client.Reader the gatherer uses for List/Get
	// against the cluster. Required.
	Reader client.Reader
	// Options carry the per-deployment names and operator-supplied
	// attestations.
	Options GatherOptions
}

// Gather reads the cluster state and returns the populated Inputs.
func (g *ClusterGatherer) Gather(ctx context.Context, from, to Tier) (Inputs, error) {
	opts := g.Options.withDefaults()
	in := Inputs{
		From:                     from,
		To:                       to,
		ChartValuesTier:          opts.ChartValuesTier,
		AuditRetainDays:          opts.AuditRetainDays,
		SecretEncryptionVerified: opts.SecretEncryptionVerified,
	}

	gw, err := readDeploymentReady(ctx, g.Reader, opts.Namespace, opts.GatewayDeployment)
	if err != nil {
		return Inputs{}, fmt.Errorf("read gateway deployment: %w", err)
	}
	in.GatewayReplicas = gw

	ctrl, err := readDeploymentReady(ctx, g.Reader, opts.Namespace, opts.ControllerDeployment)
	if err != nil {
		return Inputs{}, fmt.Errorf("read controller deployment: %w", err)
	}
	in.ControllerReplicas = ctrl

	ops, err := readDeploymentReady(ctx, g.Reader, opts.Namespace, opts.OpsDeployment)
	if err != nil {
		return Inputs{}, fmt.Errorf("read ops deployment: %w", err)
	}
	in.OpsReplicas = ops

	pgPersistent, err := dataPlaneOnPersistentStorage(ctx, g.Reader, opts.Namespace,
		opts.PostgresStatefulSet, opts.PostgresExternal)
	if err != nil {
		return Inputs{}, fmt.Errorf("read postgres statefulset: %w", err)
	}
	in.PostgresUsesPersistentStorage = pgPersistent

	redisPersistent, err := dataPlaneOnPersistentStorage(ctx, g.Reader, opts.Namespace,
		opts.RedisStatefulSet, opts.RedisExternal)
	if err != nil {
		return Inputs{}, fmt.Errorf("read redis statefulset: %w", err)
	}
	in.RedisUsesPersistentStorage = redisPersistent

	hooks, err := readLennyWebhooks(ctx, g.Reader)
	if err != nil {
		return Inputs{}, fmt.Errorf("list validating webhook configurations: %w", err)
	}
	in.AdmissionWebhooks = hooks
	return in, nil
}

// withDefaults fills empty fields with the chart-default names.
func (o GatherOptions) withDefaults() GatherOptions {
	out := o
	if out.Namespace == "" {
		out.Namespace = DefaultNamespace
	}
	if out.GatewayDeployment == "" {
		out.GatewayDeployment = DefaultGatewayDeployment
	}
	if out.ControllerDeployment == "" {
		out.ControllerDeployment = DefaultControllerDeployment
	}
	if out.OpsDeployment == "" {
		out.OpsDeployment = DefaultOpsDeployment
	}
	if out.PostgresStatefulSet == "" {
		out.PostgresStatefulSet = DefaultPostgresStatefulSet
	}
	if out.RedisStatefulSet == "" {
		out.RedisStatefulSet = DefaultRedisStatefulSet
	}
	return out
}

// readDeploymentReady reads a Deployment and returns its
// .status.readyReplicas. A missing Deployment returns 0 without an
// error so the gate's replica check can fail with a clear "0 ready"
// detail rather than the gather call itself surfacing a not-found.
func readDeploymentReady(ctx context.Context, reader client.Reader, namespace, name string) (int, error) {
	var d appsv1.Deployment
	err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &d)
	if apierrors.IsNotFound(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return int(d.Status.ReadyReplicas), nil
}

// dataPlaneOnPersistentStorage reports whether the data-plane backing
// store (Postgres or Redis) is on durable storage. An external managed
// endpoint short-circuits to true. An in-cluster StatefulSet is on
// persistent storage when it carries at least one
// volumeClaimTemplates entry. A missing in-cluster StatefulSet
// (presumed managed-but-not-attested) returns false.
func dataPlaneOnPersistentStorage(ctx context.Context, reader client.Reader, namespace, name string, external bool) (bool, error) {
	if external {
		return true, nil
	}
	var ss appsv1.StatefulSet
	err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &ss)
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if hasPersistentClaim(&ss) {
		return true, nil
	}
	return false, nil
}

// hasPersistentClaim reports whether the StatefulSet declares at
// least one volumeClaimTemplates entry or a pod-template volume that
// references a PersistentVolumeClaim. The volumeClaimTemplates path is
// the canonical chart-rendered shape; the PVC-volume path catches the
// hand-rolled topology where the operator pre-creates the claim.
func hasPersistentClaim(ss *appsv1.StatefulSet) bool {
	if len(ss.Spec.VolumeClaimTemplates) > 0 {
		return true
	}
	for _, v := range ss.Spec.Template.Spec.Volumes {
		if v.PersistentVolumeClaim != nil {
			return true
		}
	}
	return persistentClaimMounted(ss.Spec.Template.Spec)
}

// persistentClaimMounted is a defensive secondary check: a pod spec
// can reference a PVC indirectly through projected or ephemeral
// volume sources in future K8s versions. For v1 this returns false;
// the gatherer wires the source on a deliberate future change.
func persistentClaimMounted(_ corev1.PodSpec) bool { return false }

// readLennyWebhooks lists every ValidatingWebhookConfiguration whose
// metadata.name begins with LennyWebhookPrefix and projects each onto
// a WebhookPosture.
func readLennyWebhooks(ctx context.Context, reader client.Reader) ([]WebhookPosture, error) {
	var list admissionregistrationv1.ValidatingWebhookConfigurationList
	if err := reader.List(ctx, &list); err != nil {
		return nil, err
	}
	out := make([]WebhookPosture, 0, len(list.Items))
	for i := range list.Items {
		cfg := &list.Items[i]
		if !strings.HasPrefix(cfg.Name, LennyWebhookPrefix) {
			continue
		}
		out = append(out, projectWebhook(cfg))
	}
	return out, nil
}

// projectWebhook reduces a ValidatingWebhookConfiguration to the
// fields the admission-posture check inspects. Each Lenny webhook
// carries a single webhook entry, so the projection reads the first.
func projectWebhook(cfg *admissionregistrationv1.ValidatingWebhookConfiguration) WebhookPosture {
	wp := WebhookPosture{Name: cfg.Name}
	if len(cfg.Webhooks) == 0 {
		return wp
	}
	wh := &cfg.Webhooks[0]
	if wh.FailurePolicy != nil {
		wp.FailurePolicy = string(*wh.FailurePolicy)
	}
	wp.HasCABundle = len(wh.ClientConfig.CABundle) > 0
	return wp
}
