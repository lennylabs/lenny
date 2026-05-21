// SPDX-License-Identifier: MIT

// Package cloud is the §12.6 cloud-provider bring-up harness. It
// dispatches per-provider helpers and exposes a uniform
// SkipUnlessAvailable + Up / Down lifecycle to tier-6 tests.
//
// The package does not embed Terraform; it shells out to per-
// provider scripts under scripts/cloud/<provider>/{up,down}.sh.
// The operator provides only the cloud-auth env vars (AWS_PROFILE
// or AWS_ACCESS_KEY_ID/SECRET; gcloud auth login; az login). The
// scripts/cloud/<provider>/up.sh script is responsible for
// provisioning Lenny-side resources (KMS key, object-storage
// bucket, managed identity binding) and emitting the per-resource
// env vars the tests read.
//
// # Environment variables
//
//	LENNY_CLOUD_PROVIDERS  Comma-separated list of providers the
//	                       tier-6 suite must validate, e.g.
//	                       `aws`, `aws,gcp`, `aws,gcp,azure`.
//	                       Each provider in the list spawns a
//	                       subtest. A provider listed but not
//	                       authenticated (no AWS_PROFILE etc.) is a
//	                       test failure — the operator named it, so
//	                       the test expects it to work. Unset or
//	                       empty → tier-6 fails with the missing-
//	                       provider diagnosis.
//	LENNY_CLOUD_PROVIDER   (Deprecated, single-provider alias).
//	                       When set, the helper treats it as a
//	                       one-element list.
package cloud

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Provider names one of the supported clouds.
type Provider string

const (
	ProviderGCP Provider = "gcp"
	ProviderAWS Provider = "aws"
	ProviderAzure Provider = "azure"
	// Backwards-compat aliases (LENNY_CLOUD_PROVIDER originally used
	// the K8s-flavor name; the cloud-broad name is canonical now).
	ProviderGKE = ProviderGCP
	ProviderEKS = ProviderAWS
	ProviderAKS = ProviderAzure
)

// ErrProviderUnavailable is returned when the requested provider's
// CLI is not on PATH.
var ErrProviderUnavailable = errors.New("cloud: required CLI not on PATH")

// FromEnv reads LENNY_CLOUD_PROVIDERS (canonical) or
// LENNY_CLOUD_PROVIDER (deprecated single-value alias) and returns
// the first provider. Returns "" when both are unset. Tests should
// use ConfiguredProviders to iterate over the full list.
func FromEnv() Provider {
	if ps := ConfiguredProviders(); len(ps) > 0 {
		return ps[0]
	}
	return ""
}

// ConfiguredProviders returns the list of cloud providers the tier-6
// suite must validate. The canonical env var is
// LENNY_CLOUD_PROVIDERS (comma-separated); LENNY_CLOUD_PROVIDER is
// retained as a single-value alias. Unknown provider names are
// silently dropped; an empty result indicates no provider is
// configured.
func ConfiguredProviders() []Provider {
	raw := strings.TrimSpace(os.Getenv("LENNY_CLOUD_PROVIDERS"))
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("LENNY_CLOUD_PROVIDER"))
	}
	if raw == "" {
		return nil
	}
	var out []Provider
	seen := make(map[Provider]bool)
	for _, part := range strings.Split(raw, ",") {
		p := Provider(strings.ToLower(strings.TrimSpace(part)))
		if p == "" || seen[p] {
			continue
		}
		switch p {
		case ProviderAWS, ProviderGCP, ProviderAzure:
			seen[p] = true
			out = append(out, p)
		default:
			// Unknown provider; the per-test fail-closed path will
			// surface a diagnosis when the suite tries to run against
			// it.
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// RunPerProvider runs fn as a subtest for every provider listed in
// LENNY_CLOUD_PROVIDERS. The parent test fails when the env var is
// unset — tier-6 cloud assertions require at least one configured
// provider. Each subtest's name is the provider id, so a failing
// per-provider assertion is attributable in the test report.
//
// fn receives the *testing.T scoped to the subtest plus the
// provider value; SkipUnlessAvailable is called inside fn (most
// tests already invoke it via requireCloud).
func RunPerProvider(t *testing.T, fn func(t *testing.T, p Provider)) {
	t.Helper()
	providers := ConfiguredProviders()
	if len(providers) == 0 {
		t.Fatalf("cloud: LENNY_CLOUD_PROVIDERS is required for tier-6; export a comma-separated subset of aws,gcp,azure (e.g. LENNY_CLOUD_PROVIDERS=aws). The operator provides only cloud-auth credentials (AWS_PROFILE / gcloud / az); scripts/cloud/<provider>/up.sh provisions the per-release resources and emits the per-resource env vars the test reads.")
	}
	for _, p := range providers {
		p := p
		t.Run(string(p), func(t *testing.T) {
			fn(t, p)
		})
	}
}

// SkipUnlessAvailable enforces the tier-6 precondition contract:
// the suite runs against one or more configured cloud providers,
// and a configured provider that isn't reachable is a test failure
// (the provider was named, so the operator expects it to work).
//
// The contract:
//
//   - `LENNY_CLOUD_PROVIDER` lists the active provider for the
//     suite (`aws`, `gcp`, or `azure`). When the var is unset the
//     helper t.Fatals — tier-6 is an e2e cloud suite and must run
//     against at least one cloud.
//   - The active provider's CLI must be on PATH and authenticated;
//     a CLI missing or unauthenticated fails the test with a
//     diagnosis pointing at the env var to set or the auth command
//     to run.
//   - `LENNY_CLOUD_SKIP_CLI_CHECK=1` bypasses the CLI + auth check.
//     The in-cluster tier-6 runner sets it because the runner
//     pod's image carries no aws / gcloud / az CLI; the tests in
//     the suite read pre-staged env vars (`LENNY_AWS_REDIS_AUTH_TOKEN`
//     etc.) or fall back to the in-pod IRSA-resolved SDK credentials.
//
// The helper retains the historical name (callers continue to
// invoke `cloud.SkipUnlessAvailable(t, p)`); the behavior is now
// fail-closed rather than skip-on-missing.
func SkipUnlessAvailable(t testing.TB, p Provider) {
	t.Helper()
	if p == "" {
		t.Fatalf("cloud: LENNY_CLOUD_PROVIDER is required for tier-6; export one of aws | gcp | azure to drive the suite against live cloud infrastructure.")
		return
	}
	if strings.ToLower(strings.TrimSpace(os.Getenv("LENNY_CLOUD_SKIP_CLI_CHECK"))) == "1" {
		return
	}
	cli := providerCLI(p)
	if cli == "" {
		t.Fatalf("cloud: provider %q has no documented CLI in the test infrastructure; supported providers: aws, gcp, azure", p)
		return
	}
	if _, err := exec.LookPath(cli); err != nil {
		t.Fatalf("cloud: %s CLI not on PATH (LENNY_CLOUD_PROVIDER=%s); install per scripts/cloud/%s/up.sh, or unset LENNY_CLOUD_PROVIDER to skip the cloud tier entirely.", cli, p, p)
		return
	}
	if err := authenticated(p); err != nil {
		t.Fatalf("cloud: %s not authenticated (LENNY_CLOUD_PROVIDER=%s): %v; authenticate or unset LENNY_CLOUD_PROVIDER.", cli, p, err)
		return
	}
}

// Up brings up the per-provider cluster shape via
// scripts/cloud/<cloud>/up.sh. Registers a t.Cleanup that runs
// the matching down.sh. The directory is the cloud-broad name
// (aws / gcp / azure) rather than the Kubernetes-flavor provider
// identifier (eks / gke / aks), since the scripts cover the whole
// cloud surface (RDS, ElastiCache, KMS, S3, etc.) not just the
// managed-Kubernetes service.
func Up(t testing.TB, p Provider, shape string) {
	t.Helper()
	SkipUnlessAvailable(t, p)
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatalf("cloud.Up: %v", err)
	}
	dir := providerScriptsDir(p)
	if dir == "" {
		t.Skipf("cloud.Up: provider %q has no documented scripts directory", p)
	}
	script := filepath.Join(repoRoot, "scripts", "cloud", dir, "up.sh")
	if _, err := os.Stat(script); err != nil {
		t.Skipf("cloud.Up: %s not present (Phase 13+ deliverable)", script)
	}
	cmd := exec.Command("bash", script, shape)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("cloud.Up: %s failed: %v", script, err)
	}
	t.Cleanup(func() {
		down := filepath.Join(repoRoot, "scripts", "cloud", dir, "down.sh")
		if _, err := os.Stat(down); err == nil {
			cmd := exec.Command("bash", down, shape)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			_ = cmd.Run()
		}
	})
}

// providerCLI returns the canonical CLI name for the given provider.
func providerCLI(p Provider) string {
	switch p {
	case ProviderGCP:
		return "gcloud"
	case ProviderAWS:
		return "aws"
	case ProviderAzure:
		return "az"
	}
	return ""
}

// providerScriptsDir maps a Kubernetes-flavor provider identifier to
// the matching cloud-broad scripts directory under scripts/cloud/.
// The provider identifier names the K8s service (eks / gke / aks);
// the scripts directory names the underlying cloud (aws / gcp /
// azure) because the scripts also drive non-Kubernetes resources
// (RDS, ElastiCache, IAM, etc.) under the same cloud account.
func providerScriptsDir(p Provider) string {
	switch p {
	case ProviderGCP:
		return "gcp"
	case ProviderAWS:
		return "aws"
	case ProviderAzure:
		return "azure"
	}
	return ""
}

// authenticated probes whether the provider's CLI has active
// credentials. Best-effort; on uncertainty returns nil so the test
// can proceed (rather than skipping unnecessarily).
func authenticated(p Provider) error {
	switch p {
	case ProviderGCP:
		out, _ := exec.Command("gcloud", "auth", "list", "--filter=status:ACTIVE", "--format=value(account)").Output()
		if strings.TrimSpace(string(out)) == "" {
			return fmt.Errorf("no active gcloud account; run `gcloud auth login`")
		}
	case ProviderAWS:
		if err := exec.Command("aws", "sts", "get-caller-identity").Run(); err != nil {
			return fmt.Errorf("aws sts get-caller-identity failed; check AWS_PROFILE / credentials")
		}
	case ProviderAzure:
		if err := exec.Command("az", "account", "show").Run(); err != nil {
			return fmt.Errorf("az account show failed; run `az login`")
		}
	}
	return nil
}

func findRepoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for d := wd; d != "/" && d != ""; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d, nil
		}
	}
	return "", fmt.Errorf("no go.mod from %s", wd)
}
