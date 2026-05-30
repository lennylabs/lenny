// SPDX-License-Identifier: MIT

package interceptor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/lennylabs/lenny/pkg/observability/tracing"
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

	// CodeExportFileScanUnavailable is the §15.1 line 1073 error code
	// returned when a PreExportMaterialization interceptor call timed out
	// or returned a gRPC error under a fail-closed FailPolicy. The §15.1
	// classifier marks it TRANSIENT (HTTP 503): the underlying scanner is
	// not reachable, retry is allowed under the standard Retry-After
	// conventions. Under fail-open the same timeout/error conditions
	// admit the file and emit delegation.export_scan_failed_open
	// instead — that branch returns no error. Callers stamp
	// details.{filePath, interceptorRef, reason} on the §15.1 envelope
	// from the *ExportScanError this package returns. F-8.7.8.
	// spec: §15.1 line 1073; §8.3 rule 3 line 164; §4.8 line 1038
	CodeExportFileScanUnavailable = "EXPORT_FILE_SCAN_UNAVAILABLE"

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

// ExportScanOutcome is the §16.1 line 80 lenny_export_file_scans_total
// `outcome` label for one scanned exported file. The values match the
// spec enum admitted | modified | rejected | failed_open | failed_closed.
type ExportScanOutcome string

const (
	// OutcomeAdmitted: the interceptor ALLOWed the file unchanged.
	OutcomeAdmitted ExportScanOutcome = "admitted"
	// OutcomeModified: the interceptor MODIFYed the file's content.
	OutcomeModified ExportScanOutcome = "modified"
	// OutcomeRejected: the interceptor REJECTed the file (a deliberate
	// policy decision; §15.1 EXPORT_FILE_SCAN_REJECTED).
	OutcomeRejected ExportScanOutcome = "rejected"
	// OutcomeFailedOpen: an interceptor errored or timed out under
	// fail-open and the file was admitted without inspection (§8.7
	// rule 3).
	OutcomeFailedOpen ExportScanOutcome = "failed_open"
	// OutcomeFailedClosed: an interceptor errored or timed out under
	// fail-closed and the file was rejected (§15.1
	// EXPORT_FILE_SCAN_UNAVAILABLE).
	OutcomeFailedClosed ExportScanOutcome = "failed_closed"
)

// ExportScanEvent is one scanned exported file's terminal outcome,
// handed to an ExportScanObserver after the PreExportMaterialization
// decision. The size pre-gate rejection (EXPORT_FILE_SCAN_SIZE_EXCEEDED)
// fires before the file enters the chain and is therefore not a scan
// outcome: the §16.1 counter increments "once per scanned file", so a
// size-rejected file produces no ExportScanEvent.
type ExportScanEvent struct {
	// Pool, TenantID, PolicyName, InterceptorRef are the §16.1 line 80
	// lenny_export_file_scans_total label set (TenantID comes from the
	// RunPreExportMaterialization call, the rest from ExportScanContext).
	Pool           string
	TenantID       string
	SessionID      string
	PolicyName     string
	InterceptorRef string
	// FilePath and FileSize are the §11.7 lines 120-121 audit fields:
	// the exported file's child-workspace-relative path and its byte
	// length as exported (pre-MODIFY).
	FilePath string
	FileSize uint64
	// Outcome is the §16.1 metric outcome label.
	Outcome ExportScanOutcome
	// Reason carries the §11.7 line 122 reason: the interceptor-provided
	// reason for OutcomeRejected (may be empty) or the timeout/grpc_error
	// token for OutcomeFailedOpen. Empty for admitted/modified.
	Reason string
	// Duration is the per-file interceptor latency for the §16.1 line 80
	// lenny_export_file_scan_duration_seconds histogram.
	Duration time.Duration
}

// ExportScanObserver receives one ExportScanEvent per scanned exported
// file. The gateway implements it to emit the §11.7 audit events
// (delegation.export_file_scan_rejected on a REJECT,
// delegation.export_scan_failed_open on a fail-open admit) and the
// §16.1 lenny_export_file_scans_total / _duration_seconds metrics. A nil
// observer disables emission; the scan still runs. This mirrors the
// FailOpenObserver seam so the audit/metric writers stay in the gateway
// policy layer and this package remains transport-agnostic.
type ExportScanObserver interface {
	ExportFileScanned(ctx context.Context, ev ExportScanEvent)
}

// ExportScanContext carries the per-delegation labels and the observer
// the export-scan loop stamps onto each ExportScanEvent. The zero value
// (nil Observer) makes RunPreExportMaterialization a pure scan with no
// emission, preserving its use as an independently-testable helper.
type ExportScanContext struct {
	// Pool is the §16.1 pool label (the SandboxTemplate name).
	Pool string
	// PolicyName is the §11.7 line 119 policy_name: the DelegationPolicy
	// whose contentPolicy owns the export.
	PolicyName string
	// InterceptorRef is the §11.7 / §16.1 interceptor_ref: the policy's
	// contentPolicy.interceptorRef that routes the per-file scan.
	InterceptorRef string
	// Observer receives one event per scanned file. Nil disables
	// emission.
	Observer ExportScanObserver
}

// emit hands one scanned file's outcome to the observer, if configured.
func (sc ExportScanContext) emit(ctx context.Context, tenantID, sessionID, filePath string, fileSize uint64, outcome ExportScanOutcome, reason string, dur time.Duration) {
	if sc.Observer == nil {
		return
	}
	sc.Observer.ExportFileScanned(ctx, ExportScanEvent{
		Pool:           sc.Pool,
		TenantID:       tenantID,
		SessionID:      sessionID,
		PolicyName:     sc.PolicyName,
		InterceptorRef: sc.InterceptorRef,
		FilePath:       filePath,
		FileSize:       fileSize,
		Outcome:        outcome,
		Reason:         reason,
		Duration:       dur,
	})
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
// registered chain, then persist the returned (post-MODIFY) files. The
// fileExport spec is validated first by the pkg/gateway/delegation/
// fileexport helpers (destPrefix, fileExportLimits, realpath
// containment). This function is the reusable hook that path will call;
// it is independently testable today.
//
// Pass an ExportScanContext carrying the §11.7 / §16.1 labels (pool,
// policy_name, interceptor_ref) and an ExportScanObserver to emit the
// per-file audit events and metrics; the zero ExportScanContext disables
// emission for a pure scan.
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
// REJECT (CodeExportFileScanUnavailable for a timeout/error). Under
// fail-open the Chain skips the interceptor and admits the file; the skip
// is surfaced on Result.FailOpenSkips so this loop reports the
// failed_open outcome and the ExportScanObserver emits
// delegation.export_scan_failed_open. F-8.7.9; F-8.7.10.
//
// On success it returns the files with any MODIFY transformations
// applied, in the input order. On the first REJECT it returns an
// *ExportScanError and the files scanned so far are discarded by the
// caller (the delegation is rolled back).
func RunPreExportMaterialization(ctx context.Context, c *Chain, sc ExportScanContext, tenantID, sessionID string, maxExportedFileSize int64, files ...ExportFile) ([]ExportFile, error) {
	// spec: §16.3 / §16 trace inventory (`delegation.export_files` span,
	// attributed to gateway + parent pod) — every per-file scan loop
	// runs under a span so distributed traces show the export
	// materialization phase. The span totals the input file count and
	// aggregate byte size; per-file decisions ride on child spans
	// emitted by Chain.Run inside scanExportFile. F-8.7.11.
	tracer := tracing.NewTracer(nil)
	var totalBytes int64
	for _, f := range files {
		totalBytes += int64(len(f.Content))
	}
	ctx, span := tracer.Start(ctx, tracing.SpanDelegationExportFiles)
	span.SetAttributes(
		attribute.Int("delegation.export.file_count", len(files)),
		attribute.Int64("delegation.export.total_bytes", totalBytes),
		attribute.Int64("delegation.export.max_per_file_bytes", maxExportedFileSize),
	)
	var spanErr error
	defer func() {
		tracing.RecordError(span, spanErr)
		span.End()
	}()
	out := make([]ExportFile, 0, len(files))
	for _, f := range files {
		fileSize := uint64(len(f.Content))
		// §7.4 line 446: when the caller stamped an inbound Hash on a
		// resolved ExportFile (the parent-export step's
		// ComputeExportFileHash output), verify the bytes have not
		// been tampered with before any interceptor call. F-7.4.10.
		if err := VerifyExportFileHash(f.Path, f.Content, f.Hash); err != nil {
			spanErr = err
			return nil, err
		}
		// §8.7 rule 2: the per-file ceiling is enforced before any
		// interceptor call so a single over-size file cannot stall the
		// interceptor or inflate the gRPC payload. This pre-gate fires
		// before the file enters the chain, so it is not a "scanned
		// file" and emits no §16.1 ExportScanEvent (it surfaces to the
		// caller as EXPORT_FILE_SCAN_SIZE_EXCEEDED instead). F-8.7.10.
		if maxExportedFileSize > 0 && int64(fileSize) > maxExportedFileSize {
			spanErr = &ExportScanError{
				Code: CodeExportFileScanSizeExceeded,
				Path: f.Path,
			}
			return nil, spanErr
		}
		start := time.Now()
		scanned, res, err := scanExportFile(ctx, c, tenantID, sessionID, f)
		dur := time.Since(start)
		if err != nil {
			// spec: §16.1 line 80 / §11.7 lines 69-70 — classify the
			// per-file rejection so the observer increments the right
			// outcome and emits delegation.export_file_scan_rejected for a
			// deliberate REJECT. A fail-closed scanner outage
			// (EXPORT_FILE_SCAN_UNAVAILABLE) is the failed_closed outcome
			// and has no dedicated §11.7 audit event (the §15.1 503 is its
			// signal). F-8.7.9; F-8.7.10.
			outcome, reason := OutcomeRejected, ""
			var ese *ExportScanError
			if errors.As(err, &ese) {
				reason = ese.Reason
				if ese.Code == CodeExportFileScanUnavailable {
					outcome = OutcomeFailedClosed
				}
			}
			sc.emit(ctx, tenantID, sessionID, f.Path, fileSize, outcome, reason, dur)
			spanErr = err
			return nil, err
		}
		// spec: §16.1 line 80 — a fail-open skip means the file was
		// admitted without inspection; report failed_open with the §11.7
		// line 122 reason token rather than the admitted/modified the
		// action alone would imply. F-8.7.9; F-8.7.10.
		outcome, reason := OutcomeAdmitted, ""
		switch {
		case len(res.FailOpenSkips) > 0:
			outcome = OutcomeFailedOpen
			reason = res.FailOpenSkips[0].Reason
		case res.Action == ActionModify:
			outcome = OutcomeModified
		}
		sc.emit(ctx, tenantID, sessionID, f.Path, fileSize, outcome, reason, dur)
		// §7.4 line 446: stamp the post-pipeline Hash so the
		// child-delivery step can re-verify the bytes against this
		// reference. MODIFY may have rewritten Content; the stamped
		// Hash reflects the final bytes regardless. F-7.4.10.
		scanned.Hash = ComputeExportFileHash(scanned.Content)
		out = append(out, scanned)
	}
	span.SetAttributes(attribute.Int("delegation.export.scanned_count", len(out)))
	return out, nil
}

// scanExportFile runs one exported file through the
// PreExportMaterialization chain and applies the decision. It returns
// the chain's Result alongside the (possibly MODIFYed) file so the
// caller can classify the §16.1 outcome from res.Action and
// res.FailOpenSkips without re-running the chain.
func scanExportFile(ctx context.Context, c *Chain, tenantID, sessionID string, f ExportFile) (ExportFile, Result, error) {
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
		return ExportFile{}, Result{}, fmt.Errorf("interceptor: marshal exported-file record for %q: %w", f.Path, err)
	}

	res := c.Run(ctx, Request{
		Phase:     PhasePreExportMaterialization,
		SessionID: sessionID,
		TenantID:  tenantID,
		Content:   payload,
	})

	switch res.Action {
	case ActionReject:
		// spec: §15.1 line 1073; §8.3 rule 3 line 164 — a fail-closed
		// scanner that timed out or errored surfaces from Chain.Run as
		// ActionReject with Code == CodeInterceptorTimeout. The §15.1
		// classifier treats that as TRANSIENT (scanner unavailable)
		// rather than as a deliberate policy REJECT (PERMANENT). Tag
		// the wrapper with CodeExportFileScanUnavailable in that case
		// so callers can distinguish a 503 from a 422. A REJECT with
		// an empty Code (or any non-timeout code) is a real policy
		// decision and stays mapped to CodeExportFileScanRejected.
		// F-8.7.7; F-8.7.8.
		code := CodeExportFileScanRejected
		if res.Code == CodeInterceptorTimeout {
			code = CodeExportFileScanUnavailable
		}
		return ExportFile{}, res, &ExportScanError{
			Code:   code,
			Path:   f.Path,
			Reason: res.Reason,
		}
	case ActionModify:
		modified, err := applyExportModify(f, res.ModifiedContent)
		if err != nil {
			return ExportFile{}, res, err
		}
		return modified, res, nil
	default: // ActionAllow
		return f, res, nil
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
