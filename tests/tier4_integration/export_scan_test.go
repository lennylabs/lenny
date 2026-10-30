// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §4.8 PreExportMaterialization per-exported-file
// content scan on the delegation file-export path, driven as a composed
// cross-component flow. A real parent-to-child delegation runs through the
// live delegation service under a DelegationPolicy whose
// contentPolicy.scanExportedFiles is true, the real §8.7 export Materializer
// pulls and persists files, and each exported file is scanned by a
// deployer-supplied interceptor reached over a real gRPC socket. The
// interceptor package's own unit tests exercise RunPreExportMaterialization
// in isolation with in-process fakes; nothing drove the whole path from
// Service.Delegate through the ExportScanChainResolver, the §8.7
// materializer, and the §4 external interceptor adapter across a live wire.
// This test asserts the phase fires once per exported file (admitted), that
// an over-size file is rejected with EXPORT_FILE_SCAN_SIZE_EXCEEDED before any
// interceptor call is made, and that an interceptor REJECT surfaces as
// EXPORT_FILE_SCAN_REJECTED.
package tier4_integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation/export"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationpolicystore"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	interceptorv1 "github.com/lennylabs/lenny/pkg/proto/interceptor/v1"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
	stubinterceptor "github.com/lennylabs/lenny/tests/testinfra/stubs/interceptor"
)

const (
	exportScanTenant = "acme"
	exportScanPolicy = "exportpol"
	exportScanRef    = "export-scanner"
	exportScanUser   = "alice@acme.com"
)

// exportScanExporter is a §8.2-step-3 ParentExporter that returns a fixed set
// of exported files for any Spec, standing in for the parent pod's ExportPaths
// RPC so the delegation flow reaches the per-file scan without a live pod.
type exportScanExporter struct{ files []export.ExportedFile }

func (e exportScanExporter) ExportPaths(_ context.Context, _ string, _ export.Spec) ([]export.ExportedFile, error) {
	return e.files, nil
}

// exportScanSink is a §8.2-step-4 Sink that returns a deterministic uploadRef
// without a durable blob store.
type exportScanSink struct{}

func (exportScanSink) Persist(_ context.Context, tenantID, child string, f export.ExportedFile) (string, error) {
	return "lenny-blob://" + tenantID + "/export/" + child + "/" + f.Path + "?ttl=3600", nil
}

// exportScanTestbed is the composed delegation export-scan surface: the live
// delegation service, the gRPC interceptor stub, and the identifiers a test
// asserts against.
type exportScanTestbed struct {
	svc      *delegation.Service
	parentID string
	stub     *stubinterceptor.Stub
}

// newExportScanTestbed builds the composed testbed. handler decides the gRPC
// interceptor's per-file response, maxExportedFileSize sets the policy's
// contentPolicy.maxExportedFileSize per-file ceiling (0 disables it), and
// files are the parent's exported files.
func newExportScanTestbed(t *testing.T, handler stubinterceptor.Handler, maxExportedFileSize int64, files []export.ExportedFile) exportScanTestbed {
	t.Helper()
	ctx := context.Background()
	clock := func() time.Time { return time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC) }

	sessions := memstore.New()
	runtimes := runtimestore.NewMemory()
	pols := delegationpolicystore.NewMemory()

	// The child runtime `worker` names the DelegationPolicy whose
	// contentPolicy.interceptorRef points at the external scanner and whose
	// scanExportedFiles is true; the parent runs a distinct policy-free
	// runtime so no parent content policy interferes with the child's.
	for _, rt := range []runtimestore.Runtime{
		{Name: "parent", Image: "lenny/parent@sha256:abc"},
		{Name: "worker", Image: "lenny/worker@sha256:def", DelegationPolicyRef: exportScanPolicy},
	} {
		if err := runtimes.Create(ctx, rt); err != nil {
			t.Fatalf("seed runtime %s: %v", rt.Name, err)
		}
	}
	if err := pols.Create(ctx, delegationpolicystore.DelegationPolicy{
		TenantID: exportScanTenant,
		Name:     exportScanPolicy,
		ContentPolicy: delegationpolicystore.ContentPolicy{
			InterceptorRef:      exportScanRef,
			ScanExportedFiles:   true,
			MaxExportedFileSize: maxExportedFileSize,
		},
	}); err != nil {
		t.Fatalf("seed delegation policy: %v", err)
	}

	parentID := session.NewID()
	if err := sessions.Create(ctx, sessionstore.Session{
		ID: parentID, TenantID: exportScanTenant, UserID: exportScanUser,
		RuntimeRef: "parent", State: session.StateRunning,
		IsolationProfile: isolation.ProfileSandboxed, CreatedAt: clock(), UpdatedAt: clock(),
	}); err != nil {
		t.Fatalf("seed parent session: %v", err)
	}

	// Real gRPC interceptor stub on a loopback port, dialed with the same
	// insecure transport a dev-mode gateway uses, and registered on the chain
	// at the PreDelegation phase through the real §4 External adapter. Per
	// §4.8 the PreExportMaterialization phase is not independently
	// registerable: the export-scan chain resolves the same named interceptor
	// already in force at PreDelegation, so the per-file scan is a genuine
	// network round-trip to the deployer-supplied scanner.
	stub := stubinterceptor.Start(t, handler)
	conn, err := grpc.NewClient(stub.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial interceptor stub: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	chain := interceptor.NewChain()
	if _, err := chain.RegisterExternal(interceptor.PhasePreDelegation, interceptor.ExternalConfig{
		Name:       exportScanRef,
		Endpoint:   stub.Addr(),
		Client:     interceptorv1.NewRequestInterceptorClient(conn),
		FailPolicy: interceptor.FailClosed,
	}); err != nil {
		t.Fatalf("register external PreDelegation scanner: %v", err)
	}

	svc := delegation.NewService(sessions, delegation.Options{
		Clock:                   clock,
		IDFunc:                  func() string { return "sess_child" },
		Runtimes:                runtimes,
		Policies:                pols,
		ExportMaterializer:      export.NewMaterializer(exportScanExporter{files: files}, exportScanSink{}, nil),
		ExportScanChainResolver: delegation.NewChainExportScanResolver(chain, nil),
	})
	return exportScanTestbed{svc: svc, parentID: parentID, stub: stub}
}

// delegateExport runs one fileExport delegation through the composed service.
func (tb exportScanTestbed) delegateExport(ctx context.Context) (delegation.Result, error) {
	return tb.svc.Delegate(ctx, exportScanTenant, delegation.Request{
		ParentSessionID:  tb.parentID,
		RuntimeRef:       "worker",
		PoolRef:          "pool-a",
		IsolationProfile: isolation.ProfileSandboxed,
		FileExport:       []export.Spec{{Source: "**"}},
	})
}

// scannedFilePath extracts the file_path from a forwarded §4.8 exported-file
// record payload.
func scannedFilePath(t *testing.T, req *interceptorv1.InterceptRequest) string {
	t.Helper()
	var rec struct {
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal(req.GetContent(), &rec); err != nil {
		t.Fatalf("forwarded PreExportMaterialization content is not an exported-file record: %v (raw=%s)", err, req.GetContent())
	}
	return rec.FilePath
}

// spec: §4.8 — "The `PreExportMaterialization` phase fires **per exported
// file**, after `PreDelegation` has passed and before the gateway persists the
// file into the child's workspace. It activates only when the parent lease's
// effective `contentPolicy.scanExportedFiles` is `true`. ... `REJECT` blocks
// the export — the delegation fails at materialization time with
// `EXPORT_FILE_SCAN_REJECTED`." "External interceptors are invoked via gRPC."
// diagnosis: the per-exported-file content scan regressed across a component
// boundary. The delegation service's effective contentPolicy resolution, the
// ExportScanChainResolver's interceptorRef lookup, the §8.7 materializer's
// per-file scan loop, and the real §4 gRPC External adapter are each
// unit-covered in isolation; this test fails when they stop agreeing end to
// end — the gateway did not resolve the child policy's scanExportedFiles, did
// not invoke the scanner once per exported file at PreExportMaterialization,
// or persisted files the scanner never saw, any of which materializes
// unscanned delegation-exported content past a configured content filter.
func TestDelegateExportScanFiresPerFileOverGRPC_spec_4_8(t *testing.T) {
	files := []export.ExportedFile{
		{Path: "docs/a.txt", Content: []byte("alpha"), Size: 5},
		{Path: "docs/b.txt", Content: []byte("bravo"), Size: 5},
		{Path: "src/c.go", Content: []byte("package c"), Size: 9},
	}
	tb := newExportScanTestbed(t, stubinterceptor.Allow(), 1<<20, files)

	res, err := tb.delegateExport(context.Background())
	if err != nil {
		t.Fatalf("Delegate with a clean scan should succeed, got %v", err)
	}

	// Every exported file was persisted onto the child's WorkspacePlan.
	plan, _, perr := workspaceplan.ParseStored(res.Child.WorkspacePlan)
	if perr != nil {
		t.Fatalf("stamped child plan must parse: %v (raw=%s)", perr, res.Child.WorkspacePlan)
	}
	if len(plan.Sources) != len(files) {
		t.Errorf("child plan sources = %d, want %d (one per exported file)", len(plan.Sources), len(files))
	}

	// The scanner was dialed over gRPC exactly once per exported file, each at
	// the PreExportMaterialization phase, and the forwarded records name every
	// exported file exactly once.
	reqs := tb.stub.Requests()
	if len(reqs) != len(files) {
		t.Fatalf("interceptor stub received %d gRPC requests, want one per exported file (%d)", len(reqs), len(files))
	}
	gotPaths := map[string]int{}
	for _, r := range reqs {
		if r.GetPhase() != string(interceptor.PhasePreExportMaterialization) {
			t.Errorf("forwarded phase = %q, want %q", r.GetPhase(), interceptor.PhasePreExportMaterialization)
		}
		if r.GetTenantId() != exportScanTenant {
			t.Errorf("forwarded tenant_id = %q, want %q", r.GetTenantId(), exportScanTenant)
		}
		gotPaths[scannedFilePath(t, r)]++
	}
	for _, f := range files {
		if gotPaths[f.Path] != 1 {
			t.Errorf("exported file %q was scanned %d times, want exactly once", f.Path, gotPaths[f.Path])
		}
	}
}

// spec: §4.8 — "Files exceeding `contentPolicy.maxExportedFileSize` are
// rejected at admission with `EXPORT_FILE_SCAN_SIZE_EXCEEDED` before any
// interceptor call is made, bounding the per-invocation payload."
// diagnosis: the per-file size ceiling did not fire ahead of the scan — an
// over-size exported file was forwarded to the interceptor (or materialized)
// instead of being rejected with EXPORT_FILE_SCAN_SIZE_EXCEEDED, defeating the
// payload bound the spec requires before any scanner gRPC call.
func TestDelegateExportScanSizeExceededPrecedesScan_spec_4_8(t *testing.T) {
	files := []export.ExportedFile{
		{Path: "big.bin", Content: []byte("this content is well over eight bytes"), Size: 37},
	}
	// An 8-byte per-file ceiling; the stub would ALLOW, so any dialed request
	// proves the size gate did not fire ahead of the scan.
	tb := newExportScanTestbed(t, stubinterceptor.Allow(), 8, files)

	_, err := tb.delegateExport(context.Background())
	var scanErr *interceptor.ExportScanError
	if !errors.As(err, &scanErr) || scanErr.Code != interceptor.CodeExportFileScanSizeExceeded {
		t.Fatalf("Delegate error = %v, want EXPORT_FILE_SCAN_SIZE_EXCEEDED", err)
	}
	if reqs := tb.stub.Requests(); len(reqs) != 0 {
		t.Errorf("interceptor stub received %d request(s) for an over-size file; the size gate must reject before any scanner call", len(reqs))
	}
}

// spec: §4.8 — "`REJECT` blocks the export — the delegation fails at
// materialization time with `EXPORT_FILE_SCAN_REJECTED`."
// diagnosis: an interceptor REJECT on an exported file did not block the
// delegation — the file was materialized onto the child workspace or the
// delegation surfaced the wrong error code, either of which admits content the
// deployer's scanner deliberately rejected.
func TestDelegateExportScanRejectBlocksExport_spec_4_8(t *testing.T) {
	files := []export.ExportedFile{
		{Path: "AGENTS.md", Content: []byte("exported instruction file"), Size: 25},
	}
	tb := newExportScanTestbed(t, stubinterceptor.Reject("exported instruction file blocked"), 1<<20, files)

	_, err := tb.delegateExport(context.Background())
	var scanErr *interceptor.ExportScanError
	if !errors.As(err, &scanErr) || scanErr.Code != interceptor.CodeExportFileScanRejected {
		t.Fatalf("Delegate error = %v, want EXPORT_FILE_SCAN_REJECTED", err)
	}
	// The scanner was dialed once over gRPC at the PreExportMaterialization
	// phase before the delegation failed closed.
	reqs := tb.stub.Requests()
	if len(reqs) != 1 {
		t.Fatalf("interceptor stub received %d gRPC requests, want exactly one for the single exported file", len(reqs))
	}
	if reqs[0].GetPhase() != string(interceptor.PhasePreExportMaterialization) {
		t.Errorf("forwarded phase = %q, want %q", reqs[0].GetPhase(), interceptor.PhasePreExportMaterialization)
	}
}
