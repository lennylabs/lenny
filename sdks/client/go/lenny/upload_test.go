// SPDX-License-Identifier: MIT

package lenny

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// spec: §15.1 upload-archive + §7.1 uploadToken — UploadArchive POSTs the
// raw body with the X-Lenny-Upload-Token header and decodes the uploadRef.
func TestUploadArchivePostsRawBodyWithToken(t *testing.T) {
	var gotPath, gotToken, gotCT, gotHash string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotToken = r.Header.Get("X-Lenny-Upload-Token")
		gotCT = r.Header.Get("Content-Type")
		gotHash = r.Header.Get("X-Lenny-Content-Hash")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(UploadResult{
			UploadRef: "lenny-blob://acme/upload/sess_1/p1",
			Size:      int64(len(gotBody)),
			IsArchive: true,
		})
	}))
	defer srv.Close()

	c, err := New(srv.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	res, err := c.UploadArchive(context.Background(), "sess_1", "tok-123", []byte("TARGZ"),
		UploadArchiveOptions{ContentHash: "abc123"})
	if err != nil {
		t.Fatalf("UploadArchive: %v", err)
	}
	if gotPath != "/v1/sessions/sess_1/upload-archive" {
		t.Errorf("path = %q", gotPath)
	}
	if gotToken != "tok-123" {
		t.Errorf("upload token header = %q", gotToken)
	}
	if gotCT != "application/gzip" {
		t.Errorf("content-type = %q, want application/gzip", gotCT)
	}
	if gotHash != "abc123" {
		t.Errorf("content-hash header = %q", gotHash)
	}
	if string(gotBody) != "TARGZ" {
		t.Errorf("body = %q", gotBody)
	}
	if res.UploadRef != "lenny-blob://acme/upload/sess_1/p1" || !res.IsArchive {
		t.Errorf("result = %+v", res)
	}
}

// spec: §15.1 upload + §26.2 line 213 (--file) — UploadFile targets the
// plain /upload endpoint with octet-stream content type.
func TestUploadFileTargetsPlainUpload(t *testing.T) {
	var gotPath, gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotCT = r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(UploadResult{UploadRef: "lenny-blob://acme/upload/sess_1/f1"})
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	res, err := c.UploadFile(context.Background(), "sess_1", "tok", []byte("file"), UploadArchiveOptions{})
	if err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	if gotPath != "/v1/sessions/sess_1/upload" {
		t.Errorf("path = %q", gotPath)
	}
	if gotCT != "application/octet-stream" {
		t.Errorf("content-type = %q", gotCT)
	}
	if res.UploadRef == "" {
		t.Error("missing uploadRef")
	}
}

// spec: §7.1 — UploadArchive requires a session id and an upload token.
func TestUploadArchiveRejectsMissingInputs(t *testing.T) {
	c, _ := New("http://127.0.0.1:0")
	if _, err := c.UploadArchive(context.Background(), "", "tok", nil, UploadArchiveOptions{}); err == nil {
		t.Error("missing session id should error")
	}
	if _, err := c.UploadArchive(context.Background(), "sess_1", "", nil, UploadArchiveOptions{}); err == nil {
		t.Error("missing token should error")
	}
}

// spec: §15.1 upload error envelope — a non-2xx upload response is decoded
// as an APIError rather than retried (the call is single-shot).
func TestUploadArchiveSurfacesAPIError(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusGone)
		_, _ = w.Write([]byte(`{"error":{"code":"UPLOAD_CHANNEL_CLOSED","message":"closed"}}`))
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	_, err := c.UploadArchive(context.Background(), "sess_1", "tok", []byte("x"), UploadArchiveOptions{})
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *APIError
	if !asAPIError(err, &apiErr) || apiErr.Code != "UPLOAD_CHANNEL_CLOSED" {
		t.Errorf("err = %v, want UPLOAD_CHANNEL_CLOSED", err)
	}
	if calls != 1 {
		t.Errorf("upload retried %d times; want single-shot", calls)
	}
}

// spec: §7.1 step 11 / §26.2 lines 95-114 — FinalizeWorkspace POSTs the
// plan under a workspacePlan envelope; a nil plan falls back to a no-body
// finalize.
func TestFinalizeWorkspacePostsPlan(t *testing.T) {
	var gotBody []byte
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Session{ID: "sess_1", State: "ready"})
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	plan := json.RawMessage(`{"schemaVersion":1,"sources":[{"type":"uploadArchive","pathPrefix":".","uploadRef":"lenny-blob://acme/upload/sess_1/p1","format":"tar.gz"}]}`)
	sess, err := c.FinalizeWorkspace(context.Background(), "sess_1", plan)
	if err != nil {
		t.Fatalf("FinalizeWorkspace: %v", err)
	}
	if gotPath != "/v1/sessions/sess_1/finalize" {
		t.Errorf("path = %q", gotPath)
	}
	if !strings.Contains(string(gotBody), `"workspacePlan"`) ||
		!strings.Contains(string(gotBody), `"uploadArchive"`) {
		t.Errorf("body = %s", gotBody)
	}
	if sess.State != "ready" {
		t.Errorf("state = %q", sess.State)
	}
}

// TestFinalizeWorkspaceNilPlanIsNoBody confirms a nil plan sends no body
// (the plain finalize transition).
func TestFinalizeWorkspaceNilPlanIsNoBody(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Session{ID: "sess_1", State: "ready"})
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	if _, err := c.FinalizeWorkspace(context.Background(), "sess_1", nil); err != nil {
		t.Fatalf("FinalizeWorkspace: %v", err)
	}
	if len(gotBody) != 0 {
		t.Errorf("nil plan should send no body, got %q", gotBody)
	}
}

// asAPIError is a local errors.As helper kept narrow to the test file.
func asAPIError(err error, target **APIError) bool {
	for err != nil {
		if e, ok := err.(*APIError); ok {
			*target = e
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
