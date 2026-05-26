// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// spec: §18 line 234 — POST /v1/sessions/{id}/upload-archive accepts the
// archive body with the same authentication, precondition, and breaker
// pipeline as /upload, and tags the response with isArchive: true so the
// §7.4 in-gateway extraction pipeline can pick it up. Closes F-7.1.9 /
// F-7.4.5 endpoint-presence half; archive parsing and the §13.4
// ceilings land with F-7.4.1 / F-7.4.2 / F-7.4.11.
func TestHandleUploadArchive_TagsResponse_spec_18_234(t *testing.T) {
	srv, issuer, _, store, _ := newUploadServerWithSubsystem(t, nil)
	tok := seedAndMintUploadSubsystem(t, store, issuer, "sess_subsystem", "default")

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_subsystem/upload-archive", strings.NewReader("archive-bytes"))
	req.Header.Set("X-Lenny-Tenant-ID", "default")
	req.Header.Set("X-Lenny-Upload-Token", tok)
	req.Header.Set("Content-Type", "application/x-tar")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body=%s)", rr.Code, http.StatusCreated, rr.Body.String())
	}
	var resp sessionserver.UploadResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.IsArchive {
		t.Fatalf("isArchive = false, want true on /upload-archive response")
	}
	if resp.UploadRef == "" {
		t.Fatalf("uploadRef empty, want a lenny-blob:// URI")
	}
	if resp.Size != int64(len("archive-bytes")) {
		t.Fatalf("size = %d, want %d", resp.Size, len("archive-bytes"))
	}
	if resp.MimeType != "application/x-tar" {
		t.Fatalf("mimeType = %q, want application/x-tar", resp.MimeType)
	}
}

// spec: §15.1 — the plain /upload endpoint keeps the legacy response
// shape (no isArchive flag set). The /upload-archive flag is opt-in to
// avoid a wire-format diff for consumers that do not call the archive
// surface.
func TestHandleUpload_DoesNotTagResponse_spec_15_1(t *testing.T) {
	srv, issuer, _, store, _ := newUploadServerWithSubsystem(t, nil)
	tok := seedAndMintUploadSubsystem(t, store, issuer, "sess_subsystem", "default")

	rr := doUpload(t, srv.Handler(), "sess_subsystem", "default", tok, "payload")
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body=%s)", rr.Code, http.StatusCreated, rr.Body.String())
	}
	var resp sessionserver.UploadResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.IsArchive {
		t.Fatalf("isArchive = true, want false on /upload response")
	}
}

// spec: §7.1 line 58 — /upload-archive shares the §7.1 uploadToken
// authentication contract. A missing token is rejected before any body
// is read.
func TestHandleUploadArchive_RequiresUploadToken_spec_7_1_58(t *testing.T) {
	srv, issuer, _, store, _ := newUploadServerWithSubsystem(t, nil)
	// Seed a real row so the missing-token rejection happens after the
	// session lookup (matching production order: token check follows the
	// 404 / 500 row lookup gate).
	_ = seedAndMintUploadSubsystem(t, store, issuer, "sess_subsystem", "default")

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_subsystem/upload-archive", strings.NewReader("archive-bytes"))
	req.Header.Set("X-Lenny-Tenant-ID", "default")
	req.Header.Set("Content-Type", "application/zip")
	// Deliberately omit X-Lenny-Upload-Token.
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d for missing token", rr.Code, http.StatusUnauthorized)
	}
}
