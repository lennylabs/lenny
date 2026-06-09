// SPDX-License-Identifier: MIT

package lenny

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// uploadTokenHeader carries the §7.1 session-scoped upload token the
// gateway issued in the create response. Every /upload and /upload-archive
// request must present it.
const uploadTokenHeader = "X-Lenny-Upload-Token"

// contentHashHeader carries the §7.4 line 444 optional client-computed
// SHA-256 of the upload body. When set the gateway verifies the streamed
// bytes against it and rejects a mismatch with VALIDATION_ERROR.
const contentHashHeader = "X-Lenny-Content-Hash"

// UploadResult is the §15.1 POST /v1/sessions/{id}/upload(-archive)
// response. UploadRef is the §4.5 lenny-blob:// URI the caller passes as
// sources[<n>].uploadRef in the workspace plan it binds at finalize.
type UploadResult struct {
	UploadRef   string `json:"uploadRef"`
	MimeType    string `json:"mimeType,omitempty"`
	Size        int64  `json:"size"`
	ContentHash string `json:"contentHash,omitempty"`
	IsArchive   bool   `json:"isArchive,omitempty"`
}

// UploadArchiveOptions tunes a single POST /v1/sessions/{id}/upload-archive
// call.
type UploadArchiveOptions struct {
	// ContentType is the body's media type. It defaults to
	// application/gzip, matching the tar.gz the §26.2 CLI produces.
	ContentType string

	// ContentHash, when set, is sent as the §7.4 X-Lenny-Content-Hash
	// header (lowercase hex SHA-256). The gateway verifies the body
	// against it and rejects a mismatch.
	ContentHash string
}

// UploadArchive uploads an archive body to POST
// /v1/sessions/{id}/upload-archive and returns the resulting uploadRef.
// The upload requires the §7.1 uploadToken from the create response. It
// is the §26.2 line 114 step that stages a tar'd workspace before the
// plan referencing it is bound at finalize.
//
// The call is issued once (no retry): each upload mints a distinct staged
// blob, so a blind retry would orphan a duplicate. A transient failure
// surfaces to the caller, which re-drives the create → upload → finalize
// flow.
//
// spec: §15.1 (upload endpoints); §7.1 (uploadToken); §26.2 line 114.
func (c *Client) UploadArchive(ctx context.Context, sessionID, uploadToken string, body []byte, opt UploadArchiveOptions) (*UploadResult, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("lenny: UploadArchive requires a session id")
	}
	if uploadToken == "" {
		return nil, fmt.Errorf("lenny: UploadArchive requires an upload token")
	}
	contentType := opt.ContentType
	if contentType == "" {
		contentType = "application/gzip"
	}
	headers := map[string]string{uploadTokenHeader: uploadToken}
	if opt.ContentHash != "" {
		headers[contentHashHeader] = opt.ContentHash
	}
	path := "/v1/sessions/" + url.PathEscape(sessionID) + "/upload-archive"
	var out UploadResult
	if err := c.roundTripRaw(ctx, http.MethodPost, path, body, contentType, headers, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UploadFile uploads a single file body to POST /v1/sessions/{id}/upload
// and returns its uploadRef for use as an `uploadFile` plan source. Like
// UploadArchive it requires the §7.1 uploadToken and is issued once.
//
// spec: §15.1 (upload endpoint); §26.2 line 213 (`--file`).
func (c *Client) UploadFile(ctx context.Context, sessionID, uploadToken string, body []byte, opt UploadArchiveOptions) (*UploadResult, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("lenny: UploadFile requires a session id")
	}
	if uploadToken == "" {
		return nil, fmt.Errorf("lenny: UploadFile requires an upload token")
	}
	contentType := opt.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	headers := map[string]string{uploadTokenHeader: uploadToken}
	if opt.ContentHash != "" {
		headers[contentHashHeader] = opt.ContentHash
	}
	path := "/v1/sessions/" + url.PathEscape(sessionID) + "/upload"
	var out UploadResult
	if err := c.roundTripRaw(ctx, http.MethodPost, path, body, contentType, headers, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// FinalizeWorkspace calls POST /v1/sessions/{id}/finalize with a §14
// workspace plan body, binding the plan onto a created session and
// transitioning it to ready. The plan references blobs uploaded against
// this session (via UploadArchive); the gateway materializes them when
// the session starts. Pass a nil plan for a no-plan finalize (equivalent
// to Finalize).
//
// spec: §7.1 step 11 (FinalizeWorkspace); §26.2 lines 95-114.
func (c *Client) FinalizeWorkspace(ctx context.Context, id string, plan json.RawMessage, opts ...RequestOption) (*Session, error) {
	if len(plan) == 0 {
		return c.Finalize(ctx, id, opts...)
	}
	body := struct {
		WorkspacePlan json.RawMessage `json:"workspacePlan"`
	}{WorkspacePlan: plan}
	var out Session
	path := "/v1/sessions/" + url.PathEscape(id) + "/finalize"
	if err := c.do(ctx, http.MethodPost, path, body, &out, opts...); err != nil {
		return nil, err
	}
	return &out, nil
}

// roundTripRaw issues a single request with a non-JSON body and decodes a
// JSON response. It is the upload-path counterpart of do: no retry loop,
// since a staged-blob upload is not safely replayable.
func (c *Client) roundTripRaw(ctx context.Context, method, path string, body []byte, contentType string, headers map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("lenny: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if c.tenantID != "" {
		req.Header.Set("X-Lenny-Tenant-ID", c.tenantID)
	}
	if c.auth != nil {
		if err := c.auth.Apply(req); err != nil {
			return fmt.Errorf("lenny: apply auth: %w", err)
		}
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("lenny: read response body: %w", err)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if out == nil || len(respBody) == 0 {
			return nil
		}
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("lenny: decode %s %s response: %w", method, path, err)
		}
		return nil
	}
	return decodeAPIError(resp.StatusCode, respBody)
}
