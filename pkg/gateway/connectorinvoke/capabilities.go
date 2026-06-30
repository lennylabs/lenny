// SPDX-License-Identifier: MIT

package connectorinvoke

import (
	"context"
	"errors"
	"fmt"

	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/capabilityinference"
)

// EnvironmentResolver loads a §10.6 environment by (tenant, name) so the
// Invoker can apply the environment's connectorSelector capability
// filter to an outbound connector tools/call. *environmentstore.Memory
// and the pgstore satisfy it. A nil resolver on the Invoker leaves the
// §10.6 connector-capability gate open (no environment policy wired).
//
// spec: §10.6 lines 595-599, line 607.
type EnvironmentResolver interface {
	Get(ctx context.Context, tenantID, name string) (environmentstore.Environment, error)
}

// WithEnvironments wires the §10.6 environment registry so CallTool
// enforces the connectorSelector capability filter of the calling
// session's environment before dialing. A nil resolver leaves the gate
// open. spec: §10.6 line 607.
func (iv *Invoker) WithEnvironments(envs EnvironmentResolver) *Invoker {
	iv.environments = envs
	return iv
}

// CapabilityDeniedError reports that a §10.6 environment connectorSelector
// capability filter denied a connector tools/call. The gateway maps it to
// the §5.3 call-time TOOL_CAPABILITY_DENIED error code.
type CapabilityDeniedError struct {
	Connector   string
	Tool        string
	Environment string
	Capability  string
}

func (e *CapabilityDeniedError) Error() string {
	return fmt.Sprintf("connectorinvoke: tool %q on connector %q denied by environment %q capability filter (capability %q)",
		e.Tool, e.Connector, e.Environment, e.Capability)
}

// enforceConnectorCapabilities applies the §10.6 connectorSelector
// capability filter of the named environment to a connector tools/call.
// The filter governs only connectors the environment's connectorSelector
// admits; a connector outside the selector or a session not bound to a
// known environment is left unfiltered (the capability filter is
// environment-defined). A tool with no inferred capabilities falls back
// to the §5.1 conservative admin default so a restrictive filter fails
// closed on an un-inferred tool.
//
// spec: §10.6 lines 595-599, line 607; §5.1 strict inference default.
func (iv *Invoker) enforceConnectorCapabilities(ctx context.Context, tenantID, environment string, conn connectorstore.Connector, toolName string) error {
	if iv.environments == nil || environment == "" {
		return nil
	}
	env, err := iv.environments.Get(ctx, tenantID, environment)
	if err != nil {
		if errors.Is(err, environmentstore.ErrNotFound) {
			// The session is not scoped to a known environment; the
			// connectorSelector filter is environment-defined, so there is
			// nothing to enforce.
			return nil
		}
		return err
	}
	cs := env.ConnectorSelector
	if !cs.Admits(conn.ID, conn.Labels) {
		return nil
	}
	caps := capStrings(conn.ToolCapabilities[toolName])
	if len(caps) == 0 {
		// spec: §5.1 — an un-inferred tool defaults to the conservative
		// admin capability, so a restrictive environment filter fails
		// closed rather than open on a connector that was never refreshed.
		caps = []string{string(capabilityinference.CapAdmin)}
	}
	if ok, blocked := cs.PermitTool(caps); !ok {
		return &CapabilityDeniedError{
			Connector:   conn.ID,
			Tool:        toolName,
			Environment: env.Name,
			Capability:  blocked,
		}
	}
	return nil
}

// capStrings converts a typed §5.1 capability slice to plain strings for
// the environmentstore capability-filter evaluator.
func capStrings(caps []capabilityinference.Capability) []string {
	if len(caps) == 0 {
		return nil
	}
	out := make([]string, len(caps))
	for i, c := range caps {
		out[i] = string(c)
	}
	return out
}
