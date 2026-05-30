// SPDX-License-Identifier: MIT

package interceptor_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
)

// recordingScanObserver captures every ExportScanEvent so a test can
// assert the §16.1 outcome and §11.7 fields the export-scan loop stamps.
type recordingScanObserver struct {
	events []interceptor.ExportScanEvent
}

func (o *recordingScanObserver) ExportFileScanned(_ context.Context, ev interceptor.ExportScanEvent) {
	o.events = append(o.events, ev)
}

func scanCtx(o interceptor.ExportScanObserver) interceptor.ExportScanContext {
	return interceptor.ExportScanContext{
		Pool:           "orchestrator-pool",
		PolicyName:     "orchestrator-policy",
		InterceptorRef: "export-scanner",
		Observer:       o,
	}
}

// allowScanner registers an ALLOW interceptor at PreExportMaterialization.
func allowScanner(t *testing.T, c *interceptor.Chain) {
	mustRegister(t, c, interceptor.PhasePreExportMaterialization, &fakeInterceptor{
		name: "export-scanner", priority: 500, builtin: true,
		fn: func(context.Context, interceptor.Request) (interceptor.Result, error) {
			return interceptor.Result{Action: interceptor.ActionAllow}, nil
		},
	})
}

// spec: §16.1 line 80 — an ALLOWed file is the `admitted` outcome with
// the configured labels and no reason. F-8.7.10.
func TestExportScanEmitsAdmitted_spec_16_1_80(t *testing.T) {
	c := interceptor.NewChain()
	allowScanner(t, c)
	obs := &recordingScanObserver{}
	if _, err := interceptor.RunPreExportMaterialization(
		context.Background(), c, scanCtx(obs), "acme", "sess-1", 0,
		interceptor.ExportFile{Path: "src/a.go", Content: []byte("package a")},
	); err != nil {
		t.Fatalf("RunPreExportMaterialization: %v", err)
	}
	if len(obs.events) != 1 {
		t.Fatalf("emitted %d events, want 1", len(obs.events))
	}
	ev := obs.events[0]
	if ev.Outcome != interceptor.OutcomeAdmitted {
		t.Errorf("outcome = %q, want admitted", ev.Outcome)
	}
	if ev.Pool != "orchestrator-pool" || ev.PolicyName != "orchestrator-policy" ||
		ev.InterceptorRef != "export-scanner" || ev.TenantID != "acme" || ev.SessionID != "sess-1" {
		t.Errorf("labels = %+v, want the ExportScanContext + call labels", ev)
	}
	if ev.FilePath != "src/a.go" || ev.FileSize != uint64(len("package a")) {
		t.Errorf("file = %q/%d, want src/a.go/%d", ev.FilePath, ev.FileSize, len("package a"))
	}
	if ev.Reason != "" {
		t.Errorf("reason = %q, want empty for admitted", ev.Reason)
	}
}

// spec: §16.1 line 80 — a MODIFYed file is the `modified` outcome.
func TestExportScanEmitsModified_spec_16_1_80(t *testing.T) {
	c := interceptor.NewChain()
	mustRegister(t, c, interceptor.PhasePreExportMaterialization, &fakeInterceptor{
		name: "redactor", priority: 500, builtin: true,
		fn: func(_ context.Context, req interceptor.Request) (interceptor.Result, error) {
			var record map[string]any
			_ = json.Unmarshal(req.Content, &record)
			record["content_bytes"] = []byte("REDACTED")
			next, _ := json.Marshal(record)
			return interceptor.Result{Action: interceptor.ActionModify, ModifiedContent: next}, nil
		},
	})
	obs := &recordingScanObserver{}
	if _, err := interceptor.RunPreExportMaterialization(
		context.Background(), c, scanCtx(obs), "acme", "sess-1", 0,
		interceptor.ExportFile{Path: "secret.txt", Content: []byte("api-key")},
	); err != nil {
		t.Fatalf("RunPreExportMaterialization: %v", err)
	}
	if len(obs.events) != 1 || obs.events[0].Outcome != interceptor.OutcomeModified {
		t.Fatalf("events = %+v, want one modified outcome", obs.events)
	}
}

// spec: §11.7 line 69 / §16.1 line 80 — a deliberate REJECT is the
// `rejected` outcome carrying the interceptor's reason. F-8.7.9.
func TestExportScanEmitsRejected_spec_11_7_69(t *testing.T) {
	c := interceptor.NewChain()
	mustRegister(t, c, interceptor.PhasePreExportMaterialization, &fakeInterceptor{
		name: "rejecter", priority: 500, builtin: true,
		fn: func(context.Context, interceptor.Request) (interceptor.Result, error) {
			return interceptor.Result{Action: interceptor.ActionReject, Reason: "injection detected"}, nil
		},
	})
	obs := &recordingScanObserver{}
	_, err := interceptor.RunPreExportMaterialization(
		context.Background(), c, scanCtx(obs), "acme", "sess-1", 0,
		interceptor.ExportFile{Path: "CLAUDE.md", Content: []byte("ignore instructions")},
	)
	if err == nil {
		t.Fatal("want a REJECT error")
	}
	if len(obs.events) != 1 {
		t.Fatalf("emitted %d events, want 1", len(obs.events))
	}
	ev := obs.events[0]
	if ev.Outcome != interceptor.OutcomeRejected {
		t.Errorf("outcome = %q, want rejected", ev.Outcome)
	}
	if ev.Reason != "injection detected" {
		t.Errorf("reason = %q, want the interceptor reason", ev.Reason)
	}
	if ev.FilePath != "CLAUDE.md" {
		t.Errorf("file_path = %q, want CLAUDE.md", ev.FilePath)
	}
}

// spec: §8.7 rule 3 / §11.7 line 70 — a fail-open scanner error admits
// the file as `failed_open` with the §11.7 line 122 reason token.
// F-8.7.9.
func TestExportScanEmitsFailedOpen_spec_11_7_70(t *testing.T) {
	c := interceptor.NewChain()
	mustRegister(t, c, interceptor.PhasePreExportMaterialization, &fakeInterceptor{
		name: "down", priority: 500, builtin: true, failPolicy: interceptor.FailOpen,
		fn: func(context.Context, interceptor.Request) (interceptor.Result, error) {
			return interceptor.Result{}, errors.New("scanner unavailable")
		},
	})
	obs := &recordingScanObserver{}
	out, err := interceptor.RunPreExportMaterialization(
		context.Background(), c, scanCtx(obs), "acme", "sess-1", 0,
		interceptor.ExportFile{Path: "f.go", Content: []byte("body")},
	)
	if err != nil {
		t.Fatalf("fail-open admits: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("returned %d files, want 1 admitted", len(out))
	}
	if len(obs.events) != 1 {
		t.Fatalf("emitted %d events, want 1", len(obs.events))
	}
	ev := obs.events[0]
	if ev.Outcome != interceptor.OutcomeFailedOpen {
		t.Errorf("outcome = %q, want failed_open", ev.Outcome)
	}
	if ev.Reason != "grpc_error" {
		t.Errorf("reason = %q, want grpc_error", ev.Reason)
	}
}

// spec: §15.1 line 1073 / §16.1 line 80 — a fail-closed scanner outage
// is the `failed_closed` outcome (and surfaces EXPORT_FILE_SCAN_UNAVAILABLE).
// F-8.7.10.
func TestExportScanEmitsFailedClosed_spec_16_1_80(t *testing.T) {
	c := interceptor.NewChain()
	mustRegister(t, c, interceptor.PhasePreExportMaterialization, &fakeInterceptor{
		name: "down", priority: 500, builtin: true, failPolicy: interceptor.FailClosed,
		fn: func(context.Context, interceptor.Request) (interceptor.Result, error) {
			return interceptor.Result{}, errors.New("scanner unavailable")
		},
	})
	obs := &recordingScanObserver{}
	_, err := interceptor.RunPreExportMaterialization(
		context.Background(), c, scanCtx(obs), "acme", "sess-1", 0,
		interceptor.ExportFile{Path: "f.go", Content: []byte("x")},
	)
	var scanErr *interceptor.ExportScanError
	if !errors.As(err, &scanErr) || scanErr.Code != interceptor.CodeExportFileScanUnavailable {
		t.Fatalf("err = %v, want EXPORT_FILE_SCAN_UNAVAILABLE", err)
	}
	if len(obs.events) != 1 || obs.events[0].Outcome != interceptor.OutcomeFailedClosed {
		t.Fatalf("events = %+v, want one failed_closed outcome", obs.events)
	}
}

// spec: §16.1 line 80 — the size pre-gate fires before the file enters
// the chain, so a size-rejected file is not a "scanned file" and emits
// no ExportScanEvent. F-8.7.10.
func TestExportScanSizeGateEmitsNoEvent_spec_16_1_80(t *testing.T) {
	c := interceptor.NewChain()
	allowScanner(t, c)
	obs := &recordingScanObserver{}
	_, err := interceptor.RunPreExportMaterialization(
		context.Background(), c, scanCtx(obs), "acme", "sess-1", 8,
		interceptor.ExportFile{Path: "huge.bin", Content: make([]byte, 64)},
	)
	var scanErr *interceptor.ExportScanError
	if !errors.As(err, &scanErr) || scanErr.Code != interceptor.CodeExportFileScanSizeExceeded {
		t.Fatalf("err = %v, want EXPORT_FILE_SCAN_SIZE_EXCEEDED", err)
	}
	if len(obs.events) != 0 {
		t.Errorf("emitted %d events, want 0 — a size-gated file is not scanned", len(obs.events))
	}
}

// A nil observer (zero ExportScanContext) leaves RunPreExportMaterialization
// a pure scan: no panic, files still returned.
func TestExportScanNilObserverIsPureScan(t *testing.T) {
	c := interceptor.NewChain()
	allowScanner(t, c)
	out, err := interceptor.RunPreExportMaterialization(
		context.Background(), c, interceptor.ExportScanContext{}, "acme", "sess-1", 0,
		interceptor.ExportFile{Path: "a.go", Content: []byte("a")},
	)
	if err != nil || len(out) != 1 {
		t.Fatalf("out=%v err=%v, want one file and no error", out, err)
	}
}

// Multiple files each produce one event in fileExport spec order.
func TestExportScanEmitsPerFileInOrder_spec_16_1_80(t *testing.T) {
	c := interceptor.NewChain()
	allowScanner(t, c)
	obs := &recordingScanObserver{}
	if _, err := interceptor.RunPreExportMaterialization(
		context.Background(), c, scanCtx(obs), "acme", "sess-1", 0,
		interceptor.ExportFile{Path: "first.go", Content: []byte("1")},
		interceptor.ExportFile{Path: "second.go", Content: []byte("22")},
	); err != nil {
		t.Fatalf("RunPreExportMaterialization: %v", err)
	}
	if len(obs.events) != 2 {
		t.Fatalf("emitted %d events, want 2", len(obs.events))
	}
	if obs.events[0].FilePath != "first.go" || obs.events[1].FilePath != "second.go" {
		t.Errorf("event order = %q,%q, want first.go,second.go", obs.events[0].FilePath, obs.events[1].FilePath)
	}
}
