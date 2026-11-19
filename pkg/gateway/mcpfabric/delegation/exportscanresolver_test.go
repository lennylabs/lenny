// SPDX-License-Identifier: MIT

package delegation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/core/toolops"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation/export"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationpolicystore"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// scanInterceptor is a minimal interceptor.Interceptor for the export-scan
// resolver tests: it returns the configured Result/error.
type scanInterceptor struct {
	name string
	res  interceptor.Result
	err  error
}

func (s scanInterceptor) Name() string                       { return s.name }
func (s scanInterceptor) Priority() int32                    { return 50 }
func (s scanInterceptor) Builtin() bool                      { return true }
func (s scanInterceptor) FailPolicy() interceptor.FailPolicy { return interceptor.FailClosed }
func (s scanInterceptor) Timeout() time.Duration             { return 0 }
func (s scanInterceptor) Intercept(context.Context, interceptor.Request) (interceptor.Result, error) {
	return s.res, s.err
}

// recordingExportObserver captures the per-file scan events the resolver
// threads into the ExportScanContext.
type recordingExportObserver struct {
	events []interceptor.ExportScanEvent
}

func (r *recordingExportObserver) ExportFileScanned(_ context.Context, ev interceptor.ExportScanEvent) {
	r.events = append(r.events, ev)
}

// seedScanPolicy stands up a runtime referencing a scanExportedFiles policy
// plus the parent session, returning the store the test drives Delegate on.
func seedScanPolicy(t *testing.T) (*memstore.Store, *runtimestore.Memory, *delegationpolicystore.Memory) {
	t.Helper()
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
	return store, runtimes, policies
}

// spec: §13.5 mitigation 4 — with the resolver wired, a scanExportedFiles
// policy routes each exported file through the named interceptor and the
// observer records one §16.1 event stamped with the §11.7 policy_name /
// interceptor_ref / pool labels. F-13.5.5.
func TestDelegateExportScanRunsThroughResolver_spec_13_5_5(t *testing.T) {
	store, runtimes, policies := seedScanPolicy(t)
	chain := interceptor.NewChain()
	if err := chain.Register(interceptor.PhasePreDelegation,
		scanInterceptor{name: "scanner", res: interceptor.Result{Action: interceptor.ActionAllow}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	obs := &recordingExportObserver{}
	mat := export.NewMaterializer(
		inlineExporter{files: []export.ExportedFile{{Path: "docs/a.txt", Content: []byte("alpha"), Size: 5}}},
		inlineSink{}, nil,
	)

	svc := delegation.NewService(store, delegation.Options{
		Clock:              func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:             func() string { return "sess_child" },
		Runtimes:           runtimes,
		Policies:           policies,
		ExportMaterializer: mat,
		ExportScans:        toolops.NewExportScans(chain, obs),
	})
	if _, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "claude",
		PoolRef:          "pool-a",
		IsolationProfile: isolation.ProfileSandboxed,
		FileExport:       []export.Spec{{Source: "docs/*.txt"}},
	}); err != nil {
		t.Fatalf("Delegate: %v", err)
	}
	if len(obs.events) != 1 {
		t.Fatalf("scan events = %d, want 1 (the resolver did not route the file through the named interceptor)", len(obs.events))
	}
	ev := obs.events[0]
	if ev.Outcome != interceptor.OutcomeAdmitted {
		t.Errorf("outcome = %q, want admitted", ev.Outcome)
	}
	if ev.PolicyName != "pol-scan" || ev.InterceptorRef != "scanner" || ev.Pool != "pool-a" {
		t.Errorf("labels = {policy:%q ref:%q pool:%q}, want {pol-scan scanner pool-a}", ev.PolicyName, ev.InterceptorRef, ev.Pool)
	}
}

// spec: §8.7 — a REJECT from the export scan fails the delegation with
// EXPORT_FILE_SCAN_REJECTED and records the rejected outcome. F-13.5.5.
func TestDelegateExportScanRejectFailsClosed_spec_13_5_5(t *testing.T) {
	store, runtimes, policies := seedScanPolicy(t)
	chain := interceptor.NewChain()
	if err := chain.Register(interceptor.PhasePreDelegation,
		scanInterceptor{name: "scanner", res: interceptor.Result{Action: interceptor.ActionReject, Reason: "instruction-file fragment"}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	obs := &recordingExportObserver{}
	mat := export.NewMaterializer(
		inlineExporter{files: []export.ExportedFile{{Path: "docs/a.txt", Content: []byte("alpha"), Size: 5}}},
		inlineSink{}, nil,
	)

	svc := delegation.NewService(store, delegation.Options{
		Clock:              func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:             func() string { return "sess_child" },
		Runtimes:           runtimes,
		Policies:           policies,
		ExportMaterializer: mat,
		ExportScans:        toolops.NewExportScans(chain, obs),
	})
	_, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "claude",
		PoolRef:          "pool-a",
		IsolationProfile: isolation.ProfileSandboxed,
		FileExport:       []export.Spec{{Source: "docs/*.txt"}},
	})
	var ese *interceptor.ExportScanError
	if !errors.As(err, &ese) || ese.Code != interceptor.CodeExportFileScanRejected {
		t.Fatalf("got %v, want *ExportScanError{Code: %s}", err, interceptor.CodeExportFileScanRejected)
	}
	if len(obs.events) != 1 || obs.events[0].Outcome != interceptor.OutcomeRejected {
		t.Fatalf("events = %+v, want one rejected event", obs.events)
	}
}
