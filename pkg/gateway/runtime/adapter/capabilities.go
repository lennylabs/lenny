// SPDX-License-Identifier: MIT

// Package adapter defines the external protocol adapter types from §15.
// v1 builds the Capabilities declaration the §9.1 discovery surface
// embeds; the full ExternalAdapterRegistry and the protocol adapters are
// built alongside the protocol surfaces.
package adapter

// Capabilities declares the routing and protocol capabilities of an
// external protocol adapter (the §15 AdapterCapabilities type). Every
// §9.1 discovery response embeds it so a consumer can inspect
// SupportsElicitation before starting an elicitation-dependent workflow.
type Capabilities struct {
	// PathPrefix is the URL path prefix the adapter owns (for example
	// "/v1" or "/mcp"). It is unique across registered adapters.
	PathPrefix string `json:"pathPrefix"`

	// Protocol is the protocol identifier (for example "rest" or "mcp").
	// It is used in audit events and metrics.
	Protocol string `json:"protocol"`

	// SupportsSessionContinuity reports that the adapter can resume an
	// interrupted session after a gateway restart or failover.
	SupportsSessionContinuity bool `json:"supportsSessionContinuity"`

	// SupportsDelegation reports that the adapter can receive and forward
	// delegate_task calls from parent sessions.
	SupportsDelegation bool `json:"supportsDelegation"`

	// SupportsElicitation reports that the adapter can surface
	// lenny/request_elicitation calls to the client. A consumer must not
	// start an elicitation-dependent workflow when this is false.
	SupportsElicitation bool `json:"supportsElicitation"`

	// SupportsInterrupt reports that the adapter can surface interrupt
	// signals from the lifecycle channel to the client.
	SupportsInterrupt bool `json:"supportsInterrupt"`
}
