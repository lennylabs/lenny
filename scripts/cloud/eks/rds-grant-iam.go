// SPDX-License-Identifier: MIT
//
// rds-grant-iam: one-shot setup for the RDS IAM auth tier-6 tests.
// Connects to the RDS Postgres master via password auth, then:
//
//   1. REVOKE rds_iam FROM lenny (so the master keeps password auth).
//   2. CREATE USER lenny_iam if missing.
//   3. GRANT rds_iam TO lenny_iam (enables IAM auth for that user).
//
// Idempotent. Falls back to IAM-auth when the master's password auth
// is already disabled (a leftover from a previous run that granted
// rds_iam to the master directly).
//
// Invoked by scripts/cloud/eks/run-e2e.sh step 1b after the
// Terraform-provisioned RDS instance becomes available.

//go:build ignore

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/rds/auth"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/jackc/pgx/v5"
)

func main() {
	endpoint := flag.String("endpoint", "", "RDS endpoint <host>:<port>")
	secretArn := flag.String("secret-arn", "", "Secrets Manager ARN holding {username, password}")
	database := flag.String("database", "lenny", "Database to connect to")
	region := flag.String("region", "us-west-2", "AWS region")
	flag.Parse()
	if *endpoint == "" || *secretArn == "" {
		fmt.Fprintln(os.Stderr, "rds-grant-iam: --endpoint and --secret-arn are required")
		os.Exit(2)
	}

	ctx := context.Background()
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(*region))
	if err != nil {
		fmt.Fprintf(os.Stderr, "load AWS config: %v\n", err)
		os.Exit(1)
	}

	sm := secretsmanager.NewFromConfig(cfg)
	out, err := sm.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: secretArn})
	if err != nil {
		fmt.Fprintf(os.Stderr, "fetch master secret: %v\n", err)
		os.Exit(1)
	}
	var creds struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal([]byte(*out.SecretString), &creds); err != nil {
		fmt.Fprintf(os.Stderr, "decode master secret: %v\n", err)
		os.Exit(1)
	}

	conn, err := pgx.Connect(ctx, fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=require",
		creds.Username, creds.Password, *endpoint, *database))
	if err != nil {
		// Password auth might already be disabled if a prior run
		// granted rds_iam to the master. Try IAM auth as a fallback.
		token, terr := auth.BuildAuthToken(ctx, *endpoint, *region, creds.Username, cfg.Credentials)
		if terr != nil {
			fmt.Fprintf(os.Stderr, "password connect failed (%v) and IAM token build failed (%v)\n", err, terr)
			os.Exit(1)
		}
		pgxCfg, perr := pgx.ParseConfig(fmt.Sprintf("postgres://%s@%s/%s?sslmode=require",
			creds.Username, *endpoint, *database))
		if perr != nil {
			fmt.Fprintf(os.Stderr, "pgx.ParseConfig: %v\n", perr)
			os.Exit(1)
		}
		pgxCfg.Password = token
		conn, err = pgx.ConnectConfig(ctx, pgxCfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "IAM-auth connect failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("rds-grant-iam: connected via IAM auth (password fallback after prior rds_iam grant)")
	} else {
		fmt.Println("rds-grant-iam: connected via password")
	}
	defer conn.Close(ctx)

	for _, stmt := range []string{
		"REVOKE rds_iam FROM lenny",
		"DROP USER IF EXISTS lenny_iam",
		"CREATE USER lenny_iam WITH LOGIN",
		"GRANT rds_iam TO lenny_iam",
		"GRANT CONNECT ON DATABASE lenny TO lenny_iam",
		"GRANT USAGE ON SCHEMA public TO lenny_iam",
	} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			lc := strings.ToLower(err.Error())
			if strings.Contains(lc, "is not a member") || strings.Contains(lc, "does not exist") {
				fmt.Printf("rds-grant-iam: %s (benign: %v)\n", stmt, err)
				continue
			}
			fmt.Fprintf(os.Stderr, "rds-grant-iam: %s failed: %v\n", stmt, err)
			os.Exit(1)
		}
		fmt.Printf("rds-grant-iam: %s\n", stmt)
	}
}
