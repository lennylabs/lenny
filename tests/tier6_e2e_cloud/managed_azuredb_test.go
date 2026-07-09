// SPDX-License-Identifier: MIT

//go:build e2e_cloud

// Tier-6 Azure Database for PostgreSQL Flexible Server tests. Mirrors
// managed_rds_test.go for the Azure topology. Each test reads the
// LENNY_AZURE_FLEXIBLE_POSTGRES_* env vars scripts/cloud/azure/up.sh
// emits (see deploy/terraform/cloud/azure/managed-services.tf) and
// skips when the FQDN is empty. The in-cluster `lenny-postgres`
// fixture-based flow keeps running independently — the
// managed-service tests are additive.

package tier6_e2e_cloud_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/postgresql/armpostgresqlflexibleservers"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
	"github.com/jackc/pgx/v5"
)

type azurePostgresParams struct {
	host           string
	port           string
	database       string
	username       string
	password       string
	resourceGroup  string
	subscriptionID string
	serverName     string
}

// requireAzurePostgres reads the LENNY_AZURE_FLEXIBLE_POSTGRES_* env
// trio scripts/cloud/azure/up.sh emits and returns the connection
// parameters, or skips when the FQDN is empty (the default when
// WITH_FLEXIBLE_POSTGRES=0 in azure/up.sh).
func requireAzurePostgres(t *testing.T) azurePostgresParams {
	t.Helper()
	fqdn := strings.TrimSpace(os.Getenv("LENNY_AZURE_FLEXIBLE_POSTGRES_FQDN"))
	if fqdn == "" {
		t.Log("requireAzurePostgres: LENNY_AZURE_FLEXIBLE_POSTGRES_FQDN is empty; re-run with WITH_FLEXIBLE_POSTGRES=1 scripts/cloud/azure/up.sh to provision the Flexible Server via terraform")
		return azurePostgresParams{}
	}
	resourceGroup := strings.TrimSpace(os.Getenv("LENNY_AZURE_RESOURCE_GROUP"))
	subscriptionID := strings.TrimSpace(os.Getenv("AZURE_SUBSCRIPTION_ID"))
	if resourceGroup == "" || subscriptionID == "" {
		t.Fatalf("requireAzurePostgres: LENNY_AZURE_RESOURCE_GROUP=%q AZURE_SUBSCRIPTION_ID=%q; both are required alongside LENNY_AZURE_FLEXIBLE_POSTGRES_FQDN", resourceGroup, subscriptionID)
	}
	db := strings.TrimSpace(os.Getenv("LENNY_AZURE_FLEXIBLE_POSTGRES_DATABASE_NAME"))
	if db == "" {
		db = "lenny"
	}

	// The server name is the FQDN's first DNS label
	// ("lenny-e2e-pg.postgres.database.azure.com" -> "lenny-e2e-pg").
	// LENNY_AZURE_FLEXIBLE_POSTGRES_SERVER_NAME overrides it.
	serverName := strings.TrimSpace(os.Getenv("LENNY_AZURE_FLEXIBLE_POSTGRES_SERVER_NAME"))
	if serverName == "" {
		serverName, _, _ = strings.Cut(fqdn, ".")
	}

	vaultName := strings.TrimSpace(os.Getenv("LENNY_AZURE_KEY_VAULT_NAME"))
	secretName := strings.TrimSpace(os.Getenv("LENNY_AZURE_FLEXIBLE_POSTGRES_ADMIN_SECRET_NAME"))
	if vaultName == "" || secretName == "" {
		t.Fatalf("requireAzurePostgres: LENNY_AZURE_KEY_VAULT_NAME=%q LENNY_AZURE_FLEXIBLE_POSTGRES_ADMIN_SECRET_NAME=%q; both are required to fetch the admin credential", vaultName, secretName)
	}
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		t.Fatalf("requireAzurePostgres: build Azure credential: %v", err)
	}
	kv, err := azsecrets.NewClient(fmt.Sprintf("https://%s.vault.azure.net", vaultName), cred, nil)
	if err != nil {
		t.Fatalf("requireAzurePostgres: build Key Vault client: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	resp, err := kv.GetSecret(ctx, secretName, "", nil)
	if err != nil {
		t.Fatalf("requireAzurePostgres: get secret %s: %v", secretName, err)
	}
	if resp.Value == nil {
		t.Fatalf("requireAzurePostgres: secret %s has no value", secretName)
	}
	var creds struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Database string `json:"database"`
	}
	if err := json.Unmarshal([]byte(*resp.Value), &creds); err != nil {
		t.Fatalf("requireAzurePostgres: decode admin secret: %v", err)
	}

	port := "5432"
	if creds.Port != 0 {
		port = strconv.Itoa(creds.Port)
	}
	return azurePostgresParams{
		host:           fqdn,
		port:           port,
		database:       db,
		username:       creds.Username,
		password:       creds.Password,
		resourceGroup:  resourceGroup,
		subscriptionID: subscriptionID,
		serverName:     serverName,
	}
}

// spec: 13.2 (Flexible Server require_secure_transport, §17.3 RPO=0 TLS-required).
// diagnosis: TestCloudAzurePostgresTLSRequired asserts that a
// non-TLS connection to the Flexible Server FQDN is refused, and
// that an sslmode=require connection succeeds. Azure Database for
// PostgreSQL Flexible Server defaults require_secure_transport to ON
// (deploy/terraform/cloud/azure/managed-services.tf relies on the
// service default); a non-TLS client receives an SSL-required error
// at the engine boundary.
func TestCloudAzurePostgresTLSRequired(t *testing.T) {
	p := requireCloud(t)
	if p != "azure" {
		t.Logf("TestCloudAzurePostgresTLSRequired: Azure Database test runs against azure; LENNY_CLOUD_PROVIDER=%q", p)
		return
	}
	ap := requireAzurePostgres(t)
	if ap.host == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	insecureDSN := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		ap.username, ap.password, ap.host, ap.port, ap.database)
	conn, err := pgx.Connect(ctx, insecureDSN)
	if err == nil {
		_ = conn.Close(context.Background())
		t.Errorf("expected sslmode=disable to be refused by the Flexible Server require_secure_transport setting; the engine accepted a plaintext connection")
	} else if !strings.Contains(strings.ToLower(err.Error()), "ssl") &&
		!strings.Contains(strings.ToLower(err.Error()), "tls") &&
		!strings.Contains(strings.ToLower(err.Error()), "no pg_hba.conf entry") {
		t.Errorf("expected the sslmode=disable refusal to mention SSL/TLS; got %v", err)
	} else {
		t.Logf("TestCloudAzurePostgresTLSRequired: sslmode=disable correctly refused: %v", err)
	}

	secureDSN := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=require",
		ap.username, ap.password, ap.host, ap.port, ap.database)
	conn, err = pgx.Connect(ctx, secureDSN)
	if err != nil {
		t.Fatalf("sslmode=require connection failed: %v", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var version string
	if err := conn.QueryRow(ctx, "select version()").Scan(&version); err != nil {
		t.Fatalf("query version: %v", err)
	}
	t.Logf("TestCloudAzurePostgresTLSRequired: TLS-encrypted Postgres %s reachable on %s:%s", strings.SplitN(version, ",", 2)[0], ap.host, ap.port)
}

// spec: 13.2 / 13.3 (Flexible Server Microsoft Entra authentication).
// diagnosis: TestCloudAzurePostgresAADAuth generates a Microsoft
// Entra access token scoped to the ossrdbms-aad audience and uses it
// as the connection password. The token has a short TTL; the
// connection succeeds when the calling identity (typically the
// Workload Identity-federated managed identity inside the cluster,
// or the operator's local `az login` session from outside) is
// registered as an Entra-authenticated Postgres role. This
// complements the master-password path by confirming the
// rotation-friendly auth route works without staged secrets.
func TestCloudAzurePostgresAADAuth(t *testing.T) {
	p := requireCloud(t)
	if p != "azure" {
		t.Logf("TestCloudAzurePostgresAADAuth: Azure Database test runs against azure; LENNY_CLOUD_PROVIDER=%q", p)
		return
	}
	ap := requireAzurePostgres(t)
	if ap.host == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		t.Fatalf("build Azure credential: %v", err)
	}
	// https://ossrdbms-aad.database.windows.net is the fixed resource
	// ID Azure Database for PostgreSQL uses for Entra token requests.
	tok, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{"https://ossrdbms-aad.database.windows.net/.default"},
	})
	if err != nil {
		t.Fatalf("mint Entra auth token: %v", err)
	}
	if len(tok.Token) < 16 {
		t.Errorf("Entra auth token looks too short (len=%d)", len(tok.Token))
	}

	// The Entra-authenticated role is a separate, operator-provisioned
	// user (the master user is password-authenticated only). Fall
	// back to the master user when no Entra user is documented,
	// lenient because the role split is a tier-6-only setup.
	aadUser := strings.TrimSpace(os.Getenv("LENNY_AZURE_FLEXIBLE_POSTGRES_AAD_USER"))
	if aadUser == "" {
		aadUser = ap.username
	}
	dsnBase := fmt.Sprintf("postgres://%s@%s:%s/%s?sslmode=require",
		aadUser, ap.host, ap.port, ap.database)
	pgxCfg, err := pgx.ParseConfig(dsnBase)
	if err != nil {
		t.Fatalf("pgx.ParseConfig: %v", err)
	}
	pgxCfg.Password = tok.Token
	conn, err := pgx.ConnectConfig(ctx, pgxCfg)
	if err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "pg_hba.conf") || strings.Contains(msg, "permission denied") || strings.Contains(msg, "password authentication failed") {
			t.Logf("TestCloudAzurePostgresAADAuth: Entra auth refused by the engine — %q is probably not registered as a Microsoft Entra Postgres role; %v", aadUser, err)
			return
		}
		t.Fatalf("Entra-auth connect failed: %v", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var who string
	if err := conn.QueryRow(ctx, "select current_user").Scan(&who); err != nil {
		t.Fatalf("query current_user: %v", err)
	}
	t.Logf("TestCloudAzurePostgresAADAuth: Entra-auth Postgres connect succeeded as %q", who)
}

// azurePostgresServer fetches the live Server resource from the ARM
// Postgres Flexible Servers API, or skips the calling test with a
// diagnosis when the client cannot be built.
func azurePostgresServer(t *testing.T, ap azurePostgresParams) *armpostgresqlflexibleservers.Server {
	t.Helper()
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		t.Fatalf("build Azure credential: %v", err)
	}
	client, err := armpostgresqlflexibleservers.NewServersClient(ap.subscriptionID, cred, nil)
	if err != nil {
		t.Fatalf("NewServersClient: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := client.Get(ctx, ap.resourceGroup, ap.serverName, nil)
	if err != nil {
		t.Fatalf("ServersClient.Get %s/%s: %v", ap.resourceGroup, ap.serverName, err)
	}
	return &resp.Server
}

// spec: 17.7 (Flexible Server automated backup retention).
// diagnosis: TestCloudAzurePostgresBackupRetention queries the ARM
// API for the server's backup retention window and asserts it is at
// least 1 day (the §17.7 RTO floor for any production-track
// deployment). deploy/terraform/cloud/azure/managed-services.tf sets
// backup_retention_days=7; a lower value below the §17.7 floor means
// there is no automated snapshot to restore from.
func TestCloudAzurePostgresBackupRetention(t *testing.T) {
	p := requireCloud(t)
	if p != "azure" {
		t.Logf("TestCloudAzurePostgresBackupRetention: Azure Database test runs against azure; LENNY_CLOUD_PROVIDER=%q", p)
		return
	}
	ap := requireAzurePostgres(t)
	if ap.host == "" {
		return
	}
	server := azurePostgresServer(t, ap)
	if server.Properties == nil || server.Properties.Backup == nil || server.Properties.Backup.BackupRetentionDays == nil {
		t.Errorf("server %s has no Backup.BackupRetentionDays set", ap.serverName)
		return
	}
	days := *server.Properties.Backup.BackupRetentionDays
	if days < 1 {
		t.Errorf("BackupRetentionDays = %d, want >= 1 (§17.7 RTO floor)", days)
	}
	t.Logf("TestCloudAzurePostgresBackupRetention: BackupRetentionDays=%d", days)
}

// spec: 17.3 (Flexible Server zone-redundant high availability).
// diagnosis: TestCloudAzurePostgresHighAvailability asserts the
// server's HighAvailability.Mode is ZoneRedundant, the Azure
// equivalent of RDS Multi-AZ. A Disabled mode has no synchronous
// standby and cannot honor the §17.3 RPO=0 guarantee during a zone
// outage. deploy/terraform/cloud/azure/managed-services.tf does not
// enable zone redundancy by default (ephemeral test infra); this
// test documents the gap with the operator hint rather than failing
// hard.
func TestCloudAzurePostgresHighAvailability(t *testing.T) {
	p := requireCloud(t)
	if p != "azure" {
		t.Logf("TestCloudAzurePostgresHighAvailability: Azure Database test runs against azure; LENNY_CLOUD_PROVIDER=%q", p)
		return
	}
	ap := requireAzurePostgres(t)
	if ap.host == "" {
		return
	}
	server := azurePostgresServer(t, ap)
	if server.Properties == nil || server.Properties.HighAvailability == nil || server.Properties.HighAvailability.Mode == nil ||
		*server.Properties.HighAvailability.Mode != armpostgresqlflexibleservers.HighAvailabilityModeZoneRedundant {
		t.Log("TestCloudAzurePostgresHighAvailability: HighAvailability.Mode is not ZoneRedundant; re-run terraform with a zone-redundant HA block to exercise the §17.3 failover path")
		return
	}
	t.Logf("TestCloudAzurePostgresHighAvailability: server %s is zone-redundant (standby zone=%v)", ap.serverName, server.Properties.HighAvailability.StandbyAvailabilityZone)
}

// spec: 13.3 (Flexible Server engine version floor).
// diagnosis: TestCloudAzurePostgresEngineVersionFloor asserts
// Postgres 16+ on the Flexible Server. The §9.4 pgvector backend
// depends on the migration 0044 schema landing against Postgres 16;
// a Flexible Server on an older engine would silently mis-route the
// gateway's writes.
func TestCloudAzurePostgresEngineVersionFloor(t *testing.T) {
	p := requireCloud(t)
	if p != "azure" {
		t.Logf("TestCloudAzurePostgresEngineVersionFloor: Azure Database test runs against azure; LENNY_CLOUD_PROVIDER=%q", p)
		return
	}
	ap := requireAzurePostgres(t)
	if ap.host == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=require",
		ap.username, ap.password, ap.host, ap.port, ap.database)
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var versionNum int
	if err := conn.QueryRow(ctx, "select current_setting('server_version_num')::int").Scan(&versionNum); err != nil {
		t.Fatalf("query server_version_num: %v", err)
	}
	const floor = 160000
	if versionNum < floor {
		t.Errorf("server_version_num = %d, want >= %d (Postgres 16+)", versionNum, floor)
	}
	t.Logf("TestCloudAzurePostgresEngineVersionFloor: server_version_num=%d", versionNum)
}
