// SPDX-License-Identifier: MIT

package localcli

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// workspaceGateway records the §26.2 create → upload → finalize → start →
// messages flow the `session new --workspace` CLI drives.
type workspaceGateway struct {
	mu            sync.Mutex
	calls         []string
	uploadToken   string
	gotToken      string
	uploadGzip    bool
	finalizePlan  string
	startedID     string
	messageBodies []string
}

func newWorkspaceGateway(t *testing.T) (*httptest.Server, *workspaceGateway) {
	t.Helper()
	g := &workspaceGateway{uploadToken: "up-tok"}
	mux := http.NewServeMux()

	mux.HandleFunc("POST /v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		g.record("create")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"sess_ws","state":"created","uploadToken":"` + g.uploadToken + `"}`))
	})
	mux.HandleFunc("POST /v1/sessions/{id}/upload-archive", func(w http.ResponseWriter, r *http.Request) {
		g.record("upload-archive")
		g.mu.Lock()
		g.gotToken = r.Header.Get("X-Lenny-Upload-Token")
		body, _ := io.ReadAll(r.Body)
		if _, err := gzip.NewReader(bytes.NewReader(body)); err == nil {
			g.uploadGzip = true
		}
		g.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"uploadRef":"lenny-blob://default/upload/sess_ws/p1","isArchive":true}`))
	})
	mux.HandleFunc("POST /v1/sessions/{id}/upload", func(w http.ResponseWriter, r *http.Request) {
		g.record("upload")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"uploadRef":"lenny-blob://default/upload/sess_ws/f1"}`))
	})
	mux.HandleFunc("POST /v1/sessions/{id}/finalize", func(w http.ResponseWriter, r *http.Request) {
		g.record("finalize")
		body, _ := io.ReadAll(r.Body)
		g.mu.Lock()
		g.finalizePlan = string(body)
		g.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"sess_ws","state":"ready"}`))
	})
	mux.HandleFunc("POST /v1/sessions/{id}/start", func(w http.ResponseWriter, r *http.Request) {
		g.record("start")
		g.mu.Lock()
		g.startedID = r.PathValue("id")
		g.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"sess_ws","state":"running"}`))
	})
	mux.HandleFunc("POST /v1/sessions/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		g.record("messages")
		body, _ := io.ReadAll(r.Body)
		g.mu.Lock()
		g.messageBodies = append(g.messageBodies, string(body))
		g.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"deliveryReceipt":{"messageId":"msg_1","status":"delivered"}}`))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, g
}

func (g *workspaceGateway) record(name string) {
	g.mu.Lock()
	g.calls = append(g.calls, name)
	g.mu.Unlock()
}

func (g *workspaceGateway) order() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]string, len(g.calls))
	copy(out, g.calls)
	return out
}

// TestSessionNewWorkspaceStagesAndStarts_spec_26_2 drives the full
// `session new --workspace <dir>` staging flow and confirms the CLI tars
// the workspace, uploads it with the upload token, binds the plan at
// finalize, starts the session, and delivers the prompt — in that order.
// spec: §26.2 lines 95-114; §24.17 line 213. F-24.17.4 / F-26.2.4.
func TestSessionNewWorkspaceStagesAndStarts_spec_26_2(t *testing.T) {
	srv, g := newWorkspaceGateway(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var stdout, stderr bytes.Buffer
	args := []string{
		"new", "--api-url", srv.URL, "--token", "t",
		"--runtime", "claude-code", "--workspace", dir, "refactor the auth module",
	}
	code := cmdSession(args, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("session new: code=%d stderr=%s", code, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "sess_ws" {
		t.Errorf("stdout = %q, want sess_ws", stdout.String())
	}

	want := []string{"create", "upload-archive", "finalize", "start", "messages"}
	got := g.order()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("call order = %v, want %v", got, want)
	}
	if g.gotToken != "up-tok" {
		t.Errorf("upload token = %q, want up-tok", g.gotToken)
	}
	if !g.uploadGzip {
		t.Error("upload body was not gzip-compressed")
	}
	if !strings.Contains(g.finalizePlan, "uploadArchive") ||
		!strings.Contains(g.finalizePlan, "lenny-blob://default/upload/sess_ws/p1") {
		t.Errorf("finalize plan = %s", g.finalizePlan)
	}
	if g.startedID != "sess_ws" {
		t.Errorf("started id = %q", g.startedID)
	}
	if len(g.messageBodies) != 1 || !strings.Contains(g.messageBodies[0], "refactor the auth module") {
		t.Errorf("message bodies = %v", g.messageBodies)
	}
}

// TestSessionNewFileStagesUploadFile_spec_26_2 confirms a `--file` is
// uploaded via the plain /upload endpoint and bound as an uploadFile
// source. spec: §26.2 line 213; §14 uploadFile.
func TestSessionNewFileStagesUploadFile_spec_26_2(t *testing.T) {
	srv, g := newWorkspaceGateway(t)
	fp := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(fp, []byte("k: v"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var stdout, stderr bytes.Buffer
	args := []string{
		"new", "--api-url", srv.URL, "--token", "t",
		"--runtime", "claude-code", "--file", fp,
	}
	code := cmdSession(args, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("session new: code=%d stderr=%s", code, stderr.String())
	}
	got := g.order()
	want := []string{"create", "upload", "finalize", "start"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("call order = %v, want %v", got, want)
	}
	var plan struct {
		WorkspacePlan struct {
			Sources []map[string]any `json:"sources"`
		} `json:"workspacePlan"`
	}
	if err := json.Unmarshal([]byte(g.finalizePlan), &plan); err != nil {
		t.Fatalf("plan unmarshal: %v (%s)", err, g.finalizePlan)
	}
	if len(plan.WorkspacePlan.Sources) != 1 || plan.WorkspacePlan.Sources[0]["type"] != "uploadFile" {
		t.Errorf("sources = %v", plan.WorkspacePlan.Sources)
	}
	if plan.WorkspacePlan.Sources[0]["path"] != "config.yaml" {
		t.Errorf("uploadFile path = %v, want config.yaml", plan.WorkspacePlan.Sources[0]["path"])
	}
}

// TestSessionNewWorkspaceMissingDirFails confirms a missing --workspace
// directory fails fast with exit 1 before any session is created.
func TestSessionNewWorkspaceMissingDirFails(t *testing.T) {
	srv, g := newWorkspaceGateway(t)
	var stdout, stderr bytes.Buffer
	args := []string{
		"new", "--api-url", srv.URL, "--token", "t",
		"--runtime", "claude-code", "--workspace", filepath.Join(t.TempDir(), "nope"),
	}
	code := cmdSession(args, &stdout, &stderr)
	if code != 1 {
		t.Errorf("code = %d, want 1; stderr=%s", code, stderr.String())
	}
	// create runs before Pack; the flow must not reach finalize/start.
	for _, c := range g.order() {
		if c == "finalize" || c == "start" {
			t.Errorf("flow reached %q despite pack failure", c)
		}
	}
}
