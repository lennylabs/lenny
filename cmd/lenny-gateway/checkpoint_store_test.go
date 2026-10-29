// SPDX-License-Identifier: MIT

package main

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/blobstore/azureblob"
	"github.com/lennylabs/lenny/pkg/blobstore/gcs"
	"github.com/lennylabs/lenny/pkg/blobstore/miniostore"
	"github.com/lennylabs/lenny/pkg/blobstore/s3"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointer"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/resumechunks"
)

// productionBackends are the objectStorage.provider backends a pod-based
// deployment runs on. The wiring reads only the dynamic type, so a typed
// nil pointer of each stands in for a configured store.
func productionBackends() map[string]blobstore.Store {
	return map[string]blobstore.Store{
		"minio": (*miniostore.Store)(nil),
		"s3":    (*s3.Store)(nil),
		"gcs":   (*gcs.Store)(nil),
		"azure": (*azureblob.Store)(nil),
	}
}

// spec: 10.1.7 (preStop Hook Drain: Reassembly on resume), 13.2 (Network Isolation: capability model)
//
// F-10.1.45: the chunk resolver was built only when the object store
// type-asserted to the optional resumechunks.ChunkLister, which only
// miniostore implemented, so it was nil on S3, GCS, and Azure and a
// resume restored no chunks there. Every production backend now yields a
// non-nil CheckpointStore and therefore a non-nil resolver, a non-nil
// cleanup object store, and passes the pod-based startup check.
func TestResumeChunkResolverWiredOnEveryProductionBackend(t *testing.T) {
	manifests := partialmanifeststore.NewMemoryStore(nil)
	for name, store := range productionBackends() {
		t.Run(name, func(t *testing.T) {
			cs := checkpointStoreOf(store)
			if cs == nil {
				t.Fatalf("checkpointStoreOf(%T) = nil, want the backend as a CheckpointStore", store)
			}
			r := newResumeChunkResolver(cs, manifests, time.Minute, 0.5)
			if r == nil {
				t.Fatalf("newResumeChunkResolver over %s = nil, want a resolver so resume restores chunks", name)
			}
			res, ok := r.(*resumechunks.Resolver)
			if !ok {
				t.Fatalf("resolver type = %T, want *resumechunks.Resolver", r)
			}
			if res.Lister == nil || res.Presigner == nil || res.TTL != time.Minute || res.PartialRecoveryThresholdFraction != 0.5 {
				t.Fatalf("resolver = %+v, want lister, presigner, TTL 1m, and fraction 0.5 wired", res)
			}
			var _ checkpointer.ChunkObjectStore = cs
			if !requireCheckpointStore("lenny-agents", store) {
				t.Fatalf("requireCheckpointStore(%s) = false, want a pod-based deployment to boot on it", name)
			}
		})
	}
}

// spec: 13.2 (Network Isolation: capability model), 10.1.7 (preStop Hook Drain)
//
// The in-memory and filesystem stores list but do not presign, so they
// are not CheckpointStores: a dev gateway gets a nil resolver (a nil
// interface, which the session server's guard skips), and a pod-based
// deployment refuses to boot on them.
func TestNonPresigningBackendsYieldNoResolverAndFailPodStartup(t *testing.T) {
	fs, err := blobstore.NewFilesystemStore(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewFilesystemStore: %v", err)
	}
	for name, store := range map[string]blobstore.Store{
		"memory":     blobstore.NewMemoryStore(nil),
		"filesystem": fs,
	} {
		t.Run(name, func(t *testing.T) {
			if cs := checkpointStoreOf(store); cs != nil {
				t.Fatalf("checkpointStoreOf(%s) = %T, want nil for a store that cannot presign", name, cs)
			}
			if r := newResumeChunkResolver(nil, nil, time.Minute, 0); r != nil {
				t.Fatalf("newResumeChunkResolver(nil) = %T, want a nil interface", r)
			}
			if requireCheckpointStore("lenny-agents", store) {
				t.Fatalf("requireCheckpointStore(pod-based, %s) = true, want startup refused", name)
			}
			if !requireCheckpointStore("", store) {
				t.Fatalf("requireCheckpointStore(no agent namespace, %s) = false, want a pod-less gateway to boot", name)
			}
		})
	}
}
