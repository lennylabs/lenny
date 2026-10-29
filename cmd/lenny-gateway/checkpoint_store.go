// SPDX-License-Identifier: MIT

package main

import (
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/resumechunks"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// checkpointStoreOf returns the resolved object store as the §10.1.7
// chunked-checkpoint surface, or nil when the backend does not satisfy
// it. Every production backend (MinIO, S3, GCS, and Azure Blob) satisfies
// blobstore.CheckpointStore by compile-time assertion; the in-memory and
// filesystem stores do not presign, so they yield nil and a dev gateway
// runs without chunked checkpoints. A pod-based deployment never reaches
// the nil case, because startup refuses a backend that is not a
// CheckpointStore (requireCheckpointStore).
//
// The chunk resolver, the checkpoint driver's capability minting and
// chunk release, the resume-path partial cleaner, and the backstop sweep
// all read this one value, so a backend that lists and presigns serves
// every one of them. Before F-10.1.45 those seams were wired against the
// concrete MinIO store or an optional-interface assertion, which left the
// resolver nil on S3, GCS, and Azure.
//
// spec: §10.1.7; §13.2 (capability model); §4.4.
func checkpointStoreOf(objectStore blobstore.Store) blobstore.CheckpointStore {
	cs, ok := objectStore.(blobstore.CheckpointStore)
	if !ok {
		return nil
	}
	return cs
}

// requireCheckpointStore is the startup fail-closed check for a pod-based
// deployment (a non-empty agent namespace): the resolved backend must be
// a blobstore.CheckpointStore so the checkpoint driver can mint PUT
// capabilities and the resume path can list and restore the chunk set.
// It returns false when the deployment is pod-based and the backend does
// not qualify.
//
// spec: §13.2 (capability model); §10.1.7.
func requireCheckpointStore(agentNamespace string, objectStore blobstore.Store) bool {
	if agentNamespace == "" {
		return true
	}
	return checkpointStoreOf(objectStore) != nil
}

// newResumeChunkResolver builds the §10.1.7 reassembly-on-resume
// resolver over store. It returns a nil interface (not a typed nil) when
// store is nil, so the session server's nil guard skips chunk resolution
// on a dev gateway.
//
// spec: §10.1.7.
func newResumeChunkResolver(
	store blobstore.CheckpointStore,
	manifests partialmanifeststore.Store,
	ttl time.Duration,
	partialRecoveryThresholdFraction float64,
) sessionserver.ResumeChunkResolver {
	if store == nil {
		return nil
	}
	return &resumechunks.Resolver{
		Manifests:                        manifests,
		Presigner:                        store,
		Lister:                           store,
		TTL:                              ttl,
		PartialRecoveryThresholdFraction: partialRecoveryThresholdFraction,
	}
}
