//go:build integration

// SPDX-License-Identifier: MIT

// Tier-4 integration tests for the §10.2 shared signing key across
// processes. Real cmd/lenny-gateway, cmd/lenny-token-service, and
// cmd/lenny-ops processes run over one migrated Postgres and the local KMS
// master key of the test's stack (tests/testinfra/stackkey), as the
// replicas of one install do. The tests check that a Lenny-issued token
// verifies on every gateway replica, on every Token Service replica, and
// on lenny-ops; that it survives a restart of its signer; that the
// three-rollout rotation keeps old tokens verifying until the verify key
// is removed, with the §17.6 bootstrap admin credential re-minted during
// the swap; and that lenny-ops keeps its revocation set through a
// Postgres outage. The gateways of the admin-credential cases reach an
// envtest Kubernetes API, where the §17.6 provisioner writes the
// lenny-admin-token Secret.
package tier4_integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admintoken"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/opsprocess"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
	"github.com/lennylabs/lenny/tests/testinfra/stackkey"
	"github.com/lennylabs/lenny/tests/testinfra/tcpfault"
	tokensvc "github.com/lennylabs/lenny/tests/testinfra/tokenservice"
)

// opsReadBound bounds the wait for a lenny-ops revocation set read: the
// 30 s read interval plus margin for the read itself.
const opsReadBound = 35 * time.Second

// opsStartBound bounds the wait for the first revocation set read after a
// lenny-ops process starts with Postgres reachable.
const opsStartBound = 30 * time.Second

// keySetAudiences are the audiences every gateway in these tests stamps on
// the bearers it mints, so a POST /v1/oauth/token exchange can narrow a
// bearer to either one: §13.3 refuses an exchange that broadens the
// subject's audience.
const keySetAudiences = "lenny-gateway,lenny-ops"

// adminTokenNamespace is the envtest namespace the §17.6 provisioner of
// every cluster-backed gateway writes the lenny-admin-token Secret to.
const adminTokenNamespace = "default"

// adminTokenUser is the §17.6 initial admin username.
const adminTokenUser = admintoken.DefaultUsername

// adminCluster is the envtest Kubernetes API the gateways of the
// admin-credential cases run against: the gateway builds its cluster
// client from the kubeconfig, and the §17.6 provisioner writes the
// lenny-admin-token Secret through it. The gateway builds that client
// only for pod-based sandboxes (--agent-namespace), and §13.2 requires
// such a gateway to mint presigned checkpoint capabilities, so the
// cluster comes with a MinIO artifact store the gateways sign against.
type adminCluster struct {
	kubeconfig string
	secrets    kubernetes.Interface
	artifacts  *containers.MinIO
}

// startAdminCluster starts an envtest API server for the §17.6
// provisioner and the MinIO artifact store of its gateways. It skips the
// test when the envtest binaries are absent.
func startAdminCluster(t *testing.T) *adminCluster {
	t.Helper()
	envtest.SkipUnlessAvailable(t)
	env := envtest.Start(t)
	cs, err := kubernetes.NewForConfig(env.RESTConfig())
	if err != nil {
		t.Fatalf("envtest clientset: %v", err)
	}
	return &adminCluster{
		kubeconfig: env.Kubeconfig(t),
		secrets:    cs,
		artifacts:  containers.StartMinIO(t, containers.MinIOOptions{}),
	}
}

// gatewayOptions returns the options of a gateway replica with args that
// reaches the cluster, so it provisions and rotates the §17.6 admin
// credential, and that stores artifacts in the cluster's MinIO, which
// implements the presigner §13.2 requires of a pod-based gateway.
func (c *adminCluster) gatewayOptions(binary string, args []string) gateway.Options {
	return gateway.Options{
		Binary: binary,
		Args: append(append([]string(nil), args...),
			"--agent-namespace="+adminTokenNamespace, "--admin-token-namespace="+adminTokenNamespace,
			"--minio-endpoint="+c.artifacts.Endpoint, "--minio-bucket="+c.artifacts.Bucket,
			"--minio-access-key="+c.artifacts.AccessKey, "--minio-secret-key="+c.artifacts.SecretKey,
			"--minio-use-ssl=false"),
		Env: []string{"KUBECONFIG=" + c.kubeconfig},
	}
}

// adminToken returns the bearer the lenny-admin-token Secret holds.
func (c *adminCluster) adminToken(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sec, err := c.secrets.CoreV1().Secrets(adminTokenNamespace).Get(ctx, admintoken.DefaultSecretName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("read the %s/%s Secret: %v", adminTokenNamespace, admintoken.DefaultSecretName, err)
	}
	tok := string(sec.Data[admintoken.TokenKey])
	if tok == "" {
		t.Fatalf("the %s/%s Secret carries no %q value", adminTokenNamespace, admintoken.DefaultSecretName, admintoken.TokenKey)
	}
	return tok
}

// rotateAdminToken re-mints the §17.6 admin credential through base's
// POST /v1/admin/users/lenny-admin/rotate-token, which revokes the prior
// credential, and returns the re-minted bearer from the Secret.
func (c *adminCluster) rotateAdminToken(t *testing.T, base string) string {
	t.Helper()
	code, raw := deletionAdminDo(t, base, http.MethodPost, "/v1/admin/users/"+adminTokenUser+"/rotate-token", nil)
	if code != http.StatusOK {
		t.Fatalf("rotate the admin credential on %s: status %d: %s", base, code, raw)
	}
	return c.adminToken(t)
}

// requireAdminAdmitted asserts GET /v1/admin/me admits the §17.6 admin
// credential as lenny-admin on every gateway in gws, and that lenny-ops
// accepts it as lenny-admin within opsStartBound.
func requireAdminAdmitted(t *testing.T, what, bearer string, ops *opsprocess.Process, gws ...*gateway.Process) {
	t.Helper()
	for i, gw := range gws {
		code, body := tenancyCall(t, http.MethodGet, gw.BaseURL()+"/v1/admin/me", bearer, nil, nil)
		if code != http.StatusOK || body["subject"] != adminTokenUser {
			t.Errorf("%s on gateway replica %d: status %d (%v), want 200 as %s\n%s", what, i, code, body, adminTokenUser, gw.Logs())
		}
	}
	if got := ops.WaitAccepted(t, bearer, opsStartBound); got.Subject != adminTokenUser {
		t.Errorf("%s on lenny-ops: identity.sub = %q, want %s", what, got.Subject, adminTokenUser)
	}
}

// keySetGatewayArgs returns the flags of one multi-tenant gateway replica
// over pgDSN, proxying /v1/oauth/* to tsURL when it is set, with the
// platform:jwt-* references in refs. With redisAddr set it also serves the
// dev-mode playground for acme, whose bearer revocation keys live in
// Redis. The master key is the stack's, which the harness passes.
func keySetGatewayArgs(pgDSN, redisAddr, tsURL string, refs map[string]string) []string {
	args := []string{
		"--no-environment-policy", "allow-all", "--dev-mode",
		"--postgres-dsn=" + pgDSN,
		"--postgres-billing-audit-ddl-dsn=" + pgDSN,
		"--tenancy-mode=multi", "--multi-tenant=true",
		"--kms-provider=local",
		"--bearer-expected-audiences=" + keySetAudiences,
	}
	if redisAddr != "" {
		args = append(args, "--redis-url=redis://"+redisAddr+"/0",
			"--playground-enabled", "--playground-auth-mode=dev",
			"--playground-dev-tenant-id=acme")
	}
	if tsURL != "" {
		args = append(args, "--token-service-http-url="+tsURL)
	}
	return append(args, aliasArgs(refs)...)
}

// keySetTokenServiceArgs returns the flags of one Token Service replica
// over pgDSN with the references in refs.
func keySetTokenServiceArgs(pgDSN string, refs map[string]string) []string {
	return append([]string{"--postgres-dsn=" + pgDSN, "--kms-provider=local"}, aliasArgs(refs)...)
}

// keySetOpsArgs returns the flags of an authenticating lenny-ops over
// pgDSN with the references in refs.
func keySetOpsArgs(t *testing.T, pgDSN string, refs map[string]string) []string {
	args := append([]string{"--postgres-dsn=" + pgDSN, "--kms-provider=local"}, opsprocess.AuthArgs(t)...)
	return append(args, aliasArgs(refs)...)
}

// aliasArgs renders refs as the single --kms-alias flag the binaries
// take: a comma-separated alias=reference list, since a repeated flag
// keeps only its last value.
func aliasArgs(refs map[string]string) []string {
	if len(refs) == 0 {
		return nil
	}
	pairs := make([]string, 0, len(refs))
	for alias, ref := range refs {
		pairs = append(pairs, alias+"="+ref)
	}
	sort.Strings(pairs)
	return []string{"--kms-alias=" + strings.Join(pairs, ",")}
}

// startKeySetPostgres starts a migrated Postgres.
func startKeySetPostgres(t *testing.T) *containers.Postgres {
	t.Helper()
	return containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
}

// bootstrapAcme seeds the default and acme tenants through base and
// creates alice as a user and dave as a tenant-admin. On a gateway that
// reaches a cluster, the bootstrap run also provisions the §17.6 admin
// credential, a platform-admin of the default tenant.
func bootstrapAcme(t *testing.T, base string) {
	t.Helper()
	code, raw := deletionAdminDo(t, base, http.MethodPost, "/v1/admin/bootstrap", map[string]any{
		"tenants": []map[string]any{{"id": "default"}, {"id": "acme", "displayName": "Acme Corp"}},
	})
	if code != http.StatusOK {
		t.Fatalf("bootstrap: status %d: %s", code, raw)
	}
	createTenantUser(t, base, "acme", "alice@acme.com")
	code, raw = deletionAdminDo(t, base, http.MethodPost, "/v1/admin/users", map[string]any{
		"tenantId": "acme", "subject": "dave@acme.com", "roles": []string{"tenant-admin"},
	})
	if code != http.StatusCreated {
		t.Fatalf("create acme tenant-admin dave: status %d: %s", code, raw)
	}
}

// oauthExchange posts an RFC 8693 token exchange to base's
// POST /v1/oauth/token with caller as the caller token and returns the
// status and decoded body. An empty audience requests none.
func oauthExchange(t *testing.T, base, caller, subject, audience string) (int, map[string]any) {
	t.Helper()
	form := url.Values{
		"grant_type":         {"urn:ietf:params:oauth:grant-type:token-exchange"},
		"subject_token":      {subject},
		"subject_token_type": {"urn:ietf:params:oauth:token-type:jwt"},
	}
	if audience != "" {
		form.Set("audience", audience)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new exchange request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+caller)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s/v1/oauth/token: %v", base, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

// mintExchanged runs oauthExchange and returns the access_token, failing
// the test on any other outcome.
func mintExchanged(t *testing.T, base, caller, subject, audience string) string {
	t.Helper()
	code, body := oauthExchange(t, base, caller, subject, audience)
	tok, _ := body["access_token"].(string)
	if code != http.StatusOK || tok == "" {
		t.Fatalf("exchange on %s (audience %q): status %d body %v", base, audience, code, body)
	}
	return tok
}

// requireAdmittedOnGateways asserts GET /v1/admin/me admits bearer as acme
// on every gateway in gws.
func requireAdmittedOnGateways(t *testing.T, what, bearer string, gws ...*gateway.Process) {
	t.Helper()
	for i, gw := range gws {
		code, body := tenancyCall(t, http.MethodGet, gw.BaseURL()+"/v1/admin/me", bearer, nil, nil)
		if code != http.StatusOK || body["tenantId"] != "acme" {
			t.Errorf("%s on gateway replica %d: status %d (%v), want 200 as acme\n%s", what, i, code, body, gw.Logs())
		}
	}
}

// requireUnknownKID asserts every gateway in gws refuses bearer with 401
// TOKEN_INVALID naming unknown_kid.
func requireUnknownKID(t *testing.T, what, bearer string, gws ...*gateway.Process) {
	t.Helper()
	for i, gw := range gws {
		code, body := tenancyCall(t, http.MethodGet, gw.BaseURL()+"/v1/admin/me", bearer, nil, nil)
		envelope, _ := body["error"].(map[string]any)
		msg, _ := envelope["message"].(string)
		if code != http.StatusUnauthorized || tenancyErrorCode(body) != "TOKEN_INVALID" || !strings.Contains(msg, "unknown_kid") {
			t.Errorf("%s on gateway replica %d: status %d (%v), want 401 TOKEN_INVALID unknown_kid", what, i, code, body)
		}
	}
}

// spec: 10.2 (Authentication) "Signing topology", 10.2 (Authentication) "Verifier key set and rotation", 4.3 (Token Service), 25.4 (The lenny-ops Service)
// diagnosis: a refusal on a peer replica means a Lenny-issued token still
// verifies only on the process that signed it: a gateway or Token Service
// replica does not sign or verify through the shared platform:jwt-signing
// key set. A refusal of the bootstrap admin credential means the §17.6
// admin-token mint signs with something other than the shared key set. A
// refusal on lenny-ops means lenny-ops does not load the key set or refuses
// a Lenny-issued class it must accept. A refusal after a
// restart means a process derives its signing key from something other
// than the master key and the mapped reference.
func TestLennyIssuedTokensVerifyAcrossProcesses_spec_10_2(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
	stackkey.MasterKeyFile(t)
	cluster := startAdminCluster(t)
	pg := startKeySetPostgres(t)
	rd := containers.StartRedis(t, containers.RedisOptions{})
	refs := map[string]string{kms.AliasJWTSigning: "local-key-1"}
	tsBin, gwBin := tokensvc.Build(t), gateway.Build(t)
	ts1 := tokensvc.StartOpts(t, tokensvc.Options{Binary: tsBin, Args: keySetTokenServiceArgs(pg.DSN, refs)})
	ts2 := tokensvc.StartOpts(t, tokensvc.Options{Binary: tsBin, Args: keySetTokenServiceArgs(pg.DSN, refs)})
	gwOpts := func(ts *tokensvc.Process) gateway.Options {
		return cluster.gatewayOptions(gwBin, keySetGatewayArgs(pg.DSN, rd.Addr, ts.BaseURL(), refs))
	}
	gwA := gateway.StartOpts(t, gwOpts(ts1))
	gwB := gateway.StartOpts(t, gwOpts(ts2))
	ops := opsprocess.StartWith(t, keySetOpsArgs(t, pg.DSN, refs)...)
	bootstrapAcme(t, gwA.BaseURL())
	admin := cluster.adminToken(t)

	alice1 := mintImpersonationBearer(t, gwA.BaseURL(), "acme", "alice@acme.com")
	alice2 := mintImpersonationBearer(t, gwB.BaseURL(), "acme", "alice@acme.com")
	// Step (1a) of §13.3 "Write-before-issue ordering" refuses an
	// impersonation bearer as a subject_token, so each exchange's subject is
	// a user bearer for alice signed with the stack's platform:jwt-signing
	// key; the impersonation bearers stay the callers.
	_, stackSigner := stackkey.Signer(t, stackkey.MasterKeyFile(t), refs)
	aliceSubject := stackkey.UserBearer(t, stackSigner, "acme", "alice@acme.com", auth.RoleUser)
	dave := mintImpersonationBearer(t, gwB.BaseURL(), "acme", "dave@acme.com")

	t.Run("gateway-minted bearers verify on every gateway replica", func(t *testing.T) {
		requireAdmittedOnGateways(t, "impersonation bearer minted on replica 0", alice1, gwA, gwB)
		requireAdmittedOnGateways(t, "impersonation bearer minted on replica 1", alice2, gwA, gwB)
		requireAdmittedOnGateways(t, "playground bearer minted on replica 0", mintPlaygroundBearer(t, gwA.BaseURL()), gwA, gwB)
		requireAdmittedOnGateways(t, "playground bearer minted on replica 1", mintPlaygroundBearer(t, gwB.BaseURL()), gwA, gwB)
	})

	t.Run("the bootstrap admin credential verifies on every gateway replica and on lenny-ops", func(t *testing.T) {
		requireAdminAdmitted(t, "bootstrap admin credential minted on replica 0", admin, ops, gwA, gwB)
	})

	t.Run("lenny-ops accepts a gateway-minted impersonation bearer", func(t *testing.T) {
		got := ops.WaitAccepted(t, dave, opsStartBound)
		if got.Subject != "dave@acme.com" {
			t.Errorf("lenny-ops identity.sub = %q, want dave@acme.com", got.Subject)
		}
	})

	t.Run("a POST /v1/oauth/token bearer verifies on every gateway replica", func(t *testing.T) {
		tok := mintExchanged(t, gwA.BaseURL(), alice1, aliceSubject, "lenny-gateway")
		requireAdmittedOnGateways(t, "Token Service bearer", tok, gwA, gwB)
	})

	t.Run("each Token Service replica accepts the other's subject token", func(t *testing.T) {
		fromTS1 := mintExchanged(t, ts1.BaseURL(), alice1, aliceSubject, "lenny-gateway")
		fromTS2 := mintExchanged(t, ts2.BaseURL(), alice2, aliceSubject, "lenny-gateway")
		if code, body := oauthExchange(t, ts2.BaseURL(), alice1, fromTS1, "lenny-gateway"); code != http.StatusOK {
			t.Errorf("Token Service replica 1 refused replica 0's subject token: %d %v\n%s", code, body, ts2.Logs())
		}
		if code, body := oauthExchange(t, ts1.BaseURL(), alice2, fromTS2, "lenny-gateway"); code != http.StatusOK {
			t.Errorf("Token Service replica 0 refused replica 1's subject token: %d %v\n%s", code, body, ts1.Logs())
		}
	})

	t.Run("a token survives a restart of its signer", func(t *testing.T) {
		fromTS1 := mintExchanged(t, ts1.BaseURL(), alice1, aliceSubject, "lenny-gateway")
		ts1.Stop(t)
		restartedTS := tokensvc.StartOpts(t, tokensvc.Options{Binary: tsBin, Args: keySetTokenServiceArgs(pg.DSN, refs)})
		if code, body := oauthExchange(t, restartedTS.BaseURL(), alice1, fromTS1, "lenny-gateway"); code != http.StatusOK {
			t.Errorf("restarted Token Service refused the token it signed before the restart: %d %v", code, body)
		}
		gwA.Stop(t)
		restartedGW := gateway.StartOpts(t, gwOpts(restartedTS))
		requireAdmittedOnGateways(t, "impersonation bearer the replica minted before its restart", alice1, restartedGW)
	})
}

// spec: 10.2 (Authentication) "Verifier key set and rotation"
// diagnosis: an old token refused after step 1 or step 2 means a process
// does not verify with the platform:jwt-verify key, so a rotation locks out
// every holder of an unexpired token. A new token refused after step 2
// means the swapped platform:jwt-signing reference is not the key the
// process signs with, or a peer does not hold it. An old token admitted
// after step 3 means a process keeps a key the alias map no longer names.
// The replaced admin credential admitted on lenny-ops after step 2 means
// lenny-ops does not enforce the revocation the rotate-token route
// records. The re-minted admin credential refused after step 3 means the
// step-2 re-mint did not sign with the swapped key, so removing the old
// verify key locks the operator out.
func TestSigningKeyRotationUnderLocal_spec_10_2(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
	stackkey.MasterKeyFile(t)
	cluster := startAdminCluster(t)
	pg := startKeySetPostgres(t)
	gwBin, opsBin := gateway.Build(t), opsprocess.Build(t)
	authArgs := opsprocess.AuthArgs(t)

	var gws []*gateway.Process
	var ops *opsprocess.Process
	rollout := func(refs map[string]string) {
		t.Helper()
		for _, gw := range gws {
			gw.Stop(t)
		}
		if ops != nil {
			ops.Stop(t)
		}
		gws = gws[:0]
		for range 2 {
			gws = append(gws, gateway.StartOpts(t, cluster.gatewayOptions(gwBin, keySetGatewayArgs(pg.DSN, "", "", refs))))
		}
		opsArgs := append([]string{"--postgres-dsn=" + pg.DSN, "--kms-provider=local"}, authArgs...)
		ops = opsprocess.StartOpts(t, opsprocess.Options{Binary: opsBin, Args: append(opsArgs, aliasArgs(refs)...)})
	}

	rollout(map[string]string{kms.AliasJWTSigning: "rotation-key-a"})
	bootstrapAcme(t, gws[0].BaseURL())
	old := mintImpersonationBearer(t, gws[0].BaseURL(), "acme", "dave@acme.com")
	replacedAdmin := cluster.adminToken(t)

	// Step 1: map platform:jwt-verify to the new key.
	rollout(map[string]string{kms.AliasJWTSigning: "rotation-key-a", kms.AliasJWTVerify: "rotation-key-b"})
	requireAdmittedOnGateways(t, "old token after step 1", old, gws...)
	ops.WaitAccepted(t, old, opsStartBound)

	// Step 2: swap the mappings and re-mint the admin credential, which
	// revokes the one it replaces.
	rollout(map[string]string{kms.AliasJWTSigning: "rotation-key-b", kms.AliasJWTVerify: "rotation-key-a"})
	fresh := mintImpersonationBearer(t, gws[1].BaseURL(), "acme", "dave@acme.com")
	reminted := cluster.rotateAdminToken(t, gws[1].BaseURL())
	if reminted == replacedAdmin {
		t.Fatal("rotate-token left the admin credential in the Secret unchanged")
	}
	ops.WaitRefused(t, replacedAdmin, http.StatusUnauthorized, opsprocess.CodeRevoked, opsReadBound)
	requireAdmittedOnGateways(t, "old token after step 2", old, gws...)
	requireAdmittedOnGateways(t, "new token after step 2", fresh, gws...)
	ops.WaitAccepted(t, old, opsStartBound)
	ops.WaitAccepted(t, fresh, opsStartBound)

	// Step 3: remove platform:jwt-verify.
	rollout(map[string]string{kms.AliasJWTSigning: "rotation-key-b"})
	requireUnknownKID(t, "old token after step 3", old, gws...)
	requireAdmittedOnGateways(t, "new token after step 3", fresh, gws...)
	ops.WaitAccepted(t, fresh, opsStartBound)
	requireAdminAdmitted(t, "re-minted admin credential after step 3", reminted, ops, gws...)
	if got := ops.Me(t, old); got.Status != http.StatusUnauthorized || got.Code != opsprocess.CodeInvalid || !strings.Contains(got.Body, "unknown_kid") {
		t.Errorf("lenny-ops answered the old token after step 3 with %d %s, want 401 TOKEN_INVALID unknown_kid", got.Status, got.Body)
	}
}

// spec: 25.4 (The lenny-ops Service), 25.15 (Failure Mode Analysis), 10.2 (Authentication)
// diagnosis: a /healthz other than 200 during the outage means the
// production readiness check fails on Postgres alone and empties the
// Service while the Kubernetes API is reachable. The non-revoked token
// refused while the outage runs through a failed read means lenny-ops
// refuses Lenny-issued tokens when a read fails after one succeeded; the
// revoked token admitted then means a failed read clears the revocation
// set rather than leaving it unchanged. A token
// admitted after the restart with Postgres down means lenny-ops accepts a
// Lenny-issued token before any read succeeded. Either token wrong after
// Postgres returns means the read loop does not resume.
func TestOpsRevocationSetThroughPostgresOutage_spec_25_4(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
	envtest.SkipUnlessAvailable(t)
	stackkey.MasterKeyFile(t)
	env := envtest.Start(t)
	pg := startKeySetPostgres(t)
	pgProxy, proxiedDSN := tcpfault.StartURL(t, pg.DSN)
	refs := map[string]string{kms.AliasJWTSigning: "local-key-1"}
	ts := tokensvc.StartWith(t, keySetTokenServiceArgs(pg.DSN, refs)...)
	gw := gateway.StartWith(t, keySetGatewayArgs(pg.DSN, "", ts.BaseURL(), refs)...)
	opsBin := opsprocess.Build(t)
	opsOpts := opsprocess.Options{
		Binary: opsBin,
		Args:   append(keySetOpsArgs(t, proxiedDSN, refs), "--leader-election-namespace=default"),
		Env:    []string{"KUBECONFIG=" + env.Kubeconfig(t)},
	}
	ops := opsprocess.StartOpts(t, opsOpts)
	bootstrapAcme(t, gw.BaseURL())

	daveA := mintImpersonationBearer(t, gw.BaseURL(), "acme", "dave@acme.com")
	daveB := mintImpersonationBearer(t, gw.BaseURL(), "acme", "dave@acme.com")
	// The subjects are user bearers for dave signed with the stack's key,
	// because step (1a) refuses an impersonation bearer as a subject_token.
	_, stackSigner := stackkey.Signer(t, stackkey.MasterKeyFile(t), refs)
	revoked := mintExchanged(t, gw.BaseURL(), daveA, stackkey.UserBearer(t, stackSigner, "acme", "dave@acme.com", auth.RoleUser), "lenny-ops")
	kept := mintExchanged(t, gw.BaseURL(), daveB, stackkey.UserBearer(t, stackSigner, "acme", "dave@acme.com", auth.RoleUser), "lenny-ops")
	// A self-rotation of revoked revokes it in issued_tokens (§13.3).
	mintExchanged(t, gw.BaseURL(), revoked, revoked, "lenny-ops")
	// Both tokens are POST /v1/oauth/token tokens, which carry no roles
	// claim, so lenny-ops accepts the non-revoked one through
	// authentication and the revocation set, and its role gate then
	// answers 403 (opsprocess.Outcome.Authenticated).
	ops.WaitAuthenticated(t, kept, opsStartBound)
	ops.WaitRefused(t, revoked, http.StatusUnauthorized, opsprocess.CodeRevoked, opsReadBound)

	pgProxy.Cut()
	requireHealthzOK := func(when string) {
		t.Helper()
		if code := ops.Healthz(t); code != http.StatusOK {
			t.Fatalf("lenny-ops /healthz = %d %s, want 200 while the Kubernetes API is reachable", code, when)
		}
	}
	requireHealthzOK("right after Postgres stopped")
	// The outage reaches the revocation set only at the next read, up to
	// 30 s after the cut, so the outcomes are held on every poll until
	// lenny-ops has logged a failed read and checked once more after it.
	ops.RequireThroughFailedReads(t, 1, opsReadBound, func() error {
		return checkOutageOutcomes(t, ops, kept, revoked)
	})
	requireHealthzOK("after a revocation set read failed")

	ops.Stop(t)
	ops = opsprocess.StartOpts(t, opsOpts)
	requireHealthzOK("after the restart with Postgres down")
	ops.RequireRefusedThroughout(t, kept, http.StatusServiceUnavailable, opsprocess.CodeStale, 5*time.Second)
	ops.RequireRefusedThroughout(t, revoked, http.StatusServiceUnavailable, opsprocess.CodeStale, 5*time.Second)
	requireHealthzOK("at the end of the outage")

	pgProxy.Restore(t)
	ops.WaitAuthenticated(t, kept, opsReadBound)
	if got := ops.Me(t, revoked); got.Status != http.StatusUnauthorized || got.Code != opsprocess.CodeRevoked {
		t.Errorf("revoked token after Postgres returned: %d %s, want 401 %s", got.Status, got.Body, opsprocess.CodeRevoked)
	}
}

// checkOutageOutcomes checks lenny-ops's answers for a non-revoked and a
// revoked POST /v1/oauth/token token during a Postgres outage that began
// after a successful revocation set read: the set stays as the last read
// left it, so the non-revoked token is admitted through authentication and
// the revoked one keeps the revoked-token response. It returns the first
// outcome that differs, and an error when /healthz is not 200.
//
// spec: §25.4 (The lenny-ops Service: Authentication, revocation set
// rule 5), §25.15 (Failure Mode Analysis).
func checkOutageOutcomes(t *testing.T, ops *opsprocess.Process, kept, revoked string) error {
	t.Helper()
	if got := ops.Me(t, kept); !got.Authenticated() {
		return fmt.Errorf("non-revoked token during the outage: %d %s, want it admitted through authentication", got.Status, got.Body)
	}
	if got := ops.Me(t, revoked); got.Status != http.StatusUnauthorized || got.Code != opsprocess.CodeRevoked {
		return fmt.Errorf("revoked token during the outage: %d %s, want 401 %s", got.Status, got.Body, opsprocess.CodeRevoked)
	}
	if code := ops.Healthz(t); code != http.StatusOK {
		return fmt.Errorf("lenny-ops /healthz = %d during the outage, want 200 while the Kubernetes API is reachable", code)
	}
	return nil
}
