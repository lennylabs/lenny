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
// The package is split into two parts. This package holds the REST
// client, the typed error, and the wire types. Sub-package
// github.com/lennylabs/lenny/sdks/client/go/webhook holds the §14
// webhook signature verifier.
//
// The streaming and MCP-client surfaces named in §15.6 are not yet
// implemented in this SDK.
package lenny
