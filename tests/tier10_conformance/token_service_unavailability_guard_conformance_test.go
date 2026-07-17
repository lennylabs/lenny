// SPDX-License-Identifier: MIT

//go:build conformance

// Tier-10 conformance case for the §4.9 Token Service unavailability guard's
// adapter enforcement point. A conforming adapter, on ExtendCredentialLease for
// a live direct-mode lease, moves the enforced expiry deadline to the later
// deadline the gateway supplies without emitting a credentials_rotated
// lifecycle event and without altering the credential file's contents. The RPC
// is a timer-only extension: it re-delivers no credential material and runs no
// credential-rebind handshake, so a runtime bound to the direct-mode credential
// file never rebinds on an extension.
//
// This case drives the exported adapter.Server directly (the recycle-scrub and
// concurrent-slot conformance cases establish the pattern) with a real
// lifecycle channel and an external runtime peer that speaks the §4.7 JSONL
// lifecycle protocol and advertises credential_rotation, so a non-conforming
// adapter that mishandled the extension as a rotation would send
// credentials_rotated over the wire the peer observes.
//
// spec: §4.9 (line 1470, adapter ExtendCredentialLease re-arms the direct-mode
// timer without rewriting material), §4.7 (runtime adapter lifecycle).
package tier10_conformance_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/adapter/credfile"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// guardLifecycleFrame is the subset of a §4.7 runtime<->adapter lifecycle JSONL
// frame this case needs; field names match the §4.7 message-schema table.
type guardLifecycleFrame struct {
	Type            string   `json:"type"`
	ProtocolVersion string   `json:"protocolVersion,omitempty"`
	Capabilities    []string `json:"capabilities,omitempty"`
	Provider        string   `json:"provider,omitempty"`
	LeaseID         string   `json:"leaseId,omitempty"`
}

// guardRuntimePeer is the external §4.7 lifecycle runtime: it completes the
// handshake advertising credential_rotation and can assert that the adapter
// sends no frame (in particular no credentials_rotated) within a window.
type guardRuntimePeer struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
	enc  *json.Encoder
}

// dialGuardRuntimePeer connects to the adapter lifecycle socket and completes
// the lifecycle_capabilities / lifecycle_support handshake, advertising
// credential_rotation so a rotation the adapter (wrongly) initiated would reach
// this peer as credentials_rotated.
func dialGuardRuntimePeer(t *testing.T, socketPath string) *guardRuntimePeer {
	t.Helper()
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial lifecycle socket: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	p := &guardRuntimePeer{t: t, conn: conn, r: bufio.NewReader(conn), enc: json.NewEncoder(conn)}

	cap := p.read()
	if cap.Type != "lifecycle_capabilities" {
		t.Fatalf("handshake: got %q, want lifecycle_capabilities", cap.Type)
	}
	if err := p.enc.Encode(guardLifecycleFrame{
		Type:            "lifecycle_support",
		ProtocolVersion: "1.0",
		Capabilities:    []string{"checkpoint", "interrupt", "credential_rotation", "deadline_signal"},
	}); err != nil {
		t.Fatalf("send lifecycle_support: %v", err)
	}
	return p
}

func (p *guardRuntimePeer) read() guardLifecycleFrame {
	p.t.Helper()
	line, err := p.r.ReadBytes('\n')
	if err != nil {
		p.t.Fatalf("read lifecycle frame: %v", err)
	}
	var f guardLifecycleFrame
	if err := json.Unmarshal(line, &f); err != nil {
		p.t.Fatalf("decode lifecycle frame: %v", err)
	}
	return f
}

// expectNoRotation asserts the adapter sends no frame within d. A conforming
// ExtendCredentialLease sends nothing on the lifecycle channel; an adapter that
// mishandled it as a rotation would send credentials_rotated.
func (p *guardRuntimePeer) expectNoRotation(d time.Duration) {
	p.t.Helper()
	_ = p.conn.SetReadDeadline(time.Now().Add(d))
	defer func() { _ = p.conn.SetReadDeadline(time.Time{}) }()
	line, err := p.r.ReadBytes('\n')
	if err == nil {
		p.t.Fatalf("adapter sent a lifecycle frame on ExtendCredentialLease: %s", strings.TrimSpace(string(line)))
	}
	if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
		p.t.Fatalf("expected read timeout (no rotation frame), got %v", err)
	}
}

// credentialFileBytes reads the raw adapter credential file under dir. A
// missing file yields nil.
func credentialFileBytes(t *testing.T, dir string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, credfile.FileName))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read credential file: %v", err)
	}
	return data
}

// TestExtendCredentialLeaseConformanceTimerOnly pins the conformance contract
// for the §4.9 adapter enforcement point: ExtendCredentialLease on a live
// direct-mode lease moves the enforced deadline (the credential file survives
// past the original short expiry the re-armed timer replaced) while emitting no
// credentials_rotated lifecycle event and leaving the credential-file contents
// byte-for-byte unchanged.
//
// spec: §4.9 (line 1470, timer-only extension), §4.7 (runtime adapter)
//
// diagnosis: a conforming adapter mishandled a timer-only extension as a
// material rotation — it re-delivered credential material (the file bytes
// changed), fired the credentials_rotated rebind handshake (the peer saw a
// frame), or failed to move the enforced deadline (the file was deleted at the
// original expiry).
func TestExtendCredentialLeaseConformanceTimerOnly_spec_4_9(t *testing.T) {
	sockDir, err := os.MkdirTemp("", "lenny-guard-*")
	if err != nil {
		t.Fatalf("temp socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	socketPath := filepath.Join(sockDir, "lc.sock")

	lc, err := adapter.NewLifecycleChannel(socketPath)
	if err != nil {
		t.Fatalf("new lifecycle channel: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = lc.Run(ctx) }()

	base := t.TempDir()
	credsDir := filepath.Join(base, "run", "lenny")
	if err := os.MkdirAll(credsDir, 0o755); err != nil {
		t.Fatalf("make credentials dir: %v", err)
	}
	s := adapter.New("guard-conformance")
	s.CredentialsDir = credsDir
	s.Lifecycle = lc

	// A direct-mode lease with a short expiry: without the extension its real
	// adapter timer fires within the test window and deletes the file entry.
	originalExpiry := time.Now().Add(time.Second)
	if _, err := s.AssignCredentials(ctx, &adapterv1.AssignCredentialsRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-conformance"},
		Leases: map[string]*adapterv1.CredentialLease{
			"anthropic_direct": {
				LeaseId:         "l-live",
				Provider:        "anthropic_direct",
				Payload:         []byte(`{"deliveryMode":"direct","materializedConfig":{"apiKey":"sk-ant-x"}}`),
				ExpiresAtUnixMs: originalExpiry.UnixMilli(),
			},
		},
	}); err != nil {
		t.Fatalf("AssignCredentials: %v", err)
	}

	before := credentialFileBytes(t, credsDir)
	if len(before) == 0 || !strings.Contains(string(before), "anthropic_direct") {
		t.Fatalf("credential file missing the provider entry after assignment: %q", before)
	}

	peer := dialGuardRuntimePeer(t, socketPath)

	// Extend the live lease to a far-later deadline.
	newExpiry := time.Now().Add(time.Hour)
	if _, err := s.ExtendCredentialLease(ctx, &adapterv1.ExtendCredentialLeaseRequest{
		SessionId:       &adapterv1.SessionId{Value: "sess-conformance"},
		Provider:        "anthropic_direct",
		LeaseId:         "l-live",
		ExpiresAtUnixMs: newExpiry.UnixMilli(),
	}); err != nil {
		t.Fatalf("ExtendCredentialLease: %v", err)
	}

	// The credential file contents are byte-for-byte unchanged: no material
	// re-delivered.
	after := credentialFileBytes(t, credsDir)
	if string(after) != string(before) {
		t.Fatalf("ExtendCredentialLease rewrote the credential file:\n before=%q\n after =%q", before, after)
	}

	// No credentials_rotated lifecycle event: the extension runs no rebind
	// handshake.
	peer.expectNoRotation(300 * time.Millisecond)

	// The enforced deadline moved: past the original one-second expiry the
	// re-armed timer replaced, the credential file still carries the provider.
	deadline := originalExpiry.Add(400 * time.Millisecond)
	for time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	got := credentialFileBytes(t, credsDir)
	if !strings.Contains(string(got), "anthropic_direct") {
		t.Fatal("credential file lost the provider entry past the original expiry: ExtendCredentialLease did not move the enforced deadline")
	}
}
