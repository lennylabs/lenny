// SPDX-License-Identifier: MIT

// Package lenny is the official Go client SDK for the Lenny gateway.
// It wraps the §15.1 REST session API and the §15.6 SDK contract
// surface so application developers do not re-implement the wire
// protocol from the spec.
//
// A Client is constructed with a gateway base URL and an
// authentication credential. Its methods cover the session lifecycle
// (Create, Get, List, Delete, Finalize, Start, Interrupt, Terminate,
// Resume), decode the §15.1 error envelope into the typed APIError,
// support per-request idempotency keys, and retry retryable errors
// with exponential backoff and jitter.
//
// Client.StreamEvents opens the §15.1 GET /v1/sessions/{id}/events
// Server-Sent Events stream. It returns a Stream that delivers
// decoded events on a channel and reconnects automatically on a
// transport disconnect, resuming from the last delivered sequence
// via the Last-Event-ID header.
//
// The package is split into two parts. This package holds the REST
// client, the event stream, the typed error, and the wire types.
// Sub-package github.com/lennylabs/lenny/sdks/client/go/webhook
// holds the §14 webhook signature verifier.
//
// The MCP-client surface named in §15.6 is not yet implemented in
// this SDK.
package lenny
