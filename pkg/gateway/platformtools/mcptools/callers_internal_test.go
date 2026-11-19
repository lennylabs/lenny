// SPDX-License-Identifier: MIT

package mcptools

import "github.com/lennylabs/lenny/pkg/gateway/api/pipeline"

// The package's tests install principals with the authentication
// middleware's context helpers and build Deps without Callers, so they
// resolve the caller through the session core's reader of that principal,
// as the composition root wires Deps.Callers.
func init() {
	fallbackCallers = pipeline.Callers{}
}
