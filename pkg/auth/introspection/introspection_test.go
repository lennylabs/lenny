// SPDX-License-Identifier: MIT

package introspection

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// staticSource is a ConfigSource returning a fixed config (and optional
// error) for any tenant.
type staticSource struct {
	cfg Config
	err error
}

func (s staticSource) IntrospectionConfig(_ context.Context, _ string) (Config, error) {
	return s.cfg, s.err
}

// newIntrospectionServer returns a test RFC 7662 endpoint that echoes the
// supplied response body for an active token and records the call count
// and the last basic-auth credentials seen.
func newIntrospectionServer(t *testing.T, body map[string]any) (*httptest.Server, *int64, *string) {
	t.Helper()
	var calls int64
	var lastAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		if u, p, ok := r.BasicAuth(); ok {
			lastAuth = u + ":" + p
		}
		if got := r.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
			t.Errorf("content-type = %q, want application/x-www-form-urlencoded", got)
		}
		_ = r.ParseForm()
		if r.Form.Get("token") == "" {
			t.Errorf("introspection request carried no token form field")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls, &lastAuth
}

// spec: §10.6 line 661 — a tenant with introspection off keeps the JWT
// groups: the Verifier reports enabled=false and never calls the endpoint.
func TestIntrospectGroupsDisabledSkipsCall_spec_10_6(t *testing.T) {
	srv, calls, _ := newIntrospectionServer(t, map[string]any{"active": true, "groups": []string{"x"}})
	v := New(staticSource{cfg: Config{Enabled: false, Endpoint: srv.URL}})
	enabled, _, _, err := v.IntrospectGroups(context.Background(), "acme", "tok")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if enabled {
		t.Fatalf("enabled = true, want false for a tenant with introspection off")
	}
	if got := atomic.LoadInt64(calls); got != 0 {
		t.Fatalf("endpoint called %d times, want 0 when introspection is off", got)
	}
}

// spec: §10.6 line 661 — an enabled tenant gets the provider's real-time
// group set, replacing the JWT claim, and the basic-auth credentials are
// sent.
func TestIntrospectGroupsEnabledReturnsProviderGroups_spec_10_6(t *testing.T) {
	srv, calls, lastAuth := newIntrospectionServer(t, map[string]any{
		"active": true,
		"groups": []string{"eng", "oncall"},
	})
	v := New(staticSource{cfg: Config{
		Enabled:      true,
		Endpoint:     srv.URL,
		ClientID:     "gw",
		ClientSecret: "s3cret",
	}})
	enabled, active, groups, err := v.IntrospectGroups(context.Background(), "acme", "tok")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !enabled || !active {
		t.Fatalf("enabled=%v active=%v, want both true", enabled, active)
	}
	if len(groups) != 2 || groups[0] != "eng" || groups[1] != "oncall" {
		t.Fatalf("groups = %v, want [eng oncall]", groups)
	}
	if *lastAuth != "gw:s3cret" {
		t.Fatalf("basic auth = %q, want gw:s3cret", *lastAuth)
	}
	if got := atomic.LoadInt64(calls); got != 1 {
		t.Fatalf("endpoint called %d times, want 1", got)
	}
}

// spec: §10.6 line 661 — the cache bounds the latency cost: a second call
// for the same (tenant, token) inside the TTL reuses the result without a
// second round-trip.
func TestIntrospectGroupsCachesWithinTTL_spec_10_6(t *testing.T) {
	srv, calls, _ := newIntrospectionServer(t, map[string]any{"active": true, "groups": []string{"eng"}})
	now := time.Unix(1000, 0).UTC()
	v := New(staticSource{cfg: Config{Enabled: true, Endpoint: srv.URL, CacheTTL: 30 * time.Second}},
		WithClock(func() time.Time { return now }))

	for i := 0; i < 3; i++ {
		_, active, groups, err := v.IntrospectGroups(context.Background(), "acme", "tok")
		if err != nil || !active || len(groups) != 1 {
			t.Fatalf("call %d: active=%v groups=%v err=%v", i, active, groups, err)
		}
	}
	if got := atomic.LoadInt64(calls); got != 1 {
		t.Fatalf("endpoint called %d times across 3 cached lookups, want 1", got)
	}

	// Advance past the TTL: the next lookup re-queries.
	now = now.Add(31 * time.Second)
	if _, _, _, err := v.IntrospectGroups(context.Background(), "acme", "tok"); err != nil {
		t.Fatalf("post-TTL lookup: %v", err)
	}
	if got := atomic.LoadInt64(calls); got != 2 {
		t.Fatalf("endpoint called %d times after TTL expiry, want 2", got)
	}
}

// spec: §10.6 line 661 — an inactive-token verdict surfaces active=false
// (the middleware rejects). No group set is returned.
func TestIntrospectGroupsInactiveToken_spec_10_6(t *testing.T) {
	srv, _, _ := newIntrospectionServer(t, map[string]any{"active": false})
	v := New(staticSource{cfg: Config{Enabled: true, Endpoint: srv.URL}})
	enabled, active, groups, err := v.IntrospectGroups(context.Background(), "acme", "tok")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !enabled {
		t.Fatalf("enabled = false, want true")
	}
	if active {
		t.Fatalf("active = true, want false for an inactive token")
	}
	if groups != nil {
		t.Fatalf("groups = %v, want nil for an inactive token", groups)
	}
}

// spec: §10.6 line 661 — a transport failure surfaces as an error so the
// middleware fails closed rather than honoring the JWT groups.
func TestIntrospectGroupsEndpointUnreachable_spec_10_6(t *testing.T) {
	v := New(staticSource{cfg: Config{Enabled: true, Endpoint: "https://127.0.0.1:1/introspect"}})
	enabled, _, _, err := v.IntrospectGroups(context.Background(), "acme", "tok")
	if err == nil {
		t.Fatalf("expected a transport error, got nil")
	}
	if !enabled {
		t.Fatalf("enabled = false, want true (the tenant opted in)")
	}
}

// An enabled tenant whose endpoint is empty is a misconfiguration the
// Verifier fails closed on rather than silently skipping the check.
func TestIntrospectGroupsEnabledNoEndpoint_spec_10_6(t *testing.T) {
	v := New(staticSource{cfg: Config{Enabled: true, Endpoint: ""}})
	enabled, _, _, err := v.IntrospectGroups(context.Background(), "acme", "tok")
	if err == nil {
		t.Fatalf("expected a misconfiguration error, got nil")
	}
	if !enabled {
		t.Fatalf("enabled = false, want true")
	}
}

// A config-read failure surfaces so the middleware fails closed.
func TestIntrospectGroupsConfigError(t *testing.T) {
	v := New(staticSource{err: context.DeadlineExceeded})
	_, _, _, err := v.IntrospectGroups(context.Background(), "acme", "tok")
	if err == nil {
		t.Fatalf("expected the config error to propagate, got nil")
	}
}

// A non-200 introspection response is a transport-level failure.
func TestIntrospectGroupsNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)
	v := New(staticSource{cfg: Config{Enabled: true, Endpoint: srv.URL}})
	if _, _, _, err := v.IntrospectGroups(context.Background(), "acme", "tok"); err == nil {
		t.Fatalf("expected an error for a 502 response, got nil")
	}
}

// A provider that returns groups as a space-delimited string (the
// scope-style shape) is parsed into a slice. An active token with no
// group claim yields an empty set rather than an error.
func TestExtractGroupsShapes(t *testing.T) {
	t.Run("space-delimited string", func(t *testing.T) {
		srv, _, _ := newIntrospectionServer(t, map[string]any{"active": true, "groups": "eng oncall sre"})
		v := New(staticSource{cfg: Config{Enabled: true, Endpoint: srv.URL}})
		_, _, groups, err := v.IntrospectGroups(context.Background(), "acme", "tok")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(groups) != 3 || groups[0] != "eng" || groups[2] != "sre" {
			t.Fatalf("groups = %v, want [eng oncall sre]", groups)
		}
	})
	t.Run("missing claim yields empty", func(t *testing.T) {
		srv, _, _ := newIntrospectionServer(t, map[string]any{"active": true})
		v := New(staticSource{cfg: Config{Enabled: true, Endpoint: srv.URL}})
		_, active, groups, err := v.IntrospectGroups(context.Background(), "acme", "tok")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !active {
			t.Fatalf("active = false, want true")
		}
		if len(groups) != 0 {
			t.Fatalf("groups = %v, want empty", groups)
		}
	})
	t.Run("custom groups claim", func(t *testing.T) {
		srv, _, _ := newIntrospectionServer(t, map[string]any{"active": true, "roles": []string{"admin"}})
		v := New(staticSource{cfg: Config{Enabled: true, Endpoint: srv.URL, GroupsClaim: "roles"}})
		_, _, groups, err := v.IntrospectGroups(context.Background(), "acme", "tok")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(groups) != 1 || groups[0] != "admin" {
			t.Fatalf("groups = %v, want [admin]", groups)
		}
	})
}
