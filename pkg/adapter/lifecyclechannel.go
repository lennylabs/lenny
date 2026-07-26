// SPDX-License-Identifier: MIT

package adapter

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

// newLifecycleID returns a short random identifier for correlating a
// lifecycle request with its acknowledgement.
func newLifecycleID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// lifecycleProtocolVersion is the §4.7 lifecycle-channel protocol
// version the adapter advertises in the lifecycle_capabilities frame.
const lifecycleProtocolVersion = "1.0"

// errLifecycleVersionIncompatible is returned by the handshake when the
// runtime advertises a protocolVersion the adapter cannot speak. spec:
// §15.5 line 2431 (item 3) — "The adapter advertises a protocol version
// at INIT; the gateway selects a compatible version." Today the adapter
// speaks `1.x`; a runtime that names a different major version is
// rejected before any signal frames flow so a forward-compat mismatch
// surfaces at handshake rather than as silent missing capabilities.
// F-15.5.9.
var errLifecycleVersionIncompatible = errors.New("lifecycle protocol version incompatible")

// lifecycleCapabilities are the §4.7 lifecycle signals the adapter
// advertises on every pod. The adapter sends them in the
// lifecycle_capabilities handshake; the runtime replies with
// lifecycle_support naming the subset it implements. spec: §6.16 removes
// the task_lifecycle capability along with the between-task
// task_complete / task_ready frames, so the advertised set is fixed.
var lifecycleCapabilities = []string{"checkpoint", "interrupt", "credential_rotation", "deadline_signal"}

var (
	errLifecycleClosed       = errors.New("lifecycle channel is closed")
	errLifecycleNotConnected = errors.New("lifecycle channel has no runtime connection")
)

// lifecycleFrame is one JSONL frame on the §4.7 runtime↔adapter
// lifecycle channel. A single struct covers every frame type; fields
// not set for a given type are omitted on the wire. The field names
// match the §4.7 message-schema table (camelCase).
type lifecycleFrame struct {
	Type            string   `json:"type"`
	ProtocolVersion string   `json:"protocolVersion,omitempty"`
	Capabilities    []string `json:"capabilities,omitempty"`
	CheckpointID    string   `json:"checkpointId,omitempty"`
	InterruptID     string   `json:"interruptId,omitempty"`
	DeadlineMs      int32    `json:"deadlineMs,omitempty"`
	RemainingMs     int32    `json:"remainingMs,omitempty"`
	Status          string   `json:"status,omitempty"`
	Reason          string   `json:"reason,omitempty"`
	Provider        string   `json:"provider,omitempty"`
	CredentialsPath string   `json:"credentialsPath,omitempty"`
	LeaseID         string   `json:"leaseId,omitempty"`
	Trigger         string   `json:"trigger,omitempty"`
	RequestID       string   `json:"requestId,omitempty"`
	// InputTokens and OutputTokens are the §4.7 direct-mode token counts
	// the runtime optionally carries on an llm_request_completed frame,
	// extracted from the completed provider response. The adapter folds
	// them into the session's cumulative usage total (§11.2 direct-mode
	// usage). A runtime that cannot extract counts omits both, so the
	// session has no direct-mode token source. Zero-valued so an absent
	// field reads as a zero delta rather than a decode error.
	InputTokens  int64 `json:"inputTokens,omitempty"`
	OutputTokens int64 `json:"outputTokens,omitempty"`
}

// LifecycleChannel is the adapter side of the §4.7 lifecycle channel:
// a Unix-socket server the Full-level agent runtime dials to receive
// checkpoint, interrupt, credential-rotation, deadline, and terminate
// signals and to acknowledge them. The adapter listens; the runtime
// connects once per pod. The socket address (a file path or, on Linux,
// an abstract `@`-prefixed name) is published to the runtime through
// the adapter manifest's lifecycleChannel.socket field.
type LifecycleChannel struct {
	listener net.Listener

	ready chan struct{} // closed once the handshake completes
	done  chan struct{} // closed once Run returns

	mu        sync.Mutex
	conn      net.Conn
	enc       *json.Encoder
	supported map[string]bool
	pending   map[string]chan error
	// inflight is the §4.7 line 820 per-provider count of outbound LLM
	// requests the runtime reported via llm_request_started without a
	// matching llm_request_completed. The Full-level credential-rotation
	// gate reads it through InflightCount.
	inflight map[string]int
	closed   bool

	// usage receives the §4.7 direct-mode token counts an
	// llm_request_completed frame carries. Nil (the Basic/Standard path
	// and the dev path with no meter) makes token folding a no-op. Set
	// once at wiring time via SetUsageSink and read in readLoop; not
	// mutated per connection, so it needs no lock beyond the pointer
	// itself being assigned before Run starts.
	usage tokenSink
}

// tokenSink folds one llm_request_completed frame's direct-mode token
// counts into the session's cumulative usage total. The adapter wires it
// to the concrete SessionUsageMeter, resolving the pod's current session
// id; the lifecycle frame itself carries no session id (§6.1 one session
// per pod). Defined at the consumer (the lifecycle channel) per the
// accept-interfaces convention.
type tokenSink interface {
	// AddTokens folds the counts from one completed direct-mode LLM call
	// into the current session's cumulative total.
	AddTokens(inputTokens, outputTokens int64)
}

// NewLifecycleChannel listens on socketPath for the runtime's lifecycle
// connection. socketPath is a filesystem path or, on Linux, an abstract
// socket name beginning with `@`.
func NewLifecycleChannel(socketPath string) (*LifecycleChannel, error) {
	l, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("lifecycle channel listen %s: %w", socketPath, err)
	}
	return &LifecycleChannel{
		listener: l,
		ready:    make(chan struct{}),
		done:     make(chan struct{}),
		pending:  map[string]chan error{},
		inflight: map[string]int{},
	}, nil
}

// SocketPath is the address the runtime dials to reach the channel.
func (lc *LifecycleChannel) SocketPath() string {
	return lc.listener.Addr().String()
}

// SetUsageSink wires the token sink readLoop folds each
// llm_request_completed frame's §4.7 direct-mode token counts into. It
// is set once before Run starts (the adapter wiring in
// cmd/lenny-adapter), so it does not race with the read loop. A nil sink
// leaves token folding a no-op (the Basic/Standard and dev paths).
func (lc *LifecycleChannel) SetUsageSink(sink tokenSink) {
	lc.usage = sink
}

// Run serves the lifecycle channel until ctx is cancelled or the channel
// is closed. It accepts one runtime connection at a time: for each, it
// performs the lifecycle_capabilities handshake and serves frames until
// the runtime disconnects, then resets per-connection state and accepts
// the next connection. A resumed session (§4.7 lines 836-842 startup
// sequence, applied for both fresh sessions and resumes) starts a fresh
// runtime that dials the same socket and re-handshakes, so the channel
// must outlive any single runtime process. Run blocks; callers run it in
// a goroutine. The request methods block until the current connection
// completes its handshake.
//
// spec: §4.7 lines 836-842 — the Full-level lifecycle channel is rebound
// per runtime process rather than torn down after the first disconnect.
func (lc *LifecycleChannel) Run(ctx context.Context) error {
	defer close(lc.done)
	stop := context.AfterFunc(ctx, func() { _ = lc.Close() })
	defer stop()

	for {
		conn, err := lc.listener.Accept()
		if err != nil {
			if lc.isClosed() || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("lifecycle channel accept: %w", err)
		}
		if err := lc.serveConn(conn); err != nil && !errors.Is(err, errLifecycleClosed) {
			// A per-connection handshake or read error ends this runtime
			// connection but not the channel: the next runtime (e.g. after
			// Resume restarts the binary) dials again and re-handshakes.
			log.Printf("lenny-adapter: lifecycle connection ended: %v", err)
		}
		lc.resetConn()
		if lc.isClosed() {
			return nil
		}
	}
}

// serveConn binds conn as the active runtime connection, completes the
// handshake, and serves frames until the connection ends.
func (lc *LifecycleChannel) serveConn(conn net.Conn) error {
	ready := make(chan struct{})
	lc.mu.Lock()
	if lc.closed {
		lc.mu.Unlock()
		conn.Close()
		return errLifecycleClosed
	}
	lc.conn = conn
	enc := json.NewEncoder(conn)
	enc.SetEscapeHTML(false)
	lc.enc = enc
	lc.ready = ready
	lc.mu.Unlock()

	r := bufio.NewReader(conn)
	if err := lc.handshake(r); err != nil {
		return err
	}
	close(ready)
	return lc.readLoop(r)
}

// resetConn clears the state bound to a single runtime connection so the
// channel can accept the next one. It drops the connection, re-arms a
// fresh (unclosed) ready channel so requests block until the next
// handshake, zeroes the per-provider in-flight counters, and fails every
// request still waiting on the connection that just ended.
func (lc *LifecycleChannel) resetConn() {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	if lc.conn != nil {
		lc.conn.Close()
		lc.conn = nil
	}
	lc.enc = nil
	lc.supported = nil
	lc.ready = make(chan struct{})
	for provider, n := range lc.inflight {
		if n != 0 {
			SetLLMInflight(provider, 0)
		}
		delete(lc.inflight, provider)
	}
	for key, ch := range lc.pending {
		ch <- errLifecycleClosed
		delete(lc.pending, key)
	}
}

// isClosed reports whether Close has been called.
func (lc *LifecycleChannel) isClosed() bool {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	return lc.closed
}

// RuntimeConnected reports whether a runtime connection is currently bound
// and past its handshake. It returns false before the first runtime dials
// and after a runtime connection drops: Run's resetConn clears the
// connection and re-arms a fresh, unclosed ready channel, so a request that
// arrives after the drop would otherwise block on that ready channel until
// the caller's context lapses. The eviction best-effort checkpoint gate
// reads this to tell a runtime that has already exited (the default sidecar
// SIGTERMs the hookless runtime container at t=0) apart from one that is
// connected but slow to answer a checkpoint_request.
//
// spec: §4.4 (best-effort eviction snapshot), §4.6.1 (agent-pod disruption
// protection).
func (lc *LifecycleChannel) RuntimeConnected() bool {
	lc.mu.Lock()
	conn := lc.conn
	ready := lc.ready
	lc.mu.Unlock()
	if conn == nil {
		return false
	}
	select {
	case <-ready:
		return true
	default:
		return false
	}
}

// currentReady returns the channel that closes when the active
// connection's handshake completes. It is recreated per connection, so
// callers capture it under the lock before waiting.
func (lc *LifecycleChannel) currentReady() chan struct{} {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	return lc.ready
}

// handshake sends the adapter's lifecycle_capabilities and reads the
// runtime's lifecycle_support reply, recording the capabilities the
// runtime declared.
func (lc *LifecycleChannel) handshake(r *bufio.Reader) error {
	if err := lc.writeFrame(lifecycleFrame{
		Type:            "lifecycle_capabilities",
		ProtocolVersion: lifecycleProtocolVersion,
		Capabilities:    lifecycleCapabilities,
	}); err != nil {
		return fmt.Errorf("lifecycle handshake send: %w", err)
	}
	frame, err := readLifecycleFrame(r)
	if err != nil {
		return fmt.Errorf("lifecycle handshake read: %w", err)
	}
	if frame.Type != "lifecycle_support" {
		return fmt.Errorf("lifecycle handshake: expected lifecycle_support, got %q", frame.Type)
	}
	// spec: §15.5 line 2431 (item 3) — the runtime advertises a
	// `protocolVersion` at INIT and the gateway selects a compatible
	// version. The adapter speaks `1.x`; a runtime that names a
	// different major version is rejected before any signal frames
	// flow. An empty `protocolVersion` is accepted for forward
	// compatibility with runtimes that pre-date this field. F-15.5.9.
	if frame.ProtocolVersion != "" {
		if !lifecycleVersionsCompatible(lifecycleProtocolVersion, frame.ProtocolVersion) {
			return fmt.Errorf("%w: runtime advertised %q, adapter speaks %q",
				errLifecycleVersionIncompatible, frame.ProtocolVersion, lifecycleProtocolVersion)
		}
	}
	supported := make(map[string]bool, len(frame.Capabilities))
	for _, c := range frame.Capabilities {
		supported[c] = true
	}
	lc.mu.Lock()
	lc.supported = supported
	lc.mu.Unlock()
	return nil
}

// lifecycleVersionsCompatible reports whether the adapter version and
// the runtime version share a major number. v1 only ships `1.x`, so the
// compat rule is "same major"; minor differences are forward-readable.
//
// spec: §15.5 line 2431 — "Major version changes are breaking."
// F-15.5.9.
func lifecycleVersionsCompatible(adapter, runtime string) bool {
	return lifecycleMajor(adapter) == lifecycleMajor(runtime)
}

// lifecycleMajor extracts the major number from a `M.m` version string;
// it returns -1 for malformed input so a typo on the runtime side
// surfaces as a compat rejection rather than silently matching "0".
func lifecycleMajor(v string) int {
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c == '.' {
			if i == 0 {
				return -1
			}
			n := 0
			for j := 0; j < i; j++ {
				if v[j] < '0' || v[j] > '9' {
					return -1
				}
				n = n*10 + int(v[j]-'0')
			}
			return n
		}
	}
	// No dot at all is malformed for a `M.m` version.
	return -1
}

// readLoop dispatches inbound frames until the connection closes. The
// runtime's acknowledgements (checkpoint_ready, interrupt_acknowledged,
// credentials_acknowledged) wake the matching request;
// llm_request_started / llm_request_completed adjust the per-provider
// in-flight counter (§4.7 line 820). Unknown frame types are ignored for
// forward compatibility.
func (lc *LifecycleChannel) readLoop(r *bufio.Reader) error {
	for {
		frame, err := readLifecycleFrame(r)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		switch frame.Type {
		case "checkpoint_ready":
			lc.deliver("ckpt:" + frame.CheckpointID)
		case "interrupt_acknowledged":
			lc.deliver("int:" + frame.InterruptID)
		case "credentials_acknowledged":
			lc.deliver("cred:" + frame.LeaseID)
		case "llm_request_started":
			lc.adjustInflight(frame.Provider, 1)
		case "llm_request_completed":
			lc.adjustInflight(frame.Provider, -1)
			// spec: §4.7 (llm_request_completed token fields), §11.2
			// (direct-mode usage) — a direct-mode runtime carries the
			// prompt and completion token counts extracted from the
			// completed provider response; fold them into the session's
			// cumulative total for the gateway ReportUsage pull. A runtime
			// that cannot extract counts omits both fields (a zero delta),
			// which the §11.2 anomaly detector observes.
			if lc.usage != nil && (frame.InputTokens != 0 || frame.OutputTokens != 0) {
				lc.usage.AddTokens(frame.InputTokens, frame.OutputTokens)
			}
		}
	}
}

// adjustInflight changes the per-provider in-flight LLM-request counter
// by delta and mirrors the value to the lenny_llm_inflight_requests
// gauge. The count never goes below zero, so a spurious
// llm_request_completed (no matching start) is a no-op (§4.7 line 820).
func (lc *LifecycleChannel) adjustInflight(provider string, delta int) {
	if provider == "" {
		return
	}
	lc.mu.Lock()
	n := lc.inflight[provider] + delta
	if n < 0 {
		n = 0
	}
	lc.inflight[provider] = n
	lc.mu.Unlock()
	SetLLMInflight(provider, n)
}

// InflightCount reports the number of outbound LLM requests the runtime
// has reported started without a matching completion for provider. The
// Full-level credential-rotation gate (§4.7) waits for it to reach zero.
func (lc *LifecycleChannel) InflightCount(provider string) int {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	return lc.inflight[provider]
}

// deliver wakes the request waiting on key with a successful result.
// The pending channel is buffered, so the send never blocks even when
// the request has already abandoned the wait.
func (lc *LifecycleChannel) deliver(key string) {
	lc.mu.Lock()
	ch, ok := lc.pending[key]
	if ok {
		delete(lc.pending, key)
		ch <- nil
	}
	lc.mu.Unlock()
}

// RequestCheckpoint sends a checkpoint_request and blocks until the
// runtime replies checkpoint_ready for the same id, ctx is cancelled,
// or the channel closes. deadlineMs is the runtime's quiesce budget
// (§4.4); the caller bounds the wait with ctx.
func (lc *LifecycleChannel) RequestCheckpoint(ctx context.Context, checkpointID string, deadlineMs int32) error {
	return lc.request(ctx, "ckpt:"+checkpointID, lifecycleFrame{
		Type:         "checkpoint_request",
		CheckpointID: checkpointID,
		DeadlineMs:   deadlineMs,
	})
}

// CompleteCheckpoint tells the runtime the checkpoint the adapter
// requested has been stored, so the runtime resumes. status is "ok" or
// "failed"; reason carries the failure detail when status is "failed".
func (lc *LifecycleChannel) CompleteCheckpoint(checkpointID, status, reason string) error {
	return lc.writeFrame(lifecycleFrame{
		Type:         "checkpoint_complete",
		CheckpointID: checkpointID,
		Status:       status,
		Reason:       reason,
	})
}

// RequestInterrupt sends an interrupt_request and blocks until the
// runtime replies interrupt_acknowledged for the same id, ctx is
// cancelled, or the channel closes.
func (lc *LifecycleChannel) RequestInterrupt(ctx context.Context, interruptID string, deadlineMs int32) error {
	return lc.request(ctx, "int:"+interruptID, lifecycleFrame{
		Type:        "interrupt_request",
		InterruptID: interruptID,
		DeadlineMs:  deadlineMs,
	})
}

// RotateCredentials sends a credentials_rotated frame naming the
// rewritten credential file and blocks until the runtime replies
// credentials_acknowledged for the same lease, ctx is cancelled, or the
// channel closes (§4.7 credential rotation).
func (lc *LifecycleChannel) RotateCredentials(ctx context.Context, provider, credentialsPath, leaseID string) error {
	return lc.request(ctx, "cred:"+leaseID, lifecycleFrame{
		Type:            "credentials_rotated",
		Provider:        provider,
		CredentialsPath: credentialsPath,
		LeaseID:         leaseID,
	})
}

// SignalDeadlineApproaching warns the runtime that the session is
// nearing expiry or budget exhaustion so it wraps up work. remainingMs
// is the time left; trigger is one of "session_age", "budget", "idle".
func (lc *LifecycleChannel) SignalDeadlineApproaching(remainingMs int32, trigger string) error {
	return lc.writeFrame(lifecycleFrame{
		Type:        "deadline_approaching",
		RemainingMs: remainingMs,
		Trigger:     trigger,
	})
}

// Terminate tells the runtime to exit cleanly within deadlineMs. reason
// is one of "session_complete", "budget_exhausted", "eviction",
// "operator". The adapter sends SIGTERM if the runtime has not exited
// when the deadline elapses.
func (lc *LifecycleChannel) Terminate(deadlineMs int32, reason string) error {
	return lc.writeFrame(lifecycleFrame{
		Type:       "terminate",
		DeadlineMs: deadlineMs,
		Reason:     reason,
	})
}

// SignalFilesUpdated tells the runtime that a §7.4 mid-session upload
// promoted new files into /workspace/current, so the agent re-reads the
// workspace. The adapter sends it only after the atomic overlay completes,
// so the runtime never observes partially written files. One-way: the
// runtime is not required to acknowledge. When no runtime is connected
// (the pre-start path, before the agent dials the channel) writeFrame
// returns errLifecycleNotConnected, which the caller treats as a no-op.
// spec: §7.4 line 433 — F-7.4.6.
func (lc *LifecycleChannel) SignalFilesUpdated() error {
	return lc.writeFrame(lifecycleFrame{Type: "files_updated"})
}

// Supports reports whether the runtime declared a capability in the
// handshake. It returns false before the handshake completes.
func (lc *LifecycleChannel) Supports(capability string) bool {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	return lc.supported[capability]
}

// WaitHandshake blocks until the current runtime connection completes its
// lifecycle_capabilities / lifecycle_support handshake, up to wait or
// until ctx is cancelled or the channel closes, and reports whether the
// handshake completed. It returns immediately when the handshake is
// already done. A non-positive wait checks the current state without
// blocking. The §5.1 observed-integration-level probe uses it to decide
// whether the runtime opened the lifecycle channel (Full level).
func (lc *LifecycleChannel) WaitHandshake(ctx context.Context, wait time.Duration) bool {
	ready := lc.currentReady()
	select {
	case <-ready:
		return true
	default:
	}
	if wait <= 0 {
		return false
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ready:
		return true
	case <-lc.done:
		return false
	case <-ctx.Done():
		return false
	case <-timer.C:
		return false
	}
}

// request registers a pending acknowledgement under key, sends frame,
// and waits for the runtime's reply.
func (lc *LifecycleChannel) request(ctx context.Context, key string, frame lifecycleFrame) error {
	select {
	case <-lc.currentReady():
	case <-lc.done:
		return errLifecycleClosed
	case <-ctx.Done():
		return ctx.Err()
	}

	ack := make(chan error, 1)
	lc.mu.Lock()
	if lc.closed {
		lc.mu.Unlock()
		return errLifecycleClosed
	}
	lc.pending[key] = ack
	lc.mu.Unlock()

	if err := lc.writeFrame(frame); err != nil {
		lc.cancelPending(key)
		return err
	}

	select {
	case err := <-ack:
		return err
	case <-lc.done:
		return errLifecycleClosed
	case <-ctx.Done():
		lc.cancelPending(key)
		return ctx.Err()
	}
}

// cancelPending drops a pending acknowledgement that will not arrive.
func (lc *LifecycleChannel) cancelPending(key string) {
	lc.mu.Lock()
	delete(lc.pending, key)
	lc.mu.Unlock()
}

// writeFrame encodes one JSONL frame to the runtime.
func (lc *LifecycleChannel) writeFrame(f lifecycleFrame) error {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	if lc.closed {
		return errLifecycleClosed
	}
	if lc.enc == nil {
		return errLifecycleNotConnected
	}
	return lc.enc.Encode(f)
}

// Close stops the listener, drops the runtime connection, and fails
// every pending request. It is safe to call more than once.
func (lc *LifecycleChannel) Close() error {
	lc.mu.Lock()
	if lc.closed {
		lc.mu.Unlock()
		return nil
	}
	lc.closed = true
	conn := lc.conn
	for key, ch := range lc.pending {
		ch <- errLifecycleClosed
		delete(lc.pending, key)
	}
	lc.mu.Unlock()

	err := lc.listener.Close()
	if conn != nil {
		conn.Close()
	}
	return err
}

// readLifecycleFrame reads one newline-terminated JSON frame.
func readLifecycleFrame(r *bufio.Reader) (lifecycleFrame, error) {
	line, err := r.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return lifecycleFrame{}, err
	}
	var f lifecycleFrame
	if err := json.Unmarshal(line, &f); err != nil {
		return lifecycleFrame{}, fmt.Errorf("decode lifecycle frame: %w", err)
	}
	return f, nil
}
