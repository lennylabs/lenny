// SPDX-License-Identifier: MIT

// Package tlsprobe implements the §10.3 gateway startup TLS
// probe. Before a gateway replica is marked ready it verifies that a
// TLS handshake to Redis and PgBouncer succeeds and that a plaintext
// connection to the same endpoint is refused. A misconfigured backend
// (wrong port, missing certificate, or a plaintext listener left open)
// becomes a deployment failure caught before traffic is served rather
// than a silent runtime failure.
//
// The probe runs two legs per target:
//
//  1. A TLS handshake to the endpoint, which must succeed. This catches
//     the "wrong port / missing cert" class the spec calls out.
//  2. A plaintext (non-TLS) protocol exchange, which must be refused.
//     Redis and PgBouncer speak different wire protocols, so "is
//     plaintext accepted?" is answered per backend: a Redis listener is
//     PINGed inline; a PgBouncer listener is asked to negotiate SSL via
//     the Postgres SSLRequest packet. Only a positive confirmation that
//     the server completed a plaintext exchange fails the probe; every
//     dial/IO error is read as a refusal (the desired TLS-only posture).
//
// spec: GW
package tlsprobe
