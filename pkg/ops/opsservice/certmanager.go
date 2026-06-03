// SPDX-License-Identifier: MIT

package opsservice

import (
	"context"
	"fmt"
	"time"
)

// §25.8 cert-manager certificate-expiry thresholds (spec lines 3457-3459).
const (
	// certExpiryDegradedThreshold is the window within which a certificate
	// whose last renewal failed reports degraded.
	certExpiryDegradedThreshold = 30 * 24 * time.Hour
	// certExpiryUnhealthyThreshold is the window within which a certificate
	// reports unhealthy regardless of renewal state (or when expired).
	certExpiryUnhealthyThreshold = 7 * 24 * time.Hour
)

// CertStatus is the observed state of one cert-manager-managed
// certificate, as the §25.8 health probe reads it from the cert-manager
// Certificate resource's status.
type CertStatus struct {
	// Name identifies the certificate (namespace/name).
	Name string
	// NotAfter is the certificate's expiry instant (status.notAfter). A
	// zero value means cert-manager has not yet issued the certificate;
	// the check treats that as a renewal-pending degraded signal rather
	// than an immediate expiry.
	NotAfter time.Time
	// RenewalFailed reports that cert-manager's most recent renewal or
	// issuance attempt failed (a Ready condition that is not True, or an
	// Issuing condition with a failure reason).
	RenewalFailed bool
}

// CertStatusSource enumerates the cert-manager certificates lenny-ops
// observes. The production source lists the cert-manager Certificate CRs
// via the Kubernetes API; tests supply a fixed slice. A source that
// returns an error reports the probe could not reach cert-manager.
//
// spec: §25.8 Cert-Manager Integration (line 3456, "lenny-ops's health
// API probes cert-manager's certificate status").
type CertStatusSource interface {
	CertStatuses(ctx context.Context) ([]CertStatus, error)
}

// CertManagerCheck returns the §25.8 cert_manager self-health check. The
// aggregate is the worst certificate state per the spec thresholds:
//
//   - healthy: every certificate is valid for more than 30 days.
//   - degraded: at least one certificate expires within 30 days and its
//     last renewal attempt failed.
//   - unhealthy: at least one certificate expires within 7 days or has
//     already expired.
//
// A nil source reports healthy with a note that cert-manager integration
// is not configured: an operator using deployer-provided TLS Secrets or
// self-signed dev certificates has no cert-manager Certificate resources
// to probe, which is not a fault. A source error reports unhealthy: the
// probe cannot confirm certificate health.
//
// spec: §25.8 lines 3456-3461.
func CertManagerCheck(source CertStatusSource) SelfCheck {
	return func() CheckResult {
		res := CheckResult{Name: CheckCertManager, Status: StatusHealthy}
		if source == nil {
			res.Detail = "cert-manager integration not configured"
			return res
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		certs, err := source.CertStatuses(ctx)
		if err != nil {
			res.Status = StatusUnhealthy
			res.Detail = fmt.Sprintf("cert-manager unreachable: %v", err)
			return res
		}
		if len(certs) == 0 {
			res.Detail = "no cert-manager certificates found"
			return res
		}
		now := time.Now()
		var worst HealthStatus = StatusHealthy
		var detail string
		for _, c := range certs {
			st, why := classifyCert(c, now)
			if st > worst {
				worst = st
				detail = why
			}
		}
		res.Status = worst
		if worst != StatusHealthy {
			res.Detail = detail
		}
		return res
	}
}

// classifyCert maps one certificate to its §25.8 health contribution and
// a human-readable reason. An unissued certificate (zero NotAfter) is
// degraded only when its renewal/issuance failed; otherwise it is
// treated as still issuing and reports healthy.
func classifyCert(c CertStatus, now time.Time) (HealthStatus, string) {
	if c.NotAfter.IsZero() {
		if c.RenewalFailed {
			return StatusDegraded, fmt.Sprintf("%s: not yet issued and last attempt failed", c.Name)
		}
		return StatusHealthy, ""
	}
	remaining := c.NotAfter.Sub(now)
	switch {
	case remaining <= certExpiryUnhealthyThreshold:
		if remaining <= 0 {
			return StatusUnhealthy, fmt.Sprintf("%s: certificate expired", c.Name)
		}
		return StatusUnhealthy, fmt.Sprintf("%s: expires in %s", c.Name, remaining.Round(time.Hour))
	case remaining <= certExpiryDegradedThreshold && c.RenewalFailed:
		return StatusDegraded, fmt.Sprintf("%s: expires in %s and renewal failed", c.Name, remaining.Round(time.Hour))
	default:
		return StatusHealthy, ""
	}
}
