// SPDX-License-Identifier: MIT

package routetable_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/api/routetable"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
)

func marker(name string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(name))
	})
}

func serve(h http.Handler, method, path string) (int, string) {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec.Code, rec.Body.String()
}

// TestHandleRouteMountsHandlerRoutesOnTheirListenerMux checks that a route
// that leaves authentication to its handler is mounted on its listener's
// mux as it registers, that the client and internal listeners share one
// mux, that the forward listener serves only its own routes, and that a
// route for any method answers every method.
// spec: GWSEAM-HandleRoute
func TestHandleRouteMountsHandlerRoutesOnTheirListenerMux(t *testing.T) {
	table := routetable.New()
	for _, r := range []seam.Route{
		{Listener: seam.ListenerClient, Method: seam.MethodAny, Pattern: "/playground/", Access: seam.AccessHandler, Handler: marker("playground")},
		{Listener: seam.ListenerInternal, Method: http.MethodGet, Pattern: "/healthz", Access: seam.AccessHandler, Handler: marker("healthz")},
		{Listener: seam.ListenerForward, Method: seam.MethodAny, Pattern: "/mcp", Access: seam.AccessHandler, Handler: marker("mcp")},
	} {
		if err := table.HandleRoute(r); err != nil {
			t.Fatalf("HandleRoute %s %s: %v", r.Method, r.Pattern, err)
		}
	}
	if table.Handler(seam.ListenerClient) != table.Handler(seam.ListenerInternal) {
		t.Fatal("the client and internal listeners do not share one mux")
	}
	if table.Handler(seam.ListenerPodProxy) != nil {
		t.Fatal("the API front serves a mux for the pod proxy listener")
	}
	shared, forward := table.Handler(seam.ListenerClient), table.Handler(seam.ListenerForward)
	for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		if code, body := serve(shared, m, "/playground/app.js"); code != http.StatusOK || body != "playground" {
			t.Errorf("%s /playground/app.js = %d %q, want the any-method route", m, code, body)
		}
	}
	if code, body := serve(shared, http.MethodGet, "/healthz"); code != http.StatusOK || body != "healthz" {
		t.Errorf("GET /healthz on the shared mux = %d %q", code, body)
	}
	if code, _ := serve(shared, http.MethodPost, "/healthz"); code != http.StatusMethodNotAllowed {
		t.Errorf("POST /healthz = %d, want 405 from the method-scoped route", code)
	}
	if code, _ := serve(forward, http.MethodGet, "/healthz"); code != http.StatusNotFound {
		t.Errorf("GET /healthz on the forward mux = %d, want 404", code)
	}
	if code, body := serve(forward, http.MethodPost, "/mcp"); code != http.StatusOK || body != "mcp" {
		t.Errorf("POST /mcp on the forward mux = %d %q", code, body)
	}
	if code, _ := serve(shared, http.MethodPost, "/mcp"); code != http.StatusNotFound {
		t.Errorf("POST /mcp on the shared mux = %d, want 404", code)
	}
	if got := len(table.Routes()); got != 3 {
		t.Errorf("Routes() holds %d routes, want 3", got)
	}
}

// TestPipelineRoutesReturnsHeldRoutesAndClosesTheirRegistration checks
// that a route whose access check the request pipeline applies, and a route
// under the session paths that leaves authentication to its handler, is not
// mounted on the listener mux, that PipelineRoutes returns those routes of
// one listener in registration order, and that it refuses a later held
// route on that listener while still accepting a handler route outside the
// session paths there and a held route on another listener.
//
// spec: GWSEAM-HandleRoute
func TestPipelineRoutesReturnsHeldRoutesAndClosesTheirRegistration(t *testing.T) {
	table := routetable.New()
	upload := seam.Route{Listener: seam.ListenerClient, Method: http.MethodPost, Pattern: "/v1/sessions/{id}/upload-to-session", Access: seam.AccessSessionManage, Handler: marker("upload")}
	events := seam.Route{Listener: seam.ListenerClient, Method: http.MethodGet, Pattern: "/v1/sessions/{id}/events", Access: seam.AccessSessionRead, Handler: marker("events")}
	public := seam.Route{Listener: seam.ListenerClient, Method: http.MethodGet, Pattern: "/v1/runtimes/{name}/meta/{key}", Access: seam.AccessHandler, Handler: marker("public")}
	forwarded := upload
	forwarded.Listener = seam.ListenerForward
	for _, r := range []seam.Route{upload, events, public, forwarded} {
		if err := table.HandleRoute(r); err != nil {
			t.Fatalf("HandleRoute %s %s: %v", r.Method, r.Pattern, err)
		}
	}
	if code, _ := serve(table.Handler(seam.ListenerClient), http.MethodGet, "/v1/sessions/s1/events"); code != http.StatusNotFound {
		t.Errorf("a pipeline route answered %d on the listener mux, want 404", code)
	}
	if code, _ := serve(table.Handler(seam.ListenerClient), http.MethodGet, "/v1/runtimes/r/meta/k"); code != http.StatusNotFound {
		t.Errorf("a handler route under the session paths answered %d on the listener mux, want 404", code)
	}
	got := table.PipelineRoutes(seam.ListenerClient)
	if len(got) != 3 || got[0].Pattern != upload.Pattern || got[1].Pattern != events.Pattern || got[2].Pattern != public.Pattern {
		t.Fatalf("PipelineRoutes(client) = %+v, want the upload, the events, then the public route", got)
	}
	late := seam.Route{Listener: seam.ListenerClient, Method: http.MethodGet, Pattern: "/v1/sessions/{id}/late", Access: seam.AccessSessionRead, Handler: marker("late")}
	if err := table.HandleRoute(late); !errors.Is(err, seam.ErrInvalidRoute) {
		t.Errorf("a pipeline route after PipelineRoutes = %v, want ErrInvalidRoute", err)
	}
	if err := table.HandleRoute(seam.Route{Listener: seam.ListenerClient, Method: http.MethodGet, Pattern: "/late", Access: seam.AccessHandler, Handler: marker("late")}); err != nil {
		t.Errorf("a handler route after PipelineRoutes was refused: %v", err)
	}
	if err := table.HandleRoute(seam.Route{Listener: seam.ListenerClient, Method: http.MethodGet, Pattern: "/v1/runtimes/late", Access: seam.AccessHandler, Handler: marker("late")}); !errors.Is(err, seam.ErrInvalidRoute) {
		t.Errorf("a handler route under the session paths after PipelineRoutes = %v, want ErrInvalidRoute", err)
	}
	late.Listener = seam.ListenerForward
	if err := table.HandleRoute(late); err != nil {
		t.Errorf("a pipeline route on the unsealed forward listener was refused: %v", err)
	}
	if got := table.PipelineRoutes(seam.ListenerForward); len(got) != 2 {
		t.Errorf("PipelineRoutes(forward) holds %d routes, want 2", len(got))
	}
	if got := table.PipelineRoutes(seam.ListenerInternal); len(got) != 0 {
		t.Errorf("PipelineRoutes(internal) = %+v, want none", got)
	}
}

// TestHandleRouteRefusesRoutesTheAPIFrontCannotServe checks the refusals:
// an invalid route, a route of the pod proxy listener, a repeated method
// and pattern on the listener that registered it and on the listener that
// shares its mux, a malformed pattern, a pattern that names a host, and a
// route of the internal listener under a session path of the client
// listener; and that a refused route is not registered.
// spec: GWSEAM-HandleRoute
func TestHandleRouteRefusesRoutesTheAPIFrontCannotServe(t *testing.T) {
	table := routetable.New()
	base := seam.Route{Listener: seam.ListenerClient, Method: http.MethodGet, Pattern: "/metrics", Access: seam.AccessHandler, Handler: marker("metrics")}
	if err := table.HandleRoute(base); err != nil {
		t.Fatal(err)
	}
	cases := map[string]seam.Route{
		"invalid":           {Listener: seam.ListenerClient, Method: http.MethodGet, Pattern: "/x", Handler: marker("x")},
		"pod proxy":         {Listener: seam.ListenerPodProxy, Method: http.MethodPost, Pattern: "/v1/messages", Access: seam.AccessHandler, Handler: marker("proxy")},
		"repeat":            base,
		"repeat on sharing": {Listener: seam.ListenerInternal, Method: http.MethodGet, Pattern: "/metrics", Access: seam.AccessHandler, Handler: marker("metrics")},
		"malformed":         {Listener: seam.ListenerClient, Method: http.MethodGet, Pattern: "no-slash", Access: seam.AccessHandler, Handler: marker("bad")},
		"host pattern":      {Listener: seam.ListenerClient, Method: http.MethodGet, Pattern: "evil.example/v1/sessions/{id}", Access: seam.AccessHandler, Handler: marker("host")},
		"internal session":  {Listener: seam.ListenerInternal, Method: http.MethodGet, Pattern: "/v1/sessions/{id}/x", Access: seam.AccessHandler, Handler: marker("internal")},
		"internal models":   {Listener: seam.ListenerInternal, Method: http.MethodGet, Pattern: "/v1/models", Access: seam.AccessHandler, Handler: marker("internal")},
	}
	for name, r := range cases {
		t.Run(name, func(t *testing.T) {
			if err := table.HandleRoute(r); !errors.Is(err, seam.ErrInvalidRoute) {
				t.Errorf("HandleRoute = %v, want ErrInvalidRoute", err)
			}
		})
	}
	if got := len(table.Routes()); got != 1 {
		t.Errorf("Routes() holds %d routes after the refusals, want 1", got)
	}
	repeatOnForward := base
	repeatOnForward.Listener = seam.ListenerForward
	if err := table.HandleRoute(repeatOnForward); err != nil {
		t.Errorf("the same pattern on the forward listener's own mux was refused: %v", err)
	}
}

// TestHandleRouteIsSafeForConcurrentRegistration checks that parts may
// register concurrently: every distinct route registers once, and the
// table holds all of them.
// spec: GWSEAM-HandleRoute
func TestHandleRouteIsSafeForConcurrentRegistration(t *testing.T) {
	table := routetable.New()
	const n = 32
	var wg sync.WaitGroup
	errs := make(chan error, 2*n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p := "/r" + string(rune('a'+i%26)) + string(rune('a'+i/26))
			errs <- table.HandleRoute(seam.Route{Listener: seam.ListenerClient, Method: http.MethodGet, Pattern: p, Access: seam.AccessHandler, Handler: marker(p)})
			errs <- table.HandleRoute(seam.Route{Listener: seam.ListenerForward, Method: http.MethodGet, Pattern: p, Access: seam.AccessTenant, Handler: marker(p)})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := len(table.Routes()); got != 2*n {
		t.Errorf("Routes() holds %d routes, want %d", got, 2*n)
	}
	if got := len(table.PipelineRoutes(seam.ListenerForward)); got != n {
		t.Errorf("PipelineRoutes(forward) holds %d routes, want %d", got, n)
	}
}

// TestRegisterFrontRoutesMountsTheOpenAPIDocument checks that the API
// front's own routes mount on the client listener for any method.
// spec: GWSEAM-RegisterRoutes
func TestRegisterFrontRoutesMountsTheOpenAPIDocument(t *testing.T) {
	table := routetable.New()
	if err := routetable.RegisterFrontRoutes(table, marker("openapi")); err != nil {
		t.Fatal(err)
	}
	shared := table.Handler(seam.ListenerClient)
	for _, p := range []string{"/openapi.yaml", "/openapi.json", "/v1/openapi.json", "/v1/openapi.yaml"} {
		for _, m := range []string{http.MethodGet, http.MethodHead} {
			if code, _ := serve(shared, m, p); code != http.StatusOK {
				t.Errorf("%s %s = %d, want 200", m, p, code)
			}
		}
	}
	if err := routetable.RegisterFrontRoutes(table, marker("openapi")); !errors.Is(err, seam.ErrInvalidRoute) {
		t.Errorf("registering the document twice = %v, want ErrInvalidRoute", err)
	}
}

// TestHandlerMuxRegistersMountedHandlersWithoutARouteCheck checks that a
// package mounting on a HandlerMux registers each pattern, with or without
// a method, on the mux's listener with no route check, and that a
// conflicting pattern panics as net/http.ServeMux.Handle does.
// spec: GWSEAM-HandleRoute
func TestHandlerMuxRegistersMountedHandlersWithoutARouteCheck(t *testing.T) {
	table := routetable.New()
	mux := routetable.HandlerMux{Routes: table, Listener: seam.ListenerClient}
	mux.Handle("/v1/responses", marker("responses"))
	mux.Handle("POST /mcp/runtimes/{name}", marker("runtimes"))
	for _, r := range table.Routes() {
		if r.Listener != seam.ListenerClient || r.Access != seam.AccessHandler {
			t.Errorf("%s %s: listener %d, access %d", r.Method, r.Pattern, r.Listener, r.Access)
		}
	}
	shared := table.Handler(seam.ListenerClient)
	if code, body := serve(shared, http.MethodPost, "/mcp/runtimes/r1"); code != http.StatusOK || body != "runtimes" {
		t.Errorf("POST /mcp/runtimes/r1 = %d %q", code, body)
	}
	if code, _ := serve(shared, http.MethodGet, "/mcp/runtimes/r1"); code != http.StatusMethodNotAllowed {
		t.Errorf("GET /mcp/runtimes/r1 = %d, want 405", code)
	}
	defer func() {
		if recover() == nil {
			t.Error("a repeated pattern did not panic")
		}
	}()
	mux.Handle("/v1/responses", marker("again"))
}

// TestMountSessionPathsMountsTheHandlerOnEachSessionPath checks that the
// request pipeline's handler of a listener is mounted on every session path
// of that listener and on no other path, that the client and forward
// listeners have their own session paths, and that a second mount, a
// listener with no session paths the API front serves, and a session path
// a route already holds are refused.
//
// spec: GWSEAM-HandleRoute
func TestMountSessionPathsMountsTheHandlerOnEachSessionPath(t *testing.T) {
	table := routetable.New()
	if err := table.MountSessionPaths(seam.ListenerClient, marker("client-pipeline")); err != nil {
		t.Fatalf("MountSessionPaths(client): %v", err)
	}
	if err := table.MountSessionPaths(seam.ListenerForward, marker("forward-pipeline")); err != nil {
		t.Fatalf("MountSessionPaths(forward): %v", err)
	}
	for _, tc := range []struct {
		l          seam.Listener
		path, want string
		code       int
	}{
		{seam.ListenerClient, "/v1/sessions", "client-pipeline", http.StatusOK},
		{seam.ListenerClient, "/v1/sessions/s1/messages", "client-pipeline", http.StatusOK},
		{seam.ListenerClient, "/v1/blobs/b1", "client-pipeline", http.StatusOK},
		{seam.ListenerClient, "/v1/runtimes/r1", "client-pipeline", http.StatusOK},
		{seam.ListenerClient, "/v1/models", "client-pipeline", http.StatusOK},
		{seam.ListenerClient, "/v1/admin/tenants", "", http.StatusNotFound},
		{seam.ListenerForward, "/v1/sessions/s1", "forward-pipeline", http.StatusOK},
		{seam.ListenerForward, "/v1/models", "", http.StatusNotFound},
	} {
		code, body := serve(table.Handler(tc.l), http.MethodGet, tc.path)
		if code != tc.code || (tc.want != "" && body != tc.want) {
			t.Errorf("listener %d GET %s = %d %q, want %d %q", tc.l, tc.path, code, body, tc.code, tc.want)
		}
	}
	if err := table.MountSessionPaths(seam.ListenerClient, marker("again")); !errors.Is(err, seam.ErrInvalidRoute) {
		t.Errorf("a second mount = %v, want ErrInvalidRoute", err)
	}
	if err := table.MountSessionPaths(seam.ListenerPodProxy, marker("proxy")); !errors.Is(err, seam.ErrInvalidRoute) {
		t.Errorf("a pod proxy mount = %v, want ErrInvalidRoute", err)
	}
	if got := routetable.SessionPaths(seam.ListenerInternal); len(got) != 0 {
		t.Errorf("SessionPaths(internal) = %v, want none", got)
	}
}
