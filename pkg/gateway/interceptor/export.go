// SPDX-License-Identifier: MIT

package interceptor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// §8.7 file-export scan error codes. The gateway returns these on the
// delegation file-export path when the PreExportMaterialization phase
// rejects a file. They are defined here, with the PreExportMaterialization
// entry point that produces them, alongside CodeInterceptorTimeout.
const (
	// CodeExportFileScanRejected is the §8.7 error code returned when the
	// PreExportMaterialization interceptor chain returns REJECT for an
	// exported file. The delegation fails at materialization time and no
	// file is persisted into the child's workspace.
	CodeExportFileScanRejected = "EXPORT_FILE_SCAN_REJECTED"

	// CodeExportFileScanSizeExceeded is the §8.7 error code returned when
	// an exported file's byte length exceeds the delegation policy's
	// contentPolicy.maxExportedFileSize. The file is rejected before any
	// interceptor call is made and the whole export fails.
	CodeExportFileScanSizeExceeded = "EXPORT_FILE_SCAN_SIZE_EXCEEDED"

	// CodeInterceptorImmutableFieldViolation is the §4.8 error code
	// returned when a MODIFY decision alters a field the phase marks
	// immutable. At PreExportMaterialization the immutable fields are
	// file_path and every delegation_context member; the gateway
	// re-derives file_size and sha256 rather than comparing them.
	CodeInterceptorImmutableFieldViolation = "INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION"

	// CodeExportFileHashMismatch is the §7.4 line 446 error code returned
	// when the gateway's hash check on an exported file fails. The
	// gateway computes SHA-256 of the bytes at every boundary (parent
	// export, post-MODIFY, child delivery) and verifies it against a
	// recorded expectation; tampering between parent export and child
	// delivery surfaces here. F-7.4.10.
	CodeExportFileHashMismatch = "EXPORT_FILE_HASH_MISMATCH"
)

// ExportDelegationContext is the §4.8 delegation_context block embedded in
// a PreExportMaterialization payload. It is immutable across a MODIFY.
type ExportDelegationContext struct {
	// ParentPod identifies the parent pod the file is exported from.
	ParentPod string `json:"parent_pod"`
	// ChildSessionPlanDigest is the digest of the child session's
	// workspace plan.
	ChildSessionPlanDigest string `json:"child_session_plan_digest"`
}

// ExportFile is one file a delegation exports from a parent workspace to a
// child. It is the input to RunPreExportMaterialization and, serialized as
// the §4.8 exported-file record, the content payload of a
// PreExportMaterialization interceptor call.
type ExportFile struct {
	// Path is the file's destination path in the child's workspace
	// (post-rebasing per §8.7). It is immutable across a MODIFY.
	Path string
	// Content is the file bytes. A MODIFY decision rewrites it.
	Content []byte
	// DelegationContext carries the §4.8 delegation_context block. It is
	// immutable across a MODIFY.
	DelegationContext ExportDelegationContext
	// Hash is the §7.4 line 446 mandatory SHA-256 of Content, hex-encoded
	// lowercase. RunPreExportMaterialization verifies an inbound non-
	// empty Hash against the bytes and refuses on mismatch; every
	// returned file carries Hash set to the post-pipeline content's
	// SHA-256 so the export-to-child caller can persist + re-verify at
	// child delivery time without re-computing on the gateway side.
	// F-7.4.10.
	Hash string
}

// ComputeExportFileHash returns the §7.4 line 446 hex-encoded SHA-256
// of content. Used by the delegation export-to-child flow at both the
// parent-export step (stamp the hash on the resolved ExportFile) and
// the child-delivery step (verify the persisted bytes match the
// stamped hash). F-7.4.10.
func ComputeExportFileHash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// VerifyExportFileHash compares the SHA-256 of content against the
// expected hex-encoded hash. Returns nil on match, an
// *ExportScanError{Code: CodeExportFileHashMismatch} on disagreement.
// Pass an empty expected value to skip the check ("optional for
// client uploads"); the export-to-child flow always passes a
// non-empty value (mandatory per §7.4 line 446). F-7.4.10.
func VerifyExportFileHash(path string, content []byte, expected string) error {
	if expected == "" {
		return nil
	}
	got := ComputeExportFileHash(content)
	if !equalFoldASCII(got, expected) {
		return &ExportScanError{
			Code:   CodeExportFileHashMismatch,
			Path:   path,
			Reason: fmt.Sprintf("expected %s, actual %s", expected, got),
		}
	}
	return nil
}

// equalFoldASCII is a hex-comparison helper: SHA-256 hex digests are
// ASCII so a fold-compare is sufficient (no Unicode case folding).
func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

// exportFileRecord is the §4.8 PreExportMaterialization content payload:
// the serialized exported-file record handed to the interceptor.
type exportFileRecord struct {
	FilePath          string                  `json:"file_path"`
	FileSize          uint64                  `json:"file_size"`
	SHA256            string                  `json:"sha256"`
	ContentBytes      []byte                  `json:"content_bytes"`
	DelegationContext ExportDelegationContext `json:"delegation_context"`
}

// ExportScanError reports a §8.7 PreExportMaterialization rejection. Code
// is CodeExportFileScanRejected for a REJECT decision or
// CodeExportFileScanSizeExceeded for an over-size file. Path names the
// file that failed and Reason carries the interceptor's cause (empty for
// a size rejection, which fires before any interceptor call).
type ExportScanError struct {
	Code   string
	Path   string
	Reason string
}

func (e *ExportScanError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("%s: exported file %q rejected: %s", e.Code, e.Path, e.Reason)
	}
	return fmt.Sprintf("%s: exported file %q rejected", e.Code, e.Path)
}

// RunPreExportMaterialization runs the PhasePreExportMaterialization
// interceptor chain over a delegation's exported files, per §8.7. It is
// the gateway's per-file content-scan entry point: the delegation
// file-export path calls it once for a delegation's resolved fileExport
// spec, after PreDelegation has passed and before any exported byte is
// written to the child's durable workspace storage.
//
// SEAM — export-path wiring is a follow-on. The §8.7 delegation
// file-export materialization path (fileExport glob resolution,
// parent-pod file fetch, child-workspace persistence) is not yet built:
// delegation.Service.Delegate creates the child session but does not
// materialize exported files, and delegate_task carries no fileExport
// field. When that path is built it must, when the parent lease's
// effective contentPolicy.scanExportedFiles is true, call this function
// with contentPolicy.maxExportedFileSize and contentPolicy.interceptorRef's
// registered chain, then persist the returned (post-MODIFY) files. This
// function is the reusable hook that path will call; it is independently
// testable today.
//
// maxExportedFileSize is the §8.3 contentPolicy.maxExportedFileSize
// ceiling. Each file's byte length is checked against it first: an
// over-size file is rejected with CodeExportFileScanSizeExceeded before
// any interceptor call is made, bounding the per-call gRPC payload. A
// non-positive ceiling disables the size check.
//
// Files are then scanned sequentially in slice order. For each file the
// chain runs over the §4.8 exported-file record:
//
//   - ALLOW leaves the file unchanged.
//   - MODIFY replaces the file's Content with the returned content_bytes;
//     the gateway re-derives the file's sha256 from the new bytes (the
//     returned record is the source of truth for the bytes only).
//   - REJECT fails the delegation with CodeExportFileScanRejected and
//     short-circuits the remaining files — no partial materialization.
//
// An interceptor error or timeout is resolved inside Chain.Run by the
// interceptor's FailPolicy: a fail-closed interceptor surfaces here as a
// REJECT (CodeExportFileScanRejected). The §8.7 fail-open behavior — the
// file is admitted and a delegation.export_scan_failed_open audit event is
// emitted — is the Chain skipping the interceptor; the audit-event
// emission is the export-path caller's responsibility.
//
// On success it returns the files with any MODIFY transformations
// applied, in the input order. On the first REJECT it returns an
// *ExportScanError and the files scanned so far are discarded by the
// caller (the delegation is rolled back).
func RunPreExportMaterialization(ctx context.Context, c *Chain, tenantID, sessionID string, maxExportedFileSize int64, files ...ExportFile) ([]ExportFile, error) {
	out := make([]ExportFile, 0, len(files))
	for _, f := range files {
		// §7.4 line 446: when the caller stamped an inbound Hash on a
		// resolved ExportFile (the parent-export step's
		// ComputeExportFileHash output), verify the bytes have not
		// been tampered with before any interceptor call. F-7.4.10.
		if err := VerifyExportFileHash(f.Path, f.Content, f.Hash); err != nil {
			return nil, err
		}
		// §8.7 rule 2: the per-file ceiling is enforced before any
		// interceptor call so a single over-size file cannot stall the
		// interceptor or inflate the gRPC payload.
		if maxExportedFileSize > 0 && int64(len(f.Content)) > maxExportedFileSize {
			return nil, &ExportScanError{
				Code: CodeExportFileScanSizeExceeded,
				Path: f.Path,
			}
		}
		scanned, err := scanExportFile(ctx, c, tenantID, sessionID, f)
		if err != nil {
			return nil, err
		}
		// §7.4 line 446: stamp the post-pipeline Hash so the
		// child-delivery step can re-verify the bytes against this
		// reference. MODIFY may have rewritten Content; the stamped
		// Hash reflects the final bytes regardless. F-7.4.10.
		scanned.Hash = ComputeExportFileHash(scanned.Content)
		out = append(out, scanned)
	}
	return out, nil
}

// scanExportFile runs one exported file through the
// PreExportMaterialization chain and applies the decision.
func scanExportFile(ctx context.Context, c *Chain, tenantID, sessionID string, f ExportFile) (ExportFile, error) {
	sum := sha256.Sum256(f.Content)
	record := exportFileRecord{
		FilePath:          f.Path,
		FileSize:          uint64(len(f.Content)),
		SHA256:            hex.EncodeToString(sum[:]),
		ContentBytes:      f.Content,
		DelegationContext: f.DelegationContext,
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return ExportFile{}, fmt.Errorf("interceptor: marshal exported-file record for %q: %w", f.Path, err)
	}

	res := c.Run(ctx, Request{
		Phase:     PhasePreExportMaterialization,
		SessionID: sessionID,
		TenantID:  tenantID,
		Content:   payload,
	})

	switch res.Action {
	case ActionReject:
		return ExportFile{}, &ExportScanError{
			Code:   CodeExportFileScanRejected,
			Path:   f.Path,
			Reason: res.Reason,
		}
	case ActionModify:
		modified, err := applyExportModify(f, res.ModifiedContent)
		if err != nil {
			return ExportFile{}, err
		}
		return modified, nil
	default: // ActionAllow
		return f, nil
	}
}

// applyExportModify applies a MODIFY decision to an exported file. The
// interceptor returns the full §4.8 exported-file record; only
// content_bytes is honored. file_path and delegation_context are
// immutable — a mismatch is an INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION per
// §4.8. file_size and sha256 are not trusted from the interceptor: the
// gateway re-derives the file's sha256 from the returned bytes.
func applyExportModify(orig ExportFile, modifiedContent []byte) (ExportFile, error) {
	var record exportFileRecord
	if err := json.Unmarshal(modifiedContent, &record); err != nil {
		return ExportFile{}, fmt.Errorf("%s: exported file %q: interceptor returned a malformed exported-file record: %w",
			CodeInterceptorImmutableFieldViolation, orig.Path, err)
	}
	if record.FilePath != orig.Path {
		return ExportFile{}, &ExportScanError{
			Code:   CodeInterceptorImmutableFieldViolation,
			Path:   orig.Path,
			Reason: fmt.Sprintf("interceptor altered the immutable file_path to %q", record.FilePath),
		}
	}
	if record.DelegationContext != orig.DelegationContext {
		return ExportFile{}, &ExportScanError{
			Code:   CodeInterceptorImmutableFieldViolation,
			Path:   orig.Path,
			Reason: "interceptor altered the immutable delegation_context",
		}
	}
	return ExportFile{
		Path:              orig.Path,
		Content:           record.ContentBytes,
		DelegationContext: orig.DelegationContext,
	}, nil
}
