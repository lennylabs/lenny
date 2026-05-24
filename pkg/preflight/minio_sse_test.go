// SPDX-License-Identifier: MIT

package preflight

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// spec: §12.5 line 297 — the SSE preflight check audits the MinIO
// artifact bucket's server-side-encryption posture and fails the
// install under a regulated complianceProfile when SSE is absent.

func TestMinIOEncryptionDecideAdvisoryWhenUnregulated(t *testing.T) {
	probe := MinIOEncryptionProbeFunc(func(context.Context, string) (string, error) {
		return "", nil // no SSE
	})
	d := MinIOEncryptionCheck{
		Bucket:            "lenny-artifacts",
		ComplianceProfile: "",
		Prober:            probe,
	}.Decide(context.Background())
	if !d.Passed {
		t.Errorf("unregulated profile with absent SSE should pass advisory: %+v", d)
	}
}

func TestMinIOEncryptionDecideFailsOnRegulatedAbsent(t *testing.T) {
	for _, profile := range []string{"soc2", "fedramp", "hipaa"} {
		t.Run(profile, func(t *testing.T) {
			probe := MinIOEncryptionProbeFunc(func(context.Context, string) (string, error) {
				return "", nil // no SSE
			})
			d := MinIOEncryptionCheck{
				Bucket:            "lenny-artifacts",
				ComplianceProfile: profile,
				Prober:            probe,
			}.Decide(context.Background())
			if d.Passed {
				t.Errorf("regulated profile %q with absent SSE must fail: %+v", profile, d)
			}
			if !strings.Contains(d.Reason, "§12.5 line 297") {
				t.Errorf("reason should cite §12.5 line 297: %q", d.Reason)
			}
		})
	}
}

func TestMinIOEncryptionDecidePassesWithSSE(t *testing.T) {
	for _, algo := range []string{"AES256", "aws:kms", "SSE-KMS"} {
		t.Run(algo, func(t *testing.T) {
			probe := MinIOEncryptionProbeFunc(func(context.Context, string) (string, error) {
				return algo, nil
			})
			d := MinIOEncryptionCheck{
				Bucket:            "lenny-artifacts",
				ComplianceProfile: "soc2",
				Prober:            probe,
			}.Decide(context.Background())
			if !d.Passed {
				t.Errorf("algorithm %q under regulated profile should pass: %+v", algo, d)
			}
			if !strings.Contains(d.Reason, algo) {
				t.Errorf("reason should include the algorithm: %q", d.Reason)
			}
		})
	}
}

func TestMinIOEncryptionDecideFailsClosedOnProbeErrorWhenRegulated(t *testing.T) {
	probe := MinIOEncryptionProbeFunc(func(context.Context, string) (string, error) {
		return "", errors.New("connection refused")
	})
	d := MinIOEncryptionCheck{
		Bucket:            "lenny-artifacts",
		ComplianceProfile: "soc2",
		Prober:            probe,
	}.Decide(context.Background())
	if d.Passed {
		t.Errorf("regulated profile with probe error must fail closed: %+v", d)
	}
}

func TestMinIOEncryptionDecideAdvisoryOnProbeErrorWhenUnregulated(t *testing.T) {
	probe := MinIOEncryptionProbeFunc(func(context.Context, string) (string, error) {
		return "", errors.New("connection refused")
	})
	d := MinIOEncryptionCheck{
		Bucket:            "lenny-artifacts",
		ComplianceProfile: "",
		Prober:            probe,
	}.Decide(context.Background())
	if !d.Passed {
		t.Errorf("unregulated profile with probe error should be advisory: %+v", d)
	}
}

func TestMinIOEncryptionDecideRejectsZeroValues(t *testing.T) {
	d := MinIOEncryptionCheck{Bucket: "", Prober: nil}.Decide(context.Background())
	if d.Passed {
		t.Errorf("zero-valued check should fail: %+v", d)
	}
}
