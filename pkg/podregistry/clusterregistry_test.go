// SPDX-License-Identifier: MIT

package podregistry_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/podregistry"
)

// stubPodRegistry is a minimal PodRegistry used to drive the
// LocalClusterRegistry tests. Only the four RemotePodOperations methods
// record their calls; the remaining (non-remote) methods are no-ops so
// the stub satisfies the full §12.6 PodRegistry interface and can be
// returned from ClusterClient as a RemotePodOperations superset.
type stubPodRegistry struct {
	getPodCalled    bool
	claimCalled     bool
	releaseCalled   bool
	listCalled      bool
	lastClaimPoolID podregistry.PoolID
}

var _ podregistry.PodRegistry = (*stubPodRegistry)(nil)

func (s *stubPodRegistry) GetPod(context.Context, podregistry.PodID) (*podregistry.PodRecord, error) {
	s.getPodCalled = true
	return &podregistry.PodRecord{}, nil
}

func (s *stubPodRegistry) ClaimPod(_ context.Context, opts podregistry.ClaimOpts) (*podregistry.PodRecord, error) {
	s.claimCalled = true
	s.lastClaimPoolID = opts.PoolID
	return &podregistry.PodRecord{PoolID: opts.PoolID}, nil
}

func (s *stubPodRegistry) ReleasePod(context.Context, podregistry.PodID, podregistry.ReleaseReason) error {
	s.releaseCalled = true
	return nil
}

func (s *stubPodRegistry) ListPodsByPool(context.Context, podregistry.PoolID, podregistry.PodFilter) ([]podregistry.PodRecord, error) {
	s.listCalled = true
	return nil, nil
}

func (s *stubPodRegistry) UpdatePodState(context.Context, podregistry.PodID, podregistry.StateTransition) error {
	return nil
}

func (s *stubPodRegistry) CountByState(context.Context, podregistry.PoolID) (podregistry.StateCounts, error) {
	return nil, nil
}

func (s *stubPodRegistry) CreatePod(context.Context, podregistry.PoolID, podregistry.PodSpec) (*podregistry.PodRecord, error) {
	return &podregistry.PodRecord{}, nil
}

func (s *stubPodRegistry) DeletePod(context.Context, podregistry.PodID) error { return nil }

func (s *stubPodRegistry) WatchPods(context.Context, podregistry.PoolID) (<-chan podregistry.PodEvent, error) {
	return nil, nil
}

// spec: §12.6 line 586 — the v1 LocalClusterRegistry exposes only the
// local cluster.
func TestLocalClusterRegistryListClusters_spec_12_6_586(t *testing.T) {
	reg := podregistry.NewLocalClusterRegistry(&stubPodRegistry{}, "cluster-a")
	clusters, err := reg.ListClusters(context.Background())
	if err != nil {
		t.Fatalf("ListClusters: %v", err)
	}
	if len(clusters) != 1 {
		t.Fatalf("ListClusters returned %d clusters, want 1", len(clusters))
	}
	if clusters[0].ClusterID != "cluster-a" {
		t.Fatalf("cluster id = %q, want cluster-a", clusters[0].ClusterID)
	}
	if clusters[0].Health != podregistry.ClusterHealthy {
		t.Fatalf("local cluster health = %q, want healthy", clusters[0].Health)
	}
	// spec: §12.6 line 614 — the local cluster is reached in-process, so
	// it carries no endpoint and no remote CA bundle.
	if clusters[0].Endpoint != "" || clusters[0].CACertBundle != nil {
		t.Fatalf("local cluster must carry no endpoint/CA bundle, got endpoint=%q caBundle=%v",
			clusters[0].Endpoint, clusters[0].CACertBundle)
	}
}

// spec: §12.6 line 586 — an empty id selects the default local cluster id.
func TestLocalClusterRegistryDefaultID_spec_12_6_586(t *testing.T) {
	reg := podregistry.NewLocalClusterRegistry(&stubPodRegistry{}, "")
	if got := reg.LocalClusterID(); got != podregistry.DefaultLocalClusterID {
		t.Fatalf("LocalClusterID() = %q, want %q", got, podregistry.DefaultLocalClusterID)
	}
}

// spec: §12.6 line 604 — GetCluster returns the local cluster for the
// local id and ErrClusterNotFound for any other.
func TestLocalClusterRegistryGetCluster_spec_12_6_604(t *testing.T) {
	reg := podregistry.NewLocalClusterRegistry(&stubPodRegistry{}, "cluster-a")

	info, err := reg.GetCluster(context.Background(), "cluster-a")
	if err != nil {
		t.Fatalf("GetCluster(local): %v", err)
	}
	if info.ClusterID != "cluster-a" {
		t.Fatalf("GetCluster id = %q, want cluster-a", info.ClusterID)
	}

	if _, err := reg.GetCluster(context.Background(), "cluster-b"); !errors.Is(err, podregistry.ErrClusterNotFound) {
		t.Fatalf("GetCluster(remote) err = %v, want ErrClusterNotFound", err)
	}
}

// spec: §12.6 line 430 — the v1 SelectCluster ignores every request
// field and always returns LocalClusterID().
func TestLocalClusterRegistrySelectClusterIgnoresRequest_spec_12_6_430(t *testing.T) {
	reg := podregistry.NewLocalClusterRegistry(&stubPodRegistry{}, "cluster-a")
	req := podregistry.ClusterSelectionRequest{
		AffinityHints:    map[string]string{"region": "eu-west-1"},
		PoolID:           "some-pool",
		IsolationProfile: "microvm",
	}
	got, err := reg.SelectCluster(context.Background(), req)
	if err != nil {
		t.Fatalf("SelectCluster: %v", err)
	}
	if got != reg.LocalClusterID() {
		t.Fatalf("SelectCluster = %q, want LocalClusterID %q", got, reg.LocalClusterID())
	}
}

// spec: §12.6 lines 609-614 — ClusterClient returns the in-process
// PodRegistry (a RemotePodOperations superset) for the local cluster,
// and ErrClusterNotFound otherwise.
func TestLocalClusterRegistryClusterClient_spec_12_6_609(t *testing.T) {
	stub := &stubPodRegistry{}
	reg := podregistry.NewLocalClusterRegistry(stub, "cluster-a")

	client, err := reg.ClusterClient(context.Background(), "cluster-a")
	if err != nil {
		t.Fatalf("ClusterClient(local): %v", err)
	}

	// The four RemotePodOperations methods route to the in-process
	// PodRegistry; the restricted type does not expose DeletePod /
	// UpdatePodState / CreatePod / CountByState / WatchPods.
	if _, err := client.GetPod(context.Background(), "pod-1"); err != nil {
		t.Fatalf("GetPod: %v", err)
	}
	if _, err := client.ClaimPod(context.Background(), podregistry.ClaimOpts{PoolID: "pool-1"}); err != nil {
		t.Fatalf("ClaimPod: %v", err)
	}
	if err := client.ReleasePod(context.Background(), "pod-1", podregistry.ReleaseCompleted); err != nil {
		t.Fatalf("ReleasePod: %v", err)
	}
	if _, err := client.ListPodsByPool(context.Background(), "pool-1", podregistry.PodFilter{}); err != nil {
		t.Fatalf("ListPodsByPool: %v", err)
	}
	if !stub.getPodCalled || !stub.claimCalled || !stub.releaseCalled || !stub.listCalled {
		t.Fatalf("ClusterClient did not delegate to the local PodRegistry: %+v", stub)
	}
	if stub.lastClaimPoolID != "pool-1" {
		t.Fatalf("ClaimPod delegated pool = %q, want pool-1", stub.lastClaimPoolID)
	}

	if _, err := reg.ClusterClient(context.Background(), "cluster-b"); !errors.Is(err, podregistry.ErrClusterNotFound) {
		t.Fatalf("ClusterClient(remote) err = %v, want ErrClusterNotFound", err)
	}
}
