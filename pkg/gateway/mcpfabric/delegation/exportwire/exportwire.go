// SPDX-License-Identifier: MIT

// Package exportwire wires the §8.7 delegation file-export Materializer's
// transport-agnostic seams (export.ParentExporter and export.Sink) to the
// running gateway infrastructure: the §8.2-step-3 parent export RPC over
// the bound parent pod's adapter client, and the §8.2-step-4 durable
// persistence over the §4.5 blob store. The pure orchestration lives in
// pkg/gateway/delegation/export; this package keeps the infra coupling out
// of that package so the orchestrator stays unit-testable.
//
// spec: §8.7 (file export model); §8.2 lines 91-95 (steps 3, 4);
// §4.5 (blob store); §6.3 (pod binder reads the persisted blobs).
package exportwire

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation/export"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
)

// DefaultExportTTL is the §4.5 TTL stamped on a durable exported-file
// blob. It matches the upload-path default (7 days) so an exported file
// outlives the child session's materialization window the same way an
// uploaded file does.
const DefaultExportTTL = 7 * 24 * time.Hour

// adapterRegistry is the subset of *podsession.Registry PodExporter needs:
// resolving a running session's bound pod adapter client. Narrowed to an
// interface so the exporter is unit-testable without a real registry.
type adapterRegistry interface {
	Get(sessionID string) (*podsession.BindResult, bool)
}

// PodExporter implements export.ParentExporter by routing the §8.7
// ExportPaths RPC to the running parent session's bound pod adapter. The
// parent must be bound on this gateway replica (the §6.3 binder records
// the binding in the registry); an unbound parent yields ErrParentUnbound
// so the delegation fails rather than silently exporting nothing.
type PodExporter struct {
	reg adapterRegistry
}

// NewPodExporter returns a PodExporter over the gateway's pod-session
// registry. reg is required.
func NewPodExporter(reg *podsession.Registry) *PodExporter {
	return &PodExporter{reg: reg}
}

// ErrParentUnbound reports that the parent session has no pod binding on
// this gateway replica, so its /workspace/current cannot be reached for
// the §8.2-step-3 export.
var ErrParentUnbound = fmt.Errorf("exportwire: parent session has no bound pod adapter on this replica")

// ExportPaths runs the §8.2-step-3 ExportPaths RPC for one fileExport
// spec against the parent pod's adapter and adapts the result to the
// export package's ExportedFile form.
func (e *PodExporter) ExportPaths(ctx context.Context, parentSessionID string, spec export.Spec) ([]export.ExportedFile, error) {
	bind, ok := e.reg.Get(parentSessionID)
	if !ok || bind == nil || bind.Adapter == nil {
		return nil, ErrParentUnbound
	}
	files, err := bind.Adapter.ExportPaths(ctx, parentSessionID, []adapterclient.ExportSpec{
		{Source: spec.Source, DestPrefix: spec.DestPrefix},
	})
	if err != nil {
		return nil, err
	}
	out := make([]export.ExportedFile, 0, len(files))
	for _, f := range files {
		out = append(out, export.ExportedFile{
			Path:    f.Path,
			Content: f.Content,
			SHA256:  f.SHA256,
			Size:    f.Size,
		})
	}
	return out, nil
}

// BlobSink implements export.Sink by persisting each exported file to the
// §4.5 blob store under the §12.5 export object class, keyed to the
// delegating tenant and the child session so the durable blob is never
// reachable across the §12.2 tenant boundary. The returned uploadRef is
// the canonical lenny-blob:// URI the child WorkspacePlan references and
// the §6.3 binder reads at materialization.
type BlobSink struct {
	blobs blobstore.Store
	ttl   time.Duration
}

// NewBlobSink returns a BlobSink over the gateway blob store. blobs is
// required; a non-positive ttl selects DefaultExportTTL.
func NewBlobSink(blobs blobstore.Store, ttl time.Duration) *BlobSink {
	if ttl <= 0 {
		ttl = DefaultExportTTL
	}
	return &BlobSink{blobs: blobs, ttl: ttl}
}

// Persist writes one exported file's bytes to the blob store and returns
// the canonical URI. The part id is freshly minted so two exported files
// with identical content (or identical child paths across retries) never
// collide on the §4.5 immutable key.
func (s *BlobSink) Persist(ctx context.Context, tenantID, childSessionID string, f export.ExportedFile) (string, error) {
	uri := blobstore.URI{
		TenantID:   tenantID,
		ObjectType: blobstore.ObjectTypeExport,
		SessionID:  childSessionID,
		PartID:     blobstore.NewPartID(),
		TTL:        s.ttl,
	}
	ref, err := s.blobs.Put(uri, "application/octet-stream", bytes.NewReader(f.Content))
	if err != nil {
		return "", fmt.Errorf("exportwire: persist export blob for child %s: %w", childSessionID, err)
	}
	return ref, nil
}
