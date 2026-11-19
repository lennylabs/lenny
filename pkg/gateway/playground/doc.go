// SPDX-License-Identifier: MIT

// Package playground implements the §27 web playground: a browser UI
// served by the gateway at /playground plus the gateway endpoints
// that back it.
//
// The playground is a client of the public MCP and REST surface for
// session, chat, and discovery traffic (§27.5). The only
// playground-specific endpoints are the auth gatekeepers in §27.3.1:
//
//   - GET  /playground/auth/login    — start the OIDC authorization-code flow
//   - GET  /playground/auth/callback — OIDC provider redirect target
//   - POST /playground/auth/logout   — clear the playground session
//   - GET  /playground/auth/error    — render an OIDC failure to the user
//   - POST /v1/playground/token      — mode-polymorphic session-JWT mint
//
// plus the embedded single-page-app bundle served from /playground/.
//
// Gating. The playground is feature-flag gated (§27.2). When the
// gateway is built without playground.enabled the package is not
// mounted and /playground/* returns 404. The gateway reads the §27.2
// Helm values from its own configuration; see Config below for the
// field-to-value mapping.
//
// spec: PLAY
package playground
