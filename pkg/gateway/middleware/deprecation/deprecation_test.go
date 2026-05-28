// SPDX-License-Identifier: MIT

package deprecation_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/middleware/deprecation"
)

// pingHandler is the inner handler the deprecation wrapper sits in
// front of. It writes a 200 with a trivial body so a test can assert
// the wrapper preserves the inner response unchanged.
func pingHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

// spec: docs/api/index.md line 124 — when /v1 enters deprecation the
// gateway adds X-Lenny-Deprecated-Version to every response on the
// deprecated path prefix. F-15.5.11.
func TestStampsHeaderOnDeprecatedPath_spec_15_5_124(t *testing.T) {
	h := deprecation.Wrap(pingHandler(), "v1")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/sessions", nil))
	if got := rr.Header().Get(deprecation.HeaderName); got != "v1" {
		t.Errorf("X-Lenny-Deprecated-Version: got %q, want %q", got, "v1")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rr.Code)
	}
}

// spec: §15.5 item 1 — non-deprecated versions never carry the
// header. F-15.5.11.
func TestSkipsHeaderOnLiveVersion_spec_15_5_124(t *testing.T) {
	h := deprecation.Wrap(pingHandler(), "v0")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/sessions", nil))
	if got := rr.Header().Get(deprecation.HeaderName); got != "" {
		t.Errorf("X-Lenny-Deprecated-Version unexpectedly set to %q on live /v1", got)
	}
}

// spec: §15.5 item 1 — unversioned paths (healthz, metrics, openapi)
// never carry the deprecation header even when /v1 is deprecated.
// F-15.5.11.
func TestSkipsHeaderOnUnversionedPath_spec_15_5_124(t *testing.T) {
	h := deprecation.Wrap(pingHandler(), "v1")
	for _, path := range []string{"/healthz", "/metrics", "/openapi.json", "/.well-known/jwks.json"} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if got := rr.Header().Get(deprecation.HeaderName); got != "" {
			t.Errorf("%s: unexpected deprecation header %q", path, got)
		}
	}
}

// spec: docs/api/index.md line 124 — the default v1 deployment passes
// through with no header (no version is deprecated yet because /v2/
// has not shipped). F-15.5.11.
func TestNoOpWhenSetEmpty_spec_15_5_124(t *testing.T) {
	for _, args := range [][]string{nil, {""}, {"", "   "}} {
		h := deprecation.Wrap(pingHandler(), args...)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/sessions", nil))
		if got := rr.Header().Get(deprecation.HeaderName); got != "" {
			t.Errorf("empty deprecated set unexpectedly stamped %q (args=%v)", got, args)
		}
	}
}

// PathVersionPrefix's contract isolates a small parsing rule the
// middleware depends on; the unit table here makes the parser's
// boundaries explicit. F-15.5.11.
func TestPathVersionPrefix(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/v1/sessions", "v1"},
		{"/v2", "v2"},
		{"/v2/", "v2"},
		{"/v10/sessions", "v10"},
		{"/", ""},
		{"", ""},
		{"/healthz", ""},
		{"/openapi.json", ""},
		{"/.well-known/jwks.json", ""},
		{"/vfoo/bar", ""},
		{"/v/foo", ""},
		{"v1/sessions", ""},
	}
	for _, c := range cases {
		if got := deprecation.PathVersionPrefix(c.path); got != c.want {
			t.Errorf("PathVersionPrefix(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

// spec: §15.5 item 1 — the deprecation header is keyed on URL path
// prefix; multiple deprecated versions are supported simultaneously
// (e.g. during a long sunset overlap). F-15.5.11.
func TestSupportsMultipleDeprecatedVersions_spec_15_5_124(t *testing.T) {
	h := deprecation.Wrap(pingHandler(), "v1", "v2")
	for _, c := range []struct {
		path string
		want string
	}{
		{"/v1/sessions", "v1"},
		{"/v2/sessions", "v2"},
		{"/v3/sessions", ""},
	} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, c.path, nil))
		if got := rr.Header().Get(deprecation.HeaderName); got != c.want {
			t.Errorf("%s deprecation header: got %q, want %q", c.path, got, c.want)
		}
	}
}
