// SPDX-License-Identifier: MIT

package preflight

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// spec: §17.9.4; §17.6 line 494. F-17.9.3.
func TestCloudObjectStorageLifecycleCheck_spec_17_9_4(t *testing.T) {
	ctx := context.Background()
	compliant := CloudObjectStorageLifecycleStatus{
		VersioningEnabled:               true,
		NoncurrentVersionExpirationDays: 1,
		DeleteMarkerExpirationEnabled:   true,
	}
	proberFor := func(s CloudObjectStorageLifecycleStatus) CloudObjectStorageLifecycleProber {
		return CloudObjectStorageLifecycleProbeFunc(func(context.Context, string) (CloudObjectStorageLifecycleStatus, error) {
			return s, nil
		})
	}

	tests := []struct {
		name       string
		check      CloudObjectStorageLifecycleCheck
		wantPassed bool
		wantSubstr string
	}{
		{
			name:       "minio skips",
			check:      CloudObjectStorageLifecycleCheck{Provider: "minio", Bucket: "art", Prober: proberFor(CloudObjectStorageLifecycleStatus{})},
			wantPassed: true,
			wantSubstr: "SKIPPED",
		},
		{
			name:       "empty provider skips",
			check:      CloudObjectStorageLifecycleCheck{Provider: "", Bucket: ""},
			wantPassed: true,
			wantSubstr: "SKIPPED",
		},
		{
			name:       "unknown provider fails closed",
			check:      CloudObjectStorageLifecycleCheck{Provider: "wasabi", Bucket: "art"},
			wantPassed: false,
			wantSubstr: "CONFIG_INVALID",
		},
		{
			name:       "cloud provider without bucket fails",
			check:      CloudObjectStorageLifecycleCheck{Provider: "s3", Bucket: ""},
			wantPassed: false,
			wantSubstr: "objectStorage.bucket is required",
		},
		{
			name:       "nil prober is advisory pass",
			check:      CloudObjectStorageLifecycleCheck{Provider: "gcs", Bucket: "art"},
			wantPassed: true,
			wantSubstr: "WARNING",
		},
		{
			name:       "s3 fully compliant passes",
			check:      CloudObjectStorageLifecycleCheck{Provider: "s3", Bucket: "art", Prober: proberFor(compliant)},
			wantPassed: true,
			wantSubstr: "versioning enabled",
		},
		{
			name:       "s3 missing versioning fails",
			check:      CloudObjectStorageLifecycleCheck{Provider: "s3", Bucket: "art", Prober: proberFor(CloudObjectStorageLifecycleStatus{NoncurrentVersionExpirationDays: 1, DeleteMarkerExpirationEnabled: true})},
			wantPassed: false,
			wantSubstr: "bucket versioning is not enabled",
		},
		{
			name:       "s3 missing noncurrent expiration fails",
			check:      CloudObjectStorageLifecycleCheck{Provider: "s3", Bucket: "art", Prober: proberFor(CloudObjectStorageLifecycleStatus{VersioningEnabled: true, DeleteMarkerExpirationEnabled: true})},
			wantPassed: false,
			wantSubstr: "no noncurrent-version expiration rule",
		},
		{
			name:       "s3 noncurrent expiration over ceiling fails",
			check:      CloudObjectStorageLifecycleCheck{Provider: "s3", Bucket: "art", Prober: proberFor(CloudObjectStorageLifecycleStatus{VersioningEnabled: true, NoncurrentVersionExpirationDays: 30, DeleteMarkerExpirationEnabled: true})},
			wantPassed: false,
			wantSubstr: "must be ≤ 7",
		},
		{
			name:       "s3 missing delete-marker rule fails",
			check:      CloudObjectStorageLifecycleCheck{Provider: "s3", Bucket: "art", Prober: proberFor(CloudObjectStorageLifecycleStatus{VersioningEnabled: true, NoncurrentVersionExpirationDays: 1})},
			wantPassed: false,
			wantSubstr: "expired-object-delete-marker",
		},
		{
			name: "gcs does not require delete-marker rule",
			check: CloudObjectStorageLifecycleCheck{Provider: "gcs", Bucket: "art", Prober: proberFor(CloudObjectStorageLifecycleStatus{
				VersioningEnabled:               true,
				NoncurrentVersionExpirationDays: 7,
			})},
			wantPassed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := tt.check.Decide(ctx)
			if d.Passed != tt.wantPassed {
				t.Fatalf("Passed=%v want %v (reason: %s)", d.Passed, tt.wantPassed, d.Reason)
			}
			if tt.wantSubstr != "" && !strings.Contains(d.Reason, tt.wantSubstr) {
				t.Fatalf("reason %q does not contain %q", d.Reason, tt.wantSubstr)
			}
		})
	}
}

// spec: §17.6 line 494 — a prober error on a cloud provider fails the
// install fail-closed. F-17.9.3.
func TestCloudObjectStorageLifecycleCheck_proberError_failsClosed_spec_17_6_494(t *testing.T) {
	d := CloudObjectStorageLifecycleCheck{
		Provider: "s3",
		Bucket:   "art",
		Prober: CloudObjectStorageLifecycleProbeFunc(func(context.Context, string) (CloudObjectStorageLifecycleStatus, error) {
			return CloudObjectStorageLifecycleStatus{}, errors.New("AccessDenied")
		}),
	}.Decide(context.Background())
	if d.Passed {
		t.Fatalf("expected fail-closed on prober error, got pass: %s", d.Reason)
	}
	if !strings.Contains(d.Reason, "AccessDenied") || !strings.Contains(d.Reason, "Section 17.9") {
		t.Fatalf("reason %q missing error or remediation pointer", d.Reason)
	}
}
