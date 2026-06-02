// SPDX-License-Identifier: MIT

package preflight

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// VolumeEncryptionResult reports the encryption posture of one persistent
// volume the §12.9 line 1050 check inspects (the Postgres or Redis data
// volume). Detail carries the StorageClass or provider note backing the
// verdict.
type VolumeEncryptionResult struct {
	// Name identifies the probed volume, e.g. "postgres" or "redis".
	Name string
	// Encrypted reports whether the volume is backed by encrypted storage.
	Encrypted bool
	// Detail is a human-readable note (StorageClass, provider error, etc.).
	Detail string
}

// VolumeEncryptionProber reports the §12.9 line 1050 encryption posture of
// the Postgres and Redis persistent volumes. A real implementation queries
// the cloud provider's volume API (or inspects the bound StorageClass) and
// returns one result per volume. It returns a non-nil error when the check
// cannot be performed (e.g., the cloud provider API is unavailable), which
// the check converts into the attestation path. A nil VolumeEncryptionProber
// on the check is itself treated as "cannot be performed": the §17.3 BYO
// topology has no in-cluster Postgres/Redis volumes for the chart to probe,
// so v1 relies on the operator attestation the spec provides.
//
// spec: §12.9 line 1050.
type VolumeEncryptionProber interface {
	ProbeVolumeEncryption(ctx context.Context) ([]VolumeEncryptionResult, error)
}

// VolumeEncryptionProbeFunc adapts a function to VolumeEncryptionProber.
type VolumeEncryptionProbeFunc func(ctx context.Context) ([]VolumeEncryptionResult, error)

// ProbeVolumeEncryption calls f.
func (f VolumeEncryptionProbeFunc) ProbeVolumeEncryption(ctx context.Context) ([]VolumeEncryptionResult, error) {
	return f(ctx)
}

// VolumeEncryptionCheck is the §12.9 line 1050 T2 storage-layer encryption
// audit. The spec requires the preflight Job to validate that the Redis and
// Postgres volumes are backed by encrypted storage. The decision tree is:
//
//   - global.devMode exempts the check (local development volumes).
//   - When the posture can be determined and any volume is unencrypted, the
//     install fails closed: T2 data mandates volume-level encryption.
//   - When the check cannot be performed (no prober wired, or the prober
//     reports an error), the spec logs a non-blocking warning and the
//     deployer attests compliance via preflight.attestVolumeEncryption. An
//     attested install passes cleanly; an unattested install passes with a
//     WARNING so the obligation is visible but a default bring-your-own
//     install is not blocked (the monitoring.acknowledgeNoPrometheus /
//     playground.acknowledgeApiKeyMode precedent).
//
// spec: §12.9 line 1050.
type VolumeEncryptionCheck struct {
	// DevMode is the global.devMode chart value; true exempts the check.
	DevMode bool
	// Attested is the preflight.attestVolumeEncryption chart value: the
	// operator's attestation that the BYO Postgres and Redis volumes are
	// encrypted when the check cannot verify it directly.
	Attested bool
	// Prober determines the live encryption posture. Nil means the check
	// cannot be performed, which routes through the attestation path.
	Prober VolumeEncryptionProber
}

// Decide evaluates the §12.9 line 1050 volume-encryption posture.
//
// spec: §12.9 line 1050.
func (c VolumeEncryptionCheck) Decide(ctx context.Context) Decision {
	if c.DevMode {
		return Decision{
			Passed: true,
			Reason: "§12.9 line 1050: volume-encryption check exempt under global.devMode",
		}
	}
	if c.Prober == nil {
		return c.cannotPerform("no in-cluster Postgres/Redis volume to probe (BYO storage)")
	}
	results, err := c.Prober.ProbeVolumeEncryption(ctx)
	if err != nil {
		return c.cannotPerform("volume-encryption probe failed: " + err.Error())
	}
	if len(results) == 0 {
		return c.cannotPerform("volume-encryption probe returned no volumes")
	}
	var unencrypted []string
	for _, r := range results {
		if !r.Encrypted {
			detail := r.Name
			if r.Detail != "" {
				detail = fmt.Sprintf("%s (%s)", r.Name, r.Detail)
			}
			unencrypted = append(unencrypted, detail)
		}
	}
	if len(unencrypted) > 0 {
		sort.Strings(unencrypted)
		return Decision{
			Passed: false,
			Reason: fmt.Sprintf("§12.9 line 1050: T2 data requires volume-level encryption; unencrypted volume(s): %s",
				strings.Join(unencrypted, ", ")),
		}
	}
	return Decision{
		Passed: true,
		Reason: "§12.9 line 1050: Postgres and Redis volumes are backed by encrypted storage",
	}
}

// cannotPerform returns the §12.9 line 1050 non-blocking decision used when
// the live posture cannot be determined. An attested install passes
// cleanly; an unattested install passes with a WARNING that records the
// obligation to attest. Neither blocks the install, matching the spec's "a
// warning is logged" posture and the acknowledge-to-silence precedent.
func (c VolumeEncryptionCheck) cannotPerform(reason string) Decision {
	if c.Attested {
		return Decision{
			Passed: true,
			Reason: "§12.9 line 1050: volume encryption not directly verified (" + reason +
				"); accepted on preflight.attestVolumeEncryption attestation",
		}
	}
	return Decision{
		Passed: true,
		Reason: "WARNING: §12.9 line 1050 cannot verify Postgres/Redis volume encryption (" + reason +
			"); set preflight.attestVolumeEncryption: true to attest the T2 storage-layer encryption baseline",
	}
}
