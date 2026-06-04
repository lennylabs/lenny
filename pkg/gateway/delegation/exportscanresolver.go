// SPDX-License-Identifier: MIT

package delegation

import (
	"context"

	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
)

// ChainExportScanResolver resolves a §8.3 contentPolicy.interceptorRef to
// the per-file export-scan chain the gateway runs at the
// PreExportMaterialization phase. It satisfies ExportScanChainResolver.
//
// Per §4.8 lines 1038, 1050 the export-scan phase is not independently
// registerable: it invokes the same named interceptor already in force on
// the parent's DelegationPolicy at PreDelegation. The resolver therefore
// looks the ref up among the gateway interceptor chain's registered
// PreDelegation interceptors (interceptor.Chain.ExportScanChainFor) and
// pairs the resulting single-interceptor chain with an ExportScanContext
// carrying the §11.7 / §16.1 observer (audit events + metrics).
//
// A blank ref, a nil chain, or a ref that names no registered interceptor
// resolves to ErrExportScanUnavailable so a scanExportedFiles: true policy
// with an unresolvable interceptor fails closed (§8.3 rule 1) rather than
// materializing unscanned files.
//
// spec: §4.8 lines 1036-1050; §8.3 lines 160-181.
type ChainExportScanResolver struct {
	chain    *interceptor.Chain
	observer interceptor.ExportScanObserver
}

// NewChainExportScanResolver returns a resolver backed by the gateway
// interceptor chain and the export-scan observer. A nil observer disables
// per-file audit/metric emission; the scan still runs.
func NewChainExportScanResolver(chain *interceptor.Chain, observer interceptor.ExportScanObserver) *ChainExportScanResolver {
	return &ChainExportScanResolver{chain: chain, observer: observer}
}

// ResolveExportScanChain implements ExportScanChainResolver.
func (r *ChainExportScanResolver) ResolveExportScanChain(_ context.Context, _ string, interceptorRef string) (*interceptor.Chain, interceptor.ExportScanContext, error) {
	if r == nil || r.chain == nil {
		return nil, interceptor.ExportScanContext{}, ErrExportScanUnavailable
	}
	sub, ok := r.chain.ExportScanChainFor(interceptorRef)
	if !ok {
		return nil, interceptor.ExportScanContext{}, ErrExportScanUnavailable
	}
	// Pool and PolicyName are stamped by materializeExport from the
	// per-delegation context; the resolver supplies the ref and observer.
	return sub, interceptor.ExportScanContext{
		InterceptorRef: interceptorRef,
		Observer:       r.observer,
	}, nil
}

var _ ExportScanChainResolver = (*ChainExportScanResolver)(nil)
