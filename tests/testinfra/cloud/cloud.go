// SPDX-License-Identifier: MIT

// Package cloud is the §12.6 cloud-provider bring-up harness. It
// dispatches per-provider helpers and exposes a uniform
// SkipUnlessAvailable + Up / Down lifecycle to tier-6 tests.
//
// The package does not embed Terraform; it shells out to per-
// provider scripts under scripts/cloud/<provider>/{up,down}.sh.
// When the relevant SDK / CLI is not installed (gcloud, aws, az)
// the test skips with a precise diagnosis.
//
// # Environment variables
//
//	LENNY_CLOUD_PROVIDER  Selects the active provider: gke, eks, aks.
//	                      Unset / empty → tests that gate on a
//	                      specific provider skip.
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
	ProviderGKE Provider = "gke"
	ProviderEKS Provider = "eks"
	ProviderAKS Provider = "aks"
)

// ErrProviderUnavailable is returned when the requested provider's
// CLI is not on PATH.
var ErrProviderUnavailable = errors.New("cloud: required CLI not on PATH")

// FromEnv reads LENNY_CLOUD_PROVIDER and returns the named provider.
// Returns "" when unset.
func FromEnv() Provider {
	return Provider(strings.ToLower(strings.TrimSpace(os.Getenv("LENNY_CLOUD_PROVIDER"))))
}

// SkipUnlessAvailable short-circuits the test when the provider is
// not configured or the CLI is absent.
func SkipUnlessAvailable(t testing.TB, p Provider) {
	t.Helper()
	if p == "" {
		t.Skip("cloud.SkipUnlessAvailable: LENNY_CLOUD_PROVIDER is unset; export gke|eks|aks to enable tier-6 cloud tests")
	}
	cli := providerCLI(p)
	if cli == "" {
		t.Skipf("cloud.SkipUnlessAvailable: provider %q has no documented CLI", p)
	}
	if _, err := exec.LookPath(cli); err != nil {
		t.Skipf("cloud.SkipUnlessAvailable: %s CLI not on PATH; install per scripts/cloud/%s/up.sh", cli, p)
	}
	if err := authenticated(p); err != nil {
		t.Skipf("cloud.SkipUnlessAvailable: %s not authenticated: %v", p, err)
	}
}

// Up brings up the per-provider cluster shape via
// scripts/cloud/<provider>/up.sh. Registers a t.Cleanup that runs
// the matching down.sh.
func Up(t testing.TB, p Provider, shape string) {
	t.Helper()
	SkipUnlessAvailable(t, p)
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatalf("cloud.Up: %v", err)
	}
	script := filepath.Join(repoRoot, "scripts", "cloud", string(p), "up.sh")
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
		down := filepath.Join(repoRoot, "scripts", "cloud", string(p), "down.sh")
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
	case ProviderGKE:
		return "gcloud"
	case ProviderEKS:
		return "aws"
	case ProviderAKS:
		return "az"
	}
	return ""
}

// authenticated probes whether the provider's CLI has active
// credentials. Best-effort; on uncertainty returns nil so the test
// can proceed (rather than skipping unnecessarily).
func authenticated(p Provider) error {
	switch p {
	case ProviderGKE:
		out, _ := exec.Command("gcloud", "auth", "list", "--filter=status:ACTIVE", "--format=value(account)").Output()
		if strings.TrimSpace(string(out)) == "" {
			return fmt.Errorf("no active gcloud account; run `gcloud auth login`")
		}
	case ProviderEKS:
		if err := exec.Command("aws", "sts", "get-caller-identity").Run(); err != nil {
			return fmt.Errorf("aws sts get-caller-identity failed; check AWS_PROFILE / credentials")
		}
	case ProviderAKS:
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
