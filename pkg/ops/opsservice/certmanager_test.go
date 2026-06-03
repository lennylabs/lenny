// SPDX-License-Identifier: MIT

package opsservice_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/opsservice"
)

// fixedCertSource is a CertStatusSource over a fixed slice, with an
// optional injected error.
type fixedCertSource struct {
	certs []opsservice.CertStatus
	err   error
}

func (s fixedCertSource) CertStatuses(context.Context) ([]opsservice.CertStatus, error) {
	return s.certs, s.err
}

// TestCertManagerCheck_spec_25_8 exercises the §25.8 cert-manager health
// thresholds (spec lines 3457-3459): >30d healthy, within 30d + renewal
// failed degraded, within 7d or expired unhealthy, plus the
// nil-source/empty/source-error/unissued edge cases.
func TestCertManagerCheck_spec_25_8(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name   string
		source opsservice.CertStatusSource
		want   opsservice.HealthStatus
	}{
		{
			name:   "nil source is healthy (cert-manager not configured)",
			source: nil,
			want:   opsservice.StatusHealthy,
		},
		{
			name:   "no certificates is healthy",
			source: fixedCertSource{certs: nil},
			want:   opsservice.StatusHealthy,
		},
		{
			name:   "source error is unhealthy",
			source: fixedCertSource{err: errors.New("api unreachable")},
			want:   opsservice.StatusUnhealthy,
		},
		{
			name: "valid >30d is healthy",
			source: fixedCertSource{certs: []opsservice.CertStatus{
				{Name: "lenny-system/gw", NotAfter: now.Add(60 * 24 * time.Hour)},
			}},
			want: opsservice.StatusHealthy,
		},
		{
			name: "within 30d and renewal failed is degraded",
			source: fixedCertSource{certs: []opsservice.CertStatus{
				{Name: "lenny-system/gw", NotAfter: now.Add(20 * 24 * time.Hour), RenewalFailed: true},
			}},
			want: opsservice.StatusDegraded,
		},
		{
			name: "within 30d but renewal not failed stays healthy",
			source: fixedCertSource{certs: []opsservice.CertStatus{
				{Name: "lenny-system/gw", NotAfter: now.Add(20 * 24 * time.Hour), RenewalFailed: false},
			}},
			want: opsservice.StatusHealthy,
		},
		{
			name: "within 7d is unhealthy regardless of renewal",
			source: fixedCertSource{certs: []opsservice.CertStatus{
				{Name: "lenny-system/gw", NotAfter: now.Add(3 * 24 * time.Hour), RenewalFailed: false},
			}},
			want: opsservice.StatusUnhealthy,
		},
		{
			name: "expired is unhealthy",
			source: fixedCertSource{certs: []opsservice.CertStatus{
				{Name: "lenny-system/gw", NotAfter: now.Add(-time.Hour)},
			}},
			want: opsservice.StatusUnhealthy,
		},
		{
			name: "unissued and renewal failed is degraded",
			source: fixedCertSource{certs: []opsservice.CertStatus{
				{Name: "lenny-system/gw", RenewalFailed: true},
			}},
			want: opsservice.StatusDegraded,
		},
		{
			name: "unissued without failure is healthy (still issuing)",
			source: fixedCertSource{certs: []opsservice.CertStatus{
				{Name: "lenny-system/gw", RenewalFailed: false},
			}},
			want: opsservice.StatusHealthy,
		},
		{
			name: "worst certificate wins (healthy + unhealthy = unhealthy)",
			source: fixedCertSource{certs: []opsservice.CertStatus{
				{Name: "lenny-system/ok", NotAfter: now.Add(90 * 24 * time.Hour)},
				{Name: "lenny-system/bad", NotAfter: now.Add(2 * 24 * time.Hour)},
			}},
			want: opsservice.StatusUnhealthy,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := opsservice.CertManagerCheck(tc.source)()
			if res.Name != opsservice.CheckCertManager {
				t.Fatalf("check name = %q, want %q", res.Name, opsservice.CheckCertManager)
			}
			if res.Status != tc.want {
				t.Fatalf("status = %v (detail %q), want %v", res.Status, res.Detail, tc.want)
			}
		})
	}
}
