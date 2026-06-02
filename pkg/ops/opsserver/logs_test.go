// SPDX-License-Identifier: MIT

package opsserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// fakePodLogs records the options it was called with and returns a
// configurable body or error so the handler's param parsing and error
// mapping are observable.
type fakePodLogs struct {
	body    string
	err     error
	gotNS   string
	gotName string
	gotOpts opsserver.PodLogOptions
}

func (f *fakePodLogs) ReadPodLogs(_ context.Context, ns, name string, opts opsserver.PodLogOptions) (io.ReadCloser, error) {
	f.gotNS, f.gotName, f.gotOpts = ns, name, opts
	if f.err != nil {
		return nil, f.err
	}
	return io.NopCloser(strings.NewReader(f.body)), nil
}

func getLogs(srv *opsserver.Server, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

// spec: §25.4 lines 2528-2534 — the proxy streams pod logs as text and
// resolves the ?container/since/tail/previous params onto PodLogOptions.
func TestPodLogsStreamsAndParsesParams(t *testing.T) {
	fake := &fakePodLogs{body: "line-1\nline-2\n"}
	srv := opsserver.New(opsserver.Options{PodLogs: fake})
	rec := getLogs(srv, "/v1/admin/logs/pods/lenny-system/lenny-gateway-abc?container=gateway&since=5m&tail=100&previous=true")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}
	if rec.Body.String() != "line-1\nline-2\n" {
		t.Errorf("body = %q, want the streamed log lines", rec.Body.String())
	}
	if fake.gotNS != "lenny-system" || fake.gotName != "lenny-gateway-abc" {
		t.Errorf("ns/name = %s/%s, want lenny-system/lenny-gateway-abc", fake.gotNS, fake.gotName)
	}
	o := fake.gotOpts
	if o.Container != "gateway" || !o.Previous {
		t.Errorf("opts = %+v, want container=gateway previous=true", o)
	}
	if o.SinceSeconds == nil || *o.SinceSeconds != 300 {
		t.Errorf("SinceSeconds = %v, want 300 (5m)", o.SinceSeconds)
	}
	if o.TailLines == nil || *o.TailLines != 100 {
		t.Errorf("TailLines = %v, want 100", o.TailLines)
	}
}

// ?since= accepts a bare integer count of seconds as well as a duration.
func TestPodLogsSinceAcceptsBareSeconds(t *testing.T) {
	fake := &fakePodLogs{body: "x"}
	srv := opsserver.New(opsserver.Options{PodLogs: fake})
	if rec := getLogs(srv, "/v1/admin/logs/pods/ns/pod?since=90"); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if fake.gotOpts.SinceSeconds == nil || *fake.gotOpts.SinceSeconds != 90 {
		t.Errorf("SinceSeconds = %v, want 90", fake.gotOpts.SinceSeconds)
	}
}

// A malformed parameter is a 400 validation error so the caller cannot
// silently receive unfiltered logs.
func TestPodLogsRejectsBadParams(t *testing.T) {
	srv := opsserver.New(opsserver.Options{PodLogs: &fakePodLogs{body: "x"}})
	for _, q := range []string{"?tail=-5", "?tail=abc", "?since=notaduration", "?previous=maybe"} {
		rec := getLogs(srv, "/v1/admin/logs/pods/ns/pod"+q)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("query %q: status = %d, want 400", q, rec.Code)
		}
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if errObj, _ := body["error"].(map[string]any); errObj["code"] != "VALIDATION_ERROR" {
			t.Errorf("query %q: error code = %v, want VALIDATION_ERROR", q, errObj["code"])
		}
	}
}

// spec: §25.2 — an unknown pod maps to 404 POD_NOT_FOUND.
func TestPodLogsNotFound(t *testing.T) {
	srv := opsserver.New(opsserver.Options{PodLogs: &fakePodLogs{err: opsserver.ErrPodLogNotFound}})
	rec := getLogs(srv, "/v1/admin/logs/pods/ns/ghost")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if errObj, _ := body["error"].(map[string]any); errObj["code"] != "POD_NOT_FOUND" {
		t.Errorf("error code = %v, want POD_NOT_FOUND", errObj["code"])
	}
}

// A non-not-found Kubernetes error maps to 502 LOG_PROXY_ERROR (transient
// upstream), not a 500.
func TestPodLogsUpstreamErrorMapsTo502(t *testing.T) {
	srv := opsserver.New(opsserver.Options{PodLogs: &fakePodLogs{err: errors.New("connection refused")}})
	rec := getLogs(srv, "/v1/admin/logs/pods/ns/pod")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if errObj, _ := body["error"].(map[string]any); errObj["code"] != "LOG_PROXY_ERROR" || errObj["category"] != "TRANSIENT" {
		t.Errorf("error = %v, want LOG_PROXY_ERROR/TRANSIENT", errObj)
	}
}

// Without a PodLogReader wired (no cluster connection) the endpoint
// reports the proxy unavailable rather than 404.
func TestPodLogsUnavailableWithoutReader(t *testing.T) {
	srv := opsserver.New(opsserver.Options{})
	rec := getLogs(srv, "/v1/admin/logs/pods/ns/pod")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if errObj, _ := body["error"].(map[string]any); errObj["code"] != "LOG_PROXY_UNAVAILABLE" {
		t.Errorf("error code = %v, want LOG_PROXY_UNAVAILABLE", errObj["code"])
	}
}
