// SPDX-License-Identifier: MIT

// Package streamproxy is the gateway's stream proxy seam. It serves the
// session event stream, GET /v1/sessions/{id}/events, and the session log,
// GET /v1/sessions/{id}/logs, each as a Server-Sent Events stream with
// cursor replay or as a paginated JSON envelope over the retained events.
// The session event bus they read, with its cross-replica relay, is the
// subpackage sessionevents, which serves the seam interface's event
// operations to the session core and the other seams.
//
// The package reaches the session core only through pkg/gateway/seam: it
// looks up the session a request names, resolves the request's tenant, and
// asks the core for the frames a reader attaching after a coordinator
// handoff receives. It registers its routes through the route registration
// interface.
//
// spec: GWSTREAM, STREAM, CAPIEVT
package streamproxy
