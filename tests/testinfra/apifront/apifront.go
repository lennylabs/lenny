// SPDX-License-Identifier: MIT

// Package apifront gives a test's session server the API front's route
// table, as cmd/lenny-gateway does, so a part a test attaches registers its
// routes through the API front and the session server's handlers mount
// them behind their access checks.
//
// The package is test wiring that decides no platform behavior, so it
// carries no specification annotation.
package apifront

import (
	"github.com/lennylabs/lenny/pkg/gateway/api/routetable"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// Routes returns the API front's route table that srv mounts, and gives srv
// a new one when it has none. Call it before srv's handlers are built.
func Routes(srv *sessionserver.Server) *routetable.Table {
	if t, ok := srv.RouteTable().(*routetable.Table); ok {
		return t
	}
	t := routetable.New()
	srv.SetRouteTable(t)
	return t
}
