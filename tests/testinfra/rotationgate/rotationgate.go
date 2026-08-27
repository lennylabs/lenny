// SPDX-License-Identifier: MIT

// Package rotationgate drives the §4.7 Full-level credential-rotation
// protocol from an external runtime peer against a real adapter.Server.
//
// The protocol runs over the pod's single CH-RUNTIMEOPS connection: one
// runtime process serves every slot on the pod, so one peer speaks for
// every session bound there. The in-flight completion gate the rotation
// waits on is keyed by provider and counted pod-wide, which is the
// property the suites above unit exercise. The helpers here are the
// wire-level pieces those suites share: the frame subset, the peer, the
// server bring-up, the audit recorder, and the metric readers.
//
// spec: §4.7 (Full-level credential rotation protocol), §6.1 (per-session
// credential lease), §28.5.1 (CH-RUNTIMEOPS)
package rotationgate

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/lennylabs/lenny/pkg/adapter"
)

// Frame is one §4.7 runtime<->adapter lifecycle JSONL frame, in the
// subset these suites need to speak from an external runtime peer. Field
// names match the §4.7 message-schema table (camelCase).
type Frame struct {
	Type            string   `json:"type"`
	ProtocolVersion string   `json:"protocolVersion,omitempty"`
	Capabilities    []string `json:"capabilities,omitempty"`
	Provider        string   `json:"provider,omitempty"`
	CredentialsPath string   `json:"credentialsPath,omitempty"`
	LeaseID         string   `json:"leaseId,omitempty"`
	RequestID       string   `json:"requestId,omitempty"`
	Status          string   `json:"status,omitempty"`
}

// Peer is the external §4.7 CH-RUNTIMEOPS runtime, dialing the adapter's
// Unix socket and driving frames over the wire. It models a direct-mode
// Full-level runtime, which can start an LLM request and then withhold
// the completion frame to hold the in-flight gate open.
type Peer struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
	enc  *json.Encoder
}

// DialPeer connects to the adapter lifecycle socket, completes the
// lifecycle_capabilities / lifecycle_support handshake advertising
// credential_rotation, and returns the connected peer.
func DialPeer(t *testing.T, socketPath string) *Peer {
	t.Helper()
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial lifecycle socket: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	p := &Peer{t: t, conn: conn, r: bufio.NewReader(conn), enc: json.NewEncoder(conn)}

	// The adapter opens with lifecycle_capabilities; the runtime replies
	// with lifecycle_support naming the subset it implements.
	capabilities := p.Read()
	if capabilities.Type != "lifecycle_capabilities" {
		t.Fatalf("handshake: got %q, want lifecycle_capabilities", capabilities.Type)
	}
	p.Send(Frame{
		Type:            "lifecycle_support",
		ProtocolVersion: "1.0",
		Capabilities:    []string{"checkpoint", "interrupt", "credential_rotation", "deadline_signal"},
	})
	return p
}

// Send writes one frame to the adapter.
func (p *Peer) Send(f Frame) {
	p.t.Helper()
	if err := p.enc.Encode(f); err != nil {
		p.t.Fatalf("send %q frame: %v", f.Type, err)
	}
}

// Read blocks for the next frame from the adapter.
func (p *Peer) Read() Frame {
	p.t.Helper()
	line, err := p.r.ReadBytes('\n')
	if err != nil {
		p.t.Fatalf("read lifecycle frame: %v", err)
	}
	var f Frame
	if err := json.Unmarshal(line, &f); err != nil {
		p.t.Fatalf("decode lifecycle frame: %v", err)
	}
	return f
}

// ExpectSilence asserts the adapter sends no frame within d. The
// in-flight gate must hold credentials_rotated while a request for the
// provider is counted as in flight (§4.7 in-flight request completion
// gate).
func (p *Peer) ExpectSilence(d time.Duration) {
	p.t.Helper()
	_ = p.conn.SetReadDeadline(time.Now().Add(d))
	defer func() { _ = p.conn.SetReadDeadline(time.Time{}) }()
	if _, err := p.r.ReadBytes('\n'); err == nil {
		p.t.Fatal("adapter sent a frame while the in-flight gate should have blocked it")
	} else if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
		p.t.Fatalf("expected read timeout while gate holds, got %v", err)
	}
}

// StartWithheldInflight reports one llm_request_started for the provider
// and waits until the adapter's in-flight counter observes it, then
// returns without ever sending the matching llm_request_completed. This
// models the runtime that never completes, and it is how a suite holds
// the pod-wide gate open for a chosen provider.
func (p *Peer) StartWithheldInflight(s *adapter.Server, provider, requestID string) {
	p.t.Helper()
	p.Send(Frame{Type: "llm_request_started", Provider: provider, RequestID: requestID})
	deadline := time.Now().Add(2 * time.Second)
	for s.Lifecycle.InflightCount(provider) != 1 {
		if time.Now().After(deadline) {
			p.t.Fatal("adapter never observed the withheld in-flight request")
		}
		time.Sleep(time.Millisecond)
	}
}

// CeilingAudit captures the durable credential.rotation_ceiling_hit audit
// events the adapter emits to the §4.9.2 EventStore at the ceiling code
// point.
type CeilingAudit struct {
	Hits []adapter.RotationCeilingHit
}

// EmitRotationCeilingHit records one ceiling-hit audit event.
func (r *CeilingAudit) EmitRotationCeilingHit(_ context.Context, e adapter.RotationCeilingHit) {
	r.Hits = append(r.Hits, e)
}

// NewPodAdapter brings up a real adapter.Server bound to a real
// CH-RUNTIMEOPS on a Unix socket, with the pod roots the per-slot trees
// nest under (§6.4). It returns the server, the socket path, and the
// recording audit emitter wired to the §4.9.2 EventStore hook. No session
// is bound yet: the caller assigns credentials for each session it needs,
// and every session it binds shares this one runtime connection.
//
// spec: §6.1; §6.4
func NewPodAdapter(t *testing.T, pool string) (*adapter.Server, string, *CeilingAudit) {
	t.Helper()
	// t.TempDir() embeds the (long) test name, so a socket path under it can
	// overflow the platform sun_path limit (~104 bytes on darwin); bind under
	// os.MkdirTemp's short root to stay within it.
	sockDir, err := os.MkdirTemp("", "lenny-rot-*")
	if err != nil {
		t.Fatalf("temp socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	socketPath := filepath.Join(sockDir, "lc.sock")
	lc, err := adapter.NewRuntimeOps(socketPath)
	if err != nil {
		t.Fatalf("new CH-RUNTIMEOPS socket: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = lc.Run(ctx) }()

	audit := &CeilingAudit{}
	base := t.TempDir()
	s := adapter.New(pool)
	s.CredentialsDir = filepath.Join(base, "run", "lenny")
	s.WorkspaceBase = filepath.Join(base, "workspace")
	s.SessionsRoot = filepath.Join(base, "sessions")
	s.ArtifactsRoot = filepath.Join(base, "artifacts")
	s.CheckpointPoolLabel = pool
	s.RuntimeName = "claude-code"
	s.Lifecycle = lc
	s.RotationAudit = audit
	return s, socketPath, audit
}

// CounterValue reads the current value of the named counter with the
// exact label set from the default Prometheus registry, or 0 if absent.
// The adapter registers its §4.7 rotation metrics on
// prometheus.DefaultRegisterer.
func CounterValue(t *testing.T, name string, labels map[string]string) float64 {
	t.Helper()
	m := FindMetric(t, name, labels)
	if m == nil {
		return 0
	}
	return m.GetCounter().GetValue()
}

// HistogramCount reads the sample count of the named histogram with the
// exact label set, or 0 if absent.
func HistogramCount(t *testing.T, name string, labels map[string]string) uint64 {
	t.Helper()
	m := FindMetric(t, name, labels)
	if m == nil {
		return 0
	}
	return m.GetHistogram().GetSampleCount()
}

// FindMetric gathers the default registry and returns the metric whose
// name and label set match exactly, or nil.
func FindMetric(t *testing.T, name string, labels map[string]string) *dto.Metric {
	t.Helper()
	fams, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, f := range fams {
		if f.GetName() != name {
			continue
		}
		for _, m := range f.GetMetric() {
			if labelsMatch(m, labels) {
				return m
			}
		}
	}
	return nil
}

// labelsMatch reports whether the metric carries exactly the given label
// set, so a sibling series under the same metric name is never read in
// place of the one asked for.
func labelsMatch(m *dto.Metric, labels map[string]string) bool {
	if len(m.GetLabel()) != len(labels) {
		return false
	}
	for _, lp := range m.GetLabel() {
		want, ok := labels[lp.GetName()]
		if !ok || want != lp.GetValue() {
			return false
		}
	}
	return true
}
