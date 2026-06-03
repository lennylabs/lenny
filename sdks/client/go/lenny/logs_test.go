// SPDX-License-Identifier: MIT

package lenny

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestSessionLogsFetchesEnvelope_spec_15_1_673 confirms the SDK SessionLogs
// call GETs /v1/sessions/{id}/logs and decodes the §15.1 line 1228
// `{items, cursor, hasMore}` envelope. F-24.17.6.
func TestSessionLogsFetchesEnvelope_spec_15_1_673(t *testing.T) {
	var lastPath, lastQuery, lastAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastPath = r.URL.Path
		lastQuery = r.URL.RawQuery
		lastAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"seq":1,"sessionId":"sess_1","type":"log","data":{"line":"hi"},"timestamp":"2026-06-03T12:00:00Z"}],"cursor":"c2","hasMore":true}`))
	}))
	defer srv.Close()

	c, err := New(srv.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	page, err := c.SessionLogs(context.Background(), "sess_1", LogsOptions{Limit: 1})
	if err != nil {
		t.Fatalf("SessionLogs: %v", err)
	}
	if lastPath != "/v1/sessions/sess_1/logs" {
		t.Errorf("path = %q", lastPath)
	}
	if lastAccept != "application/json" {
		t.Errorf("Accept = %q, want application/json (JSON list view, not SSE)", lastAccept)
	}
	if lastQuery != "limit=1" {
		t.Errorf("query = %q, want limit=1", lastQuery)
	}
	if len(page.Items) != 1 || page.Items[0].Seq != 1 || page.Items[0].Type != "log" {
		t.Errorf("items = %+v", page.Items)
	}
	if !page.HasMore || page.Cursor != "c2" {
		t.Errorf("pagination = cursor:%q hasMore:%t", page.Cursor, page.HasMore)
	}
}

// TestSessionLogsSinceFlag_spec_24_17_220 confirms LogsOptions.Since is
// rendered as an RFC3339 `since` query parameter (the §24.17 `--since`
// flag). F-24.17.6.
func TestSessionLogsSinceFlag_spec_24_17_220(t *testing.T) {
	var gotSince string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSince = r.URL.Query().Get("since")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[],"hasMore":false}`))
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	since := time.Date(2026, 6, 3, 12, 30, 0, 0, time.UTC)
	if _, err := c.SessionLogs(context.Background(), "sess_1", LogsOptions{Since: since}); err != nil {
		t.Fatalf("SessionLogs: %v", err)
	}
	if want := since.Format(time.RFC3339); gotSince != want {
		t.Errorf("since = %q, want %q", gotSince, want)
	}
}
