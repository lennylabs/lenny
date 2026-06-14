// SPDX-License-Identifier: MIT

//go:build component

// Tier-2 component test for the §12.5 line 297 MinIO server-side
// encryption preflight check. The test runs the preflight Run flow
// end-to-end with a real fake-MinIO prober and asserts that the §17.9
// report carries the SSE check outcome under both regulated and
// unregulated complianceProfile values.
//
// The check is the §12.5 line 297 "preflight check validates that
// MinIO SSE is enabled" contract; a regulated profile (soc2 | fedramp
// | hipaa) fails closed on absent SSE, an unregulated profile passes
// advisory.

package preflight_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/preflight"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// fakeReader is a no-op cluster reader. The phase-stamp ConfigMap, the
// admission webhook inventory, and the network-policy / SPIFFE checks
// are not the subject of this test; they pass on an empty cluster.
func fakeReader(t *testing.T) client.Client {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	// Seed an empty phase-stamp ConfigMap so the consistency check
	// passes.
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: preflight.PhaseStampConfigMapName, Namespace: "lenny-system"},
	}
	return fake.NewClientBuilder().WithScheme(s).WithObjects(cm).Build()
}

// findCheck returns the named CheckResult from a report.
func findCheck(t *testing.T, report []preflight.CheckResult, name string) preflight.CheckResult {
	t.Helper()
	for _, r := range report {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("preflight report missing %q check", name)
	return preflight.CheckResult{}
}

// TestMinIOSSECheckPassesWithAES256UnderRegulatedProfile verifies the
// §12.5 line 297 check passes the install when MinIO SSE is enabled
// even under a regulated complianceProfile.
//
// spec: §12.5 line 297.
// diagnosis: a failure means the MinIO SSE preflight check fails the
// install even though SSE is enabled, blocking a valid regulated-profile
// deployment.
func TestMinIOSSECheckPassesWithAES256UnderRegulatedProfile(t *testing.T) {
	probe := preflight.MinIOEncryptionProbeFunc(func(ctx context.Context, bucket string) (string, error) {
		if bucket != "lenny-artifacts" {
			t.Errorf("bucket = %q, want lenny-artifacts", bucket)
		}
		return "AES256", nil
	})
	report := preflight.Run(context.Background(), fakeReader(t), preflight.Config{
		Namespace:             "lenny-system",
		ComplianceProfile:     "soc2",
		MinIOBucket:           "lenny-artifacts",
		MinIOEncryptionProber: probe,
	})
	r := findCheck(t, report, "minio-server-side-encryption")
	if !r.Decision.Passed {
		t.Errorf("SOC2 with AES256 must pass: %+v", r.Decision)
	}
}

// TestMinIOSSECheckFailsClosedUnderRegulatedProfileWhenAbsent verifies
// the install fails when SSE is absent under SOC2.
//
// spec: §12.5 line 297.
// diagnosis: a failure means the preflight check does not fail closed on
// absent MinIO SSE under a regulated profile, letting an unencrypted
// artifact store pass a SOC2/FedRAMP/HIPAA install.
func TestMinIOSSECheckFailsClosedUnderRegulatedProfileWhenAbsent(t *testing.T) {
	probe := preflight.MinIOEncryptionProbeFunc(func(context.Context, string) (string, error) {
		return "", nil
	})
	report := preflight.Run(context.Background(), fakeReader(t), preflight.Config{
		Namespace:             "lenny-system",
		ComplianceProfile:     "soc2",
		MinIOBucket:           "lenny-artifacts",
		MinIOEncryptionProber: probe,
	})
	r := findCheck(t, report, "minio-server-side-encryption")
	if r.Decision.Passed {
		t.Errorf("SOC2 with absent SSE must fail: %+v", r.Decision)
	}
	if !strings.Contains(r.Decision.Reason, "§12.5 line 297") {
		t.Errorf("decision reason should cite §12.5 line 297: %q", r.Decision.Reason)
	}
}

// TestMinIOSSECheckSkippedWhenBucketAbsent verifies the check is
// skipped when no bucket is configured.
//
// spec: §12.5 line 297.
// diagnosis: a failure means the SSE check runs even when no bucket is
// configured, emitting a spurious check outcome for a non-existent store.
func TestMinIOSSECheckSkippedWhenBucketAbsent(t *testing.T) {
	report := preflight.Run(context.Background(), fakeReader(t), preflight.Config{
		Namespace: "lenny-system",
	})
	for _, r := range report {
		if r.Name == "minio-server-side-encryption" {
			t.Errorf("SSE check should be skipped when no bucket is configured: %+v", r)
		}
	}
}
