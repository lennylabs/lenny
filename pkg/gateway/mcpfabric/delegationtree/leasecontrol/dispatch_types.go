// SPDX-License-Identifier: MIT

package leasecontrol

import adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"

// This file holds the plain-Go request, response, status, and error
// types the in-process §8.6 ExtendLease dispatch speaks. The dispatch
// used to carry the adapterv1.ExtendLeaseRequest/ExtendLeaseResponse/
// FileExportLimitsDelta proto messages, because the pod adapter dialed
// the GatewayControl.ExtendLease gRPC RPC. With the §8.6
// budget-exhaustion trigger relocated into the gateway LLM Proxy
// in-process (proposal 0023 / F-8.6.6), that RPC and its messages are
// removed, so the dispatch is a plain in-process Go method the proxy
// calls directly. These types replace the deleted proto surface.
//
// The §15.1 Error envelope on a REJECTED response reuses the surviving
// adapterv1.Error type and its ERROR_CODE/CATEGORY enums, which stay on
// the wire for the other GatewayControl RPCs; only the ExtendLease
// request/response/file-export messages were trimmed.
// spec: §8.6 line 629 (in-process proxy trigger); §15.1 line 1080.

// ExtendStatus is the §8.6 extension-response status the in-process
// dispatch reports. It replaces the deleted
// adapterv1.ExtendLeaseResponse_Status proto enum.
// spec: §8.6 line 743.
type ExtendStatus int

const (
	// StatusUnspecified is the zero value; the dispatch never returns it
	// for a resolved request, so a caller seeing it treats the response
	// as malformed and fails closed.
	StatusUnspecified ExtendStatus = iota
	// StatusGranted — the full requested increase fit under the ceiling
	// on every requested dimension.
	StatusGranted
	// StatusPartiallyGranted — at least one requested dimension was
	// capped to non-zero headroom.
	StatusPartiallyGranted
	// StatusCeilingReached — no requested dimension had headroom; the
	// grant is zero. The gateway LLM Proxy MUST treat this as terminal
	// and not re-request. spec: §8.6 line 712.
	StatusCeilingReached
	// StatusRejected — the subtree's extension-denied flag is set and the
	// cool-off has not expired. The response carries the cool-off expiry.
	StatusRejected
)

func (s ExtendStatus) String() string {
	switch s {
	case StatusGranted:
		return "GRANTED"
	case StatusPartiallyGranted:
		return "PARTIALLY_GRANTED"
	case StatusCeilingReached:
		return "CEILING_REACHED"
	case StatusRejected:
		return "REJECTED"
	default:
		return "UNSPECIFIED"
	}
}

// ExtendRequest is the plain-Go §8.6 extension request the in-process
// dispatch consumes. SessionID names the requesting session; Requested
// carries the per-dimension amounts asked for (§8.6 line 643). It
// replaces the deleted adapterv1.ExtendLeaseRequest proto message.
// F-8.6.1.
type ExtendRequest struct {
	// SessionID is the requesting session; the dispatch resolves its tree
	// and tenant from it. An empty SessionID is rejected.
	SessionID string
	// Requested carries every §8.6 line 643 extendable dimension the
	// caller asks to raise. A zero on a dimension indicates the caller
	// did not request it.
	Requested Dimensions
}

// ExtendResponse is the plain-Go §8.6 extension response the in-process
// dispatch returns. It replaces the deleted
// adapterv1.ExtendLeaseResponse proto message and carries the same
// fields: the outcome status, the per-dimension granted amounts, and
// (on REJECTED) the §15.1 line 1080 cool-off details and typed error
// envelope. F-8.6.1; F-8.6.9.
type ExtendResponse struct {
	// Status is the §8.6 line 743 outcome.
	Status ExtendStatus
	// Granted carries every dimension's granted amount. A zero on a
	// dimension means nothing was granted for it.
	Granted Dimensions
	// SubtreeID is the §15.1 line 1080 details.subtreeId — the requesting
	// session, set only on REJECTED.
	SubtreeID string
	// CoolOffExpiryUnixMs is the rejection cool-off expiry in Unix
	// milliseconds, set only on REJECTED.
	CoolOffExpiryUnixMs int64
	// CoolOffExpiresAt is the §15.1 line 1080 details.coolOffExpiresAt —
	// the UTC RFC 3339 rendering of the cool-off expiry, set only on
	// REJECTED.
	CoolOffExpiresAt string
	// Error is the §15.1 line 1080 typed error envelope, set only on
	// REJECTED (EXTENSION_COOL_OFF_ACTIVE). Non-REJECTED responses carry
	// nil so operator tooling treats the code as authoritative. F-8.6.9.
	Error *adapterv1.Error
}
