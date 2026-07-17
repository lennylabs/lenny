//go:build contract

// SPDX-License-Identifier: MIT

// Package adapter_extendcredlease_test is the Tier 3 contract suite for the
// §4.9 Token Service unavailability guard's ExtendCredentialLease RPC on the
// gateway → pod adapter gRPC contract (schemas/lenny-adapter.proto). The
// gateway calls ExtendCredentialLease when the Token Service circuit breaker
// is open and a still-valid lease reaches its renewBefore deadline: it moves
// the lease's enforced expiry to a later deadline from the lease record it
// already holds, so no Token Service call is made and no credential material
// is re-delivered.
//
// This suite pins the wire contract end to end over a real gRPC connection to
// the production-assembled adapter server: a request carrying session_id,
// provider, lease_id, and expires_at_unix_ms is accepted and returns an empty
// ExtendCredentialLeaseResponse; a request also carrying slot_id routes to the
// §6.1 per-slot dispatch, which re-arms the slot's own expiry timer; and a
// request with an empty session_id is rejected InvalidArgument, mirroring
// RotateCredentials's session-id guard.
//
// spec: §4.9 (line 1470, ExtendCredentialLease wire contract), §4.7 (Gateway
// → Adapter RPC surface), §6.1 (per-slot credential lease timer).
package adapter_extendcredlease_test

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/adapter/slotlayout"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

const (
	// sessionID is the direct-mode session the adapter holds (§6.1 one
	// session per pod). The slot dispatch uses the slot id as its session.
	sessionID = "sess-extend-1"
	provider  = "anthropic_direct"

	// directPayload is a direct-mode lease payload: a direct-mode lease with
	// a positive expiry arms the adapter expiry timer ExtendCredentialLease
	// re-arms. spec: §4.9 line 1149.
	directPayload = `{"deliveryMode":"direct","materializedConfig":{"apiKey":"sk-ant-x"}}`
)

// adapterServer builds the adapter the way cmd/lenny-adapter does for the
// credential RPCs, with the single-session credentials directory and the
// §6.4 per-slot roots resolved so a slot-qualified request routes to the
// per-slot tree. It registers the server over bufconn and returns the
// gateway adapter client and the credentials root the per-slot file nests
// under.
func adapterServer(t *testing.T) (adapterv1.AdapterClient, string) {
	t.Helper()

	base := t.TempDir()
	credsDir := base + "/run/lenny"
	if err := os.MkdirAll(credsDir, 0o755); err != nil {
		t.Fatalf("make credentials dir: %v", err)
	}
	s := adapter.New("contract")
	s.CredentialsDir = credsDir
	s.WorkspaceBase = base + "/workspace"
	s.SessionsRoot = base + "/sessions"
	s.ArtifactsRoot = base + "/artifacts"

	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(s)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return adapterv1.NewAdapterClient(conn), credsDir
}

// directLease is a direct-mode credential lease that arms the adapter
// expiry timer at expiresAt.
func directLease(leaseID string, expiresAt time.Time) *adapterv1.CredentialLease {
	return &adapterv1.CredentialLease{
		LeaseId:         leaseID,
		Provider:        provider,
		Payload:         []byte(directPayload),
		ExpiresAtUnixMs: expiresAt.UnixMilli(),
	}
}

// fileProviders reads a credential file and returns the set of provider
// names present in it. A missing file (the entry was deleted, or none was
// written) yields an empty set.
func fileProviders(t *testing.T, path string) map[string]bool {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]bool{}
	}
	if err != nil {
		t.Fatalf("read credential file %s: %v", path, err)
	}
	var doc struct {
		Providers []map[string]any `json:"providers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decode credential file %s: %v", path, err)
	}
	out := map[string]bool{}
	for _, p := range doc.Providers {
		if name, _ := p["provider"].(string); name != "" {
			out[name] = true
		}
	}
	return out
}

// TestExtendCredentialLeaseAcceptedOverWire pins that a gateway
// ExtendCredentialLease carrying session_id, provider, lease_id, and
// expires_at_unix_ms is accepted by the adapter over the wire and returns an
// empty ExtendCredentialLeaseResponse. A direct-mode lease is assigned first
// so the request re-arms a live expiry timer, the §4.9 guard's direct-mode
// enforcement point.
//
// spec: §4.9 (line 1470 ExtendCredentialLease wire contract), §4.7 (Gateway →
// Adapter RPC surface)
//
// diagnosis: the ExtendCredentialLease RPC is not registered on the Adapter
// gRPC service, or its handler does not accept the session_id / provider /
// lease_id / expires_at_unix_ms fields. Without it the §4.9 Token Service
// unavailability guard has no direct-mode enforcement point and a transient
// Token Service outage drives checkpoint-and-restart into the restart loop
// line 1470 forbids. Confirm schemas/lenny-adapter.proto declares the RPC and
// pkg/adapter implements Server.ExtendCredentialLease.
func TestExtendCredentialLeaseAcceptedOverWire_spec_4_9(t *testing.T) {
	client, _ := adapterServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := client.AssignCredentials(ctx, &adapterv1.AssignCredentialsRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
		Leases:    map[string]*adapterv1.CredentialLease{provider: directLease("l1", time.Now().Add(time.Hour))},
	}); err != nil {
		t.Fatalf("AssignCredentials: %v", err)
	}

	resp, err := client.ExtendCredentialLease(ctx, &adapterv1.ExtendCredentialLeaseRequest{
		SessionId:       &adapterv1.SessionId{Value: sessionID},
		Provider:        provider,
		LeaseId:         "l1",
		ExpiresAtUnixMs: time.Now().Add(90 * time.Minute).UnixMilli(),
	})
	if err != nil {
		t.Fatalf("ExtendCredentialLease over the wire: %v (code %v)", err, status.Code(err))
	}
	if resp == nil {
		t.Fatal("ExtendCredentialLease returned a nil response, want an empty ExtendCredentialLeaseResponse")
	}
}

// TestExtendCredentialLeaseSlotRoutesToSlotDispatch pins that a request also
// carrying slot_id is accepted over the wire and routes to the §6.1 per-slot
// dispatch, which re-arms the slot's own expiry timer. A direct-mode slot
// lease is assigned with a short expiry; the request extends it to a far-later
// deadline. Routing to the slot dispatch stops the slot's short timer and
// re-arms it to the later deadline, so the slot credential-file entry survives
// past the original deadline. Had the request been mis-routed to the
// single-session dispatch (which reads a different timer set), the slot's own
// short timer would fire and delete the entry, so the survival assertion fails
// against a non-slot-aware handler.
//
// spec: §4.9 (line 1470 ExtendCredentialLease wire contract), §6.1 (per-slot
// credential lease timer)
//
// diagnosis: ExtendCredentialLease ignores slot_id and takes the
// single-session path, so a concurrent-pool slot's lease deadline is never
// extended and its still-valid credential is torn down under a transient Token
// Service outage. Confirm Server.ExtendCredentialLease dispatches to
// extendCredentialLeaseSlot when slot_id is set.
func TestExtendCredentialLeaseSlotRoutesToSlotDispatch_spec_6_1(t *testing.T) {
	client, credsDir := adapterServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const slotID = "slot-a"
	shortExpiry := time.Now().Add(time.Second)
	if _, err := client.AssignCredentials(ctx, &adapterv1.AssignCredentialsRequest{
		SessionId: &adapterv1.SessionId{Value: slotID},
		SlotId:    &adapterv1.SlotId{Value: slotID},
		Leases:    map[string]*adapterv1.CredentialLease{provider: directLease("la", shortExpiry)},
	}); err != nil {
		t.Fatalf("AssignCredentials(slot): %v", err)
	}

	slotPaths, err := slotlayout.Resolve(slotlayout.Roots{Credentials: credsDir}, slotID)
	if err != nil {
		t.Fatalf("resolve slot paths: %v", err)
	}
	slotFile := slotPaths.CredentialsFile
	if !fileProviders(t, slotFile)[provider] {
		t.Fatalf("slot credential file missing %s entry before extension", provider)
	}

	resp, err := client.ExtendCredentialLease(ctx, &adapterv1.ExtendCredentialLeaseRequest{
		SessionId:       &adapterv1.SessionId{Value: slotID},
		SlotId:          &adapterv1.SlotId{Value: slotID},
		Provider:        provider,
		LeaseId:         "la",
		ExpiresAtUnixMs: time.Now().Add(2 * time.Hour).UnixMilli(),
	})
	if err != nil {
		t.Fatalf("ExtendCredentialLease(slot) over the wire: %v (code %v)", err, status.Code(err))
	}
	if resp == nil {
		t.Fatal("ExtendCredentialLease(slot) returned a nil response, want an empty ExtendCredentialLeaseResponse")
	}

	// Wait past the original short deadline. Routing to the slot dispatch
	// re-armed the slot timer to the +2h deadline, so the entry survives; a
	// mis-routed extension leaves the slot's 1s timer to fire and delete it.
	for deadline := shortExpiry.Add(time.Second); time.Now().Before(deadline); {
		if !fileProviders(t, slotFile)[provider] {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !fileProviders(t, slotFile)[provider] {
		t.Fatalf("slot credential file lost %s after extension: the slot's original timer fired, so the extension did not route to the slot dispatch", provider)
	}
}

// TestExtendCredentialLeaseEmptySessionRejected pins that a request with an
// empty session_id is rejected with InvalidArgument over the wire, matching
// RotateCredentials's session-id guard. A missing session id cannot be
// attributed to a pod's credential state, so the adapter fails it closed
// rather than re-arming an unrelated provider's timer.
//
// spec: §4.9 (line 1470 ExtendCredentialLease wire contract), §4.7 (Gateway →
// Adapter RPC surface)
//
// diagnosis: ExtendCredentialLease accepts an empty session_id, so a
// mis-addressed extension re-arms a timer for an unowned session. Confirm
// Server.ExtendCredentialLease guards the empty session id with
// codes.InvalidArgument, mirroring RotateCredentials.
func TestExtendCredentialLeaseEmptySessionRejected_spec_4_9(t *testing.T) {
	client, _ := adapterServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.ExtendCredentialLease(ctx, &adapterv1.ExtendCredentialLeaseRequest{
		SessionId:       &adapterv1.SessionId{Value: ""},
		Provider:        provider,
		LeaseId:         "l1",
		ExpiresAtUnixMs: time.Now().Add(time.Hour).UnixMilli(),
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("ExtendCredentialLease empty session err = %v (code %v), want InvalidArgument", err, status.Code(err))
	}
}
