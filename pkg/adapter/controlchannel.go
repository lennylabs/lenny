// SPDX-License-Identifier: MIT

package adapter

import (
	"encoding/json"
	"errors"
	"io"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// §4.7 lines 652-662 adapter→gateway control events. Each is surfaced as
// a JSON envelope on the LifecycleChannel server stream; type discriminates.
const (
	// eventRateLimited reports the current credential is rate-limited and
	// the gateway should promote a fallback (§4.9 credential fallback).
	eventRateLimited = "RATE_LIMITED"
	// eventAuthExpired reports the credential lease expired or was rejected
	// by the provider.
	eventAuthExpired = "AUTH_EXPIRED"
	// eventProviderUnavailable reports the provider endpoint is unreachable.
	eventProviderUnavailable = "PROVIDER_UNAVAILABLE"
	// eventLeaseRejected reports the runtime cannot use the assigned
	// credential (incompatible provider, etc.).
	eventLeaseRejected = "LEASE_REJECTED"
	// eventAdapterTerminating is the adapter's self-initiated terminal
	// notification (e.g. coordinator-loss hold timeout). It lets the gateway
	// transition the session without waiting for the orphan-session
	// reconciler (§10.1).
	eventAdapterTerminating = "AdapterTerminating"
	// eventFinalUsageReport is the final lifecycle-stream message, sent
	// after in-flight ReportUsage calls have flushed, just before the stream
	// closes. The gateway waits for it before running budget_return.lua
	// (§8.3).
	eventFinalUsageReport = "FINAL_USAGE_REPORT"
	// eventCheckpointBarrierAck is the §4.7 line 660 CheckpointBarrierAck
	// event the adapter emits in response to a §10.1 graceful-drain
	// CheckpointBarrier RPC. Fields: barrier_id, checkpoint_ref,
	// quiesced_ms (mirrors the synchronous RPC return).
	eventCheckpointBarrierAck = "CheckpointBarrierAck"
	// eventAdapterEvicting is the adapter's request that the session's
	// coordinating replica drive an eviction checkpoint before this pod
	// terminates (kubelet-driven node drain, Eviction API, cluster
	// upgrade, or direct delete). Unlike eventAdapterTerminating, which
	// transitions the session terminal without a checkpoint, this event
	// asks the coordinator to drive the Checkpoint RPC with the
	// TriggerEviction trigger first; the pod terminates once that
	// checkpoint settles or the deadline lapses. Fields: session_id, and
	// slot_id on pods with maxConcurrentSessions > 1.
	// spec: §4.7 (AdapterEvicting control event), §4.6.1 (agent-pod
	// disruption protection).
	eventAdapterEvicting = "AdapterEvicting"
)

// controlEventBuffer bounds the per-stream queue of pending control
// events. The credential-fallback and final-usage events are infrequent,
// so a small buffer absorbs bursts without unbounded growth; an overflow
// is recorded on lenny_adapter_control_events_dropped_total.
const controlEventBuffer = 64

// controlEvent is one §4.7 adapter→gateway control event. It is
// JSON-encoded into the opaque LifecycleChannelResponse.envelope_json so
// the proto schema does not grow a message per event (see the proto's
// LifecycleChannelResponse comment). Fields absent for a given type are
// omitted on the wire.
type controlEvent struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId,omitempty"`
	// SlotID names the §6.4 concurrent-workspace slot the event pertains
	// to. It is set only on pods with maxConcurrentSessions > 1 (where a
	// single control stream fans events for several co-tenant sessions)
	// and omitted on the single-session base pod, matching the
	// AdapterEvicting field contract. spec: §4.7, §6.4.
	SlotID   string        `json:"slotId,omitempty"`
	Provider string        `json:"provider,omitempty"`
	LeaseID  string        `json:"leaseId,omitempty"`
	Reason   string        `json:"reason,omitempty"`
	Usage    *controlUsage `json:"usage,omitempty"`
	// CheckpointBarrierAck fields (§4.7 line 660). Only set on the
	// eventCheckpointBarrierAck event; omitted otherwise.
	BarrierID     string `json:"barrierId,omitempty"`
	CheckpointRef string `json:"checkpointRef,omitempty"`
	QuiescedMs    int64  `json:"quiescedMs,omitempty"`
}

// controlUsage carries the token and wall-clock totals on a
// FINAL_USAGE_REPORT event.
type controlUsage struct {
	InputTokens  int64 `json:"inputTokens"`
	OutputTokens int64 `json:"outputTokens"`
	WallClockMs  int64 `json:"wallClockMs"`
}

// LifecycleChannel implements the §4.7 adapter→gateway control stream.
// The gateway opens the bidirectional stream once per pod; the adapter
// pushes control events (RATE_LIMITED, AUTH_EXPIRED, PROVIDER_UNAVAILABLE,
// LEASE_REJECTED, AdapterTerminating, FINAL_USAGE_REPORT) onto it as JSON
// envelopes. Inbound frames from the gateway are drained for liveness and
// close detection; the adapter defines no gateway→adapter frame on this
// stream today. At most one stream is active at a time, matching the
// one-session-per-pod model (§6.1).
//
// spec: §4.7 lines 652-662.
func (s *Server) LifecycleChannel(stream adapterv1.Adapter_LifecycleChannelServer) error {
	sink := make(chan controlEvent, controlEventBuffer)
	s.controlMu.Lock()
	if s.controlSink != nil {
		s.controlMu.Unlock()
		return status.Error(codes.FailedPrecondition,
			"lifecycle control channel already open for this pod")
	}
	s.controlSink = sink
	s.controlMu.Unlock()
	defer func() {
		s.controlMu.Lock()
		s.controlSink = nil
		s.controlMu.Unlock()
		// spec: §10.1 lines 47-52 — a closed gateway control stream with a
		// session still live is the coordinator-loss signal; enter hold
		// state and await a new coordinator's CoordinatorFence.
		s.onCoordinatorChannelClosed()
	}()

	ctx := stream.Context()
	recvErr := make(chan error, 1)
	go func() {
		for {
			if _, err := stream.Recv(); err != nil {
				recvErr <- err
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-recvErr:
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		case ev := <-sink:
			payload, err := json.Marshal(ev)
			if err != nil {
				return status.Errorf(codes.Internal, "encode control event: %v", err)
			}
			if err := stream.Send(&adapterv1.LifecycleChannelResponse{EnvelopeJson: payload}); err != nil {
				return err
			}
			incControlEventSent(ev.Type)
		}
	}
}

// emitControlEvent queues ev onto the active gateway control stream. The
// session id is stamped from the pod's current session. When no stream is
// attached, or the per-stream buffer is full, the event is dropped and
// recorded on lenny_adapter_control_events_dropped_total — the gateway's
// orphan-session reconciler (§10.1) is the backstop for a lost terminal
// event.
func (s *Server) emitControlEvent(ev controlEvent) {
	if ev.SessionID == "" {
		ev.SessionID = s.currentSession()
	}
	s.controlMu.Lock()
	sink := s.controlSink
	s.controlMu.Unlock()
	if sink == nil {
		incControlEventDropped(ev.Type, "no_stream")
		return
	}
	select {
	case sink <- ev:
	default:
		incControlEventDropped(ev.Type, "buffer_full")
	}
}

// currentSession returns the session the pod currently holds, empty when
// the pod is idle.
func (s *Server) currentSession() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID
}

// CurrentSessionID returns the id of the session the pod currently holds,
// empty when the pod is idle. It is the exported accessor cmd/lenny-adapter
// hands to NewSessionTokenSink so the session-less §4.7 lifecycle frame's
// direct-mode token counts fold into the right session's cumulative total
// (§6.1 one session per pod). spec: §4.7 (direct-mode usage), §11.2.
func (s *Server) CurrentSessionID() string {
	return s.currentSession()
}

// EmitRateLimited reports that provider's current credential is
// rate-limited so the gateway promotes a §4.9 fallback credential.
func (s *Server) EmitRateLimited(provider string) {
	s.emitControlEvent(controlEvent{Type: eventRateLimited, Provider: provider})
}

// EmitAuthExpired reports that provider's credential lease expired or was
// rejected by the provider.
func (s *Server) EmitAuthExpired(provider, leaseID string) {
	s.emitControlEvent(controlEvent{Type: eventAuthExpired, Provider: provider, LeaseID: leaseID})
}

// EmitProviderUnavailable reports that provider's endpoint is unreachable.
func (s *Server) EmitProviderUnavailable(provider string) {
	s.emitControlEvent(controlEvent{Type: eventProviderUnavailable, Provider: provider})
}

// EmitLeaseRejected reports that the runtime cannot use the credential
// assigned for provider (incompatible provider, malformed lease, etc.).
func (s *Server) EmitLeaseRejected(provider, leaseID, reason string) {
	s.emitControlEvent(controlEvent{
		Type:     eventLeaseRejected,
		Provider: provider,
		LeaseID:  leaseID,
		Reason:   reason,
	})
}

// EmitAdapterTerminating sends the §4.7 self-initiated terminal
// notification (reason is one of "coordinator_lost", etc.), letting the
// gateway transition the session immediately rather than waiting for the
// orphan-session reconciler (§10.1).
func (s *Server) EmitAdapterTerminating(reason string) {
	s.emitControlEvent(controlEvent{Type: eventAdapterTerminating, Reason: reason})
}

// EmitAdapterEvicting requests that the session's coordinating replica
// drive an eviction checkpoint (TriggerEviction) before this pod
// terminates. The kubelet-path SIGTERM handler emits one event per live
// bound session, passing the session id explicitly (a concurrent-pool
// session sets no pod-global s.sessionID, so emitControlEvent could not
// recover it) and slotID on pods with maxConcurrentSessions > 1. Unlike
// EmitAdapterTerminating, the adapter keeps serving the coordinator's
// Checkpoint RPC until the driven checkpoint settles or the deadline
// lapses. spec: §4.7 (AdapterEvicting control event), §4.6.1 (coordinator-
// direct eviction drive).
func (s *Server) EmitAdapterEvicting(sessionID, slotID string) {
	s.emitControlEvent(controlEvent{
		Type:      eventAdapterEvicting,
		SessionID: sessionID,
		SlotID:    slotID,
	})
}

// EmitFinalUsageReport sends the §4.7 FINAL_USAGE_REPORT terminal event
// carrying the session's final token and wall-clock totals. The gateway
// waits for it before running budget_return.lua (§8.3).
func (s *Server) EmitFinalUsageReport(u Usage) {
	s.emitControlEvent(controlEvent{
		Type: eventFinalUsageReport,
		Usage: &controlUsage{
			InputTokens:  u.InputTokens,
			OutputTokens: u.OutputTokens,
			WallClockMs:  u.WallClockMS,
		},
	})
}
