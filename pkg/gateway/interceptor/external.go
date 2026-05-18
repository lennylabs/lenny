// SPDX-License-Identifier: MIT

package interceptor

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"

	interceptorv1 "github.com/lennylabs/lenny/pkg/proto/interceptor/v1"
)

// InterceptClient is the subset of the generated §4 RequestInterceptor
// gRPC client the external adapter calls. The generated
// interceptorv1.RequestInterceptorClient satisfies it directly; a test
// supplies a fake. Keeping the adapter to this interface keeps it
// testable without a live gRPC connection.
type InterceptClient interface {
	Intercept(ctx context.Context, in *interceptorv1.InterceptRequest, opts ...grpc.CallOption) (*interceptorv1.InterceptResponse, error)
}

// External adapts a §4 gRPC RequestInterceptor service to the in-process
// Interceptor interface so a deployer-supplied interceptor runs in the
// same per-phase Chain as the built-ins. It reports Builtin() == false,
// so Register subjects it to the reserved-priority ceiling and the
// PreAuth restriction. A REJECT from the remote service blocks the
// request and a MODIFY rewrites the payload, identically to the
// in-process path. A transport error or timeout is returned to the Chain
// as a Go error, which the Chain resolves with this interceptor's
// FailPolicy.
type External struct {
	name       string
	priority   int32
	failPolicy FailPolicy
	timeout    time.Duration
	client     InterceptClient
}

// ExternalConfig configures an External interceptor.
type ExternalConfig struct {
	// Name identifies the interceptor in audit records and errors. It is
	// required.
	Name string

	// Priority orders execution within a phase; lower runs first. An
	// external interceptor must use a priority above ReservedPriorityCeiling
	// (Register rejects a lower value with ErrInvalidPriority). When zero,
	// DefaultExternalPriority is applied.
	Priority int32

	// FailPolicy governs the Chain's response to a transport error or
	// timeout. When empty, FailClosed is applied so a degraded interceptor
	// service cannot silently bypass policy.
	FailPolicy FailPolicy

	// Timeout is the per-call deadline. A non-positive value selects
	// DefaultTimeout.
	Timeout time.Duration

	// Client is the gRPC RequestInterceptor client the adapter calls. It
	// is required. In production this is interceptorv1.NewRequestInterceptorClient
	// over a dialed connection.
	Client InterceptClient
}

// DefaultExternalPriority is the priority applied to an external
// interceptor registered with a zero Priority. The §4.8 registration
// default is 500.
const DefaultExternalPriority = 500

// NewExternal builds an External interceptor from cfg. It validates that
// a name and a client are supplied; it does not validate the priority or
// phase, because Register applies the ErrInvalidPriority and
// ErrInvalidPhase checks against the live Chain.
func NewExternal(cfg ExternalConfig) (*External, error) {
	if cfg.Name == "" {
		return nil, fmt.Errorf("interceptor: external interceptor requires a name")
	}
	if cfg.Client == nil {
		return nil, fmt.Errorf("interceptor: external interceptor %q requires a gRPC client", cfg.Name)
	}
	priority := cfg.Priority
	if priority == 0 {
		priority = DefaultExternalPriority
	}
	failPolicy := cfg.FailPolicy
	if failPolicy == "" {
		failPolicy = FailClosed
	}
	return &External{
		name:       cfg.Name,
		priority:   priority,
		failPolicy: failPolicy,
		timeout:    cfg.Timeout,
		client:     cfg.Client,
	}, nil
}

// Name identifies the interceptor.
func (e *External) Name() string { return e.name }

// Priority orders execution within a phase; lower runs first.
func (e *External) Priority() int32 { return e.priority }

// Builtin reports false: an External is a deployer-supplied interceptor,
// so Register applies the reserved-priority ceiling and the PreAuth
// restriction to it.
func (e *External) Builtin() bool { return false }

// FailPolicy governs the Chain's response to a transport error or
// timeout.
func (e *External) FailPolicy() FailPolicy { return e.failPolicy }

// Timeout is the per-call deadline. A non-positive value selects
// DefaultTimeout.
func (e *External) Timeout() time.Duration { return e.timeout }

// Intercept calls the remote §4 RequestInterceptor service and maps its
// InterceptResponse onto a Result. A transport error or a context
// deadline is returned as a Go error so the Chain applies this
// interceptor's FailPolicy; a malformed Action in an otherwise-successful
// response is also returned as an error. On REJECT the Result carries the
// remote reason; on MODIFY it carries the rewritten payload.
func (e *External) Intercept(ctx context.Context, req Request) (Result, error) {
	resp, err := e.client.Intercept(ctx, &interceptorv1.InterceptRequest{
		Phase:     string(req.Phase),
		SessionId: req.SessionID,
		TenantId:  req.TenantID,
		Content:   req.Content,
		Metadata:  req.Metadata,
	})
	if err != nil {
		return Result{}, fmt.Errorf("interceptor %q gRPC call failed: %w", e.name, err)
	}
	switch resp.GetAction() {
	case interceptorv1.InterceptResponse_ALLOW:
		return Result{Action: ActionAllow}, nil
	case interceptorv1.InterceptResponse_REJECT:
		return Result{
			Action: ActionReject,
			Reason: resp.GetReason(),
		}, nil
	case interceptorv1.InterceptResponse_MODIFY:
		return Result{
			Action:          ActionModify,
			ModifiedContent: resp.GetModifiedContent(),
		}, nil
	default:
		return Result{}, fmt.Errorf("interceptor %q returned unknown action %d", e.name, resp.GetAction())
	}
}

// RegisterExternal builds an External interceptor from cfg and registers
// it on c for phase. The interceptor runs in the same Chain as the
// built-ins, subject to the same priority ordering and FailPolicy. The
// ErrInvalidPriority and ErrInvalidPhase registration checks apply: an
// external interceptor must register above ReservedPriorityCeiling and
// may not target PhasePreAuth.
func (c *Chain) RegisterExternal(phase Phase, cfg ExternalConfig) (*External, error) {
	ic, err := NewExternal(cfg)
	if err != nil {
		return nil, err
	}
	if err := c.Register(phase, ic); err != nil {
		return nil, err
	}
	return ic, nil
}
