// SPDX-License-Identifier: MIT

package preflight

import (
	"context"
	"errors"
	"fmt"
)

// MinIOEncryptionProber reports the §12.5 line 297 server-side
// encryption posture of the MinIO artifact bucket. It implements
// `GetBucketEncryption` against the configured bucket and returns the
// SSE algorithm (`AES256` for SSE-S3, `aws:kms` or `SSE-KMS` for
// SSE-KMS) when configured. Empty algorithm with nil error reports no
// SSE.
//
// spec: §12.5 line 297.
type MinIOEncryptionProber interface {
	GetBucketEncryption(ctx context.Context, bucket string) (algorithm string, err error)
}

// MinIOEncryptionProbeFunc adapts a function to MinIOEncryptionProber.
type MinIOEncryptionProbeFunc func(ctx context.Context, bucket string) (string, error)

// GetBucketEncryption calls f.
func (f MinIOEncryptionProbeFunc) GetBucketEncryption(ctx context.Context, bucket string) (string, error) {
	return f(ctx, bucket)
}

// MinIOEncryptionCheck is the §12.5 line 297 SSE posture audit. It
// fails the preflight when:
//
//   - the deployment runs with a regulated complianceProfile
//     (soc2 | fedramp | hipaa) and the bucket has no SSE enabled, or
//   - the GetBucketEncryption call returns an error and the
//     deployment runs under a regulated complianceProfile (the gate
//     is fail-closed under regulated postures).
//
// A non-regulated deployment surfaces the posture as advisory: the
// check returns a passing decision and includes the algorithm in the
// reason so operators see the configuration in the preflight report.
//
// spec: §12.5 line 297.
type MinIOEncryptionCheck struct {
	// Bucket is the artifact bucket the gateway writes to. Required.
	Bucket string
	// ComplianceProfile is the chart's complianceProfile value
	// (empty, soc2, fedramp, hipaa, etc.).
	ComplianceProfile string
	// Prober runs the GetBucketEncryption call. Required.
	Prober MinIOEncryptionProber
}

// regulatedComplianceProfile reports whether complianceProfile names a
// §11.7 / §12.5 regulated posture (soc2 | fedramp | hipaa).
func regulatedComplianceProfile(profile string) bool {
	switch profile {
	case "soc2", "fedramp", "hipaa":
		return true
	}
	return false
}

// Decide evaluates the §12.5 line 297 SSE posture and returns a
// preflight Decision.
//
// spec: §12.5 line 297.
func (c MinIOEncryptionCheck) Decide(ctx context.Context) Decision {
	if c.Prober == nil {
		return Decision{Passed: false, Reason: "MinIO encryption prober is nil"}
	}
	if c.Bucket == "" {
		return Decision{Passed: false, Reason: "MinIO bucket name is empty"}
	}
	algorithm, err := c.Prober.GetBucketEncryption(ctx, c.Bucket)
	regulated := regulatedComplianceProfile(c.ComplianceProfile)
	if err != nil {
		// A regulated profile must fail closed: the chain is
		// unreachable, so the gateway cannot guarantee the §12.5
		// line 297 SSE invariant. Non-regulated deployments degrade
		// to advisory so a dev cluster without SSE configured still
		// installs.
		if regulated {
			return Decision{
				Passed: false,
				Reason: fmt.Sprintf("MinIO GetBucketEncryption(%q) failed under regulated complianceProfile %q: %v",
					c.Bucket, c.ComplianceProfile, err),
			}
		}
		return Decision{
			Passed: true,
			Reason: fmt.Sprintf("MinIO GetBucketEncryption(%q) advisory (non-regulated profile): %v",
				c.Bucket, err),
		}
	}
	if algorithm == "" {
		if regulated {
			return Decision{
				Passed: false,
				Reason: fmt.Sprintf("§12.5 line 297: MinIO bucket %q has no server-side encryption enabled; required for complianceProfile %q",
					c.Bucket, c.ComplianceProfile),
			}
		}
		return Decision{
			Passed: true,
			Reason: fmt.Sprintf("MinIO bucket %q has no SSE configured (advisory under unregulated complianceProfile)",
				c.Bucket),
		}
	}
	return Decision{
		Passed: true,
		Reason: fmt.Sprintf("MinIO bucket %q SSE algorithm=%q", c.Bucket, algorithm),
	}
}

// ErrMinIOSSEAbsent reports that GetBucketEncryption returned no
// configuration for the bucket. Backend implementations should wrap
// the SDK-specific "encryption configuration not found" error in this
// sentinel so callers can distinguish "absent" from "unreachable".
var ErrMinIOSSEAbsent = errors.New("preflight: MinIO bucket has no server-side encryption configured")
