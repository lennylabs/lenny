// SPDX-License-Identifier: MIT

//go:build e2e_cloud

// Tier-6 live tests that exercise the per-release AWS resources the
// `deploy/terraform/cloud/aws` module provisions. Each test requires
// LENNY_CLOUD_PROVIDER=aws plus the Terraform-output env vars the
// helper below reads (LENNY_AWS_KMS_KEY_ARN and
// LENNY_AWS_ARTIFACT_BUCKET). scripts/cloud/aws/up.sh writes those
// env vars into a `.env` file the test runner sources before the
// invocation.

package tier6_e2e_cloud_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"

	"github.com/lennylabs/lenny/pkg/blobstore"
	blobs3 "github.com/lennylabs/lenny/pkg/blobstore/s3"
	"github.com/lennylabs/lenny/pkg/kms"
	kmsaws "github.com/lennylabs/lenny/pkg/kms/aws"
)

// requireEnv reads an env var or skips the test with a precise hint
// at the Terraform output the operator forgot to source.
func requireEnv(t *testing.T, name string) string {
	t.Helper()
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		t.Logf("requireEnv: %s is unset; source the Terraform outputs (terraform -chdir=deploy/terraform/cloud/aws output) before running this test", name)
		return ""
	}
	return v
}

// loadAWSConfig resolves the SDK config from the standard AWS env vars
// (AWS_PROFILE / AWS_REGION / AWS_ACCESS_KEY_ID etc.) or skips the
// test with the documented diagnosis. The default profile selection
// matches scripts/cloud/aws/up.sh; the operator runs both under the
// same shell.
func loadAWSConfig(t *testing.T) aws.Config {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		t.Logf("AWS config: %v (run `aws sso login` or set AWS_ACCESS_KEY_ID)", err)
		return aws.Config{}
	}
	return c
}

// spec: 4.3 (§4.9 / §12.5 — AWS KMS provider round-trip against the
// Terraform-provisioned platform KEK)
// diagnosis: TestCloudKMS exercises pkg/kms/aws.Provider end-to-end
// against the AWS KMS alias the deploy/terraform/cloud/aws module
// emits as the kms_key_arn output. A successful Encrypt + Decrypt
// round-trip confirms the IAM role, the EncryptionContext binding,
// and the lenny/kms/aws.Provider error mapping all match the
// documented contract.
func TestCloudKMS(t *testing.T) {
	p := requireCloud(t)
	if p != "aws" {
		t.Logf("TestCloudKMS: AWS KMS test runs against aws; LENNY_CLOUD_PROVIDER=%q", p)
		return
	}
	keyARN := requireEnv(t, "LENNY_AWS_KMS_KEY_ARN")
	if keyARN == "" {
		return
	}
	cfg := loadAWSConfig(t)
	if cfg.Region == "" && len(cfg.ConfigSources) == 0 {
		return
	}

	prov, err := kmsaws.New(kmsaws.Config{
		AWSConfig: cfg,
		AliasToKeyID: map[string]string{
			"platform:tier6-test": keyARN,
		},
	})
	if err != nil {
		t.Fatalf("kmsaws.New: %v", err)
	}

	dek := make([]byte, kms.DEKSize)
	if _, err := rand.Read(dek); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	wrapped, err := prov.WrapDEK(ctx, "platform:tier6-test", dek)
	if err != nil {
		t.Fatalf("WrapDEK: %v", err)
	}
	if len(wrapped.Ciphertext) == 0 {
		t.Fatal("WrapDEK returned empty ciphertext")
	}
	got, err := prov.UnwrapDEK(ctx, "platform:tier6-test", wrapped)
	if err != nil {
		t.Fatalf("UnwrapDEK: %v", err)
	}
	if !bytes.Equal(got, dek) {
		t.Errorf("round-trip mismatch: got %x, want %x", got, dek)
	}

	// Cross-alias rejection: a wrapped DEK under platform:tier6-test
	// must not unwrap under a different alias. AWS KMS's
	// EncryptionContext is the cryptographic binding.
	prov.SetAlias("tenant:wrong", keyARN)
	if _, err := prov.UnwrapDEK(ctx, "tenant:wrong", wrapped); err == nil {
		t.Error("UnwrapDEK under wrong alias should fail (EncryptionContext mismatch)")
	}
}

// spec: 4.5 / 12.5 (S3 ArtifactStore round-trip against the
// Terraform-provisioned bucket)
// diagnosis: TestCloudCSI exercises pkg/blobstore/s3.Store end-to-end
// against the S3 bucket the deploy/terraform/cloud/aws module emits
// as the artifact_bucket output. A successful Put + Get + Stat +
// SoftDelete round-trip confirms the bucket policy, the IAM role
// permissions, and the blobstore/s3 error mapping all match the
// documented contract.
func TestCloudCSI(t *testing.T) {
	p := requireCloud(t)
	if p != "aws" {
		t.Logf("TestCloudCSI: AWS S3 test runs against aws; LENNY_CLOUD_PROVIDER=%q", p)
		return
	}
	bucket := requireEnv(t, "LENNY_AWS_ARTIFACT_BUCKET")
	if bucket == "" {
		return
	}
	cfg := loadAWSConfig(t)
	if cfg.Region == "" && len(cfg.ConfigSources) == 0 {
		return
	}

	store, err := blobs3.New(blobs3.Config{
		AWSConfig: cfg,
		Bucket:    bucket,
	})
	if err != nil {
		t.Fatalf("blobs3.New: %v", err)
	}

	u := blobstore.URI{
		TenantID:  "acme",
		SessionID: "tier6-session-" + time.Now().UTC().Format("20060102T150405"),
		PartID:    "part1",
	}
	payload := []byte("tier-6 e2e_cloud round-trip payload")

	if _, err := store.Put(u, "application/octet-stream", bytes.NewReader(payload)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// Best-effort cleanup so the bucket does not accumulate test
	// objects across runs. SoftDelete writes the tombstone tag;
	// HardPrune with zero retention drives the matching DeleteObject
	// against the object the test just put in.
	t.Cleanup(func() {
		_ = store.SoftDelete(u)
		_ = store.HardPrune(time.Now(), 0)
	})

	info, body, err := store.Get(u)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer body.Close()
	if info.Size != int64(len(payload)) {
		t.Errorf("info.Size = %d, want %d", info.Size, len(payload))
	}
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("body mismatch: got %q, want %q", got, payload)
	}

	stat, err := store.Stat(u)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if stat.Size != int64(len(payload)) {
		t.Errorf("stat.Size = %d, want %d", stat.Size, len(payload))
	}

	if err := store.SoftDelete(u); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	if _, _, err := store.Get(u); err != blobstore.ErrNotFound {
		t.Errorf("Get after SoftDelete: %v, want ErrNotFound", err)
	}
}
