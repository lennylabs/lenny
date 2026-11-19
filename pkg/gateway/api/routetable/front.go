// SPDX-License-Identifier: MIT

package routetable

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/lennylabs/lenny/pkg/gateway/seam"
)

// RegisterFrontRoutes registers the API front's own routes on the client
// listener: the OpenAPI document openAPI serves at /openapi.yaml,
// /openapi.json, /v1/openapi.json, and /v1/openapi.yaml (§15.1), each for
// any method and with no route check by the API front, because the
// document is public.
//
// spec: GWSEAM-RegisterRoutes
func RegisterFrontRoutes(routes seam.Routes, openAPI http.Handler) error {
	for _, p := range []string{"/openapi.yaml", "/openapi.json", "/v1/openapi.json", "/v1/openapi.yaml"} {
		if err := routes.HandleRoute(seam.Route{
			Listener: seam.ListenerClient, Method: seam.MethodAny, Pattern: p,
			Access: seam.AccessHandler, Handler: openAPI,
		}); err != nil {
			return err
		}
	}
	return nil
}

// HandlerMux registers each route a Handle call names on one listener of a
// route table, with no route check by the API front, for a package that
// mounts its handlers on a mux it is given, such as the adapter registry.
// Use it only for a package that belongs to no gateway part; a part
// registers its own routes through seam.Routes.
type HandlerMux struct {
	Routes   seam.Routes
	Listener seam.Listener
}

// Handle registers h under pattern, a net/http.ServeMux pattern with or
// without a leading method. As net/http.ServeMux.Handle does, it panics on
// a pattern the table refuses, so a conflict stops the gateway at startup.
func (m HandlerMux) Handle(pattern string, h http.Handler) {
	method, path := seam.MethodAny, pattern
	if before, after, ok := strings.Cut(pattern, " "); ok {
		method, path = before, strings.TrimLeft(after, " ")
	}
	if err := m.Routes.HandleRoute(seam.Route{
		Listener: m.Listener, Method: method, Pattern: path, Access: seam.AccessHandler, Handler: h,
	}); err != nil {
		panic(fmt.Sprintf("routetable: register %s: %v", pattern, err))
	}
}
