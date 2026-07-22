// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration coverage for the §26.2 coding-agent git-clone
// credential-helper journey: a coding-agent pod cloning a private repo
// over HTTPS through the in-pod git-credential helper, resolved against
// a real §4.9 VCS credential pool, without the runtime process ever
// observing the raw token.
//
// The flow drives real production code at every hop:
//
//   - cmd/git-credential-lenny is compiled and exec'd as git's actual
//     credential.helper, exactly as it runs inside a coding-agent pod.
//   - A real adapter.Server hosts the §4.7/§15.4.3 in-pod platform MCP
//     socket the helper dials, nonce-authenticated exactly as production.
//   - pkg/gateway/gatewaycontrol/platformtools.Bridge dispatches the
//     forwarded tools/call under the calling session's principal against
//     a real *mcp.Server carrying the real mcptools.Register-installed
//     lenny/vcs_token tool.
//   - pkg/gateway/provisioning/vcscred.StoreResolver resolves the token
//     from a real §4.9 credential pool (credentialpoolstore.Memory) and a
//     Secret-reading seam, exactly as the gateway does in production.
//   - A real `git clone` runs against a stub HTTPS git host (git
//     http-backend behind Basic Auth) that only accepts the resolved
//     token, so the clone can only succeed if the helper obtained a
//     working credential through this path.
//
// spec: §26.2 line 119 (shared coding-agent credential-lease scopes):
// "The runtime never sees the raw token; `git` inside the pod uses an
// HTTPS credential helper that calls the gateway's token endpoint. The
// lease is bound to the originating session ID for audit traceability."
package tier4_integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter"
	admcp "github.com/lennylabs/lenny/pkg/adapter/mcp"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/gatewaycontrol/platformtools"
	gwmcp "github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/provisioning/vcscred"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

const (
	gitCloneTestTenant   = "acme"
	gitCloneTestSession  = "sess_coding_agent_vcs"
	gitCloneTestToken    = "ghs_live_secret_test_token"
	gitCloneTestUsername = "x-access-token"
	gitCloneTestSecret   = "lenny/gh-private/token"
)

// spec: §26.2 line 119 — "vcs.<provider>.read / vcs.<provider>.write ...
// only issued when the client's WorkspacePlan.sources[] contains a
// gitClone entry targeting a private repo ... The runtime never sees the
// raw token; git inside the pod uses an HTTPS credential helper that
// calls the gateway's token endpoint. The lease is bound to the
// originating session ID for audit traceability."
//
// diagnosis: a failure here means the §26.2 in-pod git-credential-helper
// journey regressed: either the compiled helper can no longer resolve a
// working VCS token through the real adapter->gateway platform-tool
// path (the clone fails), the resolved credential leaks into the
// runtime-visible process environment or the cloned workspace's on-disk
// state, or the §4.9.2 lease audit no longer binds the mint to the
// originating session id.
func TestCodingAgentGitCloneCredentialHelperNeverExposesRawToken(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	// ---- compile the real in-pod credential helper: the same binary a
	// coding-agent pod image installs as `git-credential-lenny`. ----
	helperBin := filepath.Join(t.TempDir(), "git-credential-lenny")
	build := exec.Command("go", "build", "-o", helperBin, "./cmd/git-credential-lenny")
	build.Dir = repoRootForMCPTest(t)
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build git-credential-lenny: %v", err)
	}

	// ---- serve a private repo over HTTPS, gated by Basic Auth on
	// exactly the token the resolver below will mint, mirroring how a
	// real VCS host challenges an unauthenticated clone. ----
	reposRoot := t.TempDir()
	buildGitCloneTestBareRepo(t, reposRoot)
	srv := newGitCloneTestHTTPStub(t, reposRoot, gitCloneTestUsername, gitCloneTestToken)
	host := strings.TrimPrefix(srv.URL, "https://")

	// ---- wire the real §4.9 credential-resolution path: a VCS
	// credential pool bound to the stub host, backed by a Secret the
	// resolver reads live rather than a canned credential. ----
	pools := credentialpoolstore.NewMemory()
	if err := pools.Create(context.Background(), credentialpoolstore.CredentialPool{
		TenantID:     gitCloneTestTenant,
		Name:         "gh-private",
		Provider:     "github",
		HostPatterns: []string{host},
		Credentials:  []credentialpoolstore.Credential{{ID: "c1", SecretRef: gitCloneTestSecret}},
	}); err != nil {
		t.Fatalf("create credential pool: %v", err)
	}
	resolver := &vcscred.StoreResolver{
		Pools:   pools,
		Secrets: gitCloneTestSecretReader{ref: gitCloneTestSecret, token: gitCloneTestToken},
	}

	// ---- wire the real gateway platform-tool surface: the same
	// mcptools.Register the gateway-edge /mcp endpoint uses, with
	// lenny/vcs_token backed by the resolver above and an auditor
	// recording every §4.9.2 lease mint. ----
	auditor := &gitCloneTestLeaseAuditor{}
	gwSrv := gwmcp.NewServer()
	mcptools.Register(gwSrv, mcptools.Deps{
		TenantID:        gitCloneTestTenant,
		VCSCreds:        resolver,
		VCSLeaseAuditor: auditor,
	})

	sessions := memstore.New()
	if err := sessions.Create(context.Background(), sessionstore.Session{
		ID: gitCloneTestSession, TenantID: gitCloneTestTenant,
		State: session.StateRunning, RuntimeRef: "claude-code",
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	bridge := platformtools.New(gwSrv, sessions)

	// ---- wire a real adapter.Server: the in-pod platform MCP server the
	// compiled helper dials over its manifest-advertised socket,
	// forwarding lenny/vcs_token to the bridge above exactly as the
	// production GatewayControl link would. ----
	a := adapter.New("coding-agent-gitclone-test")
	a.WorkspaceRoot = t.TempDir()
	a.ManifestDir = t.TempDir()
	a.MCPSocket = gitCloneTestSocketPath(t)
	a.Runtime = &noopRuntime{}
	a.PlatformForwarder = gitCloneTestForwarder{bridge: bridge}

	if _, err := a.StartSession(context.Background(), &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: gitCloneTestSession},
		Runtime:   "claude-code",
	}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	t.Cleanup(func() {
		_, _ = a.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
			SessionId: &adapterv1.SessionId{Value: gitCloneTestSession},
		})
	})
	manifestPath := filepath.Join(a.ManifestDir, adapter.ManifestFilename)

	// ---- drive a real `git clone` of the private repo through the
	// compiled credential helper, exactly as a coding-agent pod's git
	// invocation would. ----
	dest := filepath.Join(t.TempDir(), "clone")
	cloneEnv := append(
		os.Environ(),
		"LENNY_ADAPTER_MANIFEST="+manifestPath,
		"GIT_SSL_NO_VERIFY=1",
		"GIT_TERMINAL_PROMPT=0",
	)
	cloneCmd := exec.Command("git", "-c", "credential.helper="+helperBin,
		"clone", "--quiet", srv.URL+"/acme-private.git", dest)
	cloneCmd.Env = cloneEnv
	if out, err := cloneCmd.CombinedOutput(); err != nil {
		t.Fatalf("git clone through credential helper: %v\n%s", err, out)
	}

	// The clone only succeeds if the helper obtained a token the stub
	// host's Basic Auth gate accepted, so a successful clone pins the
	// end-to-end resolution.
	readme, err := os.ReadFile(filepath.Join(dest, "README.md"))
	if err != nil {
		t.Fatalf("read cloned README.md: %v", err)
	}
	if string(readme) != "private acme repo\n" {
		t.Errorf("README.md = %q, want the private repo content", readme)
	}

	// spec: §26.2 line 119 — "The runtime never sees the raw token." The
	// token must not appear in the process environment the coding-agent
	// runtime process shares (cloneEnv carries none), nor anywhere the
	// clone persisted to disk (store/erase are no-ops, and the remote
	// URL carries no embedded userinfo).
	for _, kv := range cloneEnv {
		if strings.Contains(kv, gitCloneTestToken) {
			t.Fatalf("the git-clone process environment carried the raw token: %q", kv)
		}
	}
	if err := filepath.Walk(dest, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() {
			return walkErr
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if strings.Contains(string(b), gitCloneTestToken) {
			t.Errorf("%s: the cloned workspace persisted the raw VCS token", path)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk cloned workspace: %v", err)
	}
	remoteURL, err := exec.Command("git", "-C", dest, "config", "--get", "remote.origin.url").Output()
	if err != nil {
		t.Fatalf("read remote.origin.url: %v", err)
	}
	if strings.Contains(string(remoteURL), gitCloneTestToken) || strings.Contains(string(remoteURL), "@") {
		t.Errorf("remote.origin.url embeds a credential: %q", strings.TrimSpace(string(remoteURL)))
	}

	// spec: §26.2 line 119 — "The lease is bound to the originating
	// session ID for audit traceability."
	auditor.mu.Lock()
	defer auditor.mu.Unlock()
	if len(auditor.records) == 0 {
		t.Fatal("no VCS credential lease was audited")
	}
	for _, rec := range auditor.records {
		if rec.SessionID != gitCloneTestSession {
			t.Errorf("lease record session = %q, want %q", rec.SessionID, gitCloneTestSession)
		}
		if rec.Provider != "github" || rec.Mode != "read" {
			t.Errorf("lease record = %+v, want provider=github mode=read", rec)
		}
	}
}

// buildGitCloneTestBareRepo creates a bare Git repository under
// reposRoot/acme-private.git carrying one commit, the shape
// newGitCloneTestHTTPStub serves over HTTPS.
func buildGitCloneTestBareRepo(t *testing.T, reposRoot string) {
	t.Helper()
	work := t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(
			os.Environ(),
			"GIT_AUTHOR_NAME=alice", "GIT_AUTHOR_EMAIL=alice@acme.com",
			"GIT_COMMITTER_NAME=alice", "GIT_COMMITTER_EMAIL=alice@acme.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(work, "init", "--quiet", "-b", "main")
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("private acme repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(work, "add", "README.md")
	run(work, "commit", "--quiet", "-m", "initial")
	run("", "clone", "--quiet", "--bare", work, filepath.Join(reposRoot, "acme-private.git"))
}

// newGitCloneTestHTTPStub serves reposRoot over HTTPS via `git
// http-backend`, rejecting any request that does not carry HTTP Basic
// credentials matching username/token exactly — the same challenge a
// real private VCS host issues, which is what triggers git's own
// credential-helper invocation.
func newGitCloneTestHTTPStub(t *testing.T, reposRoot, username, token string) *httptest.Server {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	backend := &cgi.Handler{
		Path: gitPath,
		Args: []string{"http-backend"},
		Dir:  reposRoot,
		Env: []string{
			"GIT_HTTP_EXPORT_ALL=1",
			"GIT_PROJECT_ROOT=" + reposRoot,
			"PATH=" + os.Getenv("PATH"),
			"HOME=" + os.Getenv("HOME"),
		},
	}
	mux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, ok := r.BasicAuth()
		if !ok || gotUser != username || gotPass != token {
			w.Header().Set("WWW-Authenticate", `Basic realm="git"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		backend.ServeHTTP(w, r)
	})
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// gitCloneTestSocketPath returns a Unix socket path under a short temp
// directory so the bound path stays within the platform sun_path limit.
func gitCloneTestSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "lenny-vcs-sock-*")
	if err != nil {
		t.Fatalf("temp socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "m")
}

// gitCloneTestSecretReader is a vcscred.SecretReader returning a fixed
// token for one expected Secret reference, mirroring how the gateway
// reads a credential's live value from its Kubernetes Secret.
type gitCloneTestSecretReader struct {
	ref, token string
}

func (r gitCloneTestSecretReader) Resolve(_ context.Context, ref string) (string, error) {
	if ref != r.ref {
		return "", &gitCloneTestUnexpectedRefError{ref}
	}
	return r.token, nil
}

type gitCloneTestUnexpectedRefError struct{ ref string }

func (e *gitCloneTestUnexpectedRefError) Error() string {
	return "unexpected secret ref: " + e.ref
}

// gitCloneTestLeaseAuditor records every §4.9.2 VCS credential lease
// lenny/vcs_token mints.
type gitCloneTestLeaseAuditor struct {
	mu      sync.Mutex
	records []mcptools.VCSLeaseRecord
}

func (a *gitCloneTestLeaseAuditor) RecordVCSLease(_ context.Context, lease mcptools.VCSLeaseRecord) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.records = append(a.records, lease)
}

// gitCloneTestForwarder adapts a real *platformtools.Bridge — the
// gateway-side GatewayControl dispatch a production adapter reaches over
// gRPC — to the adapter.PlatformToolForwarder seam, so this test wires
// the same bridge production uses without standing up the mTLS gRPC
// hop. The Bridge's isError bool travels inside the JSON result body
// (mirroring pkg/adapter/gatewaycontrol.Client.CallPlatformTool), so
// only a transport/routing failure surfaces as a Go error here.
type gitCloneTestForwarder struct {
	bridge *platformtools.Bridge
}

func (f gitCloneTestForwarder) ListPlatformTools(ctx context.Context, sessionID string) ([]admcp.Tool, error) {
	descs, err := f.bridge.ListPlatformTools(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]admcp.Tool, 0, len(descs))
	for _, d := range descs {
		out = append(out, admcp.Tool{Name: d.Name, Description: d.Description, InputSchema: d.InputSchema})
	}
	return out, nil
}

func (f gitCloneTestForwarder) CallPlatformTool(ctx context.Context, sessionID, toolName string, arguments json.RawMessage) (json.RawMessage, error) {
	result, _, err := f.bridge.CallPlatformTool(ctx, sessionID, toolName, arguments)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(result), nil
}
