//go:build integration

// SPDX-License-Identifier: MIT

// Tier-4 integration test: a gateway-minted bearer verifies on a peer
// replica. Two cmd/lenny-gateway processes share Postgres, the local KMS
// master key, and the platform:jwt-signing reference, as two replicas of
// one install do. A platform-admin impersonation bearer minted on one
// replica is admitted on the other. A third replica on a different master
// key holds a different key set and refuses it.
package tier4_integration_test

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// keySetReplicaArgs returns the flags of one replica over Postgres and the
// local KMS master key at keyFile.
func keySetReplicaArgs(pgDSN, keyFile string) []string {
	return []string{
		"--no-environment-policy", "allow-all", "--dev-mode",
		"--postgres-dsn=" + pgDSN,
		"--postgres-billing-audit-ddl-dsn=" + pgDSN,
		"--tenancy-mode=multi", "--multi-tenant=true",
		"--kms-provider=local",
		"--kms-master-key-file=" + keyFile,
		"--kms-alias=" + kms.AliasJWTSigning + "=local-key-1",
	}
}

// spec: 10.2 (Authentication) "Signing topology", 10.2 (Authentication) "Verifier key set and rotation", 4.3 (Token Service)
// diagnosis: a refusal on replica B means a Lenny-issued bearer still
// verifies only on the process that signed it: the gateway's bearer path
// does not verify through the shared platform:jwt-signing key set, or the
// impersonation mint does not sign through it. An admission on replica C
// means a gateway accepts a token signed by a key outside its key set.
func TestGatewayMintedBearerVerifiesOnPeerReplica_spec_10_2(t *testing.T) {
	t.Parallel()
	gateway.SkipUnlessAvailable(t)
	pg := sharedDatabase(t)
	dir := t.TempDir()
	sharedKey := filepath.Join(dir, "master.key")
	otherKey := filepath.Join(dir, "other-master.key")
	// Create the master keys before any replica starts, so concurrent
	// first use cannot write two different keys to one path.
	for _, p := range []string{sharedKey, otherKey} {
		if _, err := kms.LoadOrCreateMasterKey(p); err != nil {
			t.Fatalf("create master key %s: %v", p, err)
		}
	}
	gwA := gateway.StartWith(t, keySetReplicaArgs(pg.DSN, sharedKey)...)
	gwB := gateway.StartWith(t, keySetReplicaArgs(pg.DSN, sharedKey)...)
	gwC := gateway.StartWith(t, keySetReplicaArgs(pg.DSN, otherKey)...)

	code, raw := deletionAdminDo(t, gwA.BaseURL(), http.MethodPost, "/v1/admin/bootstrap", map[string]any{
		"tenants": []map[string]any{{"id": "acme", "displayName": "Acme Corp"}},
	})
	if code != http.StatusOK {
		t.Fatalf("bootstrap: status %d: %s\n%s", code, raw, gwA.Logs())
	}
	createTenantUser(t, gwA.BaseURL(), "acme", "alice@acme.com")
	bearer := mintImpersonationBearer(t, gwA.BaseURL(), "acme", "alice@acme.com")

	for _, r := range []struct {
		name string
		gw   *gateway.Process
	}{{"minting replica A", gwA}, {"peer replica B", gwB}} {
		code, body := tenancyCall(t, http.MethodGet, r.gw.BaseURL()+"/v1/admin/me", bearer, nil, nil)
		if code != http.StatusOK || body["tenantId"] != "acme" {
			t.Errorf("%s: status %d (%v), want 200 as acme\n%s", r.name, code, body, r.gw.Logs())
		}
	}
	if code, body := tenancyCall(t, http.MethodGet, gwC.BaseURL()+"/v1/admin/me", bearer, nil, nil); code != http.StatusUnauthorized {
		t.Errorf("replica C on another master key: status %d (%v), want 401", code, body)
	}
}
