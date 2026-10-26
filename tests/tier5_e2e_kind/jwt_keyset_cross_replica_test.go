// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test for the §10.2 shared signing key on the full chart.
// The e2e install runs kms.provider=local with two gateway replicas, the
// Token Service, and lenny-ops, all mounting the one local KMS master key
// Secret the chart renders. A bearer minted through one gateway replica
// must verify through the other, and the chart-deployed lenny-ops must
// accept a gateway-minted bearer and a POST /v1/oauth/token token without
// dev headers.
package tier5_e2e_kind_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// keySetOpsAcceptBound is how long after lenny-ops is Ready a Lenny-issued
// token must be accepted: the first §25.4 revocation set read runs at
// startup and every 30 s after it.
const keySetOpsAcceptBound = 35 * time.Second

// keySetCall issues one request against base and returns the status and
// decoded body. A non-empty bearer is sent as the Authorization header;
// otherwise the dev headers name a platform-admin. form, when non-nil, is
// sent as an application/x-www-form-urlencoded body; otherwise body, when
// non-nil, is sent as JSON.
func keySetCall(t *testing.T, method, target, bearer string, body any, form url.Values) (int, map[string]any) {
	t.Helper()
	var reader io.Reader
	contentType := "application/json"
	switch {
	case form != nil:
		reader = strings.NewReader(form.Encode())
		contentType = "application/x-www-form-urlencoded"
	case body != nil:
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", contentType)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	} else {
		req.Header.Set("X-Lenny-Tenant-ID", "platform")
		req.Header.Set("X-Lenny-User-ID", "carol@acme.com")
		req.Header.Set("X-Lenny-Roles", "platform-admin")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

// gatewayPods returns the names of the running gateway pods.
func gatewayPods(t *testing.T, c *kind.Cluster) []string {
	t.Helper()
	out, err := c.KubectlOut(t, "-n", t5SystemNS, "get", "pods", "-l", gatewayComponentSelector,
		"--field-selector=status.phase=Running", "-o", "jsonpath={.items[*].metadata.name}")
	if err != nil {
		t.Fatalf("list gateway pods: %v", err)
	}
	return strings.Fields(out)
}

// mintKeySetImpersonation mints an impersonation bearer for
// alice@acme.com, a tenant-admin on this install, through base.
func mintKeySetImpersonation(t *testing.T, base string) string {
	t.Helper()
	code, body := keySetCall(t, http.MethodPost, base+"/v1/admin/impersonation", "", map[string]any{
		"targetTenantId": "acme", "targetUserId": "alice@acme.com",
		"reason": "tier-5 key-set cross-replica probe", "ticketId": "tier5-keyset", "durationSeconds": 600,
	}, nil)
	tok, _ := body["bearerToken"].(string)
	if code != http.StatusOK || tok == "" {
		t.Fatalf("impersonate alice@acme.com through %s: status %d body %v", base, code, body)
	}
	return tok
}

// requireOpsAccepts polls the chart-deployed lenny-ops GET /v1/admin/me
// with bearer and no dev headers until it answers 200 with sub, within
// keySetOpsAcceptBound.
func requireOpsAccepts(t *testing.T, opsBase, what, bearer, sub string) {
	t.Helper()
	deadline := time.Now().Add(keySetOpsAcceptBound)
	for {
		code, body := keySetCall(t, http.MethodGet, opsBase+"/v1/admin/me", bearer, nil, nil)
		identity, _ := body["identity"].(map[string]any)
		if code == http.StatusOK && identity["sub"] == sub {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("lenny-ops did not accept the %s within %s: status %d body %v", what, keySetOpsAcceptBound, code, body)
			return
		}
		time.Sleep(time.Second)
	}
}

// spec: 10.2 (Authentication) "Signing topology", 10.2 (Authentication) "Verifier key set and rotation", 4.3 (Token Service), 25.4 (The lenny-ops Service)
// diagnosis: a refusal through the second replica means the chart does not
// give every gateway replica the same local KMS master key Secret, or the
// gateway does not sign and verify through the shared platform:jwt-signing
// key set. A refusal on lenny-ops means the chart does not mount that key
// in lenny-ops, does not wire lenny-ops to Postgres (so its first
// revocation set read never succeeds and every Lenny-issued token gets the
// stale-set refusal), or lenny-ops does not load the key set.
func TestLennyIssuedBearerVerifiesAcrossChartReplicas_spec_10_2(t *testing.T) {
	c := kind.InstallLenny(t)
	pods := gatewayPods(t, c)
	if len(pods) < 2 {
		t.Fatalf("the e2e install runs %d gateway pods %v; the cross-replica check needs the chart's two replicas", len(pods), pods)
	}
	baseA, stopA := c.PortForward(t, "pod/"+pods[0], t5SystemNS, efGatewayHTTPPort)
	defer stopA()
	baseB, stopB := c.PortForward(t, "pod/"+pods[1], t5SystemNS, efGatewayHTTPPort)
	defer stopB()

	minted := mintKeySetImpersonation(t, baseA)
	if code, body := keySetCall(t, http.MethodGet, baseB+"/v1/admin/me", minted, nil, nil); code != http.StatusOK || body["tenantId"] != "acme" {
		t.Errorf("bearer minted on %s, presented to %s: status %d body %v, want 200 as acme", pods[0], pods[1], code, body)
	}
	admin := readAdminBearerSecret(t, c)
	for i, base := range []string{baseA, baseB} {
		if code, body := keySetCall(t, http.MethodGet, base+"/v1/admin/me", admin, nil, nil); code != http.StatusOK {
			t.Errorf("bootstrap admin credential on gateway pod %s: status %d body %v, want 200", pods[i], code, body)
		}
	}

	second := mintKeySetImpersonation(t, baseB)
	form := url.Values{
		"grant_type":         {"urn:ietf:params:oauth:grant-type:token-exchange"},
		"subject_token":      {second},
		"subject_token_type": {"urn:ietf:params:oauth:token-type:jwt"},
	}
	code, body := keySetCall(t, http.MethodPost, baseA+"/v1/oauth/token", minted, nil, form)
	exchanged, _ := body["access_token"].(string)
	if code != http.StatusOK || exchanged == "" {
		t.Fatalf("POST /v1/oauth/token through %s: status %d body %v", pods[0], code, body)
	}

	if !t5DeploymentReady(t, c, "lenny-ops") {
		t.Fatal("lenny-ops is not Ready on the e2e install")
	}
	opsBase, stopOps := c.PortForward(t, "svc/lenny-ops", t5SystemNS, opsHTTPPort)
	defer stopOps()
	requireOpsAccepts(t, opsBase, "gateway-minted impersonation bearer", minted, "alice@acme.com")
	requireOpsAccepts(t, opsBase, "POST /v1/oauth/token token", exchanged, "alice@acme.com")
	requireOpsAccepts(t, opsBase, "bootstrap admin credential", admin, "lenny-admin")
}
