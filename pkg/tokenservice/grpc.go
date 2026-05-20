// SPDX-License-Identifier: MIT

package tokenservice

import (
	"context"
	"errors"

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
	assign *credassign.Service
	leases credleasestore.LeaseStore
}

// NewGRPCServer returns a Token Service gRPC server backed by an
// already-wired credential-assignment service. The caller registers
// every available §4.9 credential pool on assign before serving.
func NewGRPCServer(assign *credassign.Service, leases credleasestore.LeaseStore) *GRPCServer {
	return &GRPCServer{assign: assign, leases: leases}
}

// AssignCredentials materializes one §4.9 credential lease per
// requested pool and returns the resulting map keyed by provider id.
// A request that names an unregistered pool fails the whole call;
// the gateway is expected to validate pool membership in admission
// before reaching this RPC.
func (s *GRPCServer) AssignCredentials(ctx context.Context, req *tokensv1.AssignCredentialsRequest) (*tokensv1.AssignCredentialsResponse, error) {
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
		lease, err := s.assign.Assign(poolID, req.SessionId, req.PodSpiffeUri)
		if err != nil {
			if errors.Is(err, credassign.ErrPoolNotFound) {
				return nil, status.Errorf(codes.NotFound,
					"credential pool %q is not registered", poolID)
			}
			if errors.Is(err, credential.ErrPoolExhausted) {
				return nil, status.Errorf(codes.ResourceExhausted,
					"credential pool %q is exhausted", poolID)
			}
			return nil, status.Errorf(codes.Internal,
				"assign from pool %q: %v", poolID, err)
		}
		out[string(lease.Provider)] = leaseToProto(lease)
	}
	return &tokensv1.AssignCredentialsResponse{Leases: out}, nil
}

// RotateCredentials releases the existing lease and mints a fresh
// lease from the same pool, bound to the same session and SPIFFE
// identity. User-backed lease rotation is a v2 follow-on; the RPC
// returns Unimplemented when called on a SourceUser lease.
func (s *GRPCServer) RotateCredentials(ctx context.Context, req *tokensv1.RotateCredentialsRequest) (*tokensv1.RotateCredentialsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if req.TenantId == "" {
		return nil, status.Error(codes.InvalidArgument, "tenant_id is required")
	}
	if req.LeaseId == "" {
		return nil, status.Error(codes.InvalidArgument, "lease_id is required")
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
	fresh, err := s.assign.Assign(old.PoolID, old.SessionID, old.SpiffeURI)
	if err != nil {
		if errors.Is(err, credassign.ErrPoolNotFound) {
			return nil, status.Errorf(codes.NotFound,
				"credential pool %q is no longer registered", old.PoolID)
		}
		if errors.Is(err, credential.ErrPoolExhausted) {
			return nil, status.Errorf(codes.ResourceExhausted,
				"credential pool %q is exhausted on rotate", old.PoolID)
		}
		return nil, status.Errorf(codes.Internal,
			"rotate via pool %q: %v", old.PoolID, err)
	}
	return &tokensv1.RotateCredentialsResponse{Lease: leaseToProto(fresh)}, nil
}

// RevokeCredentials releases a lease. The credential's session-slot
// accounting is decremented and the lease store entry is removed so
// the lease token can no longer authenticate a proxy request.
func (s *GRPCServer) RevokeCredentials(ctx context.Context, req *tokensv1.RevokeCredentialsRequest) (*tokensv1.RevokeCredentialsResponse, error) {
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
	return &tokensv1.RevokeCredentialsResponse{}, nil
}

// leaseToProto encodes the in-process Lease record on the wire.
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
	return out
}
