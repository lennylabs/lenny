// SPDX-License-Identifier: MIT

package toolops

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/policy"
	"github.com/lennylabs/lenny/pkg/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/policy/interceptor/interceptorstore"
)

// stubInterceptor answers every request with res and records what it saw.
type stubInterceptor struct {
	name    string
	builtin bool
	res     interceptor.Result
	seen    *[]interceptor.Request
}

func (s stubInterceptor) Name() string                       { return s.name }
func (s stubInterceptor) Priority() int32                    { return 500 }
func (s stubInterceptor) Builtin() bool                      { return s.builtin }
func (s stubInterceptor) FailPolicy() interceptor.FailPolicy { return interceptor.FailClosed }
func (s stubInterceptor) Timeout() time.Duration             { return 0 }
func (s stubInterceptor) Intercept(_ context.Context, req interceptor.Request) (interceptor.Result, error) {
	if s.seen != nil {
		*s.seen = append(*s.seen, req)
	}
	return s.res, nil
}

func chainWith(t *testing.T, phase interceptor.Phase, ics ...interceptor.Interceptor) *interceptor.Chain {
	t.Helper()
	c := interceptor.NewChain()
	for _, ic := range ics {
		if err := c.Register(phase, ic); err != nil {
			t.Fatalf("register %s: %v", ic.Name(), err)
		}
	}
	return c
}

// TestNewInterceptionWithNoChainIsNil checks that no chain gives the
// platform tools seam no interception, so it runs no phase.
// spec: GWSEAM-Intercept
func TestNewInterceptionWithNoChainIsNil(t *testing.T) {
	if got := NewInterception(nil, nil); got != nil {
		t.Fatalf("NewInterception(nil) = %v, want a nil interface", got)
	}
}

// TestInterceptRunsThePhaseAndMapsTheResult checks that the request reaches
// the chain at its phase with its tenant, session, content, and metadata,
// that a modification and an allow map onto the seam's actions, and that
// Intercepts reports the phases with interceptors.
// spec: GWSEAM-Intercept, GWSEAM-Intercepts
func TestInterceptRunsThePhaseAndMapsTheResult(t *testing.T) {
	var seen []interceptor.Request
	chain := chainWith(t, interceptor.PhasePreRoute, stubInterceptor{
		name: "rewriter",
		res:  interceptor.Result{Action: interceptor.ActionModify, ModifiedContent: []byte("rewritten")},
		seen: &seen,
	})
	ic := NewInterception(chain, nil)
	res := ic.Intercept(context.Background(), seam.InterceptRequest{
		Phase: seam.InterceptPreRoute, TenantID: "acme", SessionID: "sess_parent",
		Content: []byte("input"), Metadata: map[string]string{"tenant_id": "acme"},
	})
	if res.Action != seam.InterceptModify || string(res.ModifiedContent) != "rewritten" {
		t.Fatalf("result = %+v, want a modification to rewritten", res)
	}
	if len(seen) != 1 || seen[0].Phase != interceptor.PhasePreRoute || seen[0].TenantID != "acme" ||
		seen[0].SessionID != "sess_parent" || string(seen[0].Content) != "input" || seen[0].Metadata["tenant_id"] != "acme" {
		t.Fatalf("the chain saw %+v", seen)
	}
	if !ic.Intercepts(seam.InterceptPreRoute) || ic.Intercepts(seam.InterceptPreDelegation) {
		t.Error("Intercepts does not report the phases with interceptors")
	}
	if got := ic.Intercept(context.Background(), seam.InterceptRequest{Phase: seam.InterceptPreDelegation}); got.Action != seam.InterceptAllow {
		t.Errorf("a phase with no interceptor = %+v, want allow", got)
	}
}

// TestInterceptRecordsARejection checks that a rejection carries its code,
// its rejecting interceptor, and its immutable fields, and that the core
// records one interceptor.rejected audit row for it, and none with no
// audit sink.
// spec: GWSEAM-Intercept
func TestInterceptRecordsARejection(t *testing.T) {
	chain := chainWith(t, interceptor.PhasePreMessageDelivery, stubInterceptor{
		name: "blocker",
		res: interceptor.Result{
			Action: interceptor.ActionReject, Reason: "blocked", Code: seam.CodeInterceptorImmutableFieldViolation,
			ViolatedFields: []string{"id"},
		},
	})
	rec := &rejectionAudit{}
	req := seam.InterceptRequest{Phase: seam.InterceptPreMessageDelivery, TenantID: "acme", SessionID: "sess_1", Content: []byte("x")}
	res := NewInterception(chain, policy.NewAuditSink(rec, nil)).Intercept(context.Background(), req)
	if res.Action != seam.InterceptReject || res.Reason != "blocked" || res.RejectedBy != "blocker" ||
		res.Code != seam.CodeInterceptorImmutableFieldViolation || len(res.ViolatedFields) != 1 {
		t.Fatalf("result = %+v", res)
	}
	if rec.rows != 1 {
		t.Errorf("audit rows = %d, want 1", rec.rows)
	}
	if got := NewInterception(chain, nil).Intercept(context.Background(), req); got.Action != seam.InterceptReject {
		t.Errorf("with no audit sink: %+v, want a rejection", got)
	}
}

// TestInterceptScopesExternalInterceptorsToThePolicy checks that a
// policy-scoped request runs only the external interceptor the content
// policy names, none when it names none, and refuses a name the phase does
// not register, while an unscoped request runs every interceptor.
// spec: GWSEAM-Intercept
func TestInterceptScopesExternalInterceptorsToThePolicy(t *testing.T) {
	var seen []interceptor.Request
	chain := chainWith(t, interceptor.PhasePreDelegation, stubInterceptor{
		name: "scanner", res: interceptor.Result{Action: interceptor.ActionAllow}, seen: &seen,
	})
	ic := NewInterception(chain, nil)
	base := seam.InterceptRequest{Phase: seam.InterceptPreDelegation, TenantID: "acme", Content: []byte("x")}

	scopedNone := base
	scopedNone.PolicyScoped = true
	ic.Intercept(context.Background(), scopedNone)
	if len(seen) != 0 {
		t.Fatalf("a policy naming no interceptor ran %d external interceptors", len(seen))
	}
	scoped := scopedNone
	scoped.InterceptorRef = "scanner"
	ic.Intercept(context.Background(), scoped)
	if len(seen) != 1 {
		t.Fatalf("the named interceptor ran %d times, want 1", len(seen))
	}
	unknown := scopedNone
	unknown.InterceptorRef = "missing"
	if got := ic.Intercept(context.Background(), unknown); got.Action != seam.InterceptReject {
		t.Errorf("an unregistered interceptorRef = %+v, want a rejection", got)
	}
	ic.Intercept(context.Background(), base)
	if len(seen) != 2 {
		t.Errorf("an unscoped request ran the external interceptor %d times in total, want 2", len(seen))
	}
}

// TestFailOpenCooldownReadsTheRegistry checks that the cooldown is the
// registry's recorded fail-open transition, and that an interceptor with
// none, or an unknown one, reports no cooldown.
// spec: GWSEAM-FailOpenCooldown
func TestFailOpenCooldownReadsTheRegistry(t *testing.T) {
	store := interceptorstore.NewMemory()
	at := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	registered := func(name string) interceptorstore.Interceptor {
		return interceptorstore.Interceptor{
			Name: name, Endpoint: "scanner.acme.svc:9000", Priority: 500, FailPolicy: interceptor.FailClosed,
			TimeoutMs: 500, Phases: []interceptor.Phase{interceptor.PhasePreDelegation},
		}
	}
	weakened := registered("scanner")
	weakened.FailPolicy = interceptor.FailOpen
	weakened.FailOpenTransitionAt = at
	weakened.CooldownSecondsAtTransition = 300
	if err := store.Create(context.Background(), weakened); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := store.Create(context.Background(), registered("steady")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	c := NewCooldowns(store)
	if ts, secs, ok := c.FailOpenCooldown(context.Background(), "scanner"); !ok || !ts.Equal(at) || secs != 300 {
		t.Errorf("scanner = %v, %d, %v; want %v, 300, true", ts, secs, ok, at)
	}
	for _, name := range []string{"steady", "missing", ""} {
		if _, _, ok := c.FailOpenCooldown(context.Background(), name); ok {
			t.Errorf("%q reported a cooldown", name)
		}
	}
}

// recordingObserver captures the export scan events.
type recordingObserver struct{ events []interceptor.ExportScanEvent }

func (r *recordingObserver) ExportFileScanned(_ context.Context, ev interceptor.ExportScanEvent) {
	r.events = append(r.events, ev)
}

// TestResolveExportScanFailsClosed checks that no chain, a blank reference,
// and a reference no PreDelegation interceptor carries are each
// ErrExportScanUnavailable.
// spec: GWSEAM-ResolveExportScan
func TestResolveExportScanFailsClosed(t *testing.T) {
	chain := chainWith(t, interceptor.PhasePreDelegation, stubInterceptor{name: "scanner"})
	scans := NewExportScans(chain, nil)
	for _, ref := range []string{"", "missing"} {
		if _, err := scans.ResolveExportScan(context.Background(), "acme", ref); !errors.Is(err, seam.ErrExportScanUnavailable) {
			t.Errorf("ref %q: err = %v, want ErrExportScanUnavailable", ref, err)
		}
	}
	if _, err := NewExportScans(nil, nil).ResolveExportScan(context.Background(), "acme", "scanner"); !errors.Is(err, seam.ErrExportScanUnavailable) {
		t.Errorf("no chain: err = %v, want ErrExportScanUnavailable", err)
	}
	var nilScans *ExportScans
	if _, err := nilScans.ResolveExportScan(context.Background(), "acme", "scanner"); !errors.Is(err, seam.ErrExportScanUnavailable) {
		t.Errorf("nil scans: err = %v, want ErrExportScanUnavailable", err)
	}
}

// TestScanExportFilesRunsTheNamedInterceptor checks that the scan runs the
// named interceptor once per file at PreExportMaterialization, applies a
// modification, stamps the digest of the final content, and hands the
// observer the delegation's labels; a rejection comes back as the scan's
// ExportScanError and a file over the policy's maximum is refused before
// the interceptor runs.
// spec: GWSEAM-ScanExportFiles
func TestScanExportFilesRunsTheNamedInterceptor(t *testing.T) {
	var seen []interceptor.Request
	chain := chainWith(t, interceptor.PhasePreDelegation, stubInterceptor{
		name: "scanner", res: interceptor.Result{Action: interceptor.ActionAllow}, seen: &seen,
	})
	obs := &recordingObserver{}
	scan, err := NewExportScans(chain, obs).ResolveExportScan(context.Background(), "acme", "scanner")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	target := seam.ExportScanTarget{TenantID: "acme", SessionID: "sess_child", PolicyName: "pol-scan", Pool: "pool-a"}
	out, err := scan.ScanExportFiles(context.Background(), target, []seam.ExportFile{{Path: "docs/a.txt", Content: []byte("alpha")}})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(out) != 1 || out[0].Path != "docs/a.txt" || out[0].SHA256 != interceptor.ComputeExportFileHash([]byte("alpha")) {
		t.Fatalf("scanned = %+v", out)
	}
	if len(seen) != 1 || seen[0].Phase != interceptor.PhasePreExportMaterialization {
		t.Fatalf("the interceptor saw %+v, want one PreExportMaterialization request", seen)
	}
	if len(obs.events) != 1 {
		t.Fatalf("events = %d, want 1", len(obs.events))
	}
	ev := obs.events[0]
	if ev.PolicyName != "pol-scan" || ev.InterceptorRef != "scanner" || ev.Pool != "pool-a" || ev.TenantID != "acme" || ev.SessionID != "sess_child" {
		t.Errorf("event labels = %+v", ev)
	}

	target.MaxFileSize = 2
	_, err = scan.ScanExportFiles(context.Background(), target, []seam.ExportFile{{Path: "big.txt", Content: []byte("too big")}})
	var ese *interceptor.ExportScanError
	if !errors.As(err, &ese) || ese.Code != interceptor.CodeExportFileScanSizeExceeded {
		t.Fatalf("oversize: err = %v, want EXPORT_FILE_SCAN_SIZE_EXCEEDED", err)
	}
	if len(seen) != 1 {
		t.Errorf("the oversize file reached the interceptor")
	}

	rejecting := chainWith(t, interceptor.PhasePreDelegation, stubInterceptor{
		name: "scanner", res: interceptor.Result{Action: interceptor.ActionReject, Reason: "secret"},
	})
	rscan, err := NewExportScans(rejecting, nil).ResolveExportScan(context.Background(), "acme", "scanner")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	_, err = rscan.ScanExportFiles(context.Background(), seam.ExportScanTarget{TenantID: "acme"}, []seam.ExportFile{{Path: "a", Content: []byte("x")}})
	if !errors.As(err, &ese) || ese.Code != interceptor.CodeExportFileScanRejected {
		t.Fatalf("rejection: err = %v, want EXPORT_FILE_SCAN_REJECTED", err)
	}
}

// TestSeamCodesMatchTheInterceptorCodes checks that the client API codes
// the seam declares are the ones the interceptor chain and the content
// policy evaluator emit, so a result read through the seam keeps its code.
// spec: GWSEAM-Intercept
func TestSeamCodesMatchTheInterceptorCodes(t *testing.T) {
	if seam.CodeInterceptorImmutableFieldViolation != interceptor.CodeInterceptorImmutableFieldViolation {
		t.Errorf("immutable field code %q != %q", seam.CodeInterceptorImmutableFieldViolation, interceptor.CodeInterceptorImmutableFieldViolation)
	}
	if seam.CodeInputTooLarge != policy.CodeInputTooLarge {
		t.Errorf("input too large code %q != %q", seam.CodeInputTooLarge, policy.CodeInputTooLarge)
	}
	for phase, want := range map[seam.InterceptPhase]interceptor.Phase{
		seam.InterceptPreRoute:           interceptor.PhasePreRoute,
		seam.InterceptPreDelegation:      interceptor.PhasePreDelegation,
		seam.InterceptPreMessageDelivery: interceptor.PhasePreMessageDelivery,
		seam.InterceptPreToolResult:      interceptor.PhasePreToolResult,
		seam.InterceptPostAgentOutput:    interceptor.PhasePostAgentOutput,
	} {
		if interceptor.Phase(phase) != want {
			t.Errorf("phase %q != %q", phase, want)
		}
	}
}
