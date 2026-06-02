// SPDX-License-Identifier: MIT

package preflight

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func proberReturning(version string, found bool, err error) CertManagerProber {
	return CertManagerProbeFunc(func(context.Context) (string, bool, error) {
		return version, found, err
	})
}

// spec: §10.3 line 304 — the cert-manager version preflight. F-10.3.12.
func TestCertManagerVersionCheck_spec_10_3_304(t *testing.T) {
	cases := []struct {
		name       string
		check      CertManagerVersionCheck
		wantPassed bool
		wantSubstr string
	}{
		{
			name:       "disabled is a no-op",
			check:      CertManagerVersionCheck{Required: false, Prober: proberReturning("", false, nil)},
			wantPassed: true,
			wantSubstr: "certmanager.enabled=false",
		},
		{
			name:       "no prober while required is a non-blocking warning",
			check:      CertManagerVersionCheck{Required: true, Prober: nil},
			wantPassed: true,
			wantSubstr: "WARNING",
		},
		{
			name:       "required but absent fails closed",
			check:      CertManagerVersionCheck{Required: true, Prober: proberReturning("", false, nil)},
			wantPassed: false,
			wantSubstr: "not installed",
		},
		{
			name:       "below the floor fails closed",
			check:      CertManagerVersionCheck{Required: true, Prober: proberReturning("v1.11.5", true, nil)},
			wantPassed: false,
			wantSubstr: "below the minimum supported v1.12.0",
		},
		{
			name:       "exactly the floor passes",
			check:      CertManagerVersionCheck{Required: true, Prober: proberReturning("v1.12.0", true, nil)},
			wantPassed: true,
			wantSubstr: "satisfies the minimum",
		},
		{
			name:       "above the floor passes",
			check:      CertManagerVersionCheck{Required: true, Prober: proberReturning("v1.14.2", true, nil)},
			wantPassed: true,
			wantSubstr: "satisfies the minimum",
		},
		{
			name:       "present but version-unknown is advisory",
			check:      CertManagerVersionCheck{Required: true, Prober: proberReturning("", true, nil)},
			wantPassed: true,
			wantSubstr: "could not be determined",
		},
		{
			name:       "unparseable version is advisory",
			check:      CertManagerVersionCheck{Required: true, Prober: proberReturning("nightly", true, nil)},
			wantPassed: true,
			wantSubstr: "unparseable",
		},
		{
			name:       "probe error fails closed",
			check:      CertManagerVersionCheck{Required: true, Prober: proberReturning("", false, errors.New("api down"))},
			wantPassed: false,
			wantSubstr: "probe failed",
		},
		{
			name:       "major bump above the floor passes",
			check:      CertManagerVersionCheck{Required: true, Prober: proberReturning("v2.0.0", true, nil)},
			wantPassed: true,
			wantSubstr: "satisfies the minimum",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.check.Decide(context.Background())
			if got.Passed != tc.wantPassed {
				t.Fatalf("Passed = %v, want %v (reason: %s)", got.Passed, tc.wantPassed, got.Reason)
			}
			if !strings.Contains(got.Reason, tc.wantSubstr) {
				t.Fatalf("reason %q does not contain %q", got.Reason, tc.wantSubstr)
			}
		})
	}
}

func TestVersionAtLeast(t *testing.T) {
	cases := []struct {
		v, floor string
		want     bool
		wantErr  bool
	}{
		{"v1.12.0", "v1.12.0", true, false},
		{"v1.12.1", "v1.12.0", true, false},
		{"v1.13.0", "v1.12.0", true, false},
		{"v1.11.9", "v1.12.0", false, false},
		{"1.12.0", "v1.12.0", true, false},         // leading v optional
		{"v1.12", "v1.12.0", true, false},          // short form
		{"v1.12.0-beta.1", "v1.12.0", true, false}, // pre-release suffix dropped
		{"garbage", "v1.12.0", false, true},
	}
	for _, tc := range cases {
		got, err := versionAtLeast(tc.v, tc.floor)
		if tc.wantErr {
			if err == nil {
				t.Errorf("versionAtLeast(%q,%q) expected error", tc.v, tc.floor)
			}
			continue
		}
		if err != nil {
			t.Errorf("versionAtLeast(%q,%q) unexpected error: %v", tc.v, tc.floor, err)
			continue
		}
		if got != tc.want {
			t.Errorf("versionAtLeast(%q,%q) = %v, want %v", tc.v, tc.floor, got, tc.want)
		}
	}
}
