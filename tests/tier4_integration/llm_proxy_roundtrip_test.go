// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §4.9 LLM reverse proxy delivery path end
// to end, driving a live agent-pod request through the proxy to a
// stubbed upstream provider. It exercises the proxy-mode
// lease-token-to-real-key substitution: an agent pod presents its opaque
// lease token, the proxy resolves it, injects the real upstream
// credential the pod never holds, forwards to the upstream stub,
// translates the response back to the pod dialect, and extracts the
// authoritative token usage.

package tier4_integration_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter/credfile"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/tests/testinfra/proxylease"
	"github.com/lennylabs/lenny/tests/testinfra/stubs/llmprovider"
)

// spec: 4.9 (LLM reverse proxy: validate lease token, translate to the
//
//	upstream native wire format, inject the real upstream credential
//	from the in-memory cache, forward, translate the response back,
//	and extract the authoritative token usage)
//
// diagnosis: the §4.9 proxy-mode delivery path diverged. An agent pod's
//
//	Anthropic Messages request through the LLM reverse proxy did not
//	reach the upstream provider with the real upstream credential
//	injected (lease-token-to-real-key substitution), the translated
//	response did not return to the pod, or the authoritative token
//	usage was not extracted from the upstream response. The real
//	upstream key may have leaked to the pod, or the proxy failed to
//	resolve the lease token to its cached credential.
func TestLLMProxyProxyModeRoundTrip(t *testing.T) {
	upstream := llmprovider.New(t)
	const realKey = "sk-ant-upstream-real-secret"
	fx := proxylease.Start(t, proxylease.Options{
		UpstreamBaseURL: upstream.URL(),
		UpstreamKey:     realKey,
		TenantID:        "acme",
		SessionID:       "s-proxy-1",
	})

	// The lease token must not itself be the upstream key: proxy mode
	// exists so the real key never enters the pod's SDK config.
	if fx.LeaseToken == "" || fx.LeaseToken == realKey {
		t.Fatalf("lease token must be an opaque token distinct from the upstream key; got %q", fx.LeaseToken)
	}

	// The pod points a standard Anthropic SDK at {proxyUrl} and
	// authenticates with the lease token as its x-api-key.
	reqBody := `{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":"ping-42"}]}`
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
	respRaw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("proxy request: status %d, body %s", resp.StatusCode, respRaw)
	}

	// ---- the real upstream credential was injected upstream ----
	up, ok := upstream.LastRequest()
	if !ok {
		t.Fatal("the upstream provider stub received no request; the proxy did not forward")
	}
	if got := up.Path; !strings.Contains(got, "/v1/messages") {
		t.Fatalf("translator forwarded to %q, want the Anthropic /v1/messages path", got)
	}
	if got := up.Header.Get("x-api-key"); got != realKey {
		t.Fatalf("upstream received x-api-key %q, want the injected real key %q", got, realKey)
	}
	// The opaque lease token must never reach the upstream provider.
	if bytes.Contains(bytes.ToLower(up.Body), []byte(strings.ToLower(fx.LeaseToken))) ||
		up.Header.Get("x-api-key") == fx.LeaseToken {
		t.Fatal("the pod's lease token leaked to the upstream provider")
	}

	// ---- the translated response returned to the pod, without the key ----
	if bytes.Contains(respRaw, []byte(realKey)) {
		t.Fatal("the real upstream key leaked to the agent pod in the proxy response")
	}
	var msg struct {
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(respRaw, &msg); err != nil {
		t.Fatalf("decode Anthropic Messages response: %v; body %s", err, respRaw)
	}
	if msg.Type != "message" || len(msg.Content) == 0 || msg.Content[0].Text != "ping-42" {
		t.Fatalf("proxy did not return the translated upstream response; got %s", respRaw)
	}

	// ---- the authoritative token usage was extracted ----
	usage, recorded := fx.LastUsage()
	if !recorded {
		t.Fatal("the proxy recorded no authoritative usage for the proxied request")
	}
	if usage.InputTokens <= 0 || usage.OutputTokens <= 0 {
		t.Fatalf("extracted usage must carry positive input/output token counts; got %+v", usage)
	}
}

// spec: 4.9 (LLM reverse proxy validates the lease token against the
//
//	lease store before any upstream call; a token that resolves to no
//	active lease is rejected)
//
// diagnosis: the §4.9 proxy lease-token validation diverged. A request
//
//	carrying no lease token, or a token that resolves to no active
//	lease, must be rejected before any upstream call. A regression
//	that forwards an unauthenticated request would leak an upstream
//	call (and credential) to a caller with no valid lease.
func TestLLMProxyRejectsUnknownLeaseToken(t *testing.T) {
	upstream := llmprovider.New(t)
	fx := proxylease.Start(t, proxylease.Options{UpstreamBaseURL: upstream.URL()})

	reqBody := `{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":"hi"}]}`

	// A bogus token must be rejected with 401 and never reach upstream.
	req, _ := http.NewRequest(http.MethodPost, fx.ProxyMessagesURL, strings.NewReader(reqBody))
	req.Header.Set("x-api-key", "lt-not-a-real-lease")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("issue proxy request: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unknown lease token: status %d, want 401", resp.StatusCode)
	}
	if _, ok := upstream.LastRequest(); ok {
		t.Fatal("the proxy forwarded an unauthenticated request upstream")
	}
}

// spec: 4.9 (LLM reverse proxy: "The real upstream API key never leaves
//
//	the gateway process; it never enters the agent pod's memory,
//	environment, filesystem, or network path." The pod receives a lease
//	token and proxyUrl instead of a materialized upstream key.)
//
// diagnosis: the §4.9 proxy-mode credential-isolation guarantee diverged.
//
//	A proxy-mode session's pod-facing credential material (what the
//	adapter materializes into the pod's credentials.json) carried the
//	real upstream key, or the same key was absent from the upstream
//	request the gateway forwards. Either half is a breach: the pod must
//	receive only the opaque lease token and proxyUrl on its filesystem,
//	while the real key appears only on the gateway-to-upstream network
//	call. A regression here lets a compromised agent pod read the real
//	upstream credential off its own credential file, defeating the
//	reason proxy mode exists.
func TestLLMProxyRealUpstreamKeyNeverEntersPod(t *testing.T) {
	upstream := llmprovider.New(t)
	// A recognizably-real Anthropic-style key so a substring scan of the
	// pod-facing bytes is unambiguous if it ever leaks.
	const realKey = "sk-ant-upstream-only-never-to-pod"
	fx := proxylease.Start(t, proxylease.Options{
		UpstreamBaseURL: upstream.URL(),
		UpstreamKey:     realKey,
		TenantID:        "acme",
		SessionID:       "s-proxy-isolation",
	})

	// ---- filesystem: the pod's materialized credential file holds no
	// real upstream key ----
	// credfile.Write is the production adapter writer; the bytes it lands
	// in dir are byte-identical to the pod's tmpfs credentials.json, so a
	// scan of them is a faithful check of the §4.9 "never enters the
	// agent pod's filesystem" clause.
	dir := t.TempDir()
	if err := credfile.Write(dir, []*adapterv1.CredentialLease{fx.PodCredentialLease}); err != nil {
		t.Fatalf("materialize pod credential file: %v", err)
	}
	fileBytes, err := os.ReadFile(filepath.Join(dir, credfile.FileName))
	if err != nil {
		t.Fatalf("read pod credential file: %v", err)
	}
	if bytes.Contains(fileBytes, []byte(realKey)) {
		t.Fatalf("the real upstream key leaked into the pod credential file; proxy mode must deliver only the lease token:\n%s", fileBytes)
	}
	// Positive control: proxy mode delivers the opaque lease token and the
	// proxyUrl to the pod in place of the key, so the pod's SDK can reach
	// the proxy. Their presence confirms the file is a real proxy-mode
	// credential and the absence of realKey above is not because the file
	// is empty or malformed.
	if !bytes.Contains(fileBytes, []byte(fx.LeaseToken)) {
		t.Fatalf("proxy-mode credential file is missing the lease token; got:\n%s", fileBytes)
	}
	if !bytes.Contains(fileBytes, []byte(`"deliveryMode": "proxy"`)) {
		t.Fatalf("pod credential file is not proxy-mode; got:\n%s", fileBytes)
	}

	// ---- network: the same key is injected on the gateway-to-upstream
	// request while the pod authenticates only with the lease token ----
	reqBody := `{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":"ping-iso"}]}`
	req, err := http.NewRequest(http.MethodPost, fx.ProxyMessagesURL, strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("build proxy request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// The pod holds only the lease token; it never sees the real key, so
	// the real key cannot appear on the pod's own request.
	req.Header.Set("x-api-key", fx.LeaseToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("issue proxy request: %v", err)
	}
	respRaw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("proxy request: status %d, body %s", resp.StatusCode, respRaw)
	}

	up, ok := upstream.LastRequest()
	if !ok {
		t.Fatal("the upstream provider stub received no request; the proxy did not forward")
	}
	if got := up.Header.Get("x-api-key"); got != realKey {
		t.Fatalf("upstream received x-api-key %q, want the injected real key %q", got, realKey)
	}
	// The real key must reach only the upstream: it must be absent from
	// the response streamed back to the pod (network path back into the
	// pod).
	if bytes.Contains(respRaw, []byte(realKey)) {
		t.Fatal("the real upstream key leaked to the agent pod in the proxy response")
	}
}
