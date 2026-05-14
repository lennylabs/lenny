// SPDX-License-Identifier: MIT

// Package sbom generates and verifies §12.9.11 SBOM artifacts. The
// canonical SBOM format is CycloneDX JSON; production images ship
// an attestation alongside the image so the SBOM is verifiable.
//
// Today this package wraps `syft` (the SBOM generator) and reports
// whether the documented attestation exists. When syft isn't on
// PATH the helpers skip with a precise diagnosis.
package sbom

import (
	"errors"
	"os/exec"
	"path/filepath"
	"testing"
)

// ErrSyftUnavailable signals that syft is not on PATH.
var ErrSyftUnavailable = errors.New("sbom: syft not on PATH")

// Available reports whether syft is installed.
func Available() bool {
	_, err := exec.LookPath("syft")
	return err == nil
}

// SkipUnlessAvailable short-circuits the test when syft is absent.
func SkipUnlessAvailable(t testing.TB) {
	t.Helper()
	if !Available() {
		t.Skip("sbom.SkipUnlessAvailable: syft not on PATH. Install via `brew install syft` or per https://github.com/anchore/syft.")
	}
}

// Generate produces a CycloneDX JSON SBOM for the given image and
// writes it to outDir/<image-sha>.cdx.json. Returns the absolute
// path to the generated file.
func Generate(t testing.TB, image, outDir string) string {
	t.Helper()
	SkipUnlessAvailable(t)
	if outDir == "" {
		outDir = t.TempDir()
	}
	out := filepath.Join(outDir, "sbom.cdx.json")
	cmd := exec.Command("syft", image, "-o", "cyclonedx-json="+out)
	combined, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("syft generate %s: %v\n%s", image, err, combined)
	}
	return out
}

// VerifyAttestation checks whether the image carries an SBOM
// attestation (`cosign verify-attestation --type cyclonedx`).
// Returns nil when an attestation is present and verifies against
// the supplied trust anchor.
func VerifyAttestation(t testing.TB, image, trustedKey string) error {
	t.Helper()
	if _, err := exec.LookPath("cosign"); err != nil {
		return errors.New("cosign not on PATH; SBOM attestation verify is unavailable")
	}
	cmd := exec.Command("cosign", "verify-attestation", "--type", "cyclonedx", "--key", trustedKey, image)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("cosign verify-attestation: %s", out)
		return err
	}
	return nil
}
