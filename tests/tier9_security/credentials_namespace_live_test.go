// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 live checks of the chart-installed credentials namespace. Envtest
// does not exercise chart-installed RBAC and a rendered template does not
// exercise the API server, so each claim is decided by the cluster:
// `kubectl auth can-i --as` issues a SubjectAccessReview per ServiceAccount
// and verb, a real pod create meets the ResourceQuota, and the admin probe
// and the keyed read cross the lenny-system default-deny on the
// policy-enforcing CNI. The access check follows controller_rbac_test.go.
//
// spec: §4.9 (Credential Storage Topology: Credentials namespace), §13.2.4,
// §16.4 (Credential-sensitive RPCs).

package tier9_security_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/tests/testinfra/kind"
	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
)

// credNSSecretVerbs is every verb on secrets a ServiceAccount could hold.
var credNSSecretVerbs = []string{"get", "list", "watch", "create", "update", "patch", "delete", "deletecollection"}

// credNSServiceAccount returns the username a SubjectAccessReview impersonates.
func credNSServiceAccount(name string) string {
	return "system:serviceaccount:" + lennySystemNS + ":" + name
}

// credNSCanI asks the API server whether the ServiceAccount may perform verb
// on secrets in the credentials namespace. Output that is neither answer is a
// harness failure, so a broken kubectl never reads as a denial.
func credNSCanI(t *testing.T, c *kind.Cluster, sa, verb string) bool {
	t.Helper()
	out, _ := c.KubectlOut(t, "auth", "can-i", verb, "secrets",
		"-n", credential.DefaultNamespace, "--as", credNSServiceAccount(sa))
	fields := strings.Fields(out)
	if len(fields) == 0 || (fields[0] != "yes" && fields[0] != "no") {
		t.Fatalf("kubectl auth can-i %s secrets --as %s printed %q", verb, sa, out)
	}
	return fields[0] == "yes"
}

// spec: §4.9 (Credentials namespace rules 3, 4, and 5)
// diagnosis: a chart-installed authorization decision differs from the §4.9
// matrix. A Token Service list or watch allows an enumeration and a cached
// read. A gateway get lets the gateway read values the Token Service alone
// reads. Any allowed verb for lenny-ops, a controller, or a webhook means a
// binding outside the two credentials-namespace Roles reaches the namespace.
func TestCredentialsNamespaceSecretAccessLiveRBAC_spec_4_9(t *testing.T) {
	c := kind.InstallLenny(t)
	allowed := map[string]map[string]bool{
		"lenny-token-service":           {"get": true},
		"lenny-gateway":                 {"create": true, "update": true, "delete": true, "deletecollection": true},
		"lenny-ops-sa":                  {},
		"lenny-controller":              {},
		"lenny-pool-scaling-controller": {},
		"lenny-webhook":                 {},
	}
	for sa, verbs := range allowed {
		for _, verb := range credNSSecretVerbs {
			t.Run(sa+"/"+verb, func(t *testing.T) {
				if got, want := credNSCanI(t, c, sa, verb), verbs[verb]; got != want {
					t.Fatalf("§4.9: can-i %s secrets in %s as %s = %t, want %t",
						verb, credential.DefaultNamespace, sa, got, want)
				}
			})
		}
	}
}

// spec: §4.9 (Credentials namespace rule 2), §17.2
// diagnosis: a pod was admitted to the credentials namespace, or refused by
// something other than the quota. The chart's pods: "0" quota is the control
// that closes pod mounts of credential Secrets, because the controller
// ClusterRole grants pod creation cluster-wide.
func TestCredentialsNamespaceRefusesPodsByQuota_spec_4_9(t *testing.T) {
	c := kind.InstallLenny(t)
	const name = "t9-credns-quota-probe"
	manifest := fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: %s
spec:
  containers:
    - name: probe
      image: registry.k8s.io/pause:3.9
`, name, credential.DefaultNamespace)
	del := func() {
		_, _ = c.KubectlOut(t, "-n", credential.DefaultNamespace, "delete", "pod", name,
			"--ignore-not-found", "--wait=false", "--force", "--grace-period=0")
	}
	del()
	t.Cleanup(del)
	out, err := c.ApplyStdin(t, manifest)
	if err == nil {
		t.Fatalf("§4.9: a pod was admitted to %s: %s", credential.DefaultNamespace, out)
	}
	if !strings.Contains(out, "exceeded quota") {
		t.Fatalf("§4.9: the pod create failed for a reason other than the quota: %v\n%s", err, out)
	}
}

// credNSAdmin issues one platform-admin dev-header request through d and
// returns the status and raw body.
func credNSAdmin(t *testing.T, d *sessiondriver.Driver, method, path string, body any) (int, []byte) {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequest(method, d.BaseURL()+path, bytes.NewReader(b))
	if err != nil {
		t.Fatalf("build %s %s: %v", method, path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "platform")
	req.Header.Set("X-Lenny-Roles", "platform-admin")
	req.Header.Set("X-Lenny-User-ID", "alice")
	res, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, raw
}

// credNSPool is the create body of a pool for tenant.
func credNSPool(tenant, name, provider string, hosts []string, ref string) map[string]any {
	body := map[string]any{
		"tenantId": tenant, "name": name, "provider": provider,
		"assignmentStrategy": "least-loaded", "maxConcurrentSessions": 10,
	}
	if len(hosts) > 0 {
		body["hostPatterns"] = hosts
	}
	if ref != "" {
		body["credentials"] = []map[string]string{{"id": "key-1", "secretRef": ref}}
	}
	return body
}

// credNSTenant bootstraps a run-unique tenant through d.
func credNSTenant(ctx context.Context, t *testing.T, d *sessiondriver.Driver) string {
	t.Helper()
	tenant := fmt.Sprintf("t9-credns-%d", time.Now().UnixNano())
	if err := d.BootstrapTenant(ctx, tenant); err != nil {
		t.Fatalf("bootstrap tenant: %v", err)
	}
	return tenant
}

// spec: §13.2.4 (Token Service kube-apiserver egress), §4.9 (admin-time Secret
// probe)
// diagnosis: 503 CREDENTIAL_PROBE_UNAVAILABLE means the Token Service could
// not reach the kube-apiserver under the lenny-system default-deny, so
// allow-token-service lost its kubeApiServerCIDR egress rule. The 422 is a
// definitive NOT_FOUND verdict, which only a completed get returns.
func TestTokenServiceProbeReachesKubeAPIServerUnderDefaultDeny_spec_13_2_4(t *testing.T) {
	d := sessiondriver.New(t, sessiondriver.Options{HTTPTimeout: 30 * time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	tenant := credNSTenant(ctx, t, d)
	code, raw := credNSAdmin(t, d, http.MethodPost, "/v1/admin/credential-pools",
		credNSPool(tenant, "probe", "anthropic_direct", nil, tenant+"-absent"))
	if code != http.StatusUnprocessableEntity || !bytes.Contains(raw, []byte("CREDENTIAL_SECRET_RBAC_MISSING")) {
		t.Fatalf("§13.2.4: probe of an absent Secret answered %d: %s, want a 422 verdict from the kube-apiserver", code, raw)
	}
}

// credNSLogs returns the logs of every replica of the lenny-system component.
func credNSLogs(t *testing.T, c *kind.Cluster, component string) string {
	t.Helper()
	out, err := c.KubectlOut(t, "-n", lennySystemNS, "logs", "-l", "lenny.dev/component="+component,
		"--all-containers", "--tail=-1", "--max-log-requests=10")
	if err != nil {
		t.Fatalf("read %s logs: %v\n%s", component, err, out)
	}
	return out
}

// spec: §16.4 (Credential-sensitive RPCs), §4.9 (Credential values submitted
// through the admin API; Token Service secret resolution rule 5)
// diagnosis: a submitted credential value reached a component log. The value
// travels through the gateway on the admin write and through both components
// on the keyed read, so a hit means a logging path recorded a request or a
// response body. A missing credential_rpc line means the keyed read did not
// run and the log search proved nothing about the Token Service.
func TestSecretDataValueAbsentFromGatewayAndTokenServiceLogs_spec_16_4(t *testing.T) {
	d := sessiondriver.New(t, sessiondriver.Options{HTTPTimeout: 30 * time.Second})
	c := d.Cluster()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	tenant := credNSTenant(ctx, t, d)
	if err := d.AllowSessionsWithNoEnvironment(ctx, tenant); err != nil {
		t.Fatalf("relax noEnvironmentPolicy: %v", err)
	}
	const host = "git.acme.example"
	if code, raw := credNSAdmin(t, d, http.MethodPost, "/v1/admin/credential-pools",
		credNSPool(tenant, "vcs", "github", []string{host}, "")); code != http.StatusCreated {
		t.Fatalf("create pool answered %d: %s", code, raw)
	}
	canary := "ghs_t9_canary_" + tenant
	if code, raw := credNSAdmin(t, d, http.MethodPost, "/v1/admin/credential-pools/vcs/credentials?tenantId="+tenant,
		map[string]any{"id": "key-1", "secretData": map[string]string{"token": canary}}); code != http.StatusCreated {
		t.Fatalf("secretData add answered %d: %s", code, raw)
	}
	plan := fmt.Sprintf(`{"schemaVersion":1,"sources":[{"type":"gitClone","url":"https://%s/acme/private.git",`+
		`"ref":"main","auth":{"mode":"credential-lease","leaseScope":"vcs.github.read"}}]}`, host)
	// The clone target does not exist, and the read this test needs precedes
	// the ls-remote, so the create's outcome is not the assertion.
	_, createErr := d.CreateSessionWithOptions(ctx, tenant, sessiondriver.EchoRuntimeSidecar,
		sessiondriver.StartOptions{WorkspacePlan: json.RawMessage(plan)})

	secret := credential.ManagedSecretName(tenant, "vcs", "key-1")
	tokenService := credNSLogs(t, c, "token-service")
	readLogged := false
	for _, line := range strings.Split(tokenService, "\n") {
		readLogged = readLogged || (strings.Contains(line, "credential_rpc") && strings.Contains(line, secret))
	}
	if !readLogged {
		t.Fatalf("no Token Service credential_rpc line for Secret %s; session create returned %v", secret, createErr)
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(canary))
	for component, logs := range map[string]string{"token-service": tokenService, "gateway": credNSLogs(t, c, "gateway")} {
		if strings.Contains(logs, canary) || strings.Contains(logs, encoded) {
			t.Fatalf("§16.4: the submitted value appears in %s logs", component)
		}
	}
}
