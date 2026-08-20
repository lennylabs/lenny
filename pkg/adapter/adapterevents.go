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

// §4.7 adapter→gateway control events. Each is surfaced as
// a JSON envelope on the AdapterEvents server stream; type discriminates.
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
	// eventCheckpointBarrierAck is the §4.7 CheckpointBarrierAck
	// event the adapter emits in response to a §10.1 graceful-drain
	// CheckpointBarrier RPC. Fields: barrier_id, checkpoint_ref,
	// quiesced_ms (mirrors the synchronous RPC return).
	eventCheckpointBarrierAck = "CheckpointBarrierAck"
)

// controlEventBuffer bounds the per-stream queue of pending control
// events. The credential-fallback and final-usage events are infrequent,
// so a small buffer absorbs bursts without unbounded growth; an overflow
// is recorded on lenny_adapter_control_events_dropped_total.
const controlEventBuffer = 64

// controlEvent is one §4.7 adapter→gateway control event. It is
// JSON-encoded into the opaque AdapterEventsResponse.envelope_json so
// the proto schema does not grow a message per event (see the proto's
// AdapterEventsResponse comment). Fields absent for a given type are
// omitted on the wire.
type controlEvent struct {
	Type      string        `json:"type"`
	SessionID string        `json:"sessionId,omitempty"`
	Provider  string        `json:"provider,omitempty"`
	LeaseID   string        `json:"leaseId,omitempty"`
	Reason    string        `json:"reason,omitempty"`
	Usage     *controlUsage `json:"usage,omitempty"`
	// CheckpointBarrierAck fields (§4.7). Only set on the
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

// AdapterEvents implements the §4.7 adapter→gateway control stream.
// The gateway opens the bidirectional stream once per pod; the adapter
// pushes control events (RATE_LIMITED, AUTH_EXPIRED, PROVIDER_UNAVAILABLE,
// LEASE_REJECTED, AdapterTerminating, FINAL_USAGE_REPORT) onto it as JSON
// envelopes. Inbound frames from the gateway are drained for liveness and
// close detection; the adapter defines no gateway→adapter frame on this
// stream today. At most one stream is active at a time, matching the
// one-session-per-pod model (§6.1).
//
// spec: §4.7.
func (s *Server) AdapterEvents(stream adapterv1.Adapter_AdapterEventsServer) error {
	sink := make(chan controlEvent, controlEventBuffer)
	s.controlMu.Lock()
	if s.controlSink != nil {
		s.controlMu.Unlock()
		return status.Error(codes.FailedPrecondition,
			"CH-ADAPTEREVENTS stream already open for this pod")
	}
	s.controlSink = sink
	s.controlMu.Unlock()
	defer func() {
		s.controlMu.Lock()
		s.controlSink = nil
		s.controlMu.Unlock()
		// spec: §10.1 — a closed gateway control stream with a
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
			if err := stream.Send(&adapterv1.AdapterEventsResponse{EnvelopeJson: payload}); err != nil {
				return err
			}
			incControlEventSent(ev.Type)
		}
	}
}

// emitControlEvent queues ev onto the active gateway control stream. An
// event that carries no identifier of its own is stamped from
// soleSession, so it is attributed only when the pod's shared runtime
// process has been given no session but that one; otherwise the envelope
// goes out with an empty sessionId rather than under a co-tenant's. When no stream is
// attached, or the per-stream buffer is full, the event is dropped and
// recorded on lenny_adapter_control_events_dropped_total — the gateway's
// orphan-session reconciler (§10.1) is the backstop for a lost terminal
// event.
func (s *Server) emitControlEvent(ev controlEvent) {
	if ev.SessionID == "" {
		ev.SessionID = s.soleSession()
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

// SoleSessionID returns the session the pod's one shared runtime process
// has been given, and nothing else, since it was last serving none. It is
// the exported accessor cmd/lenny-adapter hands to NewSessionTokenSink so
// a session-less §4.7 lifecycle frame's direct-mode token counts fold
// into that session's cumulative total, and it is empty whenever another
// session's code may still be resident in the process, in which case the
// counts are dropped rather than charged to a co-tenant's budget.
// spec: §4.7 (direct-mode usage), §11.2.
func (s *Server) SoleSessionID() string {
	return s.soleSession()
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
// orphan-session reconciler (§10.1). The caller supplies the session it
// is terminating: the event names a specific session, and the caller
// holds that identifier in a window where soleSession may already be
// empty.
func (s *Server) EmitAdapterTerminating(sessionID, reason string) {
	s.emitControlEvent(controlEvent{
		Type:      eventAdapterTerminating,
		SessionID: sessionID,
		Reason:    reason,
	})
}

// EmitFinalUsageReport sends the §4.7 FINAL_USAGE_REPORT terminal event
// carrying the session's final token and wall-clock totals. The gateway
// waits for it before running budget_return.lua (§8.3), so the caller
// supplies the session it computed the totals for rather than leaving the
// envelope unattributable on a pod serving more than one.
func (s *Server) EmitFinalUsageReport(sessionID string, u Usage) {
	s.emitControlEvent(controlEvent{
		Type:      eventFinalUsageReport,
		SessionID: sessionID,
		Usage: &controlUsage{
			InputTokens:  u.InputTokens,
			OutputTokens: u.OutputTokens,
			WallClockMs:  u.WallClockMS,
		},
	})
}
