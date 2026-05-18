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
	"net"
	"sync"
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

// lifecycleCapabilities are the §4.7 lifecycle signals the adapter can
// drive. The adapter advertises them in the lifecycle_capabilities
// handshake; the runtime replies with lifecycle_support naming the
// subset it implements.
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
	closed    bool
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
	}, nil
}

// SocketPath is the address the runtime dials to reach the channel.
func (lc *LifecycleChannel) SocketPath() string {
	return lc.listener.Addr().String()
}

// Run accepts the runtime's connection, performs the
// lifecycle_capabilities handshake, and serves lifecycle frames until
// the connection ends or ctx is cancelled. It blocks; callers run it in
// a goroutine. The request methods block until Run completes the
// handshake.
func (lc *LifecycleChannel) Run(ctx context.Context) error {
	defer close(lc.done)
	stop := context.AfterFunc(ctx, func() { _ = lc.Close() })
	defer stop()

	conn, err := lc.listener.Accept()
	if err != nil {
		return fmt.Errorf("lifecycle channel accept: %w", err)
	}

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
	lc.mu.Unlock()

	r := bufio.NewReader(conn)
	if err := lc.handshake(r); err != nil {
		return err
	}
	close(lc.ready)
	return lc.readLoop(r)
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
	supported := make(map[string]bool, len(frame.Capabilities))
	for _, c := range frame.Capabilities {
		supported[c] = true
	}
	lc.mu.Lock()
	lc.supported = supported
	lc.mu.Unlock()
	return nil
}

// readLoop dispatches inbound frames until the connection closes. Only
// the runtime's acknowledgements (checkpoint_ready, interrupt_-
// acknowledged, credentials_acknowledged) carry meaning to the adapter;
// other frame types are ignored for forward compatibility.
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
		}
	}
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

// Supports reports whether the runtime declared a capability in the
// handshake. It returns false before the handshake completes.
func (lc *LifecycleChannel) Supports(capability string) bool {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	return lc.supported[capability]
}

// request registers a pending acknowledgement under key, sends frame,
// and waits for the runtime's reply.
func (lc *LifecycleChannel) request(ctx context.Context, key string, frame lifecycleFrame) error {
	select {
	case <-lc.ready:
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
