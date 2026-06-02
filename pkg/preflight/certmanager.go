// SPDX-License-Identifier: MIT

package preflight

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// CertManagerMinVersion is the §10.3 line 304 minimum supported
// cert-manager version. Below it, CertificateRequest approval and the
// Certificate admission webhook behave unstably, so the mTLS PKI the
// chart renders cannot be trusted to issue leaf certificates reliably.
//
// spec: §10.3 line 304. F-10.3.12.
const CertManagerMinVersion = "v1.12.0"

// CertManagerProber reports the cert-manager version installed in the
// cluster. found is false when cert-manager is not installed at all.
// version is the discovered version string (e.g. "v1.14.2"); an empty
// version with found=true means cert-manager is present but its version
// could not be read.
type CertManagerProber interface {
	ProbeCertManager(ctx context.Context) (version string, found bool, err error)
}

// CertManagerProbeFunc adapts a function to CertManagerProber.
type CertManagerProbeFunc func(ctx context.Context) (string, bool, error)

// ProbeCertManager implements CertManagerProber.
func (f CertManagerProbeFunc) ProbeCertManager(ctx context.Context) (string, bool, error) {
	return f(ctx)
}

// CertManagerVersionCheck runs the §10.3 line 304 cert-manager version
// preflight. When the cert-manager-backed PKI is enabled
// (certmanager.enabled, default true), the check fails the install
// fail-closed if cert-manager is absent or older than MinVersion, so a
// cluster running an unstable cert-manager surfaces at preflight rather
// than at the first Certificate-resource creation. When the PKI is
// disabled (mesh-managed mTLS) the check is a no-op.
//
// spec: §10.3 line 304. F-10.3.12.
type CertManagerVersionCheck struct {
	// Required is the certmanager.enabled chart value. False means the
	// operator manages mTLS through a service mesh, so the check skips.
	Required bool
	// MinVersion is the version floor. Empty falls back to
	// CertManagerMinVersion.
	MinVersion string
	// Prober discovers the installed version. Nil while Required routes
	// through the can't-determine advisory (a non-blocking WARNING),
	// matching the volume-encryption precedent.
	Prober CertManagerProber
}

// Decide evaluates the cert-manager version posture.
//
// spec: §10.3 line 304. F-10.3.12.
func (c CertManagerVersionCheck) Decide(ctx context.Context) Decision {
	min := strings.TrimSpace(c.MinVersion)
	if min == "" {
		min = CertManagerMinVersion
	}
	if !c.Required {
		return Decision{
			Passed: true,
			Reason: "§10.3 line 304: cert-manager version check skipped (certmanager.enabled=false; mTLS is mesh-managed)",
		}
	}
	if c.Prober == nil {
		return Decision{
			Passed: true,
			Reason: "WARNING: §10.3 line 304 cannot verify the cert-manager version (no prober wired); ensure cert-manager >= " +
				min + " is installed before relying on the rendered PKI",
		}
	}
	version, found, err := c.Prober.ProbeCertManager(ctx)
	if err != nil {
		return Decision{
			Passed: false,
			Reason: fmt.Sprintf("§10.3 line 304: cert-manager version probe failed: %v", err),
		}
	}
	if !found {
		return Decision{
			Passed: false,
			Reason: "§10.3 line 304: cert-manager is required (certmanager.enabled=true) but is not installed; install cert-manager >= " +
				min + " or set certmanager.enabled=false for mesh-managed mTLS",
		}
	}
	version = strings.TrimSpace(version)
	if version == "" {
		return Decision{
			Passed: true,
			Reason: "WARNING: §10.3 line 304 cert-manager is installed but its version could not be determined; ensure it is >= " + min,
		}
	}
	atLeast, err := versionAtLeast(version, min)
	if err != nil {
		return Decision{
			Passed: true,
			Reason: fmt.Sprintf("WARNING: §10.3 line 304 cert-manager version %q is unparseable (%v); ensure it is >= %s", version, err, min),
		}
	}
	if !atLeast {
		return Decision{
			Passed: false,
			Reason: fmt.Sprintf("§10.3 line 304: cert-manager version %s is below the minimum supported %s; upgrade cert-manager before installing Lenny",
				version, min),
		}
	}
	return Decision{
		Passed: true,
		Reason: fmt.Sprintf("§10.3 line 304: cert-manager %s satisfies the minimum %s", version, min),
	}
}

// versionAtLeast reports whether the dotted MAJOR.MINOR.PATCH version
// is greater than or equal to floor. Both accept an optional leading
// "v"; any pre-release or build suffix after "-" or "+" is ignored, so
// a pre-release of the floor minor is treated as that minor (the floor
// is a stability boundary, not a pre-release gate). It is a deliberately
// small comparator for the simple vX.Y.Z strings cert-manager publishes
// rather than a full semver dependency.
func versionAtLeast(version, floor string) (bool, error) {
	v, err := parseVersionTriple(version)
	if err != nil {
		return false, err
	}
	f, err := parseVersionTriple(floor)
	if err != nil {
		return false, err
	}
	for i := 0; i < 3; i++ {
		if v[i] != f[i] {
			return v[i] > f[i], nil
		}
	}
	return true, nil
}

// parseVersionTriple parses "vX.Y.Z" (or "X.Y" / "X") into a
// [major, minor, patch] triple, dropping any pre-release/build suffix.
func parseVersionTriple(s string) ([3]int, error) {
	var out [3]int
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if cut := strings.IndexAny(s, "-+"); cut >= 0 {
		s = s[:cut]
	}
	if s == "" {
		return out, fmt.Errorf("empty version")
	}
	parts := strings.Split(s, ".")
	if len(parts) > 3 {
		return out, fmt.Errorf("too many version components in %q", s)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, fmt.Errorf("invalid version component %q", p)
		}
		out[i] = n
	}
	return out, nil
}
