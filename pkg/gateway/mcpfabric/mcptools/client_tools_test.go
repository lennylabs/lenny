// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// fakeService records the last ServiceCall and returns a canned result or
// error so the §15.2 client-facing tool routing and error projection can
// be asserted without a live gateway. F-15.2.3.
type fakeService struct {
	method      string
	target      string
	body        []byte
	contentType string
	headers     map[string]string

	result sessionserver.ServiceResult
	err    *sessionserver.ServiceError
}

func (f *fakeService) ServiceCall(_ context.Context, _ /*tenantID*/, method, target string, body []byte, contentType string, headers map[string]string) (sessionserver.ServiceResult, *sessionserver.ServiceError) {
	f.method, f.target, f.body, f.contentType, f.headers = method, target, body, contentType, headers
	if f.err != nil {
		return sessionserver.ServiceResult{}, f.err
	}
	return f.result, nil
}

func newClientToolsMCP(t *testing.T, svc mcptools.SessionService) *mcp.Server {
	t.Helper()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:          memstore.New(),
		SessionService: svc,
		TenantID:       "acme",
	})
	return srv
}

func dispatch(t *testing.T, srv *mcp.Server, name, args string) (mcp.ToolResult, error) {
	t.Helper()
	res, ok, err := srv.DispatchTool(context.Background(), name, json.RawMessage(args))
	if !ok {
		t.Fatalf("tool %q not registered", name)
	}
	return res, err
}

// TestClientToolsRegisteredWithService_spec_15_2_3 asserts the §15.2
// client-facing tools register when a SessionService is wired and are
// absent when it is nil. spec: §15.2 lines 1284-1306. F-15.2.3.
func TestClientToolsRegisteredWithService_spec_15_2_3(t *testing.T) {
	want := []string{
		"lenny/create_and_start_session", "lenny/start_session", "lenny/finalize_workspace",
		"lenny/terminate_session", "lenny/resume_session", "lenny/get_session_status",
		"lenny/list_sessions", "lenny/get_session_logs", "lenny/list_artifacts",
		"lenny/get_token_usage", "lenny/download_artifact", "lenny/upload_files",
	}

	with := newClientToolsMCP(t, &fakeService{})
	have := map[string]bool{}
	for _, tool := range with.Catalog() {
		have[tool.Name] = true
	}
	for _, name := range want {
		if !have[name] {
			t.Errorf("tool %q not registered when SessionService is wired", name)
		}
	}

	// Without a SessionService the tools are not registered.
	withoutSrv := mcp.NewServer()
	mcptools.Register(withoutSrv, mcptools.Deps{Store: memstore.New(), TenantID: "acme"})
	for _, tool := range withoutSrv.Catalog() {
		for _, name := range want {
			if tool.Name == name {
				t.Errorf("tool %q registered without a SessionService", name)
			}
		}
	}
}

// TestPostByIDToolRouting_spec_15_2_3 verifies a per-session POST tool
// strips sessionId from the body and targets the per-session path.
// F-15.2.3.
func TestPostByIDToolRouting_spec_15_2_3(t *testing.T) {
	svc := &fakeService{result: sessionserver.ServiceResult{Status: 200, Body: []byte(`{"state":"running"}`)}}
	srv := newClientToolsMCP(t, svc)

	if _, err := dispatch(t, srv, "lenny/start_session", `{"sessionId":"s1","foo":"bar"}`); err != nil {
		t.Fatalf("start_session: %v", err)
	}
	if svc.method != "POST" || svc.target != "/v1/sessions/s1/start" {
		t.Fatalf("routed to %s %s, want POST /v1/sessions/s1/start", svc.method, svc.target)
	}
	var body map[string]any
	if err := json.Unmarshal(svc.body, &body); err != nil {
		t.Fatalf("forwarded body not JSON: %v (%s)", err, svc.body)
	}
	if _, ok := body["sessionId"]; ok {
		t.Errorf("forwarded body still carries sessionId: %s", svc.body)
	}
	if body["foo"] != "bar" {
		t.Errorf("forwarded body lost foo: %s", svc.body)
	}
}

// TestPostByIDMissingSessionID_spec_15_2_3 verifies a missing sessionId is
// a canonical VALIDATION_ERROR with no dispatch. F-15.2.3.
func TestPostByIDMissingSessionID_spec_15_2_3(t *testing.T) {
	svc := &fakeService{}
	srv := newClientToolsMCP(t, svc)

	res, _ := dispatch(t, srv, "lenny/terminate_session", `{}`)
	if code := errorCode(t, res); code != "VALIDATION_ERROR" {
		t.Fatalf("missing sessionId error code = %q, want VALIDATION_ERROR", code)
	}
	if svc.method != "" {
		t.Errorf("dispatched despite missing sessionId: %s %s", svc.method, svc.target)
	}
}

// TestGetByIDToolRouting_spec_15_2_3 verifies a per-session GET tool
// targets the session subresource with no body. F-15.2.3.
func TestGetByIDToolRouting_spec_15_2_3(t *testing.T) {
	svc := &fakeService{result: sessionserver.ServiceResult{Status: 200, Body: []byte(`{"items":[]}`)}}
	srv := newClientToolsMCP(t, svc)

	if _, err := dispatch(t, srv, "lenny/list_artifacts", `{"sessionId":"s1"}`); err != nil {
		t.Fatalf("list_artifacts: %v", err)
	}
	if svc.method != "GET" || svc.target != "/v1/sessions/s1/artifacts" || svc.body != nil {
		t.Fatalf("routed to %s %s body=%s, want GET /v1/sessions/s1/artifacts nil", svc.method, svc.target, svc.body)
	}
}

// TestListSessionsQueryBuilding_spec_15_2_3 verifies the optional filters
// map onto the GET /v1/sessions query string. F-15.2.3.
func TestListSessionsQueryBuilding_spec_15_2_3(t *testing.T) {
	svc := &fakeService{result: sessionserver.ServiceResult{Status: 200, Body: []byte(`{"sessions":[]}`)}}
	srv := newClientToolsMCP(t, svc)

	if _, err := dispatch(t, srv, "lenny/list_sessions", `{"state":"running","runtime":"echo","labels":["team=pay"]}`); err != nil {
		t.Fatalf("list_sessions: %v", err)
	}
	if svc.method != "GET" {
		t.Fatalf("method = %s, want GET", svc.method)
	}
	for _, want := range []string{"state=running", "runtime=echo", "label=team%3Dpay"} {
		if !strings.Contains(svc.target, want) {
			t.Errorf("target %q missing %q", svc.target, want)
		}
	}
}

// TestServiceErrorProjection_spec_15_2_3 verifies a non-2xx ServiceError
// becomes a *mcp.ToolError carrying the canonical lenny code so the shared
// classifier assigns the same (category, retryable) pair on both surfaces.
// spec: §15.2.1 rule 5(d). F-15.2.3.
func TestServiceErrorProjection_spec_15_2_3(t *testing.T) {
	svc := &fakeService{err: &sessionserver.ServiceError{
		HTTPStatus: 404, Code: "RESOURCE_NOT_FOUND", Category: "NOT_FOUND",
		Message: "session not found",
	}}
	srv := newClientToolsMCP(t, svc)

	res, _ := dispatch(t, srv, "lenny/get_session_status", `{"sessionId":"missing"}`)
	if code := errorCode(t, res); code != "RESOURCE_NOT_FOUND" {
		t.Fatalf("error code = %q, want RESOURCE_NOT_FOUND", code)
	}
}

// errorCode extracts the canonical lenny code from an isError tool
// result's lenny/error content block.
func errorCode(t *testing.T, res mcp.ToolResult) string {
	t.Helper()
	if !res.IsError {
		t.Fatalf("expected isError result, got %+v", res)
	}
	for _, c := range res.Content {
		if c.Type == mcp.LennyErrorContentType {
			var env struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal([]byte(c.Text), &env); err != nil {
				t.Fatalf("decode error envelope: %v", err)
			}
			return env.Code
		}
	}
	t.Fatalf("no lenny/error block in %+v", res.Content)
	return ""
}

// TestDownloadArtifactBase64_spec_15_2_3 verifies download_artifact
// base64-encodes the blob bytes into a JSON result alongside the mime
// type. F-15.2.3.
func TestDownloadArtifactBase64_spec_15_2_3(t *testing.T) {
	svc := &fakeService{result: sessionserver.ServiceResult{Status: 200, Body: []byte("blob-bytes"), ContentType: "text/plain"}}
	srv := newClientToolsMCP(t, svc)

	res, err := dispatch(t, srv, "lenny/download_artifact", `{"ref":"lenny-blob://acme/workspace/s1/p1"}`)
	if err != nil {
		t.Fatalf("download_artifact: %v", err)
	}
	if svc.method != "GET" || !strings.Contains(svc.target, "/v1/blobs/") {
		t.Fatalf("routed to %s %s, want GET /v1/blobs/...", svc.method, svc.target)
	}
	var out struct {
		MimeType   string `json:"mimeType"`
		SizeBytes  int    `json:"sizeBytes"`
		DataBase64 string `json:"dataBase64"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	decoded, _ := base64.StdEncoding.DecodeString(out.DataBase64)
	if string(decoded) != "blob-bytes" || out.MimeType != "text/plain" || out.SizeBytes != 10 {
		t.Fatalf("result = %+v, want blob-bytes/text-plain/10", out)
	}
}

// TestUploadFilesTokenHeader_spec_15_2_3 verifies upload_files decodes the
// base64 body and carries the §7.1 upload token on the
// X-Lenny-Upload-Token header. F-15.2.3.
func TestUploadFilesTokenHeader_spec_15_2_3(t *testing.T) {
	svc := &fakeService{result: sessionserver.ServiceResult{Status: 201, Body: []byte(`{"ref":"lenny-blob://acme/workspace/s1/p1"}`)}}
	srv := newClientToolsMCP(t, svc)

	content := base64.StdEncoding.EncodeToString([]byte("hello"))
	args := `{"sessionId":"s1","uploadToken":"tok-123","contentBase64":"` + content + `","mimeType":"text/plain"}`
	if _, err := dispatch(t, srv, "lenny/upload_files", args); err != nil {
		t.Fatalf("upload_files: %v", err)
	}
	if svc.method != "POST" || svc.target != "/v1/sessions/s1/upload" {
		t.Fatalf("routed to %s %s, want POST /v1/sessions/s1/upload", svc.method, svc.target)
	}
	if string(svc.body) != "hello" {
		t.Errorf("body = %q, want decoded \"hello\"", svc.body)
	}
	if svc.headers["X-Lenny-Upload-Token"] != "tok-123" {
		t.Errorf("upload token header = %q, want tok-123", svc.headers["X-Lenny-Upload-Token"])
	}
	if svc.contentType != "text/plain" {
		t.Errorf("content type = %q, want text/plain", svc.contentType)
	}
}
