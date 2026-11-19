// SPDX-License-Identifier: MIT

package streamproxy

import (
	"errors"
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/seam"
)

// Options carries the stream proxy handler's dependencies. Sessions,
// Frames, and Identity reach the session core and the API front through
// the seam interface and are required. Events is the session event bus
// the handler reads.
type Options struct {
	// Events is the session event bus the event and log streams replay
	// from and tail. When nil, or a typed nil such as a nil bus pointer,
	// both endpoints answer 503 EVENT_STREAM_UNAVAILABLE.
	Events seam.Events
	// Sessions looks up the session a request names (GWSEAM-LookupSession).
	Sessions seam.Sessions
	// Frames builds the frames a reader attaching after a coordinator
	// handoff receives (GWSEAM-ResumedFrame and
	// GWSEAM-ChildrenReattachedFrame).
	Frames seam.StreamFrames
	// Identity resolves the tenant a request runs as (GWSEAM-RequestTenant).
	Identity seam.RequestIdentity
	// Clock supplies the time for the gap marker's checkpoint timestamp and
	// for the pagination cursors. Nil uses time.Now.
	Clock func() time.Time
}

// Handler serves the session event stream, GET /v1/sessions/{id}/events,
// and the session log, GET /v1/sessions/{id}/logs, from the session event
// bus.
type Handler struct {
	events   seam.Events
	sessions seam.Sessions
	frames   seam.StreamFrames
	identity seam.RequestIdentity
	clock    func() time.Time
}

// ErrIncomplete reports Options that lack Sessions, Frames, or Identity.
var ErrIncomplete = errors.New("streamproxy: options need Sessions, Frames, and Identity")

// New returns a Handler over opts, or ErrIncomplete when opts lacks
// Sessions, Frames, or Identity: each endpoint looks up the session in the
// request's tenant, and the event stream synthesizes the handoff frames.
func New(opts Options) (*Handler, error) {
	if opts.Sessions == nil || opts.Frames == nil || opts.Identity == nil {
		return nil, ErrIncomplete
	}
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}
	return &Handler{
		events:   seam.EventsOrNil(opts.Events),
		sessions: opts.Sessions,
		frames:   opts.Frames,
		identity: opts.Identity,
		clock:    clock,
	}, nil
}

func (h *Handler) now() time.Time {
	return h.clock()
}

// RegisterRoutes registers the event and log streams on the client
// listener behind the session read check, the access the session routes
// they shared the session server's mux with applied.
//
// spec: GWSEAM-RegisterRoutes
func (h *Handler) RegisterRoutes(routes seam.Routes) error {
	for _, r := range []seam.Route{
		{Method: http.MethodGet, Pattern: "/v1/sessions/{id}/events", Handler: http.HandlerFunc(h.handleEvents)},
		// The `lenny session logs` target; session logs over the durable
		// event store, a content-negotiated SSE or JSON envelope with the
		// `--since` filter.
		{Method: http.MethodGet, Pattern: "/v1/sessions/{id}/logs", Handler: http.HandlerFunc(h.handleLogs)},
	} {
		r.Listener = seam.ListenerClient
		r.Access = seam.AccessSessionRead
		if err := routes.HandleRoute(r); err != nil {
			return err
		}
	}
	return nil
}
