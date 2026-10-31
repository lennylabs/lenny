// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind tests for the credentials namespace on a stock install.
// Every request goes through the deployed gateway and Token Service to the
// chart-installed lenny-credentials namespace: a platform-admin registers an
// operator-created Secret by reference with no RBAC edit, a submitted value
// lands in a Lenny-written Secret that the gateway reads back through the
// Token Service keyed read, tenant deletion removes only the deleted tenant's
// written Secrets, and the operator-run listing prints only the operator
// Secret no pool references.
//
// spec: §4.9 (Credential Storage Topology), §12.8 (Compliance Interfaces),
// §14 (gitClone), §24.5.

package tier5_e2e_kind_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/tests/testinfra/kind"
	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
)

const (
	// credNSTenantPrefix prefixes every tenant these tests bootstrap, so a
	// later run deletes what a killed run left behind.
	credNSTenantPrefix = "t5-credns-"
	// credNSFixtureLabel marks the operator Secrets these tests create.
	credNSFixtureLabel = "lenny.dev/test-fixture=credentials-namespace"
	// credNSDeletionBound bounds a tenant deletion reaching its Phase 4
	// Secret delete. The runner advances one phase per 30s tick.
	credNSDeletionBound = 8 * time.Minute
)

// credNSFixture is one test's gateway driver and run-unique tenant.
type credNSFixture struct {
	d      *sessiondriver.Driver
	c      *kind.Cluster
	tenant string
}

// newCredNSFixture sweeps what an earlier run left, then bootstraps a tenant
// for the test.
func newCredNSFixture(t *testing.T) credNSFixture {
	t.Helper()
	d := sessiondriver.New(t, sessiondriver.Options{HTTPTimeout: 30 * time.Second})
	c := d.Cluster()
	kind.SweepLabeledOnce(t, c, credential.DefaultNamespace, credNSFixtureLabel, "secret")
	sweepStaleTenants(t, d, credNSTenantPrefix)
	f := credNSFixture{d: d, c: c, tenant: fmt.Sprintf("%s%d", credNSTenantPrefix, time.Now().UnixNano())}
	f.bootstrap(t, f.tenant)
	return f
}

// bootstrap creates tenant through the admin API.
func (f credNSFixture) bootstrap(t *testing.T, tenant string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := f.d.BootstrapTenant(ctx, tenant); err != nil {
		t.Fatalf("bootstrap tenant %q: %v", tenant, err)
	}
}

// operatorSecret creates a Secret in the credentials namespace the way an
// operator does, with the cluster admin kubeconfig, and removes it at the end
// of the test.
func (f credNSFixture) operatorSecret(t *testing.T, name string) {
	t.Helper()
	k, v, _ := strings.Cut(credNSFixtureLabel, "=")
	manifest := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
  labels:
    %s: %q
stringData:
  apiKey: operator-value
`, name, credential.DefaultNamespace, k, v)
	if out, err := f.c.ApplyStdin(t, manifest); err != nil {
		t.Fatalf("create operator Secret %s: %v\n%s", name, err, out)
	}
	t.Cleanup(func() {
		_, _ = f.c.KubectlOut(t, "-n", credential.DefaultNamespace, "delete", "secret", name, "--ignore-not-found", "--wait=false")
	})
}

// secretExists reports whether the Secret is in the credentials namespace.
func (f credNSFixture) secretExists(t *testing.T, name string) bool {
	t.Helper()
	out, err := f.c.KubectlOut(t, "-n", credential.DefaultNamespace, "get", "secret", name,
		"--ignore-not-found", "-o", "name")
	if err != nil {
		t.Fatalf("read Secret %s: %v\n%s", name, err, out)
	}
	return strings.TrimSpace(out) != ""
}

// createPool posts a pool for tenant as a platform-admin and returns the
// status and body.
func (f credNSFixture) createPool(t *testing.T, tenant, name, provider string, hosts []string, ref string) (int, []byte) {
	t.Helper()
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
	return adminRequest(t, f.d, "platform", http.MethodPost, "/v1/admin/credential-pools", body)
}

// addSecretData submits value under key to a new credential key-1 of pool and
// returns the name of the Lenny-written Secret.
func (f credNSFixture) addSecretData(t *testing.T, tenant, pool, key, value string) string {
	t.Helper()
	path := "/v1/admin/credential-pools/" + pool + "/credentials?tenantId=" + tenant
	code, raw := adminRequest(t, f.d, "platform", http.MethodPost, path, map[string]any{
		"id": "key-1", "secretData": map[string]string{key: value},
	})
	if code != http.StatusCreated {
		t.Fatalf("§4.9: secretData add answered %d: %s", code, raw)
	}
	if bytes.Contains(raw, []byte(value)) {
		t.Fatalf("§4.9: the secretData add response carries the submitted value: %s", raw)
	}
	name := credential.ManagedSecretName(tenant, pool, "key-1")
	if !f.secretExists(t, name) {
		t.Fatalf("§4.9: secretData add answered 201 and the Secret %s is absent", name)
	}
	return name
}

// tokenServiceReadSucceeded reports whether a Token Service replica logged a
// successful keyed read of the Secret.
func (f credNSFixture) tokenServiceReadSucceeded(t *testing.T, name string) bool {
	t.Helper()
	out, _ := f.c.KubectlOut(t, "-n", t5SystemNS, "logs", "-l", "lenny.dev/component=token-service",
		"--all-containers", "--tail=-1", "--max-log-requests=10")
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "credential_rpc") && strings.Contains(line, name) && strings.Contains(line, "success") {
			return true
		}
	}
	return false
}

// spec: §4.9 (Credential Storage Topology: Secret references, Credentials
// namespace rule 3), §13.2.4 (Token Service kube-apiserver egress)
// diagnosis: a stock install refused a platform-admin by-reference add. A 422
// for the present Secret means the Token Service lacks its namespace-wide get
// or the gateway and Token Service disagree on the credentials namespace. A
// 503 CREDENTIAL_PROBE_UNAVAILABLE means the Token Service cannot reach the
// kube-apiserver. A 201 for the absent Secret means the probe never ran.
func TestPlatformAdminRegistersOperatorSecretByReference_spec_4_9(t *testing.T) {
	f := newCredNSFixture(t)
	f.operatorSecret(t, f.tenant+"-registered")

	code, raw := f.createPool(t, f.tenant, "by-reference", "anthropic_direct", nil, f.tenant+"-registered")
	if code != http.StatusCreated {
		t.Fatalf("by-reference add answered %d: %s", code, raw)
	}
	var pool struct {
		Credentials []struct {
			SecretRef string `json:"secretRef"`
		} `json:"credentials"`
	}
	if err := json.Unmarshal(raw, &pool); err != nil {
		t.Fatalf("decode pool: %v\n%s", err, raw)
	}
	want := credential.CanonicalSecretRef(credential.DefaultNamespace, f.tenant+"-registered")
	if len(pool.Credentials) != 1 || pool.Credentials[0].SecretRef != want {
		t.Fatalf("stored credentials %+v, want one canonical ref %q", pool.Credentials, want)
	}

	code, raw = f.createPool(t, f.tenant, "absent", "anthropic_direct", nil, f.tenant+"-absent")
	if code != http.StatusUnprocessableEntity || !bytes.Contains(raw, []byte("CREDENTIAL_SECRET_RBAC_MISSING")) {
		t.Fatalf("add of an absent Secret answered %d: %s, want 422 CREDENTIAL_SECRET_RBAC_MISSING", code, raw)
	}
}

// spec: §4.9 (Credential values submitted through the admin API; Token
// Service secret resolution), §14 (gitClone auth)
// diagnosis: the session create that binds an authenticated gitClone did not
// reach a successful Token Service keyed read of the Lenny-written Secret. A
// failed secretData add means the gateway lacks its Secret writer Role. A 422
// GIT_CLONE_REF_UNRESOLVABLE means the read returned no usable credential. No
// credential_rpc success line means the gateway did not read through the
// Token Service.
func TestSecretDataCredentialReadThroughTokenServiceAtGitCloneBinding_spec_4_9(t *testing.T) {
	f := newCredNSFixture(t)
	ensureTenantAllowsSessionsWithNoEnvironment(t, f.d, f.tenant)
	const host = "git.acme.example"
	if code, raw := f.createPool(t, f.tenant, "vcs", "github", []string{host}, ""); code != http.StatusCreated {
		t.Fatalf("create github pool answered %d: %s", code, raw)
	}
	secret := f.addSecretData(t, f.tenant, "vcs", "token", "ghs_t5_"+f.tenant)

	plan := fmt.Sprintf(`{"schemaVersion":1,"sources":[{"type":"gitClone","url":"https://%s/acme/private.git",`+
		`"ref":"main","auth":{"mode":"credential-lease","leaseScope":"vcs.github.read"}}]}`, host)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	_, err := f.d.CreateSessionWithOptions(ctx, f.tenant, sessiondriver.EchoRuntimeSidecar,
		sessiondriver.StartOptions{WorkspacePlan: json.RawMessage(plan)})
	if err == nil {
		t.Fatalf("session create succeeded against the unroutable host %s", host)
	}
	if strings.Contains(err.Error(), "GIT_CLONE_REF_UNRESOLVABLE") {
		t.Fatalf("§4.9: the VCS read returned no usable credential for a written Secret: %v", err)
	}
	if !strings.Contains(err.Error(), "GIT_CLONE_REF_RESOLVE_TRANSIENT") {
		t.Fatalf("session create failed before plan binding reached the ref resolver: %v", err)
	}
	if !f.tokenServiceReadSucceeded(t, secret) {
		t.Fatalf("§4.9: no Token Service credential_rpc success line for Secret %s", secret)
	}

	t.Run("authenticated clone", func(t *testing.T) {
		t.Skip("blocked: the distroless gateway image carries no git binary, so ls-remote cannot run; " +
			"the kind install also has no HTTPS Git fixture the gateway trusts and may reach")
	})
}

// spec: §12.8 (Compliance Interfaces: Phase 4), §4.9 (Credential values
// submitted through the admin API rule 3, tenant-deletion row)
// diagnosis: a Secret of the deleted tenant that outlives the bound means the
// §12.8 runner never reached the credential step, or the gateway's
// delete-collection failed; the printed tenant state tells which. Another
// tenant's Secret or the operator Secret gone means the delete-collection
// selector reached past lenny.dev/tenant-hash.
func TestTenantDeletionRemovesOnlyThatTenantsWrittenSecrets_spec_12_8(t *testing.T) {
	f := newCredNSFixture(t)
	other := f.tenant + "-other"
	f.bootstrap(t, other)
	secrets := map[string]string{}
	for _, tenant := range []string{f.tenant, other} {
		if code, raw := f.createPool(t, tenant, "written", "anthropic_direct", nil, ""); code != http.StatusCreated {
			t.Fatalf("create pool for %s answered %d: %s", tenant, code, raw)
		}
		secrets[tenant] = f.addSecretData(t, tenant, "written", "apiKey", "sk-ant-t5-"+tenant)
	}
	operator := f.tenant + "-operator"
	f.operatorSecret(t, operator)

	if code, raw := adminRequest(t, f.d, "platform", http.MethodDelete, "/v1/admin/tenants/"+f.tenant, nil); code != http.StatusAccepted {
		t.Fatalf("DELETE tenant answered %d: %s", code, raw)
	}
	deadline := time.Now().Add(credNSDeletionBound)
	for f.secretExists(t, secrets[f.tenant]) {
		if time.Now().After(deadline) {
			code, raw := adminRequest(t, f.d, "platform", http.MethodGet, "/v1/admin/tenants/"+f.tenant, nil)
			t.Fatalf("§12.8: Secret %s outlived the %s bound; tenant reads %d: %s",
				secrets[f.tenant], credNSDeletionBound, code, raw)
		}
		t.Logf("waiting for tenant deletion to remove Secret %s", secrets[f.tenant])
		time.Sleep(10 * time.Second)
	}
	if !f.secretExists(t, secrets[other]) {
		t.Fatalf("§12.8: deleting %s removed Secret %s of tenant %s", f.tenant, secrets[other], other)
	}
	if !f.secretExists(t, operator) {
		t.Fatalf("§12.8: deleting %s removed the unlabelled operator Secret %s", f.tenant, operator)
	}
}

// listUnreferencedSecrets runs the built lenny-ctl with the kind admin
// kubeconfig and returns its stdout lines.
func listUnreferencedSecrets(t *testing.T, f credNSFixture) map[string]bool {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "lenny-ctl")
	if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/lenny-ctl").CombinedOutput(); err != nil {
		t.Fatalf("build lenny-ctl: %v\n%s", err, out)
	}
	cmd := exec.Command(bin, "--api-url", f.d.BaseURL(), "--dev-tenant", "platform", "--dev-roles", "platform-admin",
		"admin", "credential-pools", "list-unreferenced-secrets")
	cmd.Env = append(os.Environ(), "KUBECONFIG="+f.c.KubeconfigPath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			t.Fatalf("list-unreferenced-secrets exited %d: %s", exit.ExitCode(), stderr.String())
		}
		t.Fatalf("run lenny-ctl: %v", err)
	}
	printed := map[string]bool{}
	for _, line := range strings.Fields(stdout.String()) {
		printed[line] = true
	}
	return printed
}

// spec: §24.5 (list-unreferenced-secrets), §4.9 (Credentials namespace)
// diagnosis: the listing presented a Secret wrongly. A registered Secret in
// the output means the pool walk missed a reference, and a Lenny-written
// Secret in the output means the reserved prefix filter is missing, either of
// which sends an operator to delete a live credential. An unreferenced Secret
// missing from the output means the listing does not read the namespace with
// the caller's kubeconfig.
func TestListUnreferencedSecretsPrintsOnlyTheOperatorSecret_spec_24_5(t *testing.T) {
	f := newCredNSFixture(t)
	registered, unreferenced := f.tenant+"-registered", f.tenant+"-unreferenced"
	f.operatorSecret(t, registered)
	f.operatorSecret(t, unreferenced)
	if code, raw := f.createPool(t, f.tenant, "registered", "anthropic_direct", nil, registered); code != http.StatusCreated {
		t.Fatalf("create registered pool answered %d: %s", code, raw)
	}
	if code, raw := f.createPool(t, f.tenant, "written", "anthropic_direct", nil, ""); code != http.StatusCreated {
		t.Fatalf("create written pool answered %d: %s", code, raw)
	}
	written := f.addSecretData(t, f.tenant, "written", "apiKey", "sk-ant-t5-"+f.tenant)

	printed := listUnreferencedSecrets(t, f)
	if !printed[unreferenced] {
		t.Fatalf("§24.5: the listing omits the unreferenced Secret %s: %v", unreferenced, printed)
	}
	for _, name := range []string{registered, written} {
		if printed[name] {
			t.Fatalf("§24.5: the listing prints %s, which a pool references: %v", name, printed)
		}
	}
}
