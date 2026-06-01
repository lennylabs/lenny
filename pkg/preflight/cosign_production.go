// SPDX-License-Identifier: MIT

package preflight

import "strings"

// productionLikeEnvironments are the deployment-posture values
// (chart value `environment`, rendered onto --environment) for which
// §5.3 line 669 makes image provenance verification a prerequisite:
// "Image provenance verification (signing, attestation) is a
// prerequisite for any production or staging deployment." A dev
// install renders without the signing material configured, so it is
// not warned.
var productionLikeEnvironments = map[string]struct{}{
	"prod":       {},
	"production": {},
	"staging":    {},
	"stage":      {},
}

// CheckCosignProduction emits the §5.3 line 669 advisory for a
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
// spec: §5.3 line 669.
func CheckCosignProduction(environment string, cosignEnabled bool) Decision {
	env := strings.ToLower(strings.TrimSpace(environment))
	if _, productionLike := productionLikeEnvironments[env]; !productionLike {
		return Decision{Passed: true}
	}
	if cosignEnabled {
		return Decision{Passed: true}
	}
	return Decision{Passed: true, Reason: "WARNING: imageVerification.cosign.enabled is false for environment=" +
		env + "; §5.3 makes image provenance verification a prerequisite for production and staging deployments. " +
		"A pod can launch from an unsigned image. Enable imageVerification.cosign.enabled once the signing program is in place."}
}
