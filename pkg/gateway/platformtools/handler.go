// SPDX-License-Identifier: MIT

package platformtools

import (
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/api/errorclassify"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/gateway/session/memorystore"
	"github.com/lennylabs/lenny/pkg/session/interactionstore"
	"github.com/lennylabs/lenny/pkg/session/sessionstore"
)

// maxJSONBodyBytes caps a JSON request body the platform tools seam's
// client routes read, as the session server's routes cap theirs.
const maxJSONBodyBytes int64 = 1024 * 1024

// Options carries the client route handler's dependencies. Sessions,
// Identity, Callers, ActiveCallers, and Store are required; the others
// are optional, and a route whose store is absent answers as it did on
// the session server.
type Options struct {
	// Sessions looks up the session a memory route names
	// (GWSEAM-LookupSession).
	Sessions seam.Sessions
	// Identity resolves the tenant a request runs as (GWSEAM-RequestTenant).
	Identity seam.RequestIdentity
	// Callers resolves the authenticated caller a request runs as
	// (GWSEAM-RequestCaller).
	Callers seam.CallerIdentity
	// ActiveCallers applies the user lifecycle check before an interaction
	// is resolved (GWSEAM-AdmitActiveCaller).
	ActiveCallers seam.ActiveCallers
	// Store is the session store the tree read walks.
	Store sessionstore.Store
	// Memory backs the memory routes. When nil they answer 503
	// MEMORY_UNAVAILABLE.
	Memory memorystore.Store
	// Interactions backs the interaction resolution routes. When nil they
	// answer 503 INTERACTIONS_UNAVAILABLE.
	Interactions interactionstore.Store
	// Approvals is the tool approval wait registry a tool-use resolution
	// delivers its verdict onto. When nil no waiter is woken.
	Approvals seam.ToolApprovals
	// InteractionAudit receives the audit event of each resolved
	// interaction. When nil no event is written.
	InteractionAudit InteractionAuditSink
	// TreeCycles receives a cycle the tree read's walker meets. When nil
	// the walker truncates the cycle and reports nothing.
	TreeCycles mcptools.TreeCycleObserver
}

// Handler serves the platform tools seam's client routes: the memory
// routes, the tool-use and elicitation resolution routes, and the
// delegation tree read.
type Handler struct {
	sessions     seam.Sessions
	identity     seam.RequestIdentity
	callers      seam.CallerIdentity
	active       seam.ActiveCallers
	store        sessionstore.Store
	memory       memorystore.Store
	interactions interactionstore.Store
	approvals    seam.ToolApprovals
	audit        InteractionAuditSink
	treeCycles   mcptools.TreeCycleObserver
}

// ErrIncomplete reports Options that lack Sessions, Identity, Callers,
// ActiveCallers, or Store.
var ErrIncomplete = errors.New("platformtools: options need Sessions, Identity, Callers, ActiveCallers, and Store")

// New returns a Handler over opts, or ErrIncomplete when opts lacks one of
// the required operations or the session store.
func New(opts Options) (*Handler, error) {
	if opts.Sessions == nil || opts.Identity == nil || opts.Callers == nil || opts.ActiveCallers == nil || opts.Store == nil {
		return nil, ErrIncomplete
	}
	return &Handler{
		sessions:     opts.Sessions,
		identity:     opts.Identity,
		callers:      opts.Callers,
		active:       opts.ActiveCallers,
		store:        opts.Store,
		memory:       opts.Memory,
		interactions: opts.Interactions,
		approvals:    opts.Approvals,
		audit:        opts.InteractionAudit,
		treeCycles:   opts.TreeCycles,
	}, nil
}

// RegisterRoutes registers the client routes on the client listener with
// the access check each had on the session server's mux: read access for
// the memory query and the tree read, manage access for the memory write
// and delete and the four interaction resolutions.
//
// spec: GWSEAM-RegisterRoutes
func (h *Handler) RegisterRoutes(routes seam.Routes) error {
	for _, r := range []seam.Route{
		{Method: http.MethodPost, Pattern: "/v1/sessions/{id}/memory", Access: seam.AccessSessionManage, Handler: http.HandlerFunc(h.handleMemoryWrite)},
		{Method: http.MethodGet, Pattern: "/v1/sessions/{id}/memory", Access: seam.AccessSessionRead, Handler: http.HandlerFunc(h.handleMemoryQuery)},
		{Method: http.MethodDelete, Pattern: "/v1/sessions/{id}/memory/{memoryId}", Access: seam.AccessSessionManage, Handler: http.HandlerFunc(h.handleMemoryDelete)},
		{Method: http.MethodGet, Pattern: "/v1/sessions/{id}/tree", Access: seam.AccessSessionRead, Handler: http.HandlerFunc(h.handleTree)},
		// Path parameters use the camelCase route templates.
		{Method: http.MethodPost, Pattern: "/v1/sessions/{id}/tool-use/{toolCallId}/approve", Access: seam.AccessSessionManage, Handler: http.HandlerFunc(h.handleToolUseApprove)},
		{Method: http.MethodPost, Pattern: "/v1/sessions/{id}/tool-use/{toolCallId}/deny", Access: seam.AccessSessionManage, Handler: http.HandlerFunc(h.handleToolUseDeny)},
		{Method: http.MethodPost, Pattern: "/v1/sessions/{id}/elicitations/{elicitationId}/respond", Access: seam.AccessSessionManage, Handler: http.HandlerFunc(h.handleElicitationRespond)},
		{Method: http.MethodPost, Pattern: "/v1/sessions/{id}/elicitations/{elicitationId}/dismiss", Access: seam.AccessSessionManage, Handler: http.HandlerFunc(h.handleElicitationDismiss)},
	} {
		r.Listener = seam.ListenerClient
		if err := routes.HandleRoute(r); err != nil {
			return err
		}
	}
	return nil
}

// writeError writes the client API error envelope every gateway part's
// REST surface shares.
func (h *Handler) writeError(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	errorclassify.WriteError(w, status, code, message, details)
}

// jsonReader bounds a JSON request body at maxJSONBodyBytes.
func jsonReader(w http.ResponseWriter, r *http.Request) interface {
	Read(p []byte) (int, error)
	Close() error
} {
	return http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
}
