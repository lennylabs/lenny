// SPDX-License-Identifier: MIT

package delegation_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/delegation/export"
	"github.com/lennylabs/lenny/pkg/gateway/delegation/fileexport"
	"github.com/lennylabs/lenny/pkg/gateway/delegationpolicystore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// fakeMaterializer records the §8.7 Params it was called with and returns
// a canned Result so the Delegate wiring can be asserted without a real
// pod export or blob store.
type fakeMaterializer struct {
	gotParams export.Params
	called    bool
	result    export.Result
	err       error
}

func (m *fakeMaterializer) Materialize(_ context.Context, p export.Params) (export.Result, error) {
	m.called = true
	m.gotParams = p
	if m.err != nil {
		return export.Result{}, m.err
	}
	return m.result, nil
}

// inlineExporter / inlineSink back an end-to-end test through the real
// export.Materializer without importing the export package's test fakes.
type inlineExporter struct{ files []export.ExportedFile }

func (e inlineExporter) ExportPaths(_ context.Context, _ string, _ export.Spec) ([]export.ExportedFile, error) {
	return e.files, nil
}

type inlineSink struct{}

func (inlineSink) Persist(_ context.Context, tenantID, child string, f export.ExportedFile) (string, error) {
	return "lenny-blob://" + tenantID + "/export/" + child + "/" + f.Path + "?ttl=3600", nil
}

// TestDelegateRunsExportMaterializer_spec_8_7_F_8_7_1 asserts the §8.7
// wiring: a delegation that declares fileExport entries runs the
// materializer with the resolved §8.3 limits and tenant/child/parent
// identity, and stamps the returned §14 sources onto the child row's
// WorkspacePlan that the §6.3 binder later reads.
func TestDelegateRunsExportMaterializer_spec_8_7_F_8_7_1(t *testing.T) {
	store := memstore.New()
	seedParent(t, store, "sess_parent", "", "gemini", "pool-b", isolation.ProfileSandboxed)

	mat := &fakeMaterializer{result: export.Result{
		Sources: []workspaceplan.Source{{
			Type:    workspaceplan.TypeUploadFile,
			Variant: workspaceplan.UploadFile{PathField: "docs/a.txt", UploadRef: "lenny-blob://acme/export/sess_child/docs/a.txt?ttl=3600"},
		}},
		FileCount: 1,
	}}
	svc := delegation.NewService(store, delegation.Options{
		Clock:              func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:             func() string { return "sess_child" },
		ExportMaterializer: mat,
	})

	res, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "claude",
		PoolRef:          "pool-a",
		IsolationProfile: isolation.ProfileSandboxed,
		FileExport:       []export.Spec{{Source: "docs/*.txt", DestPrefix: ""}},
	})
	if err != nil {
		t.Fatalf("Delegate: %v", err)
	}
	if !mat.called {
		t.Fatal("materializer was not invoked for a fileExport delegation")
	}
	if mat.gotParams.ParentSessionID != "sess_parent" || mat.gotParams.ChildSessionID != "sess_child" || mat.gotParams.TenantID != "acme" {
		t.Errorf("params identity = %+v, want parent=sess_parent child=sess_child tenant=acme", mat.gotParams)
	}
	if len(mat.gotParams.Specs) != 1 || mat.gotParams.Specs[0].Source != "docs/*.txt" {
		t.Errorf("specs = %+v, want one docs/*.txt spec", mat.gotParams.Specs)
	}
	// §8.3 line 264: unset lease limits default to 100 files / 100 MiB.
	if mat.gotParams.Limits != fileexport.DefaultFileExportLimits {
		t.Errorf("limits = %+v, want defaults %+v", mat.gotParams.Limits, fileexport.DefaultFileExportLimits)
	}
	if len(res.Child.WorkspacePlan) == 0 {
		t.Fatal("child row WorkspacePlan was not stamped with the export sources")
	}
	plan, _, perr := workspaceplan.ParseStored(res.Child.WorkspacePlan)
	if perr != nil {
		t.Fatalf("stamped child plan must parse: %v", perr)
	}
	if len(plan.Sources) != 1 {
		t.Errorf("stamped plan sources = %d, want 1", len(plan.Sources))
	}
}

// TestDelegateNoFileExportSkipsMaterializer asserts a delegation with no
// fileExport entries never invokes the materializer and leaves the child
// plan unset, so the §8.7 path is inert by default (§8.7 default).
func TestDelegateNoFileExportSkipsMaterializer(t *testing.T) {
	store := memstore.New()
	seedParent(t, store, "sess_parent", "", "gemini", "pool-b", isolation.ProfileSandboxed)
	mat := &fakeMaterializer{}
	svc := delegation.NewService(store, delegation.Options{
		Clock:              func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:             func() string { return "sess_child" },
		ExportMaterializer: mat,
	})
	res, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "claude",
		PoolRef:          "pool-a",
		IsolationProfile: isolation.ProfileSandboxed,
	})
	if err != nil {
		t.Fatalf("Delegate: %v", err)
	}
	if mat.called {
		t.Error("materializer must not run when no fileExport entries are declared")
	}
	if len(res.Child.WorkspacePlan) != 0 {
		t.Errorf("child plan must stay unset, got %s", res.Child.WorkspacePlan)
	}
}

// TestDelegateFileExportWithoutMaterializerFailsClosed asserts that
// declaring fileExport with no configured materializer fails with
// ErrExportNotConfigured rather than silently dropping the files. F-8.7.1.
func TestDelegateFileExportWithoutMaterializerFailsClosed(t *testing.T) {
	store := memstore.New()
	seedParent(t, store, "sess_parent", "", "gemini", "pool-b", isolation.ProfileSandboxed)
	svc := delegation.NewService(store, delegation.Options{
		Clock:  func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc: func() string { return "sess_child" },
	})
	_, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "claude",
		PoolRef:          "pool-a",
		IsolationProfile: isolation.ProfileSandboxed,
		FileExport:       []export.Spec{{Source: "docs/*.txt"}},
	})
	if !errors.Is(err, delegation.ErrExportNotConfigured) {
		t.Fatalf("got %v, want ErrExportNotConfigured", err)
	}
}

// TestDelegateScanRequiredWithoutResolverFailsClosed asserts the §8.3
// rule-1 fail-closed posture: a DelegationPolicy with
// scanExportedFiles:true and no configured scan-chain resolver rejects the
// export with ErrExportScanUnavailable rather than materializing unscanned
// files. F-8.7.1.
func TestDelegateScanRequiredWithoutResolverFailsClosed(t *testing.T) {
	store := memstore.New()
	seedParent(t, store, "sess_parent", "", "gemini", "pool-b", isolation.ProfileSandboxed)

	runtimes := runtimestore.NewMemory()
	if err := runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "claude", Image: "lenny/claude@sha256:abc", DelegationPolicyRef: "pol-scan",
	}); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	policies := delegationpolicystore.NewMemory()
	if err := policies.Create(context.Background(), delegationpolicystore.DelegationPolicy{
		TenantID: "acme", Name: "pol-scan",
		ContentPolicy: delegationpolicystore.ContentPolicy{
			InterceptorRef:    "scanner",
			ScanExportedFiles: true,
		},
	}); err != nil {
		t.Fatalf("seed policy: %v", err)
	}

	svc := delegation.NewService(store, delegation.Options{
		Clock:              func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:             func() string { return "sess_child" },
		Runtimes:           runtimes,
		Policies:           policies,
		ExportMaterializer: &fakeMaterializer{},
		// ExportScanChainResolver intentionally omitted.
	})
	_, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "claude",
		PoolRef:          "pool-a",
		IsolationProfile: isolation.ProfileSandboxed,
		FileExport:       []export.Spec{{Source: "docs/*.txt"}},
	})
	if !errors.Is(err, delegation.ErrExportScanUnavailable) {
		t.Fatalf("got %v, want ErrExportScanUnavailable", err)
	}
}

// TestDelegateExportEndToEndStampsPlan exercises the real
// export.Materializer through Delegate: a parent export of one plain file
// and one archive yields a child WorkspacePlan with an uploadFile and an
// uploadArchive (the §8.7 line 792 archive-validator inheritance routing).
func TestDelegateExportEndToEndStampsPlan(t *testing.T) {
	store := memstore.New()
	seedParent(t, store, "sess_parent", "", "gemini", "pool-b", isolation.ProfileSandboxed)

	mat := export.NewMaterializer(
		inlineExporter{files: []export.ExportedFile{
			{Path: "docs/a.txt", Content: []byte("alpha"), Size: 5},
			{Path: "vendor/lib.tar.gz", Content: []byte("PKZ"), Size: 3},
		}},
		inlineSink{}, nil,
	)
	svc := delegation.NewService(store, delegation.Options{
		Clock:              func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:             func() string { return "sess_child" },
		ExportMaterializer: mat,
	})
	res, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "claude",
		PoolRef:          "pool-a",
		IsolationProfile: isolation.ProfileSandboxed,
		FileExport:       []export.Spec{{Source: "**"}},
	})
	if err != nil {
		t.Fatalf("Delegate: %v", err)
	}
	plan, _, perr := workspaceplan.ParseStored(res.Child.WorkspacePlan)
	if perr != nil {
		t.Fatalf("stamped child plan must parse: %v (raw=%s)", perr, res.Child.WorkspacePlan)
	}
	var files, archives int
	for _, s := range plan.Sources {
		switch s.Variant.(type) {
		case workspaceplan.UploadFile:
			files++
		case workspaceplan.UploadArchive:
			archives++
		}
	}
	if files != 1 || archives != 1 {
		t.Errorf("plan sources = %d files / %d archives, want 1/1 (archive inheritance routing)", files, archives)
	}
	// Defensive: the stamped plan must be valid JSON the store can persist.
	if !json.Valid(res.Child.WorkspacePlan) {
		t.Error("stamped child plan is not valid JSON")
	}
}
