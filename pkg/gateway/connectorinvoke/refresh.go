// SPDX-License-Identifier: MIT

package connectorinvoke

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/capabilityinference"
)

// CapabilityRefreshResult is the outcome of a §5.1 connector capability
// refresh: the union capability set across every tool, the per-tool
// capability map, and the names of tools whose absent MCP annotations
// forced the §5.1 strict admin default (the gateway emits a WARN for
// each, per §5.1 line 327).
type CapabilityRefreshResult struct {
	// Mode is the §5.1 inference mode applied (the connector's
	// CapabilityInferenceMode, defaulted to strict).
	Mode capabilityinference.Mode
	// Capabilities is the deduplicated union of inferred capabilities.
	Capabilities []capabilityinference.Capability
	// ToolCapabilities maps each discovered tool to its capability set.
	ToolCapabilities map[string][]capabilityinference.Capability
	// UnannotatedAdminTools names the tools inferred as admin because
	// they carried no MCP annotations under strict mode.
	UnannotatedAdminTools []string
}

// RefreshCapabilities runs the §9.3 line 136 / §5.1 capability inference
// for a connector on the sanctioned outbound path: it dials the
// connector's MCP endpoint, reads tools/list, infers each tool's
// capability set from its MCP ToolAnnotations under the connector's
// CapabilityInferenceMode, and persists the union and per-tool maps back
// onto the connector. The synchronous registration path makes no
// outbound call (§15.1 line 1144), so capability inference is this
// separate post-create refresh rather than part of the create handler.
//
// userID and environment scope the credential lookup for the outbound
// handshake; a public connector with no stored credential is dialed
// unauthenticated. The connector must be registered and active.
//
// spec: §9.3 line 136; §5.1 lines 312-329.
func (iv *Invoker) RefreshCapabilities(ctx context.Context, tenantID, connectorID, userID, environment string) (CapabilityRefreshResult, error) {
	conn, err := iv.connectors.Get(ctx, tenantID, connectorID)
	if err != nil {
		return CapabilityRefreshResult{}, err
	}
	if !conn.IsActive() {
		return CapabilityRefreshResult{}, ErrConnectorInactive
	}

	mode := conn.CapabilityInferenceMode
	if !mode.IsValid() {
		mode = capabilityinference.DefaultMode
	}

	bearer := iv.bearerFor(ctx, conn, userID, environment)
	sess, _, err := iv.client.Initialize(ctx, conn.MCPServerURL, bearer)
	if err != nil {
		return CapabilityRefreshResult{}, fmt.Errorf("connectorinvoke: initialize %q: %w", connectorID, err)
	}
	tools, err := sess.ListTools(ctx)
	if err != nil {
		return CapabilityRefreshResult{}, fmt.Errorf("connectorinvoke: tools/list %q: %w", connectorID, err)
	}

	result := inferCapabilities(conn.ID, tools, mode)

	if _, err := iv.connectors.Update(ctx, tenantID, connectorID, func(c *connectorstore.Connector) error {
		c.CapabilityInferenceMode = result.Mode
		c.Capabilities = result.Capabilities
		c.ToolCapabilities = result.ToolCapabilities
		c.CapabilitiesRefreshedAt = iv.clock()
		return nil
	}); err != nil {
		return CapabilityRefreshResult{}, fmt.Errorf("connectorinvoke: persist capabilities %q: %w", connectorID, err)
	}
	return result, nil
}

// inferCapabilities applies the §5.1 inference table to a connector's
// tools/list and assembles the per-tool map, the deduplicated union, and
// the WARN list for unannotated-admin tools. It emits the §5.1 line 327
// WARN log for each tool inferred as admin under strict mode.
func inferCapabilities(connectorID string, tools []ToolDescriptor, mode capabilityinference.Mode) CapabilityRefreshResult {
	result := CapabilityRefreshResult{
		Mode:             mode,
		ToolCapabilities: make(map[string][]capabilityinference.Capability, len(tools)),
	}
	union := map[capabilityinference.Capability]bool{}
	for _, t := range tools {
		res := capabilityinference.Infer(annotationsOf(t.Annotations), mode)
		result.ToolCapabilities[t.Name] = res.Capabilities
		for _, c := range res.Capabilities {
			union[c] = true
		}
		if res.InferredAdminUnannotated {
			result.UnannotatedAdminTools = append(result.UnannotatedAdminTools, t.Name)
			// spec: §5.1 line 327 — emit the verbatim registration-time WARN.
			// WarnMessage is the single source of the spec wording; the
			// connector capability refresh is the sanctioned outbound
			// discovery path that stands in for registration-time tool
			// discovery, because the synchronous create handler makes no
			// outbound call (§15.1 line 1144). Routing through WarnMessage
			// gives the spec text a production caller.
			slog.Warn(capabilityinference.WarnMessage(t.Name, connectorID),
				"tool", t.Name, "connector", connectorID)
		}
	}
	for _, c := range capabilityinference.AllCapabilities() {
		if union[c] {
			result.Capabilities = append(result.Capabilities, c)
		}
	}
	return result
}

// annotationsOf maps the connectorinvoke ToolAnnotations wire block onto
// the capabilityinference input. A nil block is an unannotated tool.
func annotationsOf(a *ToolAnnotations) capabilityinference.ToolAnnotations {
	if a == nil {
		return capabilityinference.ToolAnnotations{}
	}
	return capabilityinference.ToolAnnotations{
		ReadOnlyHint:    a.ReadOnlyHint,
		DestructiveHint: a.DestructiveHint,
		OpenWorldHint:   a.OpenWorldHint,
	}
}
