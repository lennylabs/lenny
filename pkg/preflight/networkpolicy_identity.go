// SPDX-License-Identifier: MIT

package preflight

import "fmt"

// The §13.2 / §10.3 NET-064 deployment-identity annotations. The chart
// stamps each on the lenny-gateway Deployment with the rendered
// global.spiffeTrustDomain / global.saTokenAudience value, so the
// preflight Job can enumerate the values already in use cluster-wide.
const (
	spiffeTrustDomainAnnotation = "lenny.dev/spiffe-trust-domain"
	saTokenAudienceAnnotation   = "lenny.dev/sa-token-audience"
)

// GatewayIdentity is the NET-064 deployment-identity projection of one
// existing lenny-gateway Deployment in the target cluster.
type GatewayIdentity struct {
	// Namespace is the Deployment's namespace.
	Namespace string
	// Name is the Deployment's name.
	Name string
	// SPIFFETrustDomain is the lenny.dev/spiffe-trust-domain annotation
	// value, empty when the Deployment carries no such annotation.
	SPIFFETrustDomain string
	// SATokenAudience is the lenny.dev/sa-token-audience annotation
	// value, empty when the Deployment carries no such annotation.
	SATokenAudience string
}

// CheckSPIFFETrustDomainUniqueness verifies the §13.2 / §10.3 NET-064
// SPIFFE-trust-domain uniqueness invariant: the global.spiffeTrustDomain
// value under installation must be absent from the set of trust domains
// already in use by existing lenny-gateway Deployments in the target
// cluster. Two deployments sharing a trust domain have overlapping
// SPIFFE URIs, so one deployment's pod certificate validates against
// the other's gateway — enabling cross-deployment pod impersonation and
// audit-log attribution forgery.
//
// The check is fail-closed: a collision aborts the install. A
// Deployment matching the value's own namespace is skipped, so a
// re-install or helm upgrade of the same deployment does not collide
// with itself. When trustDomain is empty the check passes — the
// chart's required guard on global.spiffeTrustDomain fails templating
// before preflight runs, so an empty value never reaches this check on
// a real install.
func CheckSPIFFETrustDomainUniqueness(trustDomain, installNamespace string, existing []GatewayIdentity) Decision {
	if trustDomain == "" {
		return Decision{Passed: true}
	}
	for _, gw := range existing {
		if gw.Namespace == installNamespace {
			continue
		}
		if gw.SPIFFETrustDomain == trustDomain {
			return Decision{Passed: false, Reason: fmt.Sprintf(
				"NETWORK_POLICY_SPIFFE_TRUST_DOMAIN_COLLISION: global.spiffeTrustDomain %q "+
					"is already in use by Lenny Deployment %q. Two deployments sharing a "+
					"trust domain have overlapping SPIFFE URIs, enabling cross-deployment "+
					"pod impersonation. Choose a deployment-unique value "+
					"(e.g. 'lenny-<cluster>-<namespace>') — see §10.3 (NET-064)",
				trustDomain, gw.Namespace+"/"+gw.Name,
			)}
		}
	}
	return Decision{Passed: true}
}

// CheckSATokenAudienceUniqueness verifies the §13.2 / §10.3 NET-064
// SA-token-audience uniqueness invariant: the global.saTokenAudience
// value under installation must be absent from the set of audiences
// already in use by existing lenny-gateway Deployments in the target
// cluster. A shared audience lets a projected SA token minted for one
// deployment's gateway be accepted by another's, enabling
// cross-deployment token replay and audit-log attribution forgery.
//
// The check is fail-closed: a collision aborts the install. A
// Deployment matching the value's own namespace is skipped so a
// re-install does not collide with itself. When audience is empty the
// check passes — the chart's required guard on global.saTokenAudience
// fails templating before preflight runs.
func CheckSATokenAudienceUniqueness(audience, installNamespace string, existing []GatewayIdentity) Decision {
	if audience == "" {
		return Decision{Passed: true}
	}
	for _, gw := range existing {
		if gw.Namespace == installNamespace {
			continue
		}
		if gw.SATokenAudience == audience {
			return Decision{Passed: false, Reason: fmt.Sprintf(
				"NETWORK_POLICY_SA_TOKEN_AUDIENCE_COLLISION: global.saTokenAudience %q is "+
					"already in use by Lenny Deployment %q. A shared audience allows SA "+
					"tokens minted for one deployment's gateway to be accepted by another's, "+
					"enabling cross-deployment token replay. Choose a deployment-unique "+
					"value (e.g. 'lenny-gateway-<cluster>') — see §10.3 (NET-064)",
				audience, gw.Namespace+"/"+gw.Name,
			)}
		}
	}
	return Decision{Passed: true}
}
