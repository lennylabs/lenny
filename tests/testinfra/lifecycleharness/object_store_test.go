// SPDX-License-Identifier: MIT

package lifecycleharness

import (
	"context"
	"errors"
	"io"
	"slices"
	"testing"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/resumechunks"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// nopTransport is a pod adapter object-store transport the wiring tests
// compare by identity; it is never called.
type nopTransport struct{ name string }

func (nopTransport) PutChunk(context.Context, string, map[string]string, int64, io.Reader) (int, string, error) {
	return 0, "", errors.New("nopTransport: PutChunk")
}

func (nopTransport) GetChunk(context.Context, string, map[string]string) (io.ReadCloser, error) {
	return nil, errors.New("nopTransport: GetChunk")
}

// nopResolver is a session server chunk resolver the wiring tests compare
// by identity; it is never called.
type nopResolver struct{ name string }

func (nopResolver) Resolve(context.Context, string, string, string) (resumechunks.ResolveResult, error) {
	return resumechunks.ResolveResult{}, errors.New("nopResolver: Resolve")
}

// A harness with an object store wires its blob store, manifest store, and
// chunk resolver into every replica's session server options and its
// transport into every pod adapter; a harness without one leaves the
// options and the transport unset, so derive and replay stay unwired.
//
// spec: §7.1 (Derive restore semantics)
func TestObjectStoreWiresReplicaAndPodOnlyWhenSet_spec_7_1(t *testing.T) {
	transport := nopTransport{name: "pod"}
	resolver := nopResolver{name: "replica"}
	o := &ObjectStore{
		Blobs:     blobstore.NewMemoryStore(nil),
		Manifests: partialmanifeststore.NewMemoryStore(nil),
		Resolver:  resolver,
		Transport: transport,
	}

	var wired sessionserver.Options
	(&Harness{objects: o}).applyObjectStore(&wired)
	if wired.Blobs != o.Blobs || wired.CheckpointManifestReader != o.Manifests || wired.CheckpointManifestWriter != o.Manifests || wired.ResumeChunkResolver != resolver {
		t.Errorf("replica options = %+v, want the object store's blob store, manifest store, and resolver", wired)
	}
	if got := objectTransport(o); got != transport {
		t.Errorf("objectTransport = %v, want the object store's transport", got)
	}

	var bare sessionserver.Options
	(&Harness{}).applyObjectStore(&bare)
	if bare.Blobs != nil || bare.CheckpointManifestReader != nil || bare.CheckpointManifestWriter != nil || bare.ResumeChunkResolver != nil {
		t.Errorf("replica options without an object store = %+v, want none wired", bare)
	}
	if got := objectTransport(nil); got != nil {
		t.Errorf("objectTransport(nil) = %v, want nil", got)
	}
}

// Started reports the sessions the runtime started in start order, as a
// copy the caller cannot mutate the runtime's record through.
//
// spec: §7.1 (Derive restore semantics)
func TestRecordingRuntimeStartedReportsStartOrder_spec_7_1(t *testing.T) {
	r := NewRecordingRuntime()
	ctx := context.Background()
	for _, id := range []string{"sess-a", "sess-b"} {
		if err := r.Start(ctx, id); err != nil {
			t.Fatalf("Start(%s): %v", id, err)
		}
	}
	got := r.Started()
	if !slices.Equal(got, []string{"sess-a", "sess-b"}) {
		t.Fatalf("Started = %v, want [sess-a sess-b]", got)
	}
	got[0] = "mutated"
	if r.Started()[0] != "sess-a" {
		t.Errorf("Started returned the runtime's own slice")
	}
}
