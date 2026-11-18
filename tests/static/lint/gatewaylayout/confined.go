// SPDX-License-Identifier: MIT

package gatewaylayout

// Confinement names one package subtree that holds credential material,
// tokens or keys, or authorization and admission rules, which GW-R2
// confines to the parts that act on it.
type Confinement struct {
	// Path is the repository-relative directory of the subtree.
	Path string
	// APIFront marks a subtree the API front may import as well: the
	// authentication middleware its request pipeline runs, and the stores
	// and packages its admin handlers read.
	APIFront bool
}

// Confined lists the subtrees GW-R2 names. Each is listed at its directory
// today and at the concern directory the move manifest gives it, so the
// rule holds before and after the step that moves it. A package a member
// change removes has no destination. The first entry whose directory holds
// a path decides, so a narrower entry comes before a wider one.
var Confined = []Confinement{
	// The credential packages.
	{Path: "pkg/gateway/credentials/credentialstore"},
	{Path: "pkg/gateway/credentials/credleasestore"},
	{Path: "pkg/gateway/credentials/credassign"},
	{Path: "pkg/gateway/credentials/credcache"},
	{Path: "pkg/gateway/credentials/credentialpoolstore", APIFront: true},
	{Path: "pkg/credential/credentialpoolstore", APIFront: true},
	{Path: "pkg/gateway/credentials/credentialserver"},
	{Path: "pkg/gateway/credentials/usercreds"},
	{Path: "pkg/gateway/credentials/impersonation", APIFront: true},
	{Path: "pkg/credential/impersonation", APIFront: true},
	{Path: "pkg/gateway/credentials/revocation"},
	{Path: "pkg/gateway/credentials/denylist"},
	{Path: "pkg/gateway/connectors/connectorcredstore", APIFront: true},
	{Path: "pkg/gateway/connectors/connectorsecret"},
	{Path: "pkg/gateway/provisioning/vcscred"},
	{Path: "pkg/workspaceplan/vcscred"},
	{Path: "pkg/gateway/llmproxy/credrouter"},
	{Path: "pkg/credential/credentialstore"},
	{Path: "pkg/credential/credcache"},
	{Path: "pkg/credential/credentialserver"},
	{Path: "pkg/credential/usercreds"},
	{Path: "pkg/credential/revocation"},
	{Path: "pkg/credential/denylist"},
	{Path: "pkg/credential", APIFront: true},
	// The token and key packages.
	{Path: "pkg/auth/jwt", APIFront: true},
	{Path: "pkg/gateway/storage/issuedtokenstore", APIFront: true},
	{Path: "pkg/storage/issuedtokenstore", APIFront: true},
	{Path: "pkg/kms/rekey", APIFront: true},
	{Path: "pkg/kms"},
	{Path: "pkg/tenantkms"},
	// The admission packages.
	{Path: "pkg/gateway/middleware/auth", APIFront: true},
	{Path: "pkg/auth/auth", APIFront: true},
	{Path: "pkg/gateway/quota", APIFront: true},
	{Path: "pkg/quota", APIFront: true},
	{Path: "pkg/gateway/policy/interceptor", APIFront: true},
	{Path: "pkg/policy/interceptor", APIFront: true},
	{Path: "pkg/gateway/policy/ratelimit", APIFront: true},
	{Path: "pkg/policy/ratelimit", APIFront: true},
	{Path: "pkg/gateway/policy"},
	{Path: "pkg/policy"},
	{Path: "pkg/gateway/environment/tenantstore", APIFront: true},
	{Path: "pkg/tenancy/tenantstore", APIFront: true},
	// The authorization predicates.
	{Path: "pkg/gateway/callercheck"},
	{Path: "pkg/auth/callercheck"},
	{Path: "pkg/gateway/environment/envaccess"},
	{Path: "pkg/tenancy/envaccess"},
	{Path: "pkg/gateway/environment/tenantaccessstore", APIFront: true},
	{Path: "pkg/tenancy/tenantaccessstore", APIFront: true},
	{Path: "pkg/gateway/environment/customrolestore", APIFront: true},
	{Path: "pkg/tenancy/customrolestore", APIFront: true},
	{Path: "pkg/gateway/mcpfabric/delegationpolicystore", APIFront: true},
	{Path: "pkg/delegation/delegationpolicystore", APIFront: true},
}

// confinedImport reports whether an import from a package of the given kind
// and part to the repository-relative path to breaks the confinement rule.
// Only a gateway part is held to it: the session core, the metering proxy,
// and the composition root import every confined package, the API front
// imports the subtrees marked for it, and every other part imports none. A
// package in transit or outside the gateway is not held to it.
func confinedImport(kind Kind, part, to string, confined []Confinement) bool {
	var hit *Confinement
	for i := range confined {
		if within(to, confined[i].Path) {
			hit = &confined[i]
			break
		}
	}
	if hit == nil {
		return false
	}
	switch kind {
	case KindSeamInterface, KindReplica:
		return true
	case KindSeam:
		return part != "metering-proxy"
	case KindAPIFront:
		return !hit.APIFront
	}
	return false
}

// ConfinedImport reports whether a package of the given kind and part may
// not hold or import the repository-relative package path under GW-R2.
func ConfinedImport(kind Kind, part, rel string) bool {
	return confinedImport(kind, part, rel, Confined)
}
