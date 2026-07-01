// SPDX-License-Identifier: MIT

package interceptor_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
)

// spec: §7.4 line 446 — Mandatory for delegation file exports (the
// gateway computes and verifies hashes during the export-to-child
// flow). F-7.4.10.

// ComputeExportFileHash returns the same hex SHA-256 the
// interceptor pipeline stamps onto every output ExportFile.
func TestComputeExportFileHashIsLowercaseHex(t *testing.T) {
	got := interceptor.ComputeExportFileHash([]byte("hello world"))
	const want = "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"
	if got != want {
		t.Fatalf("hash = %q, want %q (the §4.5 line 311 well-known value)", got, want)
	}
}

// VerifyExportFileHash accepts a matching expected value.
func TestVerifyExportFileHashMatch(t *testing.T) {
	content := []byte("file body")
	expected := interceptor.ComputeExportFileHash(content)
	if err := interceptor.VerifyExportFileHash("lib/x.go", content, expected); err != nil {
		t.Fatalf("match should return nil, got %v", err)
	}
}

// VerifyExportFileHash skips the check when expected is empty
// (the optional client-uploads variant — F-7.4.10).
func TestVerifyExportFileHashEmptyExpectedSkips(t *testing.T) {
	if err := interceptor.VerifyExportFileHash("a", []byte("x"), ""); err != nil {
		t.Errorf("empty expected should skip the check, got %v", err)
	}
}

// VerifyExportFileHash rejects on mismatch with
// CodeExportFileHashMismatch.
func TestVerifyExportFileHashMismatch(t *testing.T) {
	err := interceptor.VerifyExportFileHash("lib/x.go", []byte("file body"),
		"0000000000000000000000000000000000000000000000000000000000000000")
	var scan *interceptor.ExportScanError
	if !errors.As(err, &scan) {
		t.Fatalf("mismatch should return *ExportScanError, got %T: %v", err, err)
	}
	if scan.Code != interceptor.CodeExportFileHashMismatch {
		t.Errorf("scan.Code = %q, want %q", scan.Code, interceptor.CodeExportFileHashMismatch)
	}
	if scan.Path != "lib/x.go" {
		t.Errorf("scan.Path = %q, want lib/x.go", scan.Path)
	}
	if !strings.Contains(scan.Reason, "expected") || !strings.Contains(scan.Reason, "actual") {
		t.Errorf("scan.Reason = %q, want both expected and actual hex", scan.Reason)
	}
}

// VerifyExportFileHash is case-insensitive over the hex alphabet
// (uppercase/lowercase hex are equivalent per §4.5 line 311).
func TestVerifyExportFileHashCaseInsensitive(t *testing.T) {
	content := []byte("hello world")
	expected := strings.ToUpper(interceptor.ComputeExportFileHash(content))
	if err := interceptor.VerifyExportFileHash("a", content, expected); err != nil {
		t.Errorf("uppercase hex should still verify, got %v", err)
	}
}

// RunPreExportMaterialization verifies inbound Hash before the
// interceptor call: a tampered byte slice with an unchanged stamped
// hash surfaces a hash-mismatch error without ever reaching the
// interceptor chain. F-7.4.10.
func TestRunPreExportMaterializationVerifiesInboundHash(t *testing.T) {
	c := interceptor.NewChain()
	calls := 0
	mustRegister(t, c, interceptor.PhasePreExportMaterialization, &fakeInterceptor{
		name: "scanner", priority: 500, builtin: true,
		fn: func(_ context.Context, _ interceptor.Request) (interceptor.Result, error) {
			calls++
			return interceptor.Result{Action: interceptor.ActionAllow}, nil
		},
	})

	original := []byte("file body")
	stamped := interceptor.ComputeExportFileHash(original)
	tampered := []byte("tampered")

	_, err := interceptor.RunPreExportMaterialization(
		context.Background(), c, interceptor.ExportScanContext{}, "acme", "sess-1", 0,
		interceptor.ExportFile{Path: "lib/x.go", Content: tampered, Hash: stamped},
	)
	var scan *interceptor.ExportScanError
	if !errors.As(err, &scan) || scan.Code != interceptor.CodeExportFileHashMismatch {
		t.Fatalf("tampered bytes should fail with %s, got %v", interceptor.CodeExportFileHashMismatch, err)
	}
	if calls != 0 {
		t.Errorf("interceptor was called %d times; hash mismatch must short-circuit before the scan", calls)
	}
}

// RunPreExportMaterialization admits a file whose inbound Hash
// matches the bytes, and stamps an equal Hash on the output.
func TestRunPreExportMaterializationStampsOutputHash(t *testing.T) {
	c := interceptor.NewChain()
	mustRegister(t, c, interceptor.PhasePreExportMaterialization, &fakeInterceptor{
		name: "scanner", priority: 500, builtin: true,
		fn: func(_ context.Context, _ interceptor.Request) (interceptor.Result, error) {
			return interceptor.Result{Action: interceptor.ActionAllow}, nil
		},
	})

	content := []byte("file body")
	in := interceptor.ExportFile{
		Path:    "lib/x.go",
		Content: content,
		Hash:    interceptor.ComputeExportFileHash(content),
	}
	out, err := interceptor.RunPreExportMaterialization(
		context.Background(), c, interceptor.ExportScanContext{}, "acme", "sess-1", 0, in,
	)
	if err != nil {
		t.Fatalf("RunPreExportMaterialization: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("returned %d files, want 1", len(out))
	}
	if out[0].Hash != interceptor.ComputeExportFileHash(content) {
		t.Errorf("output Hash = %q, want %q", out[0].Hash, interceptor.ComputeExportFileHash(content))
	}
}

// RunPreExportMaterialization re-derives Hash after a MODIFY so the
// stamped value reflects the post-MODIFY bytes the gateway will
// persist into the child workspace. F-7.4.10.
func TestRunPreExportMaterializationModifyRewritesHash(t *testing.T) {
	c := interceptor.NewChain()
	// Modify rewrites the content to "redacted".
	const newBody = "redacted"
	mustRegister(t, c, interceptor.PhasePreExportMaterialization, &fakeInterceptor{
		name: "redactor", priority: 500, builtin: true,
		fn: func(_ context.Context, req interceptor.Request) (interceptor.Result, error) {
			record := decodeExportRecord(t, req.Content)
			record["content_bytes"] = []byte(newBody)
			payload, err := json.Marshal(record)
			if err != nil {
				return interceptor.Result{}, err
			}
			return interceptor.Result{
				Action:          interceptor.ActionModify,
				ModifiedContent: payload,
			}, nil
		},
	})

	in := interceptor.ExportFile{
		Path:    "lib/x.go",
		Content: []byte("secret-original"),
		Hash:    interceptor.ComputeExportFileHash([]byte("secret-original")),
	}
	out, err := interceptor.RunPreExportMaterialization(
		context.Background(), c, interceptor.ExportScanContext{}, "acme", "sess-1", 0, in,
	)
	if err != nil {
		t.Fatalf("RunPreExportMaterialization: %v", err)
	}
	if string(out[0].Content) != newBody {
		t.Fatalf("Content = %q, want %q", out[0].Content, newBody)
	}
	if out[0].Hash != interceptor.ComputeExportFileHash([]byte(newBody)) {
		t.Errorf("post-MODIFY Hash = %q, want %q (must reflect rewritten bytes)",
			out[0].Hash, interceptor.ComputeExportFileHash([]byte(newBody)))
	}
}

// RunPreExportMaterialization with an empty inbound Hash still
// stamps the output: callers that haven't yet computed the hash
// (e.g., older paths) get a usable post-pipeline reference.
func TestRunPreExportMaterializationStampsHashEvenWithoutInbound(t *testing.T) {
	c := interceptor.NewChain()
	mustRegister(t, c, interceptor.PhasePreExportMaterialization, &fakeInterceptor{
		name: "scanner", priority: 500, builtin: true,
		fn: func(_ context.Context, _ interceptor.Request) (interceptor.Result, error) {
			return interceptor.Result{Action: interceptor.ActionAllow}, nil
		},
	})

	content := []byte("xyz")
	out, err := interceptor.RunPreExportMaterialization(
		context.Background(), c, interceptor.ExportScanContext{}, "acme", "sess-1", 0,
		interceptor.ExportFile{Path: "a", Content: content},
	)
	if err != nil {
		t.Fatalf("RunPreExportMaterialization: %v", err)
	}
	if out[0].Hash != interceptor.ComputeExportFileHash(content) {
		t.Errorf("stamped Hash = %q, want %q", out[0].Hash, interceptor.ComputeExportFileHash(content))
	}
}
