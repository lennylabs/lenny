// SPDX-License-Identifier: MIT

package main

import (
	"context"
)

// sessionArtifactDeleter is implemented by session-scoped stores that
// expose the per-session DeleteBySession adapter — the transcript and
// blob stores. It backs both the §12.8 erasure orchestrator and the
// §7.1 retention GC.
type sessionArtifactDeleter interface {
	DeleteBySession(ctx context.Context, tenantID, sessionID string) (int, error)
}

// artifactMetricsSink is implemented by every §17.9.3 artifact-store
// backend that surfaces the §12.5 ll. 282/303 metric callbacks
// (MinIO, S3, GCS, Azure). The gateway type-asserts the resolved
// blobstore.Store onto it so the fail-closed KMS-unavailable and
// retry-exhausted upload-error counters are wired no matter which
// provider serves the bucket. spec: §12.5 ll. 282, 303; F-17.5.1.
type artifactMetricsSink interface {
	SetOnArtifactUploadError(func(tenantID, errorType string))
	SetOnKMSUnavailable(func(tenantID string))
}

// tierMismatchSink is implemented by the non-envelope-capable artifact
// stores (the in-memory and §17.4 local-filesystem backends) that reject
// a T4 tenant's write under the §12.9 storage-boundary tier
// check. The cloud backends do not implement it: they enforce the T4
// contract through their own SSE-KMS resolver and surface
// kms_unavailable instead.
//
// spec: §12.9.
type tierMismatchSink interface {
	SetOnTierStoreMismatch(func(tenantID string))
}
