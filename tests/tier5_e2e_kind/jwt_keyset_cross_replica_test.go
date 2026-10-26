// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test for the §10.2 shared signing key on the full chart.
// The e2e install runs kms.provider=local with two gateway replicas, the
// Token Service, and lenny-ops, all mounting the one local KMS master key
// Secret the chart renders. A bearer minted through one gateway replica
// must verify through the other, and the chart-deployed lenny-ops must
// accept a gateway-minted bearer and admit a POST /v1/oauth/token token
// through authentication and its revocation set without dev headers.
package tier5_e2e_kind_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

// skipUnlessSharedMasterKey skips the test when the deployed release runs
// the local KMS provider without the master key file the chart passes
// every replica. Such a release predates the chart's local master key
// Secret, so each gateway pod derives its own key from a random seed and
// no cross-replica outcome is meaningful until the release is upgraded
// from the current chart. The chart render tests pin the wiring itself.
//
// spec: §10.2 (Authentication) "Verifier key set and rotation", §17.4 (Local Development Mode).
func skipUnlessSharedMasterKey(t *testing.T, c *kind.Cluster) {
	t.Helper()
	args, err := c.KubectlOut(t, "-n", t5SystemNS, "get", "deployment", "lenny-gateway",
		"-o", "jsonpath={.spec.template.spec.containers[?(@.name==\"gateway\")].args}")
	if err != nil {
		t.Fatalf("read lenny-gateway args: %v\n%s", err, args)
	}
	if strings.Contains(args, "--kms-provider=local") && !strings.Contains(args, "--kms-master-key-file=") {
		t.Skipf("blocked: the deployed lenny-e2e release runs --kms-provider=local without --kms-master-key-file, "+
			"so it predates the chart's local KMS master key Secret; upgrade the release from the current chart "+
			"and rebuild the images (gateway args %s)", args)
	}
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

// opsPodSelector selects the lenny-ops pods. lenny-ops pods carry
// app=lenny-ops rather than the lenny.dev/component label (§13.2.3).
const opsPodSelector = "app=lenny-ops"

// opsReadyTransitions lists, one line per Running lenny-ops pod, the Ready
// condition's status and lastTransitionTime separated by a space.
const opsReadyTransitions = `{range .items[*]}{.status.conditions[?(@.type=="Ready")].status}{" "}` +
	`{.status.conditions[?(@.type=="Ready")].lastTransitionTime}{"\n"}{end}`

// latestOpsReadyAt parses the opsReadyTransitions output and returns the
// latest Ready transition among the pods whose Ready condition is True.
// The latest one bounds every replica the Service can route a probe to.
// No Ready pod, or an unparseable time, is an error.
func latestOpsReadyAt(out string) (time.Time, error) {
	var latest time.Time
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		status, at, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || status != "True" {
			continue
		}
		readyAt, err := time.Parse(time.RFC3339, strings.TrimSpace(at))
		if err != nil {
			return time.Time{}, fmt.Errorf("parse lenny-ops Ready lastTransitionTime %q: %w", at, err)
		}
		if readyAt.After(latest) {
			latest = readyAt
		}
	}
	if latest.IsZero() {
		return time.Time{}, fmt.Errorf("no Running lenny-ops pod reports Ready=True in %q", out)
	}
	return latest, nil
}

// opsAcceptDeadline returns the moment by which the chart-deployed
// lenny-ops must accept a Lenny-issued token: keySetOpsAcceptBound after
// its pods became Ready, read from the Ready condition's
// lastTransitionTime rather than from when the test reaches the probe.
func opsAcceptDeadline(t *testing.T, c *kind.Cluster) time.Time {
	t.Helper()
	out, err := c.KubectlOut(t, "-n", t5SystemNS, "get", "pods", "-l", opsPodSelector,
		"--field-selector=status.phase=Running", "-o", "jsonpath="+opsReadyTransitions)
	if err != nil {
		t.Fatalf("read lenny-ops pod Ready conditions: %v", err)
	}
	readyAt, err := latestOpsReadyAt(out)
	if err != nil {
		t.Fatalf("lenny-ops Ready time: %v", err)
	}
	return readyAt.Add(keySetOpsAcceptBound)
}

// requireOpsAccepts polls the chart-deployed lenny-ops GET /v1/admin/me
// with bearer and no dev headers until it answers 200 with sub, or until
// deadline, the shared bound measured from lenny-ops becoming Ready. The
// first attempt always runs, so a probe that starts after the deadline
// passes only when acceptance had already begun within the bound.
func requireOpsAccepts(t *testing.T, opsBase string, deadline time.Time, what, bearer, sub string) {
	t.Helper()
	requireOpsAnswers(t, opsBase, deadline, what, bearer, func(code int, body map[string]any) bool {
		identity, _ := body["identity"].(map[string]any)
		return code == http.StatusOK && identity["sub"] == sub
	})
}

// requireOpsAuthenticates is requireOpsAccepts for a token that carries no
// roles claim, such as a POST /v1/oauth/token token: lenny-ops admits it
// through authentication and the §25.4 revocation set, and its role gate
// then answers 403 FORBIDDEN. A 200 also passes. Any refusal by the key
// set or the revocation set (401 or 503) does not.
//
// spec: §25.4 (The lenny-ops Service: Authentication).
func requireOpsAuthenticates(t *testing.T, opsBase string, deadline time.Time, what, bearer string) {
	t.Helper()
	requireOpsAnswers(t, opsBase, deadline, what, bearer, func(code int, body map[string]any) bool {
		envelope, _ := body["error"].(map[string]any)
		return code == http.StatusOK || (code == http.StatusForbidden && envelope["code"] == "FORBIDDEN")
	})
}

// requireOpsAnswers polls lenny-ops GET /v1/admin/me with bearer until
// done accepts the answer or deadline passes, and reports a test error
// in the second case.
func requireOpsAnswers(t *testing.T, opsBase string, deadline time.Time, what, bearer string, done func(int, map[string]any) bool) {
	t.Helper()
	for {
		code, body := keySetCall(t, http.MethodGet, opsBase+"/v1/admin/me", bearer, nil, nil)
		if done(code, body) {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("lenny-ops did not accept the %s within %s of becoming Ready (deadline %s): status %d body %v",
				what, keySetOpsAcceptBound, deadline.Format(time.RFC3339), code, body)
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
	skipUnlessSharedMasterKey(t, c)
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
	deadline := opsAcceptDeadline(t, c)
	opsBase, stopOps := c.PortForward(t, "svc/lenny-ops", t5SystemNS, opsHTTPPort)
	defer stopOps()
	requireOpsAccepts(t, opsBase, deadline, "gateway-minted impersonation bearer", minted, "alice@acme.com")
	requireOpsAuthenticates(t, opsBase, deadline, "POST /v1/oauth/token token", exchanged)
	requireOpsAccepts(t, opsBase, deadline, "bootstrap admin credential", admin, "lenny-admin")
}

// spec: 25.4 (The lenny-ops Service)
// diagnosis: a failure means the acceptance deadline is not taken from the
// latest Ready transition of the lenny-ops pods, so the tier-5 bound on
// lenny-ops accepting a Lenny-issued token after becoming Ready is
// measured from the wrong moment or from a pod that is not Ready.
func TestOpsAcceptBoundAnchorsOnReadyTransition_spec_25_4(t *testing.T) {
	early := "2026-10-26T10:00:00Z"
	late := "2026-10-26T10:05:00Z"
	cases := []struct {
		name    string
		out     string
		want    string
		wantErr bool
	}{
		{name: "single ready pod", out: "True " + early + "\n", want: early},
		{name: "latest of two ready pods", out: "True " + late + "\nTrue " + early + "\n", want: late},
		{name: "not-ready pod ignored", out: "False " + late + "\nTrue " + early + "\n", want: early},
		{name: "no ready pod", out: "False " + early + "\n", wantErr: true},
		{name: "empty listing", out: "", wantErr: true},
		{name: "unparseable time", out: "True yesterday\n", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := latestOpsReadyAt(tc.out)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("latestOpsReadyAt(%q) = %s, want an error", tc.out, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("latestOpsReadyAt(%q): %v", tc.out, err)
			}
			if want, _ := time.Parse(time.RFC3339, tc.want); !got.Equal(want) {
				t.Fatalf("latestOpsReadyAt(%q) = %s, want %s", tc.out, got, want)
			}
		})
	}
}
