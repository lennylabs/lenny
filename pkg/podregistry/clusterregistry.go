// SPDX-License-Identifier: MIT

package podregistry

import (
	"context"
	"errors"
)

// DefaultLocalClusterID is the ClusterID the v1 LocalClusterRegistry
// reports for the single in-process cluster when no explicit id is
// configured. spec: §12.6 line 586.
const DefaultLocalClusterID ClusterID = "local"

// ClusterHealth is the §12.6 line 429 health-status field of a
// ClusterInfo.
type ClusterHealth string

const (
	// ClusterHealthy reports that the cluster's PodRegistry endpoint is
	// reachable and serving.
	ClusterHealthy ClusterHealth = "healthy"
	// ClusterDegraded reports that the endpoint is reachable but is
	// serving with reduced capacity or an elevated error rate.
	ClusterDegraded ClusterHealth = "degraded"
	// ClusterUnreachable reports that the endpoint cannot be reached.
	ClusterUnreachable ClusterHealth = "unreachable"
)

// ClusterCapacity is the §12.6 line 429 capacity-metadata field of a
// ClusterInfo. A cross-cluster scheduler reads it to bias SelectCluster
// toward a cluster with idle warm capacity. The v1 LocalClusterRegistry
// leaves it zero: capacity for the single local cluster is read from the
// PodRegistry directly (CountByState), not from the topology layer.
type ClusterCapacity struct {
	// IdlePods is the count of idle warm pods available to satisfy a
	// claim across the cluster's pools.
	IdlePods int32
	// TotalPods is the count of pods in any state across the cluster's
	// pools.
	TotalPods int32
}

// ClusterInfo describes one cluster in a multi-cluster topology. spec:
// §12.6 line 429 — ClusterID, endpoint, CACertBundle (remote CA chain
// for mTLS verification), capacity metadata, and health status.
type ClusterInfo struct {
	// ClusterID identifies the cluster.
	ClusterID ClusterID
	// Endpoint is the cluster's PodRegistry gRPC endpoint for
	// cross-cluster RPC (host:port). It is empty for the local cluster,
	// which is reached in-process with no network transport — the
	// LocalClusterRegistry is exempt from the §12.6 line 614
	// cross-cluster mTLS transport contract.
	Endpoint string
	// CACertBundle is the remote cluster's CA certificate chain
	// (PEM-encoded), used by a cross-cluster ClusterClient to verify the
	// remote endpoint's server certificate (§12.6 line 614 requirement
	// 2). During a CA rotation overlap window it carries both the old
	// and new CA certificates so a connection succeeds regardless of
	// which CA signed the remote endpoint's current certificate (§12.6
	// line 616). It is empty for the local cluster.
	CACertBundle []byte
	// Capacity is the cluster's warm-pod capacity metadata.
	Capacity ClusterCapacity
	// Health is the cluster's current health status.
	Health ClusterHealth
}

// ClusterSelectionRequest carries the hints SelectCluster uses to route
// a claim to a cluster. spec: §12.6 line 430 — affinity hints and
// resource requirements. The v1 LocalClusterRegistry ignores every
// field and always returns LocalClusterID().
type ClusterSelectionRequest struct {
	// AffinityHints are optional cluster-affinity preferences (for
	// example a data-residency region or a node-locality label). A
	// multi-cluster ClusterRegistry biases selection toward a cluster
	// matching these hints; v1 ignores them.
	AffinityHints map[string]string
	// PoolID is the pool the claim targets. A multi-cluster registry
	// restricts selection to clusters that host the pool; v1 ignores it.
	PoolID PoolID
	// IsolationProfile is the claim's required isolation profile. A
	// multi-cluster registry restricts selection to clusters that offer
	// a compatible profile; v1 ignores it.
	IsolationProfile string
}

// RemotePodOperations is the subset of PodRegistry permitted over
// cross-cluster connections, returned by ClusterRegistry.ClusterClient.
// The restricted type enforces the §12.6 line 614 cross-cluster
// security contract at compile time: a caller holding a
// RemotePodOperations cannot invoke DeletePod, UpdatePodState,
// CreatePod, CountByState, or WatchPods on a remote cluster. spec: §12.6
// lines 589-600.
type RemotePodOperations interface {
	GetPod(ctx context.Context, podID PodID) (*PodRecord, error)
	ClaimPod(ctx context.Context, opts ClaimOpts) (*PodRecord, error)
	ReleasePod(ctx context.Context, podID PodID, reason ReleaseReason) error
	ListPodsByPool(ctx context.Context, poolID PoolID, filter PodFilter) ([]PodRecord, error)
}

// Compile-time proof that PodRegistry is a superset of
// RemotePodOperations: any PodRegistry value (including the
// CRDPodRegistry the LocalClusterRegistry returns from ClusterClient)
// satisfies the restricted cross-cluster interface. spec: §12.6 lines
// 609-612 ("the local CRDPodRegistry ... satisfies RemotePodOperations
// (it is a superset)").
var _ RemotePodOperations = (PodRegistry)(nil)

// ClusterRegistry abstracts cluster topology for multi-cluster
// delegation routing (§8 lenny/delegate_task cross-cluster routing).
// spec: §12.6 lines 602-607.
type ClusterRegistry interface {
	// ListClusters returns every cluster in the topology.
	ListClusters(ctx context.Context) ([]ClusterInfo, error)
	// GetCluster returns the named cluster, or ErrClusterNotFound when
	// no cluster with that id is in the topology.
	GetCluster(ctx context.Context, clusterID ClusterID) (*ClusterInfo, error)
	// SelectCluster chooses a cluster for a claim. The v1 implementation
	// ignores req and returns LocalClusterID().
	SelectCluster(ctx context.Context, req ClusterSelectionRequest) (ClusterID, error)
	// ClusterClient returns the RemotePodOperations subset for the named
	// cluster. The LocalClusterRegistry returns the in-process
	// PodRegistry (a superset); a multi-cluster implementation returns
	// an mTLS-authenticated remote client (§12.6 line 614).
	ClusterClient(ctx context.Context, clusterID ClusterID) (RemotePodOperations, error)
	// LocalClusterID returns the id of the cluster this process runs in.
	LocalClusterID() ClusterID
}

// ErrClusterNotFound is returned by GetCluster and ClusterClient for a
// cluster id absent from the topology.
var ErrClusterNotFound = errors.New("podregistry: cluster not found")

// LocalClusterRegistry is the §12.6 v1 ClusterRegistry: a single-cluster
// topology that exposes only the in-process PodRegistry. ClaimOpts.ClusterID
// is always nil in v1, and ClusterClient returns the local PodRegistry
// (which satisfies RemotePodOperations as a superset) with no network
// transport, so the cross-cluster mTLS contract (§12.6 line 614) does not
// apply. spec: §12.6 lines 586, 609-614.
type LocalClusterRegistry struct {
	local PodRegistry
	id    ClusterID
}

var _ ClusterRegistry = (*LocalClusterRegistry)(nil)

// NewLocalClusterRegistry returns a LocalClusterRegistry over the
// in-process PodRegistry. An empty id selects DefaultLocalClusterID.
func NewLocalClusterRegistry(local PodRegistry, id ClusterID) *LocalClusterRegistry {
	if id == "" {
		id = DefaultLocalClusterID
	}
	return &LocalClusterRegistry{local: local, id: id}
}

// localInfo returns the ClusterInfo for the single local cluster. It
// carries no endpoint and no CACertBundle: the local cluster is reached
// in-process, so the §12.6 line 614 cross-cluster mTLS fields are unset.
func (r *LocalClusterRegistry) localInfo() ClusterInfo {
	return ClusterInfo{ClusterID: r.id, Health: ClusterHealthy}
}

// ListClusters returns the single local cluster. spec: §12.6 line 603.
func (r *LocalClusterRegistry) ListClusters(context.Context) ([]ClusterInfo, error) {
	return []ClusterInfo{r.localInfo()}, nil
}

// GetCluster returns the local cluster iff clusterID is the local id,
// else ErrClusterNotFound. spec: §12.6 line 604.
func (r *LocalClusterRegistry) GetCluster(_ context.Context, clusterID ClusterID) (*ClusterInfo, error) {
	if clusterID != r.id {
		return nil, ErrClusterNotFound
	}
	info := r.localInfo()
	return &info, nil
}

// SelectCluster ignores req and returns the local cluster id: v1 is
// single-cluster, so every claim routes locally. spec: §12.6 line 430
// ("v1 implementation ignores all fields and returns LocalClusterID()"),
// line 586.
func (r *LocalClusterRegistry) SelectCluster(context.Context, ClusterSelectionRequest) (ClusterID, error) {
	return r.id, nil
}

// ClusterClient returns the in-process PodRegistry for the local cluster
// (it satisfies RemotePodOperations as a superset), or ErrClusterNotFound
// for any other id. No network transport is established — the §12.6 line
// 614 cross-cluster mTLS contract applies only to remote clients. spec:
// §12.6 lines 609-614.
func (r *LocalClusterRegistry) ClusterClient(_ context.Context, clusterID ClusterID) (RemotePodOperations, error) {
	if clusterID != r.id {
		return nil, ErrClusterNotFound
	}
	return r.local, nil
}

// LocalClusterID returns the local cluster id. spec: §12.6 line 607.
func (r *LocalClusterRegistry) LocalClusterID() ClusterID { return r.id }
