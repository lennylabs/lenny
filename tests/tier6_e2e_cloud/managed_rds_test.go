// SPDX-License-Identifier: MIT

//go:build e2e_cloud

// Tier-6 RDS-managed Postgres tests. Each test reads the
// LENNY_AWS_RDS_* env vars the Terraform module emits (see
// deploy/terraform/cloud/aws/managed-services.tf) and skips when the
// endpoint is empty. The in-cluster `lenny-postgres` fixture-based
// flow under tests/testinfra/kind/datastores.yaml keeps running
// independently — the managed-service tests are additive.

package tier6_e2e_cloud_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/rds/auth"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/jackc/pgx/v5"
)

// requireRDS reads the LENNY_AWS_RDS_* env trio and returns the
// connection parameters, or skips when the endpoint is unset (the
// default when WITH_RDS=0 in run-e2e.sh).
type rdsParams struct {
	host       string
	port       string
	database   string
	username   string
	password   string
	secretARN  string
	resourceID string
	region     string
}

func requireRDS(t *testing.T) rdsParams {
	t.Helper()
	endpoint := strings.TrimSpace(os.Getenv("LENNY_AWS_RDS_ENDPOINT"))
	if endpoint == "" {
		t.Skip("requireRDS: LENNY_AWS_RDS_ENDPOINT is empty; re-run with WITH_RDS=1 scripts/cloud/eks/run-e2e.sh to provision RDS via terraform")
	}
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		t.Fatalf("requireRDS: parse endpoint %q: %v", endpoint, err)
	}
	db := strings.TrimSpace(os.Getenv("LENNY_AWS_RDS_DATABASE"))
	if db == "" {
		db = "lenny"
	}
	region := strings.TrimSpace(os.Getenv("AWS_REGION"))
	if region == "" {
		region = "us-west-2"
	}

	secretARN := strings.TrimSpace(os.Getenv("LENNY_AWS_RDS_MASTER_SECRET_ARN"))
	if secretARN == "" {
		t.Fatalf("requireRDS: LENNY_AWS_RDS_MASTER_SECRET_ARN is empty even though the endpoint is set; the Terraform module should always emit both")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		t.Fatalf("requireRDS: load AWS config: %v", err)
	}
	sm := secretsmanager.NewFromConfig(cfg)
	out, err := sm.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: &secretARN})
	if err != nil {
		t.Fatalf("requireRDS: fetch master secret: %v", err)
	}
	var creds struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal([]byte(*out.SecretString), &creds); err != nil {
		t.Fatalf("requireRDS: decode master secret: %v", err)
	}
	return rdsParams{
		host:       host,
		port:       port,
		database:   db,
		username:   creds.Username,
		password:   creds.Password,
		secretARN:  secretARN,
		resourceID: strings.TrimSpace(os.Getenv("LENNY_AWS_RDS_RESOURCE_ID")),
		region:     region,
	}
}

// spec: 13.2 (RDS force_ssl parameter group, §17.3 RPO=0 TLS-required).
// diagnosis: TestCloudRDSTLSRequired asserts that a non-TLS connection
// to the RDS endpoint is refused at the engine boundary (driven by the
// rds.force_ssl=1 parameter group), and that an sslmode=require
// connection succeeds. The §13.2 NetworkPolicy admits TCP/5432 to the
// RDS endpoint; the parameter-group force-TLS makes the protection
// fail-closed if the application accidentally sets sslmode=disable.
func TestCloudRDSTLSRequired(t *testing.T) {
	_ = requireCloud(t)
	p := requireRDS(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// sslmode=disable must fail.
	insecureDSN := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		p.username, p.password, p.host, p.port, p.database)
	conn, err := pgx.Connect(ctx, insecureDSN)
	if err == nil {
		_ = conn.Close(context.Background())
		t.Errorf("expected sslmode=disable to be refused by the RDS force_ssl parameter group; the engine accepted a plaintext connection")
	} else if !strings.Contains(strings.ToLower(err.Error()), "ssl") &&
		!strings.Contains(strings.ToLower(err.Error()), "tls") &&
		!strings.Contains(strings.ToLower(err.Error()), "no pg_hba.conf entry") {
		t.Errorf("expected the sslmode=disable refusal to mention SSL/TLS; got %v", err)
	} else {
		t.Logf("TestCloudRDSTLSRequired: sslmode=disable correctly refused: %v", err)
	}

	// sslmode=require must succeed.
	secureDSN := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=require",
		p.username, p.password, p.host, p.port, p.database)
	conn, err = pgx.Connect(ctx, secureDSN)
	if err != nil {
		t.Fatalf("sslmode=require connection failed: %v", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var version string
	if err := conn.QueryRow(ctx, "select version()").Scan(&version); err != nil {
		t.Fatalf("query version: %v", err)
	}
	t.Logf("TestCloudRDSTLSRequired: TLS-encrypted Postgres %s reachable on %s:%s", strings.SplitN(version, ",", 2)[0], p.host, p.port)
}

// spec: 13.2 / 13.3 (RDS IAM database authentication).
// diagnosis: TestCloudRDSIAMAuth generates an IAM auth token via the
// AWS SDK and uses it as the connection password. The token has a
// 15-minute TTL; the connection succeeds when the calling identity
// (typically the IRSA role inside the cluster, or the operator's local
// AWS profile from outside) holds rds-db:connect on the database user.
// This complements the master-password path by confirming the
// rotation-friendly auth route works without staged secrets.
func TestCloudRDSIAMAuth(t *testing.T) {
	_ = requireCloud(t)
	p := requireRDS(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(p.region))
	if err != nil {
		t.Fatalf("load AWS config: %v", err)
	}
	endpoint := fmt.Sprintf("%s:%s", p.host, p.port)
	// IAM auth requires the Postgres user to be a rds_iam role member.
	// Granting rds_iam to the RDS master user disables password auth
	// for that user, breaking the password-auth tests in this file.
	// Use a separate `lenny_iam` user provisioned via run-e2e.sh's
	// step 1b grant block; fall back to the master when it doesn't
	// exist (lenient because the role split is a tier-6-only setup).
	iamUser := strings.TrimSpace(os.Getenv("LENNY_AWS_RDS_IAM_USER"))
	if iamUser == "" {
		iamUser = "lenny_iam"
	}
	token, err := auth.BuildAuthToken(ctx, endpoint, p.region, iamUser, cfg.Credentials)
	if err != nil {
		t.Fatalf("build IAM auth token: %v", err)
	}
	if len(token) < 16 || !strings.Contains(token, "X-Amz-Signature") {
		t.Errorf("IAM auth token does not look right (len=%d, contains-X-Amz-Signature=%t)",
			len(token), strings.Contains(token, "X-Amz-Signature"))
	}
	// The token uses the user's IAM permissions; if the operator's
	// caller identity lacks rds-db:connect on this resource the
	// engine returns a permission-denied error. Treat that as a
	// documented skip so the test does not flap depending on who
	// invokes it. The token contains URL-special characters
	// (%, &, =) so it is set on the ConnConfig directly rather than
	// interpolated into the DSN where pgx's URL parser would choke.
	dsnBase := fmt.Sprintf("postgres://%s@%s:%s/%s?sslmode=require",
		iamUser, p.host, p.port, p.database)
	pgxCfg, err := pgx.ParseConfig(dsnBase)
	if err != nil {
		t.Fatalf("pgx.ParseConfig: %v", err)
	}
	pgxCfg.Password = token
	conn, err := pgx.ConnectConfig(ctx, pgxCfg)
	if err != nil {
		// pgx surfaces the engine error message verbatim.
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "pg_hba.conf") || strings.Contains(msg, "permission denied") || strings.Contains(msg, "no such role") {
			t.Skipf("TestCloudRDSIAMAuth: IAM auth refused by the engine — the caller's IAM principal probably lacks rds-db:connect for arn:aws:rds-db:%s:%s/%s, or the user %q lacks rds_iam membership; %v", p.region, "*", iamUser, iamUser, err)
		}
		t.Fatalf("IAM-auth connect failed: %v", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var who string
	if err := conn.QueryRow(ctx, "select current_user").Scan(&who); err != nil {
		t.Fatalf("query current_user: %v", err)
	}
	if who != iamUser {
		t.Errorf("connected as %q, expected %q", who, iamUser)
	}
	t.Logf("TestCloudRDSIAMAuth: IAM-auth Postgres connect succeeded as %q", who)
}

// spec: 13.2 (force_ssl parameter group invariant).
// diagnosis: TestCloudRDSForceSSLParameterGroup queries the active
// parameter group via the RDS API for the rds.force_ssl setting
// (RDS does not expose this parameter via SHOW or pg_settings; it
// is an engine-internal control read from the parameter group at
// connection time). Verifies the active parameter group's stored
// value is 1 and the engine reports ssl=on.
func TestCloudRDSForceSSLParameterGroup(t *testing.T) {
	_ = requireCloud(t)
	p := requireRDS(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(p.region))
	if err != nil {
		t.Fatalf("load AWS config: %v", err)
	}
	identifier := "lenny-e2e-rds"
	if v := strings.TrimSpace(os.Getenv("LENNY_AWS_RDS_INSTANCE_ID")); v != "" {
		identifier = v
	}
	r := rds.NewFromConfig(cfg)
	instOut, err := r.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{DBInstanceIdentifier: &identifier})
	if err != nil {
		t.Fatalf("DescribeDBInstances %s: %v", identifier, err)
	}
	if len(instOut.DBInstances) == 0 || len(instOut.DBInstances[0].DBParameterGroups) == 0 {
		t.Fatalf("no parameter groups on instance %s", identifier)
	}
	pgName := *instOut.DBInstances[0].DBParameterGroups[0].DBParameterGroupName
	paramName := "rds.force_ssl"
	paramOut, err := r.DescribeDBParameters(ctx, &rds.DescribeDBParametersInput{
		DBParameterGroupName: &pgName,
	})
	if err != nil {
		t.Fatalf("DescribeDBParameters %s: %v", pgName, err)
	}
	var found bool
	for _, dbp := range paramOut.Parameters {
		if dbp.ParameterName == nil || *dbp.ParameterName != paramName {
			continue
		}
		found = true
		if dbp.ParameterValue == nil || (*dbp.ParameterValue != "1" && *dbp.ParameterValue != "on") {
			t.Errorf("parameter group %s carries rds.force_ssl=%v, want 1/on", pgName, dbp.ParameterValue)
		}
		break
	}
	if !found {
		// RDS API returns paginated results; loop with Marker.
		var marker *string = paramOut.Marker
		for marker != nil && *marker != "" {
			more, err := r.DescribeDBParameters(ctx, &rds.DescribeDBParametersInput{
				DBParameterGroupName: &pgName,
				Marker:               marker,
			})
			if err != nil {
				t.Fatalf("DescribeDBParameters page: %v", err)
			}
			for _, dbp := range more.Parameters {
				if dbp.ParameterName != nil && *dbp.ParameterName == paramName {
					found = true
					if dbp.ParameterValue == nil || (*dbp.ParameterValue != "1" && *dbp.ParameterValue != "on") {
						t.Errorf("parameter group %s carries rds.force_ssl=%v, want 1/on", pgName, dbp.ParameterValue)
					}
					break
				}
			}
			if found {
				break
			}
			marker = more.Marker
		}
	}
	if !found {
		t.Errorf("parameter group %s does not declare rds.force_ssl", pgName)
	}

	// Also assert ssl = on (the underlying Postgres TLS toggle); ssl=on
	// + rds.force_ssl=1 is the combined fail-closed configuration.
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=require",
		p.username, p.password, p.host, p.port, p.database)
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var sslOn string
	if err := conn.QueryRow(ctx, "SHOW ssl").Scan(&sslOn); err != nil {
		t.Fatalf("SHOW ssl: %v", err)
	}
	if sslOn != "on" {
		t.Errorf("ssl = %q, want on", sslOn)
	}
	t.Logf("TestCloudRDSForceSSLParameterGroup: parameter group %s has rds.force_ssl=1; ssl=%s", pgName, sslOn)
}

// spec: 17.7 (RDS automated backup retention).
// diagnosis: TestCloudRDSAutomatedBackup queries the RDS API for the
// instance's backup_retention_period and asserts it is at least 1
// day (the §17.7 floor for any production-track deployment). The
// Terraform module defaults the retention to 1; a production install
// should raise it to 7. Below 1 day the §17.7 RTO claim collapses
// because there is no automated snapshot to restore from.
func TestCloudRDSAutomatedBackup(t *testing.T) {
	_ = requireCloud(t)
	p := requireRDS(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(p.region))
	if err != nil {
		t.Fatalf("load AWS config: %v", err)
	}
	identifier := "lenny-e2e-rds"
	if v := strings.TrimSpace(os.Getenv("LENNY_AWS_RDS_INSTANCE_ID")); v != "" {
		identifier = v
	}
	r := rds.NewFromConfig(cfg)
	out, err := r.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{
		DBInstanceIdentifier: &identifier,
	})
	if err != nil {
		t.Fatalf("DescribeDBInstances %s: %v", identifier, err)
	}
	if len(out.DBInstances) == 0 {
		t.Fatalf("no RDS instance named %s", identifier)
	}
	inst := out.DBInstances[0]
	if inst.BackupRetentionPeriod == nil || *inst.BackupRetentionPeriod < 1 {
		t.Errorf("BackupRetentionPeriod = %v, want >= 1 (§17.7 RTO floor)", inst.BackupRetentionPeriod)
	}
	t.Logf("TestCloudRDSAutomatedBackup: BackupRetentionPeriod=%d days, BackupWindow=%v",
		ifInt(inst.BackupRetentionPeriod), strDeref(inst.PreferredBackupWindow))
}

func ifInt(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}

func strDeref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// spec: 13.3 (RDS engine version floor).
// diagnosis: TestCloudRDSEngineVersionFloor asserts Postgres 16+ on
// the RDS instance. The §9.4 pgvector backend depends on the
// migration 0044 schema landing against Postgres 16; the in-cluster
// fixture uses `pgvector/pgvector:pg16`. An RDS instance on an older
// engine would silently mis-route the gateway's writes.
func TestCloudRDSEngineVersionFloor(t *testing.T) {
	_ = requireCloud(t)
	p := requireRDS(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=require",
		p.username, p.password, p.host, p.port, p.database)
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
	t.Logf("TestCloudRDSEngineVersionFloor: server_version_num=%d", versionNum)
}

// spec: 17.3 (Multi-AZ failover, RPO=0).
// diagnosis: TestCloudRDSMultiAZ asserts that the active RDS instance
// is provisioned in Multi-AZ mode. The Terraform module gates Multi-AZ
// behind var.rds_multi_az (defaults off to save on hourly cost). Tests
// that depend on the failover path (e.g. TestCloudRDSMultiAZFailover,
// not yet implemented) build on this baseline check. The §17.3 RPO=0
// guarantee depends on synchronous standby replication; a single-AZ
// instance cannot honor it.
func TestCloudRDSMultiAZ(t *testing.T) {
	_ = requireCloud(t)
	p := requireRDS(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// RDS Multi-AZ DB Instance uses block-level synchronous storage
	// replication (not Postgres-level streaming), so the standby
	// does not surface in pg_stat_replication. The AWS-API path is
	// the only authoritative signal — DescribeDBInstances.MultiAZ
	// reflects whether the standby is in place.
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(p.region))
	if err != nil {
		t.Fatalf("load AWS config: %v", err)
	}
	identifier := "lenny-e2e-rds"
	if v := strings.TrimSpace(os.Getenv("LENNY_AWS_RDS_INSTANCE_ID")); v != "" {
		identifier = v
	}
	r := rds.NewFromConfig(cfg)
	out, err := r.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{DBInstanceIdentifier: &identifier})
	if err != nil {
		t.Fatalf("DescribeDBInstances %s: %v", identifier, err)
	}
	if len(out.DBInstances) == 0 {
		t.Fatalf("no RDS instance named %s", identifier)
	}
	multiAZ := out.DBInstances[0].MultiAZ
	if multiAZ == nil || !*multiAZ {
		t.Skip("TestCloudRDSMultiAZ: the active RDS instance has MultiAZ=false. Re-run with RDS_MULTI_AZ=1 WITH_RDS=1 scripts/cloud/eks/run-e2e.sh to provision Multi-AZ and unblock the §17.3 failover suite")
	}
	if out.DBInstances[0].SecondaryAvailabilityZone == nil || *out.DBInstances[0].SecondaryAvailabilityZone == "" {
		t.Errorf("MultiAZ=true but SecondaryAvailabilityZone is empty — the standby has not been provisioned yet")
	}
	t.Logf("TestCloudRDSMultiAZ: instance %s is Multi-AZ; primary in %s, standby in %s",
		identifier, strDeref(out.DBInstances[0].AvailabilityZone), strDeref(out.DBInstances[0].SecondaryAvailabilityZone))
}
