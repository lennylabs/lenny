// SPDX-License-Identifier: MIT

package export_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/delegation/export"
	"github.com/lennylabs/lenny/pkg/gateway/delegation/fileexport"
	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// fakeExporter returns canned files per source glob, recording the
// specs it was asked to export so a test can assert the §8.7 declared
// order and per-spec call discipline.
type fakeExporter struct {
	bySource map[string][]export.ExportedFile
	calls    []export.Spec
	err      error
}

func (f *fakeExporter) ExportPaths(_ context.Context, _ string, spec export.Spec) ([]export.ExportedFile, error) {
	f.calls = append(f.calls, spec)
	if f.err != nil {
		return nil, f.err
	}
	return f.bySource[spec.Source], nil
}

// fakeSink records every persisted file and returns a deterministic
// blob ref so a test can assert the §8.2-step-4 durable hand-off.
type fakeSink struct {
	persisted []export.ExportedFile
	err       error
}

func (s *fakeSink) Persist(_ context.Context, child string, fl export.ExportedFile) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	s.persisted = append(s.persisted, fl)
	return "blob://" + child + "/" + fl.Path, nil
}

// fakeAuditor records the §8.7 line 793 overwrite events.
type fakeAuditor struct {
	events []auditEvent
}

type auditEvent struct {
	typ    string
	detail map[string]any
}

func (a *fakeAuditor) EmitDelegationEvent(_ context.Context, typ string, detail map[string]any) {
	a.events = append(a.events, auditEvent{typ: typ, detail: detail})
}

// scanInterceptor is a minimal interceptor.Interceptor double for the
// PreExportMaterialization scan tests.
type scanInterceptor struct {
	fn func(ctx context.Context, req interceptor.Request) (interceptor.Result, error)
}

func (scanInterceptor) Name() string                       { return "scan-double" }
func (scanInterceptor) Priority() int32                    { return 500 }
func (scanInterceptor) Builtin() bool                      { return true }
func (scanInterceptor) FailPolicy() interceptor.FailPolicy { return interceptor.FailClosed }
func (scanInterceptor) Timeout() time.Duration             { return 0 }
func (s scanInterceptor) Intercept(ctx context.Context, req interceptor.Request) (interceptor.Result, error) {
	return s.fn(ctx, req)
}

func file(p, content string) export.ExportedFile {
	return export.ExportedFile{Path: p, Content: []byte(content), Size: int64(len(content))}
}

func newMat(exp export.ParentExporter, sink export.Sink, aud export.Auditor) *export.Materializer {
	return export.NewMaterializer(exp, sink, aud)
}

// TestMaterializeRebasesAndPersists_spec_8_7 covers the happy path: two
// distinct-path specs produce two uploadFile child sources, the durable
// sink is called once per file, and the accounting sums the collapsed
// set. spec: §8.7; §8.2 lines 91-95.
func TestMaterializeRebasesAndPersists_spec_8_7(t *testing.T) {
	exp := &fakeExporter{bySource: map[string][]export.ExportedFile{
		"./src/*":      {file("src/auth.ts", "auth")},
		"./cfg/*.json": {file("cfg/app.json", "{}")},
	}}
	sink := &fakeSink{}
	res, err := newMat(exp, sink, nil).Materialize(context.Background(), export.Params{
		ParentSessionID: "parent",
		ChildSessionID:  "child",
		TenantID:        "acme",
		Specs: []export.Spec{
			{Source: "./src/*", DestPrefix: "src/"},
			{Source: "./cfg/*.json", DestPrefix: ""},
		},
		Limits: fileexport.DefaultFileExportLimits,
	})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if res.FileCount != 2 || res.TotalBytes != int64(len("auth")+len("{}")) {
		t.Fatalf("count=%d bytes=%d, want 2 / %d", res.FileCount, res.TotalBytes, len("auth")+len("{}"))
	}
	if len(res.Sources) != 2 {
		t.Fatalf("sources=%d, want 2", len(res.Sources))
	}
	if len(sink.persisted) != 2 {
		t.Fatalf("persisted=%d, want 2", len(sink.persisted))
	}
	// Per-spec, declared-order export discipline (§8.7 line 774).
	if len(exp.calls) != 2 || exp.calls[0].Source != "./src/*" || exp.calls[1].Source != "./cfg/*.json" {
		t.Fatalf("export calls = %+v, want the two specs in declared order", exp.calls)
	}
	uf, ok := res.Sources[0].Variant.(workspaceplan.UploadFile)
	if !ok || uf.PathField != "src/auth.ts" || uf.UploadRef == "" {
		t.Fatalf("source[0] = %+v, want uploadFile at src/auth.ts with a ref", res.Sources[0])
	}
}

// TestMaterializeRejectsBadDestPrefix_spec_8_7_789 asserts a destPrefix
// with a `..` segment is rejected before any export RPC. spec: §8.7 line
// 789.
func TestMaterializeRejectsBadDestPrefix_spec_8_7_789(t *testing.T) {
	exp := &fakeExporter{}
	_, err := newMat(exp, &fakeSink{}, nil).Materialize(context.Background(), export.Params{
		Specs:  []export.Spec{{Source: "./a/*", DestPrefix: "../escape/"}},
		Limits: fileexport.DefaultFileExportLimits,
	})
	if !errors.Is(err, fileexport.ErrDestPrefixParentSegment) {
		t.Fatalf("err = %v, want ErrDestPrefixParentSegment", err)
	}
	if len(exp.calls) != 0 {
		t.Fatalf("export was called %d times, want 0 (destPrefix rejected first)", len(exp.calls))
	}
}

// TestMaterializeEnforcesFileCount_spec_8_7_790 asserts the fileExportLimits
// count ceiling. spec: §8.7 line 790.
func TestMaterializeEnforcesFileCount_spec_8_7_790(t *testing.T) {
	exp := &fakeExporter{bySource: map[string][]export.ExportedFile{
		"./*": {file("a.txt", "a"), file("b.txt", "b")},
	}}
	sink := &fakeSink{}
	_, err := newMat(exp, sink, nil).Materialize(context.Background(), export.Params{
		Specs:  []export.Spec{{Source: "./*"}},
		Limits: fileexport.FileExportLimits{MaxFiles: 1},
	})
	if !errors.Is(err, fileexport.ErrTooManyFiles) {
		t.Fatalf("err = %v, want ErrTooManyFiles", err)
	}
	if len(sink.persisted) != 0 {
		t.Fatalf("persisted %d files, want 0 (limit fails before persistence)", len(sink.persisted))
	}
}

// TestMaterializeEnforcesTotalSize_spec_8_7_790 asserts the aggregate-size
// ceiling. spec: §8.7 lines 790-791.
func TestMaterializeEnforcesTotalSize_spec_8_7_790(t *testing.T) {
	exp := &fakeExporter{bySource: map[string][]export.ExportedFile{
		"./*": {file("big.txt", "0123456789")},
	}}
	_, err := newMat(exp, &fakeSink{}, nil).Materialize(context.Background(), export.Params{
		Specs:  []export.Spec{{Source: "./*"}},
		Limits: fileexport.FileExportLimits{MaxTotalSize: 5},
	})
	if !errors.Is(err, fileexport.ErrTotalSizeExceeded) {
		t.Fatalf("err = %v, want ErrTotalSizeExceeded", err)
	}
}

// TestMaterializeDetectsOverwrite_spec_8_7_793 asserts a later export
// entry overwriting an earlier child path is collapsed (last-write-wins),
// recorded, and audited. spec: §8.7 lines 774, 793.
func TestMaterializeDetectsOverwrite_spec_8_7_793(t *testing.T) {
	exp := &fakeExporter{bySource: map[string][]export.ExportedFile{
		"./first/config.json":  {file("config.json", "FIRST")},
		"./second/config.json": {file("config.json", "SECOND")},
	}}
	sink := &fakeSink{}
	aud := &fakeAuditor{}
	res, err := newMat(exp, sink, aud).Materialize(context.Background(), export.Params{
		ParentSessionID: "parent",
		ChildSessionID:  "child",
		TenantID:        "acme",
		Specs: []export.Spec{
			{Source: "./first/config.json"},
			{Source: "./second/config.json"},
		},
		Limits: fileexport.DefaultFileExportLimits,
	})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if res.FileCount != 1 || len(res.Sources) != 1 {
		t.Fatalf("count=%d sources=%d, want 1/1 after collapse", res.FileCount, len(res.Sources))
	}
	if len(res.Overwrites) != 1 || res.Overwrites[0].Path != "config.json" || res.Overwrites[0].SpecIndex != 1 {
		t.Fatalf("overwrites = %+v, want one at config.json from spec index 1", res.Overwrites)
	}
	// Last writer wins: the persisted content is the second spec's.
	if len(sink.persisted) != 1 || string(sink.persisted[0].Content) != "SECOND" {
		t.Fatalf("persisted = %+v, want one file with SECOND content", sink.persisted)
	}
	if len(aud.events) != 1 || aud.events[0].typ != export.EventExportOverwrite {
		t.Fatalf("audit events = %+v, want one %s", aud.events, export.EventExportOverwrite)
	}
	if got := aud.events[0].detail["overwritten_path"]; got != "config.json" {
		t.Errorf("audit overwritten_path = %v, want config.json", got)
	}
}

// TestMaterializeRoutesArchiveAsUploadArchive_spec_8_7_792 asserts an
// exported archive becomes an uploadArchive child source so the child
// materialization inherits the §13.4 / §7.4 upload archive validators,
// while a plain file stays an uploadFile. spec: §8.7 line 792.
func TestMaterializeRoutesArchiveAsUploadArchive_spec_8_7_792(t *testing.T) {
	exp := &fakeExporter{bySource: map[string][]export.ExportedFile{
		"./*": {file("input/bundle.tar.gz", "ARCHIVE"), file("notes.txt", "hi")},
	}}
	res, err := newMat(exp, &fakeSink{}, nil).Materialize(context.Background(), export.Params{
		ChildSessionID: "child",
		Specs:          []export.Spec{{Source: "./*"}},
		Limits:         fileexport.DefaultFileExportLimits,
	})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	var ua workspaceplan.UploadArchive
	var foundArchive, foundFile bool
	for _, s := range res.Sources {
		switch v := s.Variant.(type) {
		case workspaceplan.UploadArchive:
			ua, foundArchive = v, true
		case workspaceplan.UploadFile:
			foundFile = true
		}
	}
	if !foundArchive || !foundFile {
		t.Fatalf("sources = %+v, want one uploadArchive and one uploadFile", res.Sources)
	}
	if ua.Format != "tar.gz" || ua.PathPrefix != "input" {
		t.Errorf("uploadArchive = %+v, want format tar.gz under input/", ua)
	}
}

// TestMaterializeScanRejectFailsClosed_spec_8_7 asserts a REJECT from the
// PreExportMaterialization scan fails the whole export and persists
// nothing. spec: §8.7 (contentPolicy.scanExportedFiles).
func TestMaterializeScanRejectFailsClosed_spec_8_7(t *testing.T) {
	c := interceptor.NewChain()
	if err := c.Register(interceptor.PhasePreExportMaterialization, scanInterceptor{
		fn: func(_ context.Context, _ interceptor.Request) (interceptor.Result, error) {
			return interceptor.Result{Action: interceptor.ActionReject, Reason: "secret detected"}, nil
		},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	exp := &fakeExporter{bySource: map[string][]export.ExportedFile{"./*": {file("a.txt", "x")}}}
	sink := &fakeSink{}
	_, err := newMat(exp, sink, nil).Materialize(context.Background(), export.Params{
		ChildSessionID: "child",
		Specs:          []export.Spec{{Source: "./*"}},
		Limits:         fileexport.DefaultFileExportLimits,
		Scan:           export.ContentScan{Enabled: true, Chain: c},
	})
	var scanErr *interceptor.ExportScanError
	if !errors.As(err, &scanErr) || scanErr.Code != interceptor.CodeExportFileScanRejected {
		t.Fatalf("err = %v, want EXPORT_FILE_SCAN_REJECTED", err)
	}
	if len(sink.persisted) != 0 {
		t.Fatalf("persisted %d files, want 0 (REJECT rolls the export back)", len(sink.persisted))
	}
}

// TestMaterializeScanModifyPersistsRewritten_spec_8_7 asserts a MODIFY
// from the scan is the content persisted into the child workspace.
func TestMaterializeScanModifyPersistsRewritten_spec_8_7(t *testing.T) {
	c := interceptor.NewChain()
	if err := c.Register(interceptor.PhasePreExportMaterialization, scanInterceptor{
		fn: func(_ context.Context, req interceptor.Request) (interceptor.Result, error) {
			var rec map[string]any
			if err := json.Unmarshal(req.Content, &rec); err != nil {
				return interceptor.Result{}, err
			}
			rec["content_bytes"] = []byte("REDACTED")
			next, _ := json.Marshal(rec)
			return interceptor.Result{Action: interceptor.ActionModify, ModifiedContent: next}, nil
		},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	exp := &fakeExporter{bySource: map[string][]export.ExportedFile{"./*": {file("a.txt", "api-key=sk-live")}}}
	sink := &fakeSink{}
	_, err := newMat(exp, sink, nil).Materialize(context.Background(), export.Params{
		ChildSessionID: "child",
		Specs:          []export.Spec{{Source: "./*"}},
		Limits:         fileexport.DefaultFileExportLimits,
		Scan:           export.ContentScan{Enabled: true, Chain: c},
	})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if len(sink.persisted) != 1 || string(sink.persisted[0].Content) != "REDACTED" {
		t.Fatalf("persisted = %+v, want the MODIFY-rewritten REDACTED bytes", sink.persisted)
	}
}

// TestMaterializeScanEnabledWithoutChainFailsClosed_spec_8_7 asserts a
// scanExportedFiles:true with no resolved interceptor chain is a
// fail-closed configuration error, not a silent skip.
func TestMaterializeScanEnabledWithoutChainFailsClosed_spec_8_7(t *testing.T) {
	exp := &fakeExporter{bySource: map[string][]export.ExportedFile{"./*": {file("a.txt", "x")}}}
	_, err := newMat(exp, &fakeSink{}, nil).Materialize(context.Background(), export.Params{
		Specs:  []export.Spec{{Source: "./*"}},
		Limits: fileexport.DefaultFileExportLimits,
		Scan:   export.ContentScan{Enabled: true, Chain: nil},
	})
	if err == nil {
		t.Fatal("want a fail-closed error for scanExportedFiles:true with no chain")
	}
}

// TestMaterializeRequiresExporterAndSink asserts the misconfiguration
// guard.
func TestMaterializeRequiresExporterAndSink(t *testing.T) {
	if _, err := export.NewMaterializer(nil, &fakeSink{}, nil).Materialize(context.Background(), export.Params{}); err == nil {
		t.Error("nil exporter should error")
	}
	if _, err := export.NewMaterializer(&fakeExporter{}, nil, nil).Materialize(context.Background(), export.Params{}); err == nil {
		t.Error("nil sink should error")
	}
}

// TestMaterializeEmptySpecs asserts the empty export is a no-op success.
func TestMaterializeEmptySpecs(t *testing.T) {
	res, err := newMat(&fakeExporter{}, &fakeSink{}, nil).Materialize(context.Background(), export.Params{
		Limits: fileexport.DefaultFileExportLimits,
	})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if res.FileCount != 0 || len(res.Sources) != 0 {
		t.Fatalf("res = %+v, want an empty result", res)
	}
}
