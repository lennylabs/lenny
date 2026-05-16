// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"sort"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter/credfile"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// AssignCredentials materializes the session's per-provider credential
// leases into the pod's credential file before the runtime starts
// (§4.7 item 4). The leases replace any previously assigned set. The
// request payload carries credential material; per §4.7 item 6 it must
// never be written to access logs or telemetry.
func (s *Server) AssignCredentials(_ context.Context, req *adapterv1.AssignCredentialsRequest) (*adapterv1.AssignCredentialsResponse, error) {
	sessionID := req.GetSessionId().GetValue()
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "AssignCredentials requires a session id")
	}
	if s.CredentialsDir == "" {
		return nil, status.Error(codes.FailedPrecondition,
			"adapter is not configured with a credentials directory")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.credSessionID != "" && s.credSessionID != sessionID {
		return nil, status.Errorf(codes.FailedPrecondition,
			"credentials are already assigned to session %s", s.credSessionID)
	}

	leases := make(map[string]*adapterv1.CredentialLease, len(req.GetLeases()))
	for provider, lease := range req.GetLeases() {
		leases[provider] = lease
	}
	if err := s.writeCredentialFile(leases); err != nil {
		return nil, err
	}
	s.credSessionID = sessionID
	s.credLeases = leases
	return &adapterv1.AssignCredentialsResponse{}, nil
}

// RotateCredentials replaces the leases for the providers named in the
// request and rewrites the credential file (§4.7 item 4). Leases for
// providers not named in the request are retained. The request payload
// carries credential material; per §4.7 item 6 it must never be
// written to access logs or telemetry.
func (s *Server) RotateCredentials(_ context.Context, req *adapterv1.RotateCredentialsRequest) (*adapterv1.RotateCredentialsResponse, error) {
	sessionID := req.GetSessionId().GetValue()
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "RotateCredentials requires a session id")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkCredentialSession(sessionID); err != nil {
		return nil, err
	}

	leases := s.cloneCredentialLeases()
	for provider, lease := range req.GetLeases() {
		leases[provider] = lease
	}
	if err := s.writeCredentialFile(leases); err != nil {
		return nil, err
	}
	s.credLeases = leases
	return &adapterv1.RotateCredentialsResponse{}, nil
}

// RevokeCredentials removes the named providers' leases and rewrites
// the credential file without them (§4.7 item 4).
func (s *Server) RevokeCredentials(_ context.Context, req *adapterv1.RevokeCredentialsRequest) (*adapterv1.RevokeCredentialsResponse, error) {
	sessionID := req.GetSessionId().GetValue()
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "RevokeCredentials requires a session id")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkCredentialSession(sessionID); err != nil {
		return nil, err
	}

	leases := s.cloneCredentialLeases()
	for _, provider := range req.GetProviders() {
		delete(leases, provider)
	}
	if err := s.writeCredentialFile(leases); err != nil {
		return nil, err
	}
	s.credLeases = leases
	return &adapterv1.RevokeCredentialsResponse{}, nil
}

// checkCredentialSession confirms credentials were assigned for
// sessionID and the adapter is configured to write them. Callers hold
// s.mu.
func (s *Server) checkCredentialSession(sessionID string) error {
	if s.CredentialsDir == "" {
		return status.Error(codes.FailedPrecondition,
			"adapter is not configured with a credentials directory")
	}
	if s.credSessionID == "" {
		return status.Error(codes.FailedPrecondition,
			"no credentials have been assigned to this pod")
	}
	if s.credSessionID != sessionID {
		return status.Errorf(codes.NotFound,
			"credentials are assigned to session %s, not %s", s.credSessionID, sessionID)
	}
	return nil
}

// cloneCredentialLeases returns a copy of the current lease set so a
// rewrite is computed off-line and committed only when the file write
// succeeds. Callers hold s.mu.
func (s *Server) cloneCredentialLeases() map[string]*adapterv1.CredentialLease {
	leases := make(map[string]*adapterv1.CredentialLease, len(s.credLeases))
	for provider, lease := range s.credLeases {
		leases[provider] = lease
	}
	return leases
}

// writeCredentialFile materializes leases to the credential file,
// ordered by provider for a deterministic file. Callers hold s.mu.
func (s *Server) writeCredentialFile(leases map[string]*adapterv1.CredentialLease) error {
	providers := make([]string, 0, len(leases))
	for provider := range leases {
		providers = append(providers, provider)
	}
	sort.Strings(providers)

	ordered := make([]*adapterv1.CredentialLease, 0, len(providers))
	for _, provider := range providers {
		ordered = append(ordered, leases[provider])
	}
	if err := credfile.Write(s.CredentialsDir, ordered); err != nil {
		return status.Errorf(codes.Internal, "write credential file: %v", err)
	}
	return nil
}
