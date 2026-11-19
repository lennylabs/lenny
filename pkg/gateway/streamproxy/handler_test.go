// SPDX-License-Identifier: MIT

package streamproxy_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/streamproxy"
	"github.com/lennylabs/lenny/pkg/gateway/streamproxy/sessionevents"
	"github.com/lennylabs/lenny/tests/testinfra/apifront"
)

// recordingRoutes records the routes a part registers and refuses the
// route at index refuse with ErrInvalidRoute, or none when refuse is
// negative.
type recordingRoutes struct {
	routes []seam.Route
	refuse int
}

func (r *recordingRoutes) HandleRoute(route seam.Route) error {
	if len(r.routes) == r.refuse {
		return seam.ErrInvalidRoute
	}
	r.routes = append(r.routes, route)
	return nil
}

// TestNewRefusesOptionsWithoutTheCoreOperations checks that the handler
// refuses to build without the session lookup, the handoff frames, or the
// request identity, and builds without a bus.
//
// spec: GWSEAM-LookupSession, GWSEAM-ResumedFrame, GWSEAM-RequestTenant
func TestNewRefusesOptionsWithoutTheCoreOperations(t *testing.T) {
	core := sessionserver.New(memstore.New(), sessionserver.Options{}).StreamSeam()
	full := streamproxy.Options{Sessions: core, Frames: core, Identity: core}
	for name, opts := range map[string]streamproxy.Options{
		"no sessions": {Frames: core, Identity: core},
		"no frames":   {Sessions: core, Identity: core},
		"no identity": {Sessions: core, Frames: core},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := streamproxy.New(opts); !errors.Is(err, streamproxy.ErrIncomplete) {
				t.Fatalf("New = %v, want ErrIncomplete", err)
			}
		})
	}
	if _, err := streamproxy.New(full); err != nil {
		t.Fatalf("New without a bus: %v", err)
	}
}

// TestRegisterRoutesNamesTheClientListenerAndTheSessionReadCheck checks
// that the handler registers the event and log streams on the client
// listener behind the session read check, and that a refused route stops
// the registration with the refusal.
//
// spec: GWSEAM-RegisterRoutes
func TestRegisterRoutesNamesTheClientListenerAndTheSessionReadCheck(t *testing.T) {
	core := sessionserver.New(memstore.New(), sessionserver.Options{}).StreamSeam()
	h, err := streamproxy.New(streamproxy.Options{Events: sessionevents.NewBus(0), Sessions: core, Frames: core, Identity: core})
	if err != nil {
		t.Fatal(err)
	}
	rec := &recordingRoutes{refuse: -1}
	if err := h.RegisterRoutes(rec); err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}
	want := map[string]bool{"/v1/sessions/{id}/events": true, "/v1/sessions/{id}/logs": true}
	if len(rec.routes) != len(want) {
		t.Fatalf("registered %d routes, want %d", len(rec.routes), len(want))
	}
	for _, r := range rec.routes {
		if !want[r.Pattern] || r.Method != http.MethodGet || r.Listener != seam.ListenerClient || r.Access != seam.AccessSessionRead || r.Handler == nil {
			t.Errorf("route %+v, want GET on the client listener behind the session read check", r)
		}
		if err := r.Validate(); err != nil {
			t.Errorf("route %s fails validation: %v", r.Pattern, err)
		}
	}

	refusing := &recordingRoutes{refuse: 1}
	if err := h.RegisterRoutes(refusing); !errors.Is(err, seam.ErrInvalidRoute) {
		t.Fatalf("RegisterRoutes with a refused route = %v, want ErrInvalidRoute", err)
	}
}

// failingSessions is a session lookup that fails with a transient error.
type failingSessions struct {
	seam.Sessions
	err error
}

func (f failingSessions) LookupSession(context.Context, string, string) (seam.Session, error) {
	return seam.Session{}, f.err
}

// TestStreamsAnswerAStoreFailureAsAnInternalError checks that the event and
// log streams answer a session lookup that fails for a reason other than a
// missing session with INTERNAL_ERROR, as they did on the store's error.
//
// spec: GWSEAM-LookupSession
func TestStreamsAnswerAStoreFailureAsAnInternalError(t *testing.T) {
	store := memstore.New()
	now := time.Now()
	if err := store.Create(context.Background(), sessionstore.Session{
		UserID: testOwner, ID: "sess_fail", TenantID: "acme", State: session.StateRunning, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	srv := sessionserver.New(store, sessionserver.Options{DevTenantHeader: true})
	core := srv.StreamSeam()
	h, err := streamproxy.New(streamproxy.Options{
		Events:   sessionevents.NewBus(0),
		Sessions: failingSessions{Sessions: core, err: errors.New("postgres unavailable")},
		Frames:   core,
		Identity: core,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.RegisterRoutes(apifront.Routes(srv)); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(asOwner(apifront.Handler(srv)))
	defer ts.Close()
	for _, path := range []string{"/v1/sessions/sess_fail/events", "/v1/sessions/sess_fail/logs"} {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		req.Header.Set("Accept", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body := make([]byte, 512)
		n, _ := resp.Body.Read(body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusInternalServerError || !strings.Contains(string(body[:n]), "INTERNAL_ERROR") {
			t.Errorf("%s = %d %s, want 500 INTERNAL_ERROR", path, resp.StatusCode, body[:n])
		}
	}
}

// TestStreamsTreatANilBusPointerAsNoBus checks that a handler built with a
// nil *sessionevents.Bus answers both streams 503 EVENT_STREAM_UNAVAILABLE,
// as a handler with no bus does, instead of calling the bus through a typed
// nil.
//
// spec: GWSEAM-Subscribe
func TestStreamsTreatANilBusPointerAsNoBus(t *testing.T) {
	store := memstore.New()
	now := time.Now()
	if err := store.Create(context.Background(), sessionstore.Session{
		UserID: testOwner, ID: "sess_nilbus", TenantID: "acme", State: session.StateRunning, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	srv := sessionserver.New(store, sessionserver.Options{DevTenantHeader: true})
	core := srv.StreamSeam()
	var bus *sessionevents.Bus
	h, err := streamproxy.New(streamproxy.Options{Events: bus, Sessions: core, Frames: core, Identity: core})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.RegisterRoutes(apifront.Routes(srv)); err != nil {
		t.Fatal(err)
	}
	handler := asOwner(apifront.Handler(srv))
	for _, path := range []string{"/v1/sessions/sess_nilbus/events", "/v1/sessions/sess_nilbus/logs"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusServiceUnavailable || !strings.Contains(rr.Body.String(), "EVENT_STREAM_UNAVAILABLE") {
			t.Errorf("%s = %d %s, want 503 EVENT_STREAM_UNAVAILABLE", path, rr.Code, rr.Body.String())
		}
	}
}
