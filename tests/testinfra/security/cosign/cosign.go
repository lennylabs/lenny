// SPDX-License-Identifier: MIT

// Package cosign wraps the §12.9.11 image-signing checks: cosign
// signature verification and trivy CVE scanning. The §12.9.11 gate
// runs pre-release; this scaffold ships the entry points so the
// tier-9 tests can call them once the production image bundle is
// available.
package cosign

import (
	"errors"
	"os/exec"
	"testing"
)

// ErrCosignUnavailable signals that the cosign binary is not on PATH.
var ErrCosignUnavailable = errors.New("cosign: cosign not on PATH")

// CosignAvailable reports whether cosign is installed.
func CosignAvailable() bool {
	_, err := exec.LookPath("cosign")
	return err == nil
}

// TrivyAvailable reports whether trivy is installed.
func TrivyAvailable() bool {
	_, err := exec.LookPath("trivy")
	return err == nil
}

// SkipUnlessAvailable skips when neither cosign nor trivy is
// reachable.
func SkipUnlessAvailable(t testing.TB) {
	t.Helper()
	if !CosignAvailable() && !TrivyAvailable() {
		t.Skip("cosign.SkipUnlessAvailable: neither cosign nor trivy on PATH. Install per TESTING.md §12.9.11.")
	}
}

// VerifySignature checks the cosign signature on the image. Returns
// nil when the signature verifies against the trusted key.
func VerifySignature(t testing.TB, image, trustedKey string) error {
	t.Helper()
	if !CosignAvailable() {
		return ErrCosignUnavailable
	}
	args := []string{"verify", "--key", trustedKey, image}
	cmd := exec.Command("cosign", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("cosign verify output: %s", out)
		return err
	}
	return nil
}

// ScanCriticalCVEs runs trivy against the image and reports the
// count of CRITICAL severity vulnerabilities. The §12.9.11 gate
// requires zero criticals at release time.
func ScanCriticalCVEs(t testing.TB, image string) (int, error) {
	t.Helper()
	if !TrivyAvailable() {
		return 0, errors.New("trivy not on PATH")
	}
	args := []string{"image", "--severity", "CRITICAL", "--quiet", "--format", "json", image}
	cmd := exec.Command("trivy", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, err
	}
	count := countCriticalsInTrivyJSON(out)
	return count, nil
}

// countCriticalsInTrivyJSON parses a minimal subset of trivy's JSON
// output. Trivy's schema is verbose; here we count the substring
// occurrences of "Severity":"CRITICAL", which is conservative but
// matches the spec rule (the count is what we care about, not the
// per-CVE detail).
func countCriticalsInTrivyJSON(body []byte) int {
	count := 0
	needle := []byte(`"Severity":"CRITICAL"`)
	for i := 0; i+len(needle) <= len(body); i++ {
		if string(body[i:i+len(needle)]) == string(needle) {
			count++
			i += len(needle) - 1
		}
	}
	return count
}
