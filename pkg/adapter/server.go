// SPDX-License-Identifier: MIT

// Package adapter implements the §4.7 runtime adapter: the gRPC server
// that runs as a sidecar in every Lenny agent pod and bridges the
// gateway to the pod's runtime binary.
//
// Server implements the generated adapterv1.AdapterServer contract. It
// embeds UnimplementedAdapterServer, so RPCs that are not yet built
// return codes.Unimplemented rather than breaking the build as the
// contract grows. This file carries the version-negotiation handshake;
// the workspace, session, credential, and lifecycle RPCs are
// implemented in later increments.
package adapter

import (
	"context"
	"sync"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// ProtocolVersionV1 is the adapter↔gateway protocol version for the v1
// contract (§15.5).
const ProtocolVersionV1 = "1.0.0"

// Server implements adapterv1.AdapterServer. It embeds the generated
// UnimplementedAdapterServer for forward compatibility.
type Server struct {
	adapterv1.UnimplementedAdapterServer

	// ProtocolVersions are the adapter↔gateway protocol versions this
	// adapter speaks. NegotiateVersion selects from this set.
	ProtocolVersions []string
	// Capabilities are the capability tokens the adapter advertises
	// during negotiation, for example preConnect or fullLifecycle.
	// They start empty and grow as the adapter implements the
	// corresponding features.
	Capabilities []string
	// Version is the adapter build version, surfaced for observability.
	Version string

	// WorkspaceRoot is the directory StartSession materializes the
	// session workspace into — the pod's /workspace/current.
	WorkspaceRoot string
	// CredentialsDir is the directory the credential RPCs materialize
	// the §4.7 credential file into — the pod's /run/lenny.
	CredentialsDir string
	// Runtime manages the pod's runtime process. StartSession starts it
	// once the workspace is prepared.
	Runtime RuntimeProcess
	// Checkpoints stores the §4.4 workspace checkpoints the Checkpoint
	// RPC produces. Nil leaves Checkpoint Unimplemented.
	Checkpoints CheckpointSink
	// Restorer loads the §4.4 workspace checkpoints the Resume RPC
	// restores from. Nil leaves Resume Unimplemented.
	Restorer CheckpointSource
	// Usage reports the session's token and wall-clock accounting the
	// ReportUsage RPC returns. Nil leaves ReportUsage Unimplemented.
	Usage UsageMeter

	// mu guards sessionID and the credential fields.
	mu sync.Mutex
	// sessionID is the session currently assigned to the pod, empty
	// when the pod is idle. Per §6.1 a session-mode pod is
	// one-session-only.
	sessionID string
	// credSessionID is the session the current credential leases were
	// assigned for, empty when none are assigned.
	credSessionID string
	// credLeases is the credential lease set materialized into the
	// credential file, keyed by provider.
	credLeases map[string]*adapterv1.CredentialLease
}

// New returns a Server advertising the given build version and the v1
// protocol contract. Capabilities start empty.
func New(version string) *Server {
	return &Server{
		ProtocolVersions: []string{ProtocolVersionV1},
		Version:          version,
	}
}

// NegotiateVersion performs the §4.7 and §15.5 gateway↔adapter
// handshake. It selects the gateway's highest-preference protocol
// version that the adapter also speaks. When the two sets do not
// overlap the response is marked incompatible, and the gateway tears
// the connection down and evicts the pod.
func (s *Server) NegotiateVersion(_ context.Context, req *adapterv1.NegotiateVersionRequest) (*adapterv1.NegotiateVersionResponse, error) {
	resp := &adapterv1.NegotiateVersionResponse{
		Capabilities:   s.Capabilities,
		AdapterVersion: s.Version,
	}
	selected := highestCommonVersion(req.GetAcceptedProtocolVersions(), s.ProtocolVersions)
	if selected == "" {
		resp.Incompatible = true
		return resp, nil
	}
	resp.SelectedProtocolVersion = selected
	return resp, nil
}

// highestCommonVersion returns the first entry of gatewayAccepted that
// also appears in adapterSupported. The gateway orders its accepted
// list highest-preference first (§4.7), so the first match is the
// highest mutually-supported version. An empty string means the sets
// do not overlap.
func highestCommonVersion(gatewayAccepted, adapterSupported []string) string {
	supported := make(map[string]bool, len(adapterSupported))
	for _, v := range adapterSupported {
		supported[v] = true
	}
	for _, v := range gatewayAccepted {
		if supported[v] {
			return v
		}
	}
	return ""
}
