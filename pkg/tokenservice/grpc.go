// SPDX-License-Identifier: MIT

package tokenservice

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credleasestore"
	tokensv1 "github.com/lennylabs/lenny/pkg/proto/tokenservice/v1"
)

// GRPCServer is the §4.3 / §12.2.4 Token Service gRPC server. The
// gateway calls it over mTLS to materialize, rotate, and revoke
// credential leases without ever holding KMS decrypt rights for the
// underlying credential material itself.
//
// The server is intentionally a thin adapter over the in-process §4.9
// credential-assignment service (`pkg/gateway/credassign`): the same
// pool-selection strategy, lease minting, and lease-store recording
// logic the §4.9 in-process path uses today now sits behind a gRPC
// boundary. The Token Service deploys this server as the public
// surface; the gateway's eventual switch from the in-process
// MintLease call to the gRPC client lands as a separate change.
type GRPCServer struct {
	tokensv1.UnimplementedTokenServiceServer
	assign  *credassign.Service
	leases  credleasestore.LeaseStore
	auditor Auditor
	metrics Metrics
	prober  SecretAccessProber
}

// NewGRPCServer returns a Token Service gRPC server backed by an
// already-wired credential-assignment service. The caller registers
// every available §4.9 credential pool on assign before serving.
func NewGRPCServer(assign *credassign.Service, leases credleasestore.LeaseStore) *GRPCServer {
	return &GRPCServer{assign: assign, leases: leases, metrics: NoMetrics{}}
}

// SetAuditor wires the §13.3 audit emitter onto the gRPC server. The
// RevokeCredentials and RotateCredentials handlers emit
// `token.revoked` rows through it on every successful revocation /
// rotation. spec: §13.3 line 597.
func (s *GRPCServer) SetAuditor(a Auditor) { s.auditor = a }

// SetMetrics wires the §16.1 Token Service catalog onto the gRPC
// server. The AssignCredentials, RotateCredentials, and
// RevokeCredentials handlers record latency and error counts through
// it. spec: §16.1 lenny_token_service_request_duration_seconds /
// lenny_token_service_errors_total.
func (s *GRPCServer) SetMetrics(m Metrics) {
	if m == nil {
		m = NoMetrics{}
	}
	s.metrics = m
}

// AssignCredentials materializes one §4.9 credential lease per
// requested pool and returns the resulting map keyed by provider id.
// A request that names an unregistered pool fails the whole call;
// the gateway is expected to validate pool membership in admission
// before reaching this RPC.
func (s *GRPCServer) AssignCredentials(ctx context.Context, req *tokensv1.AssignCredentialsRequest) (resp *tokensv1.AssignCredentialsResponse, err error) {
	defer s.observe("assign", time.Now())(&err)
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if req.TenantId == "" {
		return nil, status.Error(codes.InvalidArgument, "tenant_id is required")
	}
	if req.SessionId == "" {
		return nil, status.Error(codes.InvalidArgument, "session_id is required")
	}
	if len(req.PoolIds) == 0 {
		return nil, status.Error(codes.InvalidArgument, "pool_ids must name at least one credential pool")
	}
	out := make(map[string]*tokensv1.CredentialLease, len(req.PoolIds))
	for _, poolID := range req.PoolIds {
		lease, lerr := s.assign.Assign(poolID, req.SessionId, req.PodSpiffeUri, req.TenantId)
		if lerr != nil {
			if errors.Is(lerr, credassign.ErrPoolNotFound) {
				return nil, status.Errorf(codes.NotFound,
					"credential pool %q is not registered", poolID)
			}
			if errors.Is(lerr, credential.ErrPoolExhausted) {
				return nil, status.Errorf(codes.ResourceExhausted,
					"credential pool %q is exhausted", poolID)
			}
			if me := materializationError(lerr); me != nil {
				// §4.9 line 1298: a missing required materializedConfig
				// field fails issuance with CREDENTIAL_MATERIALIZATION_ERROR
				// (category INTERNAL) but surfaces to the client as
				// CREDENTIAL_POOL_EXHAUSTED. ResourceExhausted maps to that
				// client code via the gateway's mapAssignError.
				return nil, status.Errorf(codes.ResourceExhausted,
					"credential pool %q: %s: %v", poolID, credential.CodeCredentialMaterializationError, me)
			}
			return nil, status.Errorf(codes.Internal,
				"assign from pool %q: %v", poolID, lerr)
		}
		out[string(lease.Provider)] = s.leaseToProtoWithSecret(lease)
	}
	return &tokensv1.AssignCredentialsResponse{Leases: out}, nil
}

// RotateCredentials releases the existing lease and mints a fresh
// lease from the same pool, bound to the same session and SPIFFE
// identity. User-backed lease rotation is a v2 follow-on; the RPC
// returns Unimplemented when called on a SourceUser lease.
func (s *GRPCServer) RotateCredentials(ctx context.Context, req *tokensv1.RotateCredentialsRequest) (resp *tokensv1.RotateCredentialsResponse, err error) {
	defer s.observe("rotate", time.Now())(&err)
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if req.TenantId == "" {
		return nil, status.Error(codes.InvalidArgument, "tenant_id is required")
	}
	if req.LeaseId == "" {
		return nil, status.Error(codes.InvalidArgument, "lease_id is required")
	}
	// §4.9 line 1413: every RotateCredentials carries a rotationTrigger
	// identifying the cause. An empty value is treated as a fault trigger
	// (fail-closed: the §4.7 ceiling applies and the old credential is
	// assumed untrustworthy); a non-empty value that is not one of the
	// seven §4.9 triggers is rejected so a typo cannot silently disable
	// the ceiling discipline. F-13.3.10.
	trigger := credential.RotationTrigger(req.RotationTrigger)
	if req.RotationTrigger == "" {
		trigger = credential.TriggerFaultProviderUnavailable
	} else if !trigger.IsValid() {
		return nil, status.Errorf(codes.InvalidArgument,
			"rotation_trigger %q is not a §4.9 trigger", req.RotationTrigger)
	}
	old, ok := s.leases.GetByID(req.LeaseId)
	if !ok {
		return nil, status.Errorf(codes.NotFound, "lease %q not found", req.LeaseId)
	}
	if old.Source != credential.SourcePool {
		return nil, status.Errorf(codes.Unimplemented,
			"user-backed lease rotation is a v2 follow-on (lease source: %q)", old.Source)
	}
	s.assign.Release(req.LeaseId)
	// §13.3 line 597: rotation revokes the previous lease's lease
	// token. Emit token.revoked so SIEM has a revocation receipt; the
	// §4.9 rotationTrigger is recorded as the revocation reason so the
	// audit trail distinguishes a proactive renewal from a fault- or
	// operator-driven rotation. F-13.3.10.
	s.emitRevocation(ctx, req.TenantId, "", req.LeaseId, "rotation:"+string(trigger))
	fresh, rerr := s.assign.Assign(old.PoolID, old.SessionID, old.SpiffeURI, old.TenantID)
	if rerr != nil {
		if errors.Is(rerr, credassign.ErrPoolNotFound) {
			return nil, status.Errorf(codes.NotFound,
				"credential pool %q is no longer registered", old.PoolID)
		}
		if errors.Is(rerr, credential.ErrPoolExhausted) {
			return nil, status.Errorf(codes.ResourceExhausted,
				"credential pool %q is exhausted on rotate", old.PoolID)
		}
		if me := materializationError(rerr); me != nil {
			// §4.9 line 1298: surfaces to the client as
			// CREDENTIAL_POOL_EXHAUSTED, like the assign path.
			return nil, status.Errorf(codes.ResourceExhausted,
				"credential pool %q: %s: %v", old.PoolID, credential.CodeCredentialMaterializationError, me)
		}
		return nil, status.Errorf(codes.Internal,
			"rotate via pool %q: %v", old.PoolID, rerr)
	}
	return &tokensv1.RotateCredentialsResponse{Lease: s.leaseToProtoWithSecret(fresh)}, nil
}

// RevokeCredentials releases a lease. The credential's session-slot
// accounting is decremented and the lease store entry is removed so
// the lease token can no longer authenticate a proxy request.
func (s *GRPCServer) RevokeCredentials(ctx context.Context, req *tokensv1.RevokeCredentialsRequest) (resp *tokensv1.RevokeCredentialsResponse, err error) {
	defer s.observe("revoke", time.Now())(&err)
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if req.TenantId == "" {
		return nil, status.Error(codes.InvalidArgument, "tenant_id is required")
	}
	if req.LeaseId == "" {
		return nil, status.Error(codes.InvalidArgument, "lease_id is required")
	}
	if _, ok := s.leases.GetByID(req.LeaseId); !ok {
		return nil, status.Errorf(codes.NotFound, "lease %q not found", req.LeaseId)
	}
	s.assign.Release(req.LeaseId)
	// §13.3 line 597: emit `token.revoked` for every successful
	// revocation so SIEM has a revocation receipt and operators can
	// reconstruct when a lease token was deactivated.
	s.emitRevocation(ctx, req.TenantId, "", req.LeaseId, req.Reason)
	return &tokensv1.RevokeCredentialsResponse{}, nil
}

// SetSecretAccessProber wires the §4.9 admin-time RBAC live-probe onto
// the gRPC server. With it set, ProbeSecretAccess runs a
// SelfSubjectAccessReview under the Token Service ServiceAccount. Left
// unset (the dev path with no in-cluster Kubernetes client),
// ProbeSecretAccess returns codes.Unavailable so the gateway admin
// handler maps it to 503 CREDENTIAL_PROBE_UNAVAILABLE and never fails
// open. spec: §4.9 line 1212.
func (s *GRPCServer) SetSecretAccessProber(p SecretAccessProber) { s.prober = p }

// ProbeSecretAccess answers whether the Token Service ServiceAccount can
// read the named Kubernetes Secret. It returns a definitive
// {ALLOWED, DENIED, NOT_FOUND} verdict; a missing prober or any
// non-deterministic evaluation failure is returned as codes.Unavailable
// so the caller distinguishes a denied probe (fix RBAC) from a failed
// probe (fix reachability) and never persists an unprobed secretRef.
//
// spec: §4.9 line 1212.
func (s *GRPCServer) ProbeSecretAccess(ctx context.Context, req *tokensv1.ProbeSecretAccessRequest) (resp *tokensv1.ProbeSecretAccessResponse, err error) {
	defer s.observe("probe_secret_access", time.Now())(&err)
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if req.SecretName == "" {
		return nil, status.Error(codes.InvalidArgument, "secret_name is required")
	}
	if s.prober == nil {
		return nil, status.Error(codes.Unavailable,
			"secret-access probe not configured (Token Service has no in-cluster Kubernetes client)")
	}
	verdict, perr := s.prober.ProbeSecretAccess(ctx, req.Namespace, req.SecretName)
	if perr != nil {
		return nil, status.Errorf(codes.Unavailable, "secret-access probe failed: %v", perr)
	}
	return &tokensv1.ProbeSecretAccessResponse{Verdict: secretVerdictToProto(verdict)}, nil
}

// materializationError returns the *credential.MaterializationError
// wrapped in err, or nil when err is not a materialization failure. The
// §4.9 direct-mode mint path returns it when a Required:yes
// materializedConfig field is absent.
func materializationError(err error) *credential.MaterializationError {
	var me *credential.MaterializationError
	if errors.As(err, &me) {
		return me
	}
	return nil
}

// secretVerdictToProto maps the internal verdict onto the wire enum.
func secretVerdictToProto(v SecretAccessVerdict) tokensv1.Verdict {
	switch v {
	case SecretAccessAllowed:
		return tokensv1.Verdict_VERDICT_ALLOWED
	case SecretAccessDenied:
		return tokensv1.Verdict_VERDICT_DENIED
	case SecretAccessNotFound:
		return tokensv1.Verdict_VERDICT_NOT_FOUND
	default:
		return tokensv1.Verdict_VERDICT_UNSPECIFIED
	}
}

// observe records request duration and error class through the
// configured Metrics. The returned closure is deferred by callers and
// finalizes the histogram + error counter when the RPC returns.
func (s *GRPCServer) observe(operation string, start time.Time) func(*error) {
	return func(errp *error) {
		s.metrics.RecordRequestDuration(operation, time.Since(start))
		if errp != nil && *errp != nil {
			s.metrics.IncErrors(operation, status.Code(*errp).String())
		}
	}
}

// emitRevocation writes `token.revoked` through the configured
// Auditor. A nil auditor is a no-op (the dev path keeps no audit
// chain).
func (s *GRPCServer) emitRevocation(ctx context.Context, tenantID, sub, leaseID, reason string) {
	if s.auditor == nil || tenantID == "" {
		return
	}
	now := time.Now().UTC()
	// Reuse the tokenservice.Server's payload shape for consistency
	// with the http handler's `token.revoked` payload (sub, jti,
	// reason, timestamp). For credential leases the jti slot carries
	// the lease id; sub is empty because the lease is bound to a
	// session, not an OIDC subject.
	srv := &Server{auditor: s.auditor}
	srv.EmitRevocation(ctx, tenantID, sub, leaseID, reason, now)
}

// leaseToProto encodes the in-process Lease record on the wire. It
// omits the materialized upstream credential; use
// leaseToProtoWithSecret on the AssignCredentials and RotateCredentials
// response paths so the gateway can populate its §4.9 credential cache.
func leaseToProto(l credential.Lease) *tokensv1.CredentialLease {
	out := &tokensv1.CredentialLease{
		LeaseId:         l.LeaseID,
		SessionId:       l.SessionID,
		Provider:        string(l.Provider),
		Source:          string(l.Source),
		PoolId:          l.PoolID,
		CredentialId:    l.CredentialID,
		TenantId:        l.TenantID,
		CredentialRef:   l.CredentialRef,
		DeliveryMode:    string(l.DeliveryMode),
		IssuedAt:        timestamppb.New(l.IssuedAt),
		ExpiresAt:       timestamppb.New(l.ExpiresAt),
		RenewBefore:     timestamppb.New(l.RenewBefore),
		FallbackAllowed: l.FallbackAllowed,
		SpiffeUri:       l.SpiffeURI,
	}
	if l.Proxy != nil {
		out.ProxyUrl = l.Proxy.ProxyURL
		out.ProxyDialect = l.Proxy.ProxyDialect
		out.LeaseToken = l.Proxy.LeaseToken
		out.UpstreamModel = l.Proxy.UpstreamModel
	}
	if len(l.Direct) > 0 {
		// §4.9 direct-mode materializedConfig: the per-provider bundle of
		// real upstream credential fields the gateway forwards to the pod
		// through the adapter credential file. Copy so the wire form does
		// not alias the lease's in-memory map.
		out.MaterializedConfig = make(map[string]string, len(l.Direct))
		for k, v := range l.Direct {
			out.MaterializedConfig[k] = v
		}
	}
	return out
}

// leaseToProtoWithSecret extends leaseToProto with the upstream
// provider secret the gateway needs to inject into upstream LLM calls
// from its §4.9 reverse proxy. The mTLS transport between gateway and
// Token Service protects the secret on the wire; the gateway caches it
// in `pkg/gateway/credcache` and never persists it.
func (s *GRPCServer) leaseToProtoWithSecret(l credential.Lease) *tokensv1.CredentialLease {
	out := leaseToProto(l)
	if secret, ok := s.assign.UpstreamCredential(l); ok {
		out.UpstreamCredential = secret
	}
	return out
}
