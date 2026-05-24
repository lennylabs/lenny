// SPDX-License-Identifier: MIT

package controllermetrics

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// defaultLeaseMonitorInterval is the sampling cadence for the leader
// lease renewal-age gauge. It is well under the 15s leaseDuration so the
// gauge crosses the §16.5 ControllerLeaderElectionFailed threshold within
// one sample of an actual renewal stall.
const defaultLeaseMonitorInterval = 2 * time.Second

// LeaseRenewalMonitor publishes the §4.6.1
// lenny_controller_leader_lease_renewal_age_seconds gauge for one
// controller by sampling the controller's leader-election Lease and
// reporting the seconds since its last renewal. It runs on every replica
// (NeedLeaderElection is false) so a non-leader replica still reports the
// age of the lease the current leader holds, which is exactly what the
// §16.5 ControllerLeaderElectionFailed alert needs to detect a renewal
// stall before the lease fully expires.
type LeaseRenewalMonitor struct {
	// Reader reads the Lease. It must be an uncached reader
	// (manager.GetAPIReader) so the read uses only the `get` verb the
	// §4.6.3 RBAC grants on Leases, not the list/watch a cached client
	// would require.
	Reader client.Reader
	// Namespace and Name address the controller's leader-election Lease
	// (for example lenny-system / lenny-warm-pool-controller).
	Namespace string
	Name      string
	// Controller is the metric's controller label value.
	Controller string
	// Interval is the sampling cadence. A non-positive value selects
	// defaultLeaseMonitorInterval.
	Interval time.Duration
	// Now returns the current time. When nil, time.Now is used.
	Now func() time.Time
}

var (
	_ manager.Runnable               = (*LeaseRenewalMonitor)(nil)
	_ manager.LeaderElectionRunnable = (*LeaseRenewalMonitor)(nil)
)

// Start samples the lease until ctx is cancelled.
func (m *LeaseRenewalMonitor) Start(ctx context.Context) error {
	interval := m.Interval
	if interval <= 0 {
		interval = defaultLeaseMonitorInterval
	}
	logger := logf.FromContext(ctx).WithName("lease-monitor").WithValues("controller", m.Controller)

	m.sample(ctx, logger)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			m.sample(ctx, logger)
		}
	}
}

// sample reads the Lease and sets the renewal-age gauge. A missing Lease
// (leader election disabled, or no leader has acquired it yet) leaves the
// gauge untouched rather than reporting a misleading age.
func (m *LeaseRenewalMonitor) sample(ctx context.Context, logger logr.Logger) {
	now := time.Now
	if m.Now != nil {
		now = m.Now
	}
	var lease coordinationv1.Lease
	err := m.Reader.Get(ctx, client.ObjectKey{Namespace: m.Namespace, Name: m.Name}, &lease)
	if apierrors.IsNotFound(err) {
		return
	}
	if err != nil {
		logger.Error(err, "read leader-election lease")
		return
	}
	if lease.Spec.RenewTime == nil {
		return
	}
	age := now().Sub(lease.Spec.RenewTime.Time).Seconds()
	if age < 0 {
		age = 0
	}
	leaseRenewalAge.WithLabelValues(m.Controller).Set(age)
}

// NeedLeaderElection reports false so the monitor runs on every replica,
// not just the leader.
func (m *LeaseRenewalMonitor) NeedLeaderElection() bool { return false }
