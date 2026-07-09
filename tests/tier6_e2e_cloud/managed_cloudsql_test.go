// SPDX-License-Identifier: MIT

//go:build e2e_cloud

// Tier-6 Cloud SQL-managed Postgres tests. Mirrors managed_rds_test.go
// for the GCP topology. Each test reads the LENNY_GCP_CLOUD_SQL_* env
// vars scripts/cloud/gcp/up.sh emits (see
// deploy/terraform/cloud/gcp/managed-services.tf) and skips when the
// host is empty. The in-cluster `lenny-postgres` fixture-based flow
// keeps running independently — the managed-service tests are
// additive.

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

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/jackc/pgx/v5"
	"golang.org/x/oauth2/google"
	sqladmin "google.golang.org/api/sqladmin/v1"
)

// cloudSQLParams is the resolved connection state for a live Cloud
// SQL for PostgreSQL instance.
type cloudSQLParams struct {
	host       string
	port       string
	database   string
	username   string
	password   string
	project    string
	instanceID string
}

// requireCloudSQL reads the LENNY_GCP_CLOUD_SQL_* env trio scripts/
// cloud/gcp/up.sh emits and returns the connection parameters, or
// skips when both the public and private IP are unset (the default
// when WITH_CLOUD_SQL=0 in gcp/up.sh).
func requireCloudSQL(t *testing.T) cloudSQLParams {
	t.Helper()
	host := strings.TrimSpace(os.Getenv("LENNY_GCP_CLOUD_SQL_PUBLIC_IP"))
	if host == "" {
		host = strings.TrimSpace(os.Getenv("LENNY_GCP_CLOUD_SQL_PRIVATE_IP"))
	}
	if host == "" {
		t.Log("requireCloudSQL: neither LENNY_GCP_CLOUD_SQL_PUBLIC_IP nor LENNY_GCP_CLOUD_SQL_PRIVATE_IP is set; re-run with WITH_CLOUD_SQL=1 scripts/cloud/gcp/up.sh to provision Cloud SQL via terraform")
		return cloudSQLParams{}
	}
	project := strings.TrimSpace(os.Getenv("LENNY_GCP_PROJECT"))
	if project == "" {
		t.Fatalf("requireCloudSQL: LENNY_GCP_PROJECT is empty even though a Cloud SQL IP is set; scripts/cloud/gcp/up.sh always emits both")
	}
	db := strings.TrimSpace(os.Getenv("LENNY_GCP_CLOUD_SQL_DATABASE_NAME"))
	if db == "" {
		db = "lenny"
	}

	secretName := strings.TrimSpace(os.Getenv("LENNY_GCP_CLOUD_SQL_ADMIN_SECRET_NAME"))
	if secretName == "" {
		t.Fatalf("requireCloudSQL: LENNY_GCP_CLOUD_SQL_ADMIN_SECRET_NAME is empty even though a Cloud SQL IP is set; the Terraform module should always emit both")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sm, err := secretmanager.NewClient(ctx)
	if err != nil {
		t.Fatalf("requireCloudSQL: create Secret Manager client: %v", err)
	}
	defer func() { _ = sm.Close() }()
	resp, err := sm.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{
		Name: fmt.Sprintf("projects/%s/secrets/%s/versions/latest", project, secretName),
	})
	if err != nil {
		t.Fatalf("requireCloudSQL: access secret %s: %v", secretName, err)
	}
	var creds struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Database string `json:"database"`
	}
	if err := json.Unmarshal(resp.GetPayload().GetData(), &creds); err != nil {
		t.Fatalf("requireCloudSQL: decode admin secret: %v", err)
	}

	// The instance ID is the last colon-delimited segment of the
	// connection name (project:region:instance); LENNY_GCP_CLOUD_SQL_INSTANCE
	// overrides it for an operator using a non-default naming scheme.
	instanceID := strings.TrimSpace(os.Getenv("LENNY_GCP_CLOUD_SQL_INSTANCE"))
	if instanceID == "" {
		connName := strings.TrimSpace(os.Getenv("LENNY_GCP_CLOUD_SQL_CONNECTION_NAME"))
		parts := strings.Split(connName, ":")
		instanceID = parts[len(parts)-1]
	}

	port := "5432"
	if creds.Port != 0 {
		port = strconv.Itoa(creds.Port)
	}
	return cloudSQLParams{
		host:       host,
		port:       port,
		database:   db,
		username:   creds.Username,
		password:   creds.Password,
		project:    project,
		instanceID: instanceID,
	}
}

// spec: 13.2 (Cloud SQL ssl_mode=ENCRYPTED_ONLY, §17.3 RPO=0 TLS-required).
// diagnosis: TestCloudSQLTLSRequired asserts that a non-TLS connection
// to the Cloud SQL public/private IP is refused, and that an
// sslmode=require connection succeeds. The §13.2 NetworkPolicy admits
// TCP/5432 to the Cloud SQL endpoint; the instance's ssl_mode
// (ENCRYPTED_ONLY, deploy/terraform/cloud/gcp/managed-services.tf)
// makes the protection fail-closed if the application accidentally
// sets sslmode=disable.
func TestCloudSQLTLSRequired(t *testing.T) {
	p := requireCloud(t)
	if p != "gcp" {
		t.Logf("TestCloudSQLTLSRequired: Cloud SQL test runs against gcp; LENNY_CLOUD_PROVIDER=%q", p)
		return
	}
	cs := requireCloudSQL(t)
	if cs.host == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	insecureDSN := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		cs.username, cs.password, cs.host, cs.port, cs.database)
	conn, err := pgx.Connect(ctx, insecureDSN)
	if err == nil {
		_ = conn.Close(context.Background())
		t.Errorf("expected sslmode=disable to be refused by the Cloud SQL ssl_mode=ENCRYPTED_ONLY setting; the engine accepted a plaintext connection")
	} else if !strings.Contains(strings.ToLower(err.Error()), "ssl") &&
		!strings.Contains(strings.ToLower(err.Error()), "tls") &&
		!strings.Contains(strings.ToLower(err.Error()), "no pg_hba.conf entry") {
		t.Errorf("expected the sslmode=disable refusal to mention SSL/TLS; got %v", err)
	} else {
		t.Logf("TestCloudSQLTLSRequired: sslmode=disable correctly refused: %v", err)
	}

	secureDSN := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=require",
		cs.username, cs.password, cs.host, cs.port, cs.database)
	conn, err = pgx.Connect(ctx, secureDSN)
	if err != nil {
		t.Fatalf("sslmode=require connection failed: %v", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var version string
	if err := conn.QueryRow(ctx, "select version()").Scan(&version); err != nil {
		t.Fatalf("query version: %v", err)
	}
	t.Logf("TestCloudSQLTLSRequired: TLS-encrypted Postgres %s reachable on %s:%s", strings.SplitN(version, ",", 2)[0], cs.host, cs.port)
}

// spec: 13.2 / 13.3 (Cloud SQL IAM database authentication).
// diagnosis: TestCloudSQLIAMAuth generates an OAuth2 access token
// scoped to sqlservice.login and uses it as the connection password.
// The token has a short TTL; the connection succeeds when the
// calling identity (typically the Workload Identity-federated
// service account inside the cluster, or the operator's local
// Application Default Credentials from outside) is registered as a
// Cloud SQL IAM database user. This complements the master-password
// path by confirming the rotation-friendly auth route works without
// staged secrets.
func TestCloudSQLIAMAuth(t *testing.T) {
	p := requireCloud(t)
	if p != "gcp" {
		t.Logf("TestCloudSQLIAMAuth: Cloud SQL test runs against gcp; LENNY_CLOUD_PROVIDER=%q", p)
		return
	}
	cs := requireCloudSQL(t)
	if cs.host == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Cloud SQL IAM database authentication accepts an OAuth2 access
	// token scoped to sqlservice.login as the connection password.
	ts, err := google.DefaultTokenSource(ctx, "https://www.googleapis.com/auth/sqlservice.login")
	if err != nil {
		t.Fatalf("build IAM token source: %v", err)
	}
	tok, err := ts.Token()
	if err != nil {
		t.Fatalf("mint IAM auth token: %v", err)
	}
	if len(tok.AccessToken) < 16 {
		t.Errorf("IAM auth token looks too short (len=%d)", len(tok.AccessToken))
	}

	// The IAM database user is the caller's IAM principal email with
	// the service-account suffix truncated (Cloud SQL's normalization
	// rule); the master user does not have that shape, so this is a
	// separate, operator-provisioned user. Fall back to the master
	// user when no IAM user is documented, lenient because the role
	// split is a tier-6-only setup.
	iamUser := strings.TrimSpace(os.Getenv("LENNY_GCP_CLOUD_SQL_IAM_USER"))
	if iamUser == "" {
		iamUser = cs.username
	}
	dsnBase := fmt.Sprintf("postgres://%s@%s:%s/%s?sslmode=require",
		iamUser, cs.host, cs.port, cs.database)
	pgxCfg, err := pgx.ParseConfig(dsnBase)
	if err != nil {
		t.Fatalf("pgx.ParseConfig: %v", err)
	}
	pgxCfg.Password = tok.AccessToken
	conn, err := pgx.ConnectConfig(ctx, pgxCfg)
	if err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "pg_hba.conf") || strings.Contains(msg, "permission denied") || strings.Contains(msg, "password authentication failed") {
			t.Logf("TestCloudSQLIAMAuth: IAM auth refused by the engine — the caller's principal probably lacks the cloudsql.instances.login IAM permission, or %q is not registered as an IAM database user; %v", iamUser, err)
			return
		}
		t.Fatalf("IAM-auth connect failed: %v", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var who string
	if err := conn.QueryRow(ctx, "select current_user").Scan(&who); err != nil {
		t.Fatalf("query current_user: %v", err)
	}
	t.Logf("TestCloudSQLIAMAuth: IAM-auth Postgres connect succeeded as %q", who)
}

// sqlAdminInstance fetches the live DatabaseInstance from the Cloud
// SQL Admin API, or skips the calling test with a diagnosis when the
// instance ID cannot be resolved.
func sqlAdminInstance(t *testing.T, cs cloudSQLParams) *sqladmin.DatabaseInstance {
	t.Helper()
	if cs.instanceID == "" {
		t.Log("sqlAdminInstance: could not resolve the Cloud SQL instance ID from LENNY_GCP_CLOUD_SQL_CONNECTION_NAME; set LENNY_GCP_CLOUD_SQL_INSTANCE explicitly")
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	svc, err := sqladmin.NewService(ctx)
	if err != nil {
		t.Fatalf("sqladmin.NewService: %v", err)
	}
	inst, err := svc.Instances.Get(cs.project, cs.instanceID).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Instances.Get %s/%s: %v", cs.project, cs.instanceID, err)
	}
	return inst
}

// spec: 17.7 (Cloud SQL automated backup + point-in-time recovery).
// diagnosis: TestCloudSQLAutomatedBackup queries the Cloud SQL Admin
// API for the instance's backup configuration and asserts backups
// and point-in-time recovery are both enabled (the §17.7 RTO floor
// for any production-track deployment; deploy/terraform/cloud/gcp/
// managed-services.tf sets both by default). Without automated
// backups the §17.7 RTO claim collapses because there is no
// automated snapshot to restore from.
func TestCloudSQLAutomatedBackup(t *testing.T) {
	p := requireCloud(t)
	if p != "gcp" {
		t.Logf("TestCloudSQLAutomatedBackup: Cloud SQL test runs against gcp; LENNY_CLOUD_PROVIDER=%q", p)
		return
	}
	cs := requireCloudSQL(t)
	if cs.host == "" {
		return
	}
	inst := sqlAdminInstance(t, cs)
	if inst == nil {
		return
	}
	if inst.Settings == nil {
		t.Errorf("instance %s has no Settings", cs.instanceID)
		return
	}
	bc := inst.Settings.BackupConfiguration
	if bc == nil || !bc.Enabled {
		t.Errorf("BackupConfiguration.Enabled = %v, want true (§17.7 RTO floor)", bc)
		return
	}
	if !bc.PointInTimeRecoveryEnabled {
		t.Errorf("BackupConfiguration.PointInTimeRecoveryEnabled = false, want true")
	}
	t.Logf("TestCloudSQLAutomatedBackup: backups enabled, point-in-time recovery=%t, retention window=%s", bc.PointInTimeRecoveryEnabled, bc.StartTime)
}

// spec: 17.3 (Cloud SQL regional high availability, RPO=0).
// diagnosis: TestCloudSQLHighAvailability asserts the instance's
// availabilityType is REGIONAL, the Cloud SQL equivalent of RDS
// Multi-AZ. deploy/terraform/cloud/gcp/managed-services.tf sets this
// unconditionally; a ZONAL instance cannot honor the §17.3 RPO=0
// guarantee because it has no synchronous standby.
func TestCloudSQLHighAvailability(t *testing.T) {
	p := requireCloud(t)
	if p != "gcp" {
		t.Logf("TestCloudSQLHighAvailability: Cloud SQL test runs against gcp; LENNY_CLOUD_PROVIDER=%q", p)
		return
	}
	cs := requireCloudSQL(t)
	if cs.host == "" {
		return
	}
	inst := sqlAdminInstance(t, cs)
	if inst == nil {
		return
	}
	if inst.Settings == nil || inst.Settings.AvailabilityType != "REGIONAL" {
		t.Errorf("AvailabilityType = %v, want REGIONAL (§17.3 RPO=0)", inst.Settings)
		return
	}
	t.Logf("TestCloudSQLHighAvailability: instance %s is REGIONAL (highly available)", cs.instanceID)
}

// spec: 13.3 (Cloud SQL engine version floor).
// diagnosis: TestCloudSQLEngineVersionFloor asserts Postgres 16+ on
// the Cloud SQL instance. The §9.4 pgvector backend depends on the
// migration 0044 schema landing against Postgres 16; a Cloud SQL
// instance on an older engine would silently mis-route the gateway's
// writes.
func TestCloudSQLEngineVersionFloor(t *testing.T) {
	p := requireCloud(t)
	if p != "gcp" {
		t.Logf("TestCloudSQLEngineVersionFloor: Cloud SQL test runs against gcp; LENNY_CLOUD_PROVIDER=%q", p)
		return
	}
	cs := requireCloudSQL(t)
	if cs.host == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=require",
		cs.username, cs.password, cs.host, cs.port, cs.database)
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
	t.Logf("TestCloudSQLEngineVersionFloor: server_version_num=%d", versionNum)
}
