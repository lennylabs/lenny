// SPDX-License-Identifier: MIT

// Package toolseam builds the session core's side of the platform tools
// seam for tests that wire the MCP tools with a message executor or a
// messaging coordinator but no full session server. The MCP tools reach the
// executor and the inbox only through the seam interface, so such a test
// hands them the operations of a session server built around the executor
// or the coordinator it holds.
//
// The package is test wiring that decides no platform behavior, so it
// carries no specification annotation.
package toolseam

import (
	"context"

	"github.com/lennylabs/lenny/pkg/gateway/core/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// Executor returns the session core's message delivery over exec, through
// which the MCP tools deliver a message to a runtime.
func Executor(exec executor.Executor) seam.MessageDelivery {
	return sessionserver.New(memstore.New(), sessionserver.Options{Executor: exec}).ToolsSeam()
}

// Messaging returns the session core's message delivery over coord, through
// which the MCP tools place a message in a session's inbox.
func Messaging(coord *sessioninbox.Coordinator) seam.MessageDelivery {
	return sessionserver.New(memstore.New(), sessionserver.Options{Messaging: coord}).ToolsSeam()
}

// TenantCaller is a caller identity that answers every request with a
// caller of one tenant, for a test that serves the MCP attach stream under a
// fixed tenant.
type TenantCaller string

// RequestCaller answers the caller of the tenant.
func (t TenantCaller) RequestCaller(context.Context) (seam.Caller, bool) {
	return seam.Caller{TenantID: string(t)}, true
}
