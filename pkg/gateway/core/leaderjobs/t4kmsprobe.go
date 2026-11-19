// SPDX-License-Identifier: MIT

package leaderjobs

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/tenantkms"
)

// T4KMSProbe configures the continuous T4 KMS availability probe of §12.5:
// on each cadence it re-runs the encrypt and decrypt round trip against
// every T4 tenant's key, so the metrics that the T4KmsKeyUnusable alert
// and the admin API read observe key drift after provisioning.
type T4KMSProbe struct {
	// Lifecycle runs the round trip against a tenant's key.
	Lifecycle *tenantkms.Lifecycle
	// Tenants lists the T4 tenants.
	Tenants tenantkms.TenantSource
	// Registerer is the gateway's metric registry, on which the probe
	// registers its metrics.
	Registerer prometheus.Registerer
	// Interval is the probe cadence; the probe clamps it to its floor.
	Interval time.Duration
	// RateLimit caps the KMS calls per second.
	RateLimit float64
	// Now is the probe's clock.
	Now func() time.Time
}

// AddT4KMSProbe builds the probe from cfg and registers it on s as the job
// t4-kms-probe, so the probe and the key lifecycle it drives are values of
// the session core rather than of the leader gate. It fails, registering
// nothing, when the probe's metrics cannot be registered.
func (s *Set) AddT4KMSProbe(cfg T4KMSProbe) error {
	metrics, err := tenantkms.NewProbeMetrics(cfg.Registerer)
	if err != nil {
		return fmt.Errorf("T4 KMS probe metrics: %w", err)
	}
	prober := &tenantkms.Prober{
		Lifecycle: cfg.Lifecycle,
		Tenants:   cfg.Tenants,
		Metrics:   metrics,
		Interval:  cfg.Interval,
		RateLimit: cfg.RateLimit,
		Now:       cfg.Now,
	}
	s.Add("t4-kms-probe", func(ctx context.Context) {
		if err := prober.Start(ctx); err != nil {
			log.Printf("lenny-gateway: §12.5 T4 KMS probe loop exited: %v", err)
		}
	})
	return nil
}
