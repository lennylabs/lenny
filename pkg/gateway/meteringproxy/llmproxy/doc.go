// SPDX-License-Identifier: MIT

// Package llmproxy is the gateway's LLM reverse proxy, the request path of
// the metering proxy seam: the credential-injecting path that lets a
// proxy-mode agent pod reach an upstream LLM provider without ever holding
// the provider's API key.
//
// A translator converts the agent pod's proxy-dialect request into an
// upstream provider's native wire format, the proxy injects the real
// upstream credential read from the Token Service in-memory cache, and the
// translator converts the upstream response back into the pod's dialect.
// The package also registers the flags only the proxy reads and builds the
// proxy's HTTP server on its own listener. It reaches the session core only
// through pkg/gateway/seam.
//
// spec: GWLLM, LLMUP
package llmproxy
