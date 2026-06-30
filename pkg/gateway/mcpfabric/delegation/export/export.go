// SPDX-License-Identifier: MIT

// Package export implements the §8.7 gateway-side delegation file-export
// materialization orchestrator. It is the caller the §8.7 primitive
// packages (pkg/gateway/delegation/fileexport validators,
// pkg/gateway/interceptor.RunPreExportMaterialization scan,
// pkg/upload archive validators) were built for: it composes the
// §8.2-step-3 parent-runtime export with the §8.7 validation, the
// per-file content scan, the archive-validator inheritance for exported
// archives, and cross-export overwrite detection + audit, producing the
// §14 workspace upload sources the gateway stamps onto the delegated
// child's WorkspacePlan (§8.2 steps 4 and 6).
//
// The package is transport-agnostic: the parent-pod ExportPaths RPC and
// the durable blob persistence are injected as the ParentExporter and
// Sink seams so the orchestration is unit-testable without a pod or a
// blob store.
//
// spec: §8.7 (file export model); §8.2 lines 91-95 (delegation flow
// steps 3, 4, 6).
package export

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation/fileexport"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// Spec is one §8.7 fileExport entry: a source glob resolved inside the
// parent's /workspace/current and the relative destPrefix the matched
// files are rebased under in the child workspace.
type Spec struct {
	Source     string
	DestPrefix string
}

// ExportedFile is one rebased file the parent runtime returned for a
// single Spec (the §8.2-step-3 ExportPaths result form). Path is the
// child-workspace-relative destination after the §8.7 rebasing rule.
type ExportedFile struct {
	Path    string
	Content []byte
	SHA256  string
	Size    int64
}

// ParentExporter performs the §8.2 step 3 export: it asks the parent
// runtime to resolve a single fileExport Spec against its
// /workspace/current and return the rebased files. The orchestrator
// calls it once per Spec, in declared order, so cross-Spec path
// collisions are observable at the gateway (the §8.7 line 793 overwrite
// audit) rather than being silently collapsed inside one RPC. The
// production implementation backs onto the parent pod adapter's
// ExportPaths RPC.
type ParentExporter interface {
	ExportPaths(ctx context.Context, parentSessionID string, spec Spec) ([]ExportedFile, error)
}

// Sink persists one validated, post-scan exported file durably (§8.2
// step 4: "stores exported files durably") and returns the §14
// uploadRef the child WorkspacePlan references. The production
// implementation writes to the gateway blob store the §6.3 pod binder
// reads at child workspace materialization (§8.2 step 6). tenantID
// scopes the durable key to the delegating tenant so a child blob is
// never reachable across the §12.2 tenant boundary.
type Sink interface {
	Persist(ctx context.Context, tenantID, childSessionID string, f ExportedFile) (uploadRef string, err error)
}

// Auditor emits the §8.7 line 793 delegation.export_overwrite audit
// event into the session's delegation audit trail. It matches the
// delegation.Service Auditor seam so the gateway wires one auditor for
// both. A nil Auditor disables the emission.
type Auditor interface {
	EmitDelegationEvent(ctx context.Context, eventType string, detail map[string]any)
}

// EventExportOverwrite is the §8.7 line 793 audit event the orchestrator
// emits when a later export entry overwrites a path an earlier entry
// already placed in the child workspace.
const EventExportOverwrite = "delegation.export_overwrite"

// ContentScan carries the §8.3 effective contentPolicy inputs to the
// optional PreExportMaterialization per-file scan. When Enabled is
// false (the §8.7 default) no interceptor call is made on the export
// path; structure is still validated.
type ContentScan struct {
	// Enabled mirrors contentPolicy.scanExportedFiles. When false the
	// scan loop is skipped entirely (§8.7 default).
	Enabled bool
	// MaxFileSize is contentPolicy.maxExportedFileSize: a file larger
	// than this is rejected with EXPORT_FILE_SCAN_SIZE_EXCEEDED before
	// any interceptor call. Non-positive disables the per-file ceiling.
	MaxFileSize int64
	// Chain is the resolved contentPolicy.interceptorRef chain. Required
	// when Enabled is true; a nil Chain with Enabled true is a
	// configuration error surfaced by Materialize.
	Chain *interceptor.Chain
	// ScanCtx carries the §11.7 / §16.1 labels and the ExportScanObserver
	// that emits the per-file audit events and metrics.
	ScanCtx interceptor.ExportScanContext
}

// Params is one delegation's resolved file-export request.
type Params struct {
	// ParentSessionID is the running parent the files are exported from.
	ParentSessionID string
	// ChildSessionID is the delegated child the rebased files are
	// persisted for; the Sink keys durable blobs on it.
	ChildSessionID string
	// TenantID scopes the §11.7 audit + the §16.1 scan metric labels.
	TenantID string
	// Specs are the §8.7 fileExport entries in declared order. Later
	// entries overwrite earlier ones on a child-path collision.
	Specs []Spec
	// Limits is the §8.3 lease fileExportLimits structural ceiling
	// (count + aggregate bytes) checked across all Specs.
	Limits fileexport.FileExportLimits
	// Scan carries the §8.3 contentPolicy per-file scan inputs.
	Scan ContentScan
}

// Overwrite records one §8.7 line 793 child-workspace path collision:
// a later export Spec placed a file at a path an earlier Spec had
// already written.
type Overwrite struct {
	// Path is the colliding child-workspace-relative destination.
	Path string
	// SpecIndex is the zero-based index of the later Spec that caused
	// the overwrite.
	SpecIndex int
	// Source is that Spec's source glob, for the audit trail.
	Source string
}

// Result is the orchestrator output the gateway stamps onto the child
// WorkspacePlan and uses for the §8.2 step-4 durable accounting.
type Result struct {
	// Sources are the §14 child WorkspacePlan upload sources (uploadFile
	// for a plain file, uploadArchive for a detected archive) in
	// child-path order.
	Sources []workspaceplan.Source
	// FileCount is the number of distinct child files after overwrite
	// collapse.
	FileCount int
	// TotalBytes is the aggregate exported size after overwrite collapse,
	// the value checked against Limits.MaxTotalSize.
	TotalBytes int64
	// Overwrites are the §8.7 line 793 collisions detected across Specs.
	Overwrites []Overwrite
}

// Materializer runs the §8.7 gateway-side export materialization.
type Materializer struct {
	exporter ParentExporter
	sink     Sink
	auditor  Auditor
}

// NewMaterializer returns a Materializer. exporter (the §8.2-step-3
// parent export RPC) and sink (the §8.2-step-4 durable persistence) are
// required; a nil auditor disables the §8.7 line 793 overwrite audit.
func NewMaterializer(exporter ParentExporter, sink Sink, auditor Auditor) *Materializer {
	return &Materializer{exporter: exporter, sink: sink, auditor: auditor}
}

// Materialize runs the §8.7 export pipeline for one delegation:
//
//  1. validate each Spec's destPrefix (§8.7 line 789);
//  2. export each Spec from the parent in declared order (§8.2 step 3),
//     merging into a child-path map and recording every overwrite a
//     later Spec causes (§8.7 line 793 — audited);
//  3. enforce the lease fileExportLimits count + aggregate-size ceilings
//     (§8.7 lines 790-791);
//  4. run the optional PreExportMaterialization per-file content scan
//     (§8.7 — contentPolicy.scanExportedFiles), applying MODIFY rewrites
//     and failing the whole export on the first REJECT;
//  5. persist each surviving file durably and build the §14 child upload
//     sources (§8.2 steps 4, 6). An exported archive is routed as an
//     uploadArchive source so the child materialization inherits the
//     §13.4 / §7.4 upload archive validators (§8.7 line 792); every
//     other file is an uploadFile placed verbatim.
//
// On any validation, scan-REJECT, or persistence failure it returns the
// typed error and persists nothing further; the caller rolls the
// delegation back (no partial child workspace).
func (m *Materializer) Materialize(ctx context.Context, p Params) (Result, error) {
	if m.exporter == nil || m.sink == nil {
		return Result{}, fmt.Errorf("export: materializer requires a parent exporter and a sink")
	}
	if p.Scan.Enabled && p.Scan.Chain == nil {
		// spec: §8.7 — scanExportedFiles:true presupposes a resolvable
		// contentPolicy.interceptorRef. A true flag with no chain is a
		// fail-closed configuration error rather than a silent skip.
		return Result{}, fmt.Errorf("export: contentPolicy.scanExportedFiles is true but no interceptor chain was resolved")
	}

	// §8.7 lines 774, 793: process Specs in declared order, merging into
	// a child-path map. order preserves first-seen child paths; byPath
	// holds the live (last-write-wins) file. A later Spec writing an
	// existing path is an overwrite — recorded and audited.
	var order []string
	byPath := map[string]ExportedFile{}
	var overwrites []Overwrite

	for i, spec := range p.Specs {
		if err := fileexport.ValidateDestPrefix(spec.DestPrefix); err != nil {
			return Result{}, err
		}
		files, err := m.exporter.ExportPaths(ctx, p.ParentSessionID, spec)
		if err != nil {
			return Result{}, fmt.Errorf("export: parent export of source %q: %w", spec.Source, err)
		}
		for _, f := range files {
			clean := path.Clean(strings.TrimPrefix(filepathToSlash(f.Path), "./"))
			if _, seen := byPath[clean]; seen {
				ow := Overwrite{Path: clean, SpecIndex: i, Source: spec.Source}
				overwrites = append(overwrites, ow)
				m.auditOverwrite(ctx, p, ow)
			} else {
				order = append(order, clean)
			}
			f.Path = clean
			byPath[clean] = f
		}
	}

	// §8.7 lines 790-791: the structural ceilings are checked against the
	// collapsed (post-overwrite) set — the bytes that actually reach the
	// child workspace, not the pre-collapse export total.
	var totalBytes int64
	for _, pth := range order {
		totalBytes += int64(len(byPath[pth].Content))
	}
	if err := p.Limits.Check(len(order), totalBytes); err != nil {
		return Result{}, err
	}

	// §8.7: optional PreExportMaterialization per-file scan. The scan runs
	// over the collapsed set so a path overwritten by a later Spec is
	// scanned once (its surviving content). RunPreExportMaterialization
	// applies MODIFY rewrites and fails on the first REJECT.
	if p.Scan.Enabled {
		scanIn := make([]interceptor.ExportFile, 0, len(order))
		for _, pth := range order {
			f := byPath[pth]
			scanIn = append(scanIn, interceptor.ExportFile{Path: pth, Content: f.Content})
		}
		scanned, err := interceptor.RunPreExportMaterialization(
			ctx, p.Scan.Chain, p.Scan.ScanCtx, p.TenantID, p.ChildSessionID, p.Scan.MaxFileSize, scanIn...,
		)
		if err != nil {
			return Result{}, err
		}
		for _, sf := range scanned {
			f := byPath[sf.Path]
			f.Content = sf.Content
			f.Size = int64(len(sf.Content))
			f.SHA256 = interceptor.ComputeExportFileHash(sf.Content)
			byPath[sf.Path] = f
		}
	}

	// §8.2 steps 4, 6: persist each surviving file durably and build the
	// child upload sources in child-path order.
	sources := make([]workspaceplan.Source, 0, len(order))
	var finalBytes int64
	for _, pth := range order {
		f := byPath[pth]
		ref, err := m.sink.Persist(ctx, p.TenantID, p.ChildSessionID, f)
		if err != nil {
			return Result{}, fmt.Errorf("export: persist %q for child %s: %w", pth, p.ChildSessionID, err)
		}
		sources = append(sources, childSource(pth, ref))
		finalBytes += int64(len(f.Content))
	}

	return Result{
		Sources:    sources,
		FileCount:  len(order),
		TotalBytes: finalBytes,
		Overwrites: overwrites,
	}, nil
}

// WorkspacePlanJSON renders the materialized upload sources as a §14
// WorkspacePlan document the gateway stamps on the child session row.
// The §6.3 pod binder re-parses it through workspaceplan.ParseStored at
// child workspace materialization (§8.2 step 6) and streams each
// uploadRef's bytes onto the child pod. It returns (nil, nil) when the
// export produced no files so the caller leaves the child row's plan
// unset rather than stamping an empty plan.
//
// A root-level exported archive carries an empty Materializer pathPrefix
// (the §8.7 unpack-at-workspace-root case); §14 uploadArchive requires a
// non-empty pathPrefix, so the renderer maps it to "." (the workspace
// root) to keep the stamped plan ParseStored-valid.
//
// spec: §14 (WorkspacePlan); §8.2 line 95 (delegation flow step 6).
func (r Result) WorkspacePlanJSON() (json.RawMessage, error) {
	if len(r.Sources) == 0 {
		return nil, nil
	}
	type wireSource struct {
		Type       string `json:"type"`
		Path       string `json:"path,omitempty"`
		PathPrefix string `json:"pathPrefix,omitempty"`
		UploadRef  string `json:"uploadRef"`
		Format     string `json:"format,omitempty"`
	}
	type wirePlan struct {
		SchemaVersion int          `json:"schemaVersion"`
		Sources       []wireSource `json:"sources"`
	}
	plan := wirePlan{SchemaVersion: workspaceplan.SchemaVersion}
	for i, src := range r.Sources {
		switch v := src.Variant.(type) {
		case workspaceplan.UploadFile:
			plan.Sources = append(plan.Sources, wireSource{
				Type:      workspaceplan.TypeUploadFile,
				Path:      v.PathField,
				UploadRef: v.UploadRef,
			})
		case workspaceplan.UploadArchive:
			prefix := v.PathPrefix
			if prefix == "" {
				prefix = "."
			}
			plan.Sources = append(plan.Sources, wireSource{
				Type:       workspaceplan.TypeUploadArchive,
				PathPrefix: prefix,
				UploadRef:  v.UploadRef,
				Format:     v.Format,
			})
		default:
			return nil, fmt.Errorf("export: source %d has unexpected variant %T", i, src.Variant)
		}
	}
	return json.Marshal(plan)
}

// auditOverwrite emits the §8.7 line 793 delegation.export_overwrite
// event for one child-workspace path collision.
func (m *Materializer) auditOverwrite(ctx context.Context, p Params, ow Overwrite) {
	if m.auditor == nil {
		return
	}
	m.auditor.EmitDelegationEvent(ctx, EventExportOverwrite, map[string]any{
		"parent_session_id": p.ParentSessionID,
		"child_session_id":  p.ChildSessionID,
		"tenant_id":         p.TenantID,
		"overwritten_path":  ow.Path,
		"export_source":     ow.Source,
		"export_spec_index": ow.SpecIndex,
	})
}

// childSource builds the §14 child WorkspacePlan source for one rebased
// file. An archive (detected from its extension) becomes an uploadArchive
// so the child materialization inherits the §13.4 / §7.4 upload archive
// validators (§8.7 line 792); any other file is an uploadFile placed
// verbatim at its rebased child path.
func childSource(childPath, uploadRef string) workspaceplan.Source {
	if format, ok := archiveFormat(childPath); ok {
		return workspaceplan.Source{
			Type: workspaceplan.TypeUploadArchive,
			Variant: workspaceplan.UploadArchive{
				PathPrefix: archivePathPrefix(childPath),
				UploadRef:  uploadRef,
				Format:     format,
			},
		}
	}
	return workspaceplan.Source{
		Type: workspaceplan.TypeUploadFile,
		Variant: workspaceplan.UploadFile{
			PathField: childPath,
			UploadRef: uploadRef,
		},
	}
}

// archiveFormat reports the §13.4 uploadArchive format for a child path
// whose extension marks it as an archive, so the child materialization
// unpacks it under the §13.4 / §7.4 validators (§8.7 line 792). An
// unrecognised extension returns ("", false) and the file is placed as a
// plain uploadFile.
func archiveFormat(p string) (string, bool) {
	lower := strings.ToLower(p)
	switch {
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return "tar.gz", true
	case strings.HasSuffix(lower, ".tar"):
		return "tar", true
	case strings.HasSuffix(lower, ".zip"):
		return "zip", true
	}
	return "", false
}

// archivePathPrefix is the child-workspace directory an exported archive
// is unpacked under: its rebased parent directory, so the archive's
// contents land where the archive file would have. A root-level archive
// unpacks at the workspace root (empty prefix).
func archivePathPrefix(childPath string) string {
	dir := path.Dir(childPath)
	if dir == "." || dir == "/" {
		return ""
	}
	return dir
}

// filepathToSlash normalises a child path to forward slashes; the
// adapter already returns slash paths but a defensive normalisation
// keeps the merge map keyed consistently.
func filepathToSlash(p string) string {
	return strings.ReplaceAll(p, "\\", "/")
}
