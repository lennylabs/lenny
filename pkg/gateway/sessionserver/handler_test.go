// SPDX-License-Identifier: MIT

package sessionserver

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/lennylabs/lenny/pkg/gateway/api/pipeline"
	"github.com/lennylabs/lenny/pkg/gateway/api/routetable"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/tests/testinfra/apifront"
)

// Handler returns the handler of the client listener's session paths that
// the API front's request pipeline builds for s, as the composition root
// mounts it, so a test drives the session routes behind their checks.
func (s *Server) Handler() http.Handler {
	return apifront.Handler(s)
}

// ForwardHandler returns the handler of the forward listener's session
// paths that the API front's request pipeline builds for s.
func (s *Server) ForwardHandler() http.Handler {
	return apifront.ForwardHandler(s)
}

// spyPipeline returns the handler of the client listener's session paths of
// a request pipeline over s's checks that mounts only spy, at pattern ("METHOD
// /path") behind access, so a test observes whether the check lets a request
// reach the handler.
func (s *Server) spyPipeline(pattern string, access seam.RouteAccess, spy http.HandlerFunc) http.Handler {
	method, path, ok := strings.Cut(pattern, " ")
	if !ok {
		method, path = seam.MethodAny, pattern
	}
	table := routetable.New()
	if err := table.HandleRoute(seam.Route{
		Listener: seam.ListenerClient, Method: method, Pattern: path, Access: access, Handler: spy,
	}); err != nil {
		panic(fmt.Sprintf("register the spy route: %v", err))
	}
	sessions, permissions := s.RequestChecks()
	p, err := pipeline.New(pipeline.Config{
		Table: table, Sessions: sessions, Permissions: permissions, DevTenantHeader: s.devTenantHeader,
	})
	if err != nil {
		panic(fmt.Sprintf("build the spy pipeline: %v", err))
	}
	return p.Handler(seam.ListenerClient)
}
