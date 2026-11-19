// SPDX-License-Identifier: MIT

package llmproxy_test

import (
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/meteringproxy/llmproxy"
)

// parseProxyFlags registers the proxy's flags on a fresh flag set and
// parses args.
func parseProxyFlags(t *testing.T, args ...string) *llmproxy.Flags {
	t.Helper()
	fs := flag.NewFlagSet("lenny-gateway", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	f := llmproxy.RegisterFlags(fs)
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	return f
}

func providers(r llmproxy.TranslatorRegistry) []string {
	out := make([]string, 0, len(r))
	for p := range r {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// TestRegisterFlagsTakesDefaultsFromTheEnvironment checks that each flag
// defaults to its environment variable, and that the integer flags keep
// their built-in defaults when the variable is unset or does not parse.
// spec: §4.9 (LLM Reverse Proxy)
func TestRegisterFlagsTakesDefaultsFromTheEnvironment(t *testing.T) {
	t.Setenv("LENNY_LLM_PROXY_ADDR", ":9443")
	t.Setenv("LENNY_ANTHROPIC_VERSION", "2023-06-01")
	t.Setenv("LENNY_CREDENTIAL_FALLBACK_MAX_ROTATIONS", "not-a-number")
	f := parseProxyFlags(t)
	if *f.Addr != ":9443" || *f.AnthropicVersion != "2023-06-01" {
		t.Errorf("addr %q, anthropic version %q, want the environment's values", *f.Addr, *f.AnthropicVersion)
	}
	if *f.FallbackMaxRotations != 3 || *f.FallbackCooldownSeconds != 60 {
		t.Errorf("fallback budget %d rotations, %ds cooldown, want 3 and 60",
			*f.FallbackMaxRotations, *f.FallbackCooldownSeconds)
	}

	t.Setenv("LENNY_CREDENTIAL_FALLBACK_COOLDOWN_SECONDS", "15")
	f = parseProxyFlags(t, "-credential-fallback-max-rotations", "5", "-llm-proxy-addr", "")
	if *f.FallbackMaxRotations != 5 || *f.FallbackCooldownSeconds != 15 || *f.Addr != "" {
		t.Errorf("parsed rotations %d, cooldown %d, addr %q, want 5, 15, and empty",
			*f.FallbackMaxRotations, *f.FallbackCooldownSeconds, *f.Addr)
	}
}

// TestTranslatorsRegisterEachProviderOnlyWithItsConfiguration checks that
// the direct providers are always registered and that aws_bedrock,
// vertex_ai, and azure_openai register only when every flag they need is
// set.
// spec: §4.9 (LLM Reverse Proxy)
func TestTranslatorsRegisterEachProviderOnlyWithItsConfiguration(t *testing.T) {
	direct := []string{string(credential.ProviderAnthropicDirect), "openai_direct"}
	sort.Strings(direct)
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"defaults", nil, direct},
		{"vertex region without project", []string{"-llm-vertex-region", "us-east5"}, direct},
		{"azure endpoint without version", []string{"-llm-azure-endpoint", "https://acme.openai.azure.com"}, direct},
		{"every provider configured", []string{
			"-llm-bedrock-region", "us-east-1",
			"-llm-vertex-region", "us-east5", "-llm-vertex-project", "acme",
			"-llm-azure-endpoint", "https://acme.openai.azure.com", "-llm-azure-api-version", "2024-10-21",
		}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := providers(parseProxyFlags(t, c.args...).Translators())
			want := c.want
			if want == nil {
				want = append([]string{
					string(credential.ProviderAWSBedrock), string(credential.ProviderVertexAI), string(credential.ProviderAzureOpenAI),
				}, direct...)
				sort.Strings(want)
			}
			if len(got) != len(want) {
				t.Fatalf("providers %v, want %v", got, want)
			}
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("providers %v, want %v", got, want)
				}
			}
		})
	}
}

// TestFallbackControllerTakesTheRotationBudgetFromTheFlags checks that the
// controller the flags build exhausts a session's chain after the
// configured count of rotations.
// spec: §4.9 (Fallback Flow)
func TestFallbackControllerTakesTheRotationBudgetFromTheFlags(t *testing.T) {
	ctl := parseProxyFlags(t, "-credential-fallback-max-rotations", "1").FallbackController()
	ctl.RegisterChain("s_1", credential.ProviderAnthropicDirect, []string{"pool-a", "pool-b", "pool-c"})
	first := ctl.Fault("s_1", credential.ProviderAnthropicDirect, "pool-a", credential.TriggerFaultRateLimited)
	if first.Exhausted || first.NextPool != "pool-b" {
		t.Fatalf("first fault = %+v, want a rotation to pool-b", first)
	}
	if second := ctl.Fault("s_1", credential.ProviderAnthropicDirect, "pool-b", credential.TriggerFaultRateLimited); !second.Exhausted {
		t.Fatalf("second fault = %+v, want the chain exhausted after one rotation", second)
	}
}

// TestNewServerMountsTheProxyRouteOnItsOwnListener checks that NewServer
// disables the listener for an empty address, binds a configured one, and
// routes only POST /llm-proxy/v1/messages to the handler.
// spec: §4.9 (LLM Reverse Proxy)
func TestNewServerMountsTheProxyRouteOnItsOwnListener(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	if srv := llmproxy.NewServer("", h); srv != nil {
		t.Fatalf("NewServer with an empty address = %v, want nil", srv)
	}
	srv := llmproxy.NewServer(":8443", h)
	if srv == nil || srv.Addr != ":8443" || srv.ReadHeaderTimeout <= 0 {
		t.Fatalf("NewServer = %+v, want a server on :8443 with a header read timeout", srv)
	}
	for _, c := range []struct {
		method, path string
		want         int
	}{
		{http.MethodPost, "/llm-proxy/v1/messages", http.StatusTeapot},
		{http.MethodGet, "/llm-proxy/v1/messages", http.StatusMethodNotAllowed},
		{http.MethodPost, "/llm-proxy/v1/no-such-endpoint", http.StatusNotFound},
	} {
		rr := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rr, httptest.NewRequest(c.method, c.path, nil))
		if rr.Code != c.want {
			t.Errorf("%s %s = %d, want %d", c.method, c.path, rr.Code, c.want)
		}
	}
}
