// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration coverage for the §26.2 shared coding-agent
// credential-lease scopes beyond the private-repo VCS half already
// covered by coding_agent_gitclone_credential_helper_test.go:
//
//   - the `llm.provider.<name>.inference` scope is attached as a header
//     the LLM proxy injects on the runtime's behalf in proxy-mode
//     delivery, never as a credential the coding-agent pod itself holds;
//   - a WorkspacePlan whose gitClone source targets a public repository
//     (no auth block, so no leaseScope) issues no VCS credential lease at
//     all, because the in-pod credential helper is only ever invoked when
//     git itself demands credentials.
//
// spec: §26.2 lines 116-119 (shared coding-agent credentialCapabilities):
// "llm.provider.<name>.inference — required; issued by the credential
// leasing service for the pool-configured provider identity; attached as
// a header the LLM proxy injects on the runtime's behalf (proxy mode) or
// as an env var (direct mode)." and "vcs.<provider>.read /
// vcs.<provider>.write — optional; only issued when the client's
// WorkspacePlan.sources[] contains a gitClone entry targeting a private
// repo."
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
	"github.com/lennylabs/lenny/pkg/adapter/credfile"
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
	"github.com/lennylabs/lenny/tests/testinfra/proxylease"
	"github.com/lennylabs/lenny/tests/testinfra/stubs/llmprovider"
)

const (
	codingAgentCredTestTenant  = "acme"
	codingAgentCredTestSession = "sess_coding_agent_llm_vcs"
)

// spec: §26.2 lines 116-118 — "llm.provider.<name>.inference — required;
// issued by the credential leasing service for the pool-configured
// provider identity; attached as a header the LLM proxy injects on the
// runtime's behalf (proxy mode) or as an env var (direct mode)."
//
// diagnosis: a failure here means a coding-agent runtime's proxy-mode LLM
// inference lease regressed: either the real upstream provider credential
// is no longer injected as a header on the gateway's outbound request on
// the runtime's behalf, or the real credential leaked into the
// coding-agent pod's own materialized credential file instead of staying
// gateway-side behind the opaque lease token.
func TestCodingAgentLLMInferenceScopeInjectedByProxyNeverHandedToPod(t *testing.T) {
	upstream := llmprovider.New(t)
	const realKey = "sk-ant-coding-agent-upstream-secret"
	fx := proxylease.Start(t, proxylease.Options{
		UpstreamBaseURL: upstream.URL(),
		UpstreamKey:     realKey,
		TenantID:        codingAgentCredTestTenant,
		SessionID:       codingAgentCredTestSession,
	})

	// spec: §26.2 line 118 — proxy-mode delivery attaches the credential
	// "as a header the LLM proxy injects on the runtime's behalf". The
	// coding-agent pod authenticates to the proxy with only the opaque
	// lease token; the real key must never be part of what the pod holds.
	dir := t.TempDir()
	if err := credfile.Write(dir, []*adapterv1.CredentialLease{fx.PodCredentialLease}); err != nil {
		t.Fatalf("materialize coding-agent pod credential file: %v", err)
	}
	fileBytes, err := os.ReadFile(filepath.Join(dir, credfile.FileName))
	if err != nil {
		t.Fatalf("read pod credential file: %v", err)
	}
	if strings.Contains(string(fileBytes), realKey) {
		t.Fatalf("the real llm.provider inference credential leaked into the coding-agent pod's credential file:\n%s", fileBytes)
	}
	if !strings.Contains(string(fileBytes), fx.LeaseToken) {
		t.Fatalf("coding-agent pod credential file is missing the opaque lease token; got:\n%s", fileBytes)
	}

	// Drive one inference call as the coding-agent runtime's SDK would:
	// POST to the proxy authenticating with the lease token.
	reqBody := `{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":"coding-agent-ping"}]}`
	req, err := http.NewRequest(http.MethodPost, fx.ProxyMessagesURL, strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("build proxy request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", fx.LeaseToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("issue proxy request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("proxy request: status %d", resp.StatusCode)
	}

	// spec: §26.2 line 118 — the credential is "attached as a header the
	// LLM proxy injects on the runtime's behalf". The upstream provider
	// stub must see the real key on the request the gateway forwards, and
	// the coding-agent's own lease token must never reach the upstream.
	up, ok := upstream.LastRequest()
	if !ok {
		t.Fatal("the upstream provider stub received no request; the proxy did not forward on the coding-agent runtime's behalf")
	}
	if got := up.Header.Get("x-api-key"); got != realKey {
		t.Fatalf("upstream received x-api-key %q, want the proxy-injected real key %q", got, realKey)
	}
	if up.Header.Get("x-api-key") == fx.LeaseToken {
		t.Fatal("the coding-agent's opaque lease token leaked to the upstream provider instead of the proxy-injected real key")
	}
}

// spec: §26.2 line 119 — "vcs.<provider>.read / vcs.<provider>.write ...
// only issued when the client's WorkspacePlan.sources[] contains a
// gitClone entry targeting a private repo."
//
// diagnosis: a failure here means the in-pod git-credential helper (or
// the gateway's lenny/vcs_token tool it calls) requests or is issued a
// VCS credential lease for a public repository clone that never
// challenged for authentication. That would mint and audit a lease scope
// §26.2 says must not be issued for a public gitClone source, widening
// the credential's exposure and audit surface beyond what the spec
// permits.
func TestCodingAgentPublicRepoWorkspacePlanIssuesNoVCSLease(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	// ---- compile the real in-pod credential helper, exactly as the
	// private-repo journey test does. ----
	helperBin := filepath.Join(t.TempDir(), "git-credential-lenny")
	build := exec.Command("go", "build", "-o", helperBin, "./cmd/git-credential-lenny")
	build.Dir = repoRootForMCPTest(t)
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build git-credential-lenny: %v", err)
	}

	// ---- serve a repo over HTTPS with no auth challenge at all, the
	// shape of a public gitClone source: git never receives a 401 and so
	// never invokes the credential helper in the first place. ----
	reposRoot := t.TempDir()
	buildCodingAgentPublicBareRepo(t, reposRoot)
	srv := newCodingAgentPublicHTTPStub(t, reposRoot)

	// ---- wire the real §4.9 VCS credential-resolution and audit path.
	// If the helper were ever invoked it would resolve against this pool,
	// so a populated pool proves an empty audit trail reflects "the
	// helper was never asked", not "no pool was configured to serve it". ----
	pools := credentialpoolstore.NewMemory()
	if err := pools.Create(context.Background(), credentialpoolstore.CredentialPool{
		TenantID:     codingAgentCredTestTenant,
		Name:         "gh-public-would-serve",
		Provider:     "github",
		HostPatterns: []string{strings.TrimPrefix(srv.URL, "https://")},
		Credentials:  []credentialpoolstore.Credential{{ID: "c1", SecretRef: "lenny/gh-public/token"}},
	}); err != nil {
		t.Fatalf("create credential pool: %v", err)
	}
	resolver := &vcscred.StoreResolver{
		Pools:   pools,
		Secrets: codingAgentPublicSecretReader{},
	}
	auditor := &codingAgentPublicLeaseAuditor{}
	gwSrv := gwmcp.NewServer()
	mcptools.Register(gwSrv, mcptools.Deps{
		TenantID:        codingAgentCredTestTenant,
		VCSCreds:        resolver,
		VCSLeaseAuditor: auditor,
	})

	sessions := memstore.New()
	if err := sessions.Create(context.Background(), sessionstore.Session{
		ID: codingAgentCredTestSession, TenantID: codingAgentCredTestTenant,
		State: session.StateRunning, RuntimeRef: "claude-code",
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	bridge := platformtools.New(gwSrv, sessions)

	a := adapter.New("coding-agent-public-repo-test")
	a.WorkspaceRoot = t.TempDir()
	a.ManifestDir = t.TempDir()
	a.MCPSocket = codingAgentPublicSocketPath(t)
	a.Runtime = &noopRuntime{}
	a.PlatformForwarder = codingAgentPublicForwarder{bridge: bridge}

	if _, err := a.StartSession(context.Background(), &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: codingAgentCredTestSession},
		Runtime:   "claude-code",
	}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	t.Cleanup(func() {
		_, _ = a.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
			SessionId: &adapterv1.SessionId{Value: codingAgentCredTestSession},
		})
	})
	manifestPath := filepath.Join(a.ManifestDir, adapter.ManifestFilename)

	// ---- drive a real, unauthenticated `git clone` of the public repo
	// through the compiled credential helper, exactly as a coding-agent
	// pod cloning a public gitClone WorkspacePlan source would. ----
	dest := filepath.Join(t.TempDir(), "clone")
	cloneEnv := append(
		os.Environ(),
		"LENNY_ADAPTER_MANIFEST="+manifestPath,
		"GIT_SSL_NO_VERIFY=1",
		"GIT_TERMINAL_PROMPT=0",
	)
	cloneCmd := exec.Command("git", "-c", "credential.helper="+helperBin,
		"clone", "--quiet", srv.URL+"/acme-public.git", dest)
	cloneCmd.Env = cloneEnv
	if out, err := cloneCmd.CombinedOutput(); err != nil {
		t.Fatalf("git clone of public repo through credential helper: %v\n%s", err, out)
	}
	readme, err := os.ReadFile(filepath.Join(dest, "README.md"))
	if err != nil {
		t.Fatalf("read cloned README.md: %v", err)
	}
	if string(readme) != "public acme repo\n" {
		t.Errorf("README.md = %q, want the public repo content", readme)
	}

	// spec: §26.2 line 119 — a public-repo gitClone source issues no VCS
	// lease. The clone above succeeded with no Basic Auth challenge, so
	// git never invoked the credential helper's lenny/vcs_token call, and
	// the audit trail must be empty even though a resolvable pool exists.
	auditor.mu.Lock()
	defer auditor.mu.Unlock()
	if len(auditor.records) != 0 {
		t.Fatalf("public gitClone source issued %d VCS credential lease(s), want 0: %+v", len(auditor.records), auditor.records)
	}
}

// buildCodingAgentPublicBareRepo creates a bare Git repository under
// reposRoot/acme-public.git carrying one commit, the shape
// newCodingAgentPublicHTTPStub serves over HTTPS with no auth challenge.
func buildCodingAgentPublicBareRepo(t *testing.T, reposRoot string) {
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
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("public acme repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(work, "add", "README.md")
	run(work, "commit", "--quiet", "-m", "initial")
	run("", "clone", "--quiet", "--bare", work, filepath.Join(reposRoot, "acme-public.git"))
}

// newCodingAgentPublicHTTPStub serves reposRoot over HTTPS via `git
// http-backend` with no auth gate, the shape of a public VCS host: git
// clones it without ever receiving a 401, so it never invokes the
// configured credential helper.
func newCodingAgentPublicHTTPStub(t *testing.T, reposRoot string) *httptest.Server {
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
	srv := httptest.NewTLSServer(backend)
	t.Cleanup(srv.Close)
	return srv
}

// codingAgentPublicSocketPath returns a Unix socket path under a short
// temp directory so the bound path stays within the platform sun_path
// limit.
func codingAgentPublicSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "lenny-vcs-pub-sock-*")
	if err != nil {
		t.Fatalf("temp socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "m")
}

// codingAgentPublicSecretReader must never be called: a public clone
// never triggers credential resolution, so any call to Resolve is itself
// a test failure surfaced through an error distinguishable from a real
// secret value.
type codingAgentPublicSecretReader struct{}

func (codingAgentPublicSecretReader) Resolve(_ context.Context, ref string) (string, error) {
	return "", &codingAgentPublicUnexpectedResolveError{ref}
}

type codingAgentPublicUnexpectedResolveError struct{ ref string }

func (e *codingAgentPublicUnexpectedResolveError) Error() string {
	return "unexpected VCS secret resolution for a public gitClone source, ref: " + e.ref
}

// codingAgentPublicLeaseAuditor records every §4.9.2 VCS credential lease
// lenny/vcs_token mints, so the test can assert none were minted for a
// public repo clone.
type codingAgentPublicLeaseAuditor struct {
	mu      sync.Mutex
	records []mcptools.VCSLeaseRecord
}

func (a *codingAgentPublicLeaseAuditor) RecordVCSLease(_ context.Context, lease mcptools.VCSLeaseRecord) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.records = append(a.records, lease)
}

// codingAgentPublicForwarder adapts a real *platformtools.Bridge to the
// adapter.PlatformToolForwarder seam, mirroring
// gitCloneTestForwarder in coding_agent_gitclone_credential_helper_test.go.
type codingAgentPublicForwarder struct {
	bridge *platformtools.Bridge
}

func (f codingAgentPublicForwarder) ListPlatformTools(ctx context.Context, sessionID string) ([]admcp.Tool, error) {
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

func (f codingAgentPublicForwarder) CallPlatformTool(ctx context.Context, sessionID, toolName string, arguments json.RawMessage) (json.RawMessage, error) {
	result, _, err := f.bridge.CallPlatformTool(ctx, sessionID, toolName, arguments)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(result), nil
}
