// SPDX-License-Identifier: MIT

package preflight

import "github.com/lennylabs/lenny/pkg/deployenv"

// CheckCosignProduction emits the §5.3 advisory for a
// production-or-staging install that ships with cosign image-signature
// verification disabled. It mirrors the §17.6 non-blocking WARNING the
// preflight Job already raises for unverifiable production controls
// (node disk encryption): the install is not aborted, but the operator
// is told that the spec treats signature verification as a prerequisite
// for this posture and that a pod can launch from an unsigned image.
//
// The check is warning-only by construction. The spec wording
// ("prerequisite") is strong, but cosign requires signing material the
// chart cannot synthesize, so a hard failure would block every
// first-time production install before the signing program is wired.
// The advisory closes the gap the §17.6 disk-encryption warning leaves:
// a production install that completes with no signature verification now
// surfaces an operator notification instead of completing silently.
//
// The advisory applies to the chart `environment` value (rendered onto
// --environment) when it is production mode or staging, which is §5.3's
// "production or staging". Production mode is the exact value prod, read
// through deployenv.IsProduction, and staging is the exact value staging;
// the value is neither case-folded nor trimmed.
//
// spec: §5.3, §17.9.1 (Composition Dimensions) "Production mode".
func CheckCosignProduction(env string, cosignEnabled bool) Decision {
	if !deployenv.IsProduction(env) && env != deployenv.Staging {
		return Decision{Passed: true}
	}
	if cosignEnabled {
		return Decision{Passed: true}
	}
	return Decision{Passed: true, Reason: "WARNING: imageVerification.cosign.enabled is false for environment=" +
		env + "; §5.3 makes image provenance verification a prerequisite for production and staging deployments. " +
		"A pod can launch from an unsigned image. Enable imageVerification.cosign.enabled once the signing program is in place."}
}
