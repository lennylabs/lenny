// SPDX-License-Identifier: MIT

package opsserver

import (
	"github.com/lennylabs/lenny/pkg/releasechannel"
)

// registerReleaseChannelRoutes wires the §25.8 release-channel manifest
// publisher onto the Server's mux when a Publisher is configured. The
// publisher serves GET /v1/latest with the Ed25519-signed `latest`
// manifest in its JSON body and the X-Lenny-Release-Signature header.
//
// The route is registered only when opts.ReleaseChannel is non-nil so
// a deployment that has not yet configured an operator-held signing
// key (the air-gap mirror story) does not silently serve unsigned
// responses on the canonical path. lenny-ops surfaces a 404 on the
// path when no publisher is configured, which matches the §25.8
// "channel returned an unexpected status" failure mode the upgrade-
// check client already handles.
func (s *Server) registerReleaseChannelRoutes() {
	if s.releaseChannel == nil {
		return
	}
	// The publisher itself is an http.Handler. We attach a GET-only
	// pattern so a POST or PUT surfaces a 405 from the mux rather than
	// dropping through to the publisher's own method check.
	s.mux.Handle("GET "+releasechannel.EndpointPath, s.releaseChannel)
}
