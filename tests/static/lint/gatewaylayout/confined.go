// SPDX-License-Identifier: MIT

package gatewaylayout

// Confinement names one package subtree that holds credential material or
// decides admission, which GW-R2 confines to the parts that act on it.
type Confinement struct {
	// Path is the repository-relative directory of the subtree.
	Path string
	// APIFront marks the authentication middleware, which the API front
	// may import as well.
	APIFront bool
}

// Confined lists the subtrees GW-R2 names. Each is listed at its directory
// today and at the concern directory the move manifest gives it, so the
// rule holds before and after the step that moves it.
var Confined = []Confinement{
	{Path: "pkg/gateway/credentials/credentialstore"},
	{Path: "pkg/credential/credentialstore"},
	{Path: "pkg/gateway/credentials/credleasestore"},
	{Path: "pkg/gateway/middleware/auth", APIFront: true},
	{Path: "pkg/auth/auth", APIFront: true},
	{Path: "pkg/gateway/quota"},
	{Path: "pkg/quota"},
	{Path: "pkg/gateway/policy"},
	{Path: "pkg/policy"},
	{Path: "pkg/gateway/environment/tenantstore"},
	{Path: "pkg/tenancy/tenantstore"},
}

// confinedImport reports whether an import from a package of the given kind
// and part to the repository-relative path to breaks the confinement rule.
// Only a gateway part is held to it: the session core, the metering proxy,
// and the composition root import every confined package, the API front
// imports the authentication middleware, and every other part imports
// none. A package in transit or outside the gateway is not held to it.
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
