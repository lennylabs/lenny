// SPDX-License-Identifier: MIT

// Package sessionbudget is the metering proxy's mid-session token-budget
// enforcement fast path. The LLM proxy records the authoritative
// per-request token counts; this package tracks each proxy-mode session's
// cumulative consumption against the session's token budget, and gates
// further proxied requests for an over-budget session.
//
// A proxy-mode session that reaches its budget does not end
// unconditionally. At the exhaustion boundary the enforcer consults an
// injected budget extension (extendOnExhaustion): a granted extension
// raises the session's budget and the session continues; a terminal
// outcome (ceiling reached, user-rejected, error, or a genuine request
// cancellation) ends it; and an elicitation still pending at the caller's
// in-path deadline leaves the session alive but denying every subsequent
// request until the extension resolves out-of-band. The non-extendable and
// nil-extension paths end the session at once (proposal 0023). The enforcer
// ends a session through the session core's GWSEAM-ExpireSession, reached
// through pkg/gateway/seam.
//
// spec: GWLLM
package sessionbudget
