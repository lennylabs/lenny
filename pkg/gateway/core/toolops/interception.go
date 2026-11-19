// SPDX-License-Identifier: MIT

package toolops

import (
	"context"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor/interceptorstore"
	"github.com/lennylabs/lenny/pkg/gateway/policy/policy"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
)

// Interception serves seam.Interception over the gateway interceptor chain
// and the interceptor rejection audit sink.
type Interception struct {
	chain *interceptor.Chain
	audit *policy.AuditSink
}

var _ seam.Interception = (*Interception)(nil)

// NewInterception returns the interception over chain. A nil audit sink
// records no rejection event. It returns a nil interface for a nil chain,
// so the platform tools seam sees no interception and runs no phase, as it
// does with no chain wired.
func NewInterception(chain *interceptor.Chain, audit *policy.AuditSink) seam.Interception {
	if chain == nil {
		return nil
	}
	return &Interception{chain: chain, audit: audit}
}

// Intercept runs the chain at the request's phase, scoped to the content
// policy's interceptor when the request asks for it, and records the
// interceptor.rejected audit event on a rejection. The record is
// best-effort: the caller fails the request closed either way.
// spec: GWSEAM-Intercept
func (i *Interception) Intercept(ctx context.Context, req seam.InterceptRequest) seam.InterceptResult {
	phase := interceptor.Phase(req.Phase)
	ireq := interceptor.Request{
		Phase:     phase,
		SessionID: req.SessionID,
		TenantID:  req.TenantID,
		Content:   req.Content,
		Metadata:  req.Metadata,
	}
	var res interceptor.Result
	if req.PolicyScoped {
		res = i.chain.RunPolicyScoped(ctx, ireq, req.InterceptorRef)
	} else {
		res = i.chain.Run(ctx, ireq)
	}
	if res.Action == interceptor.ActionReject && i.audit != nil {
		_ = i.audit.RecordRejection(ctx, policy.RejectionContext{
			TenantID:  req.TenantID,
			SessionID: req.SessionID,
			Phase:     phase,
		}, res)
	}
	return seamResult(res)
}

// Intercepts reports whether any interceptor is registered at phase.
// spec: GWSEAM-Intercepts
func (i *Interception) Intercepts(phase seam.InterceptPhase) bool {
	return i.chain.Len(interceptor.Phase(phase)) > 0
}

// seamResult maps a chain result onto the seam's interception result.
func seamResult(res interceptor.Result) seam.InterceptResult {
	action := seam.InterceptAllow
	switch res.Action {
	case interceptor.ActionReject:
		action = seam.InterceptReject
	case interceptor.ActionModify:
		action = seam.InterceptModify
	}
	return seam.InterceptResult{
		Action:          action,
		Reason:          res.Reason,
		Code:            res.Code,
		ModifiedContent: res.ModifiedContent,
		RejectedBy:      res.RejectedBy,
		ViolatedFields:  res.ViolatedFields,
	}
}

// Cooldowns serves seam.InterceptorCooldowns over the interceptor
// registry's fail-open weakening cooldowns.
type Cooldowns struct {
	resolver interceptorstore.CooldownResolver
}

var _ seam.InterceptorCooldowns = Cooldowns{}

// NewCooldowns returns the cooldowns of the interceptors in store.
func NewCooldowns(store interceptorstore.Store) Cooldowns {
	return Cooldowns{resolver: interceptorstore.NewCooldownResolver(store)}
}

// FailOpenCooldown returns the named interceptor's active fail-open
// weakening cooldown.
// spec: GWSEAM-FailOpenCooldown
func (c Cooldowns) FailOpenCooldown(ctx context.Context, interceptorRef string) (time.Time, int, bool) {
	return c.resolver.FailOpenCooldown(ctx, interceptorRef)
}

// ExportScans serves seam.ExportScans over the PreDelegation interceptors
// registered on the chain and the export scan observer, which emits the
// export scan audit events and metrics.
type ExportScans struct {
	chain    *interceptor.Chain
	observer interceptor.ExportScanObserver
}

var _ seam.ExportScans = (*ExportScans)(nil)

// NewExportScans returns the export scans of chain. A nil observer emits
// no audit event or metric; the scan still runs.
func NewExportScans(chain *interceptor.Chain, observer interceptor.ExportScanObserver) *ExportScans {
	return &ExportScans{chain: chain, observer: observer}
}

// ResolveExportScan returns the scan of the named interceptor, which
// PreExportMaterialization runs as the same interceptor in force at
// PreDelegation. A nil chain, a blank reference, or a reference that names
// no registered interceptor is seam.ErrExportScanUnavailable, so the
// export fails closed.
// spec: GWSEAM-ResolveExportScan
func (e *ExportScans) ResolveExportScan(_ context.Context, _ string, interceptorRef string) (seam.ExportScan, error) {
	if e == nil || e.chain == nil {
		return nil, seam.ErrExportScanUnavailable
	}
	sub, ok := e.chain.ExportScanChainFor(interceptorRef)
	if !ok {
		return nil, seam.ErrExportScanUnavailable
	}
	return exportScan{chain: sub, ref: interceptorRef, observer: e.observer}, nil
}

// exportScan is the scan of one interceptor.
type exportScan struct {
	chain    *interceptor.Chain
	ref      string
	observer interceptor.ExportScanObserver
}

// ScanExportFiles runs PreExportMaterialization over files. Its error,
// such as an *interceptor.ExportScanError, is returned as the scan
// produced it.
// spec: GWSEAM-ScanExportFiles
func (s exportScan) ScanExportFiles(ctx context.Context, t seam.ExportScanTarget, files []seam.ExportFile) ([]seam.ExportFile, error) {
	in := make([]interceptor.ExportFile, 0, len(files))
	for _, f := range files {
		in = append(in, interceptor.ExportFile{Path: f.Path, Content: f.Content})
	}
	sc := interceptor.ExportScanContext{
		Pool:           t.Pool,
		PolicyName:     t.PolicyName,
		InterceptorRef: s.ref,
		Observer:       s.observer,
	}
	scanned, err := interceptor.RunPreExportMaterialization(ctx, s.chain, sc, t.TenantID, t.SessionID, t.MaxFileSize, in...)
	if err != nil {
		return nil, err
	}
	out := make([]seam.ExportFile, 0, len(scanned))
	for _, f := range scanned {
		out = append(out, seam.ExportFile{Path: f.Path, Content: f.Content, SHA256: f.Hash})
	}
	return out, nil
}
