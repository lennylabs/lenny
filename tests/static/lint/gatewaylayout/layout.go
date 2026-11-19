// SPDX-License-Identifier: MIT

package gatewaylayout

import "strings"

// Kind is the role a package plays in the layout.
type Kind int

// The kinds of package the rules distinguish.
const (
	// KindExternal is a package outside the module.
	KindExternal Kind = iota
	// KindSeamInterface is a package of pkg/gateway/seam.
	KindSeamInterface
	// KindSeam is a package of one seam.
	KindSeam
	// KindCore is a package of the session core.
	KindCore
	// KindAPIFront is a package of the API front.
	KindAPIFront
	// KindReplica is a package of a replica-level part other than the API
	// front, or of the web playground. It may import the session core and
	// the seam interface, and no seam.
	KindReplica
	// KindDomain is a registered package that leaves pkg/gateway.
	KindDomain
	// KindRemoved is a registered package that a change removes.
	KindRemoved
	// KindUnplaced is a package under pkg/gateway that sits in no
	// directory of the table and has no register row.
	KindUnplaced
	// KindCompositionRoot is cmd/lenny-gateway, which imports every part.
	KindCompositionRoot
	// KindExempt is a test package under tests/.
	KindExempt
	// KindOutside is any other package of the module.
	KindOutside
)

// Transit parts are the register parts of packages in transit.
const (
	PartDomain  = "domain"
	PartRemoved = "removed"
)

// PartPlayground is the part of the web playground, which GW-R2 allows the
// packages its token mint reads.
const PartPlayground = "playground"

// PartPlatformTools is the part of the platform tools seam, which GW-R2
// allows the delegation policy store its delegation service evaluates.
const PartPlatformTools = "platform-tools"

// Dir is one row of the directory table: a directory under the module root
// and the part every package in its subtree belongs to.
type Dir struct {
	Path string
	Part string
	Kind Kind
}

// GatewayRoot is the directory of the gateway's packages, and
// CompositionRoot the binary that wires them.
const (
	GatewayRoot     = "pkg/gateway"
	CompositionRoot = "cmd/lenny-gateway"
	TestsRoot       = "tests"
)

// Layout is the directory table. Each part's directory and its page in the
// specification:
//
//   - seam: the seam interface (GWSEAM), and under it the seam model;
//   - streamproxy, uploadhandler, platformtools, meteringproxy: the seams
//     (GWSTREAM, GWUPLOAD, GWTOOLS, GWLLM);
//   - core: the session core (GWCOORD, GWCLAIM, GWBARRIER, and the session
//     capability pages);
//   - api: the API front (GWAPI);
//   - drain, scaling, opsendpoints, replica: the other replica-level parts
//     (GWDRAIN, GWSCALE, GWOPS, GW);
//   - playground: the web playground (PLAY).
var Layout = []Dir{
	{Path: "pkg/gateway/seam", Part: "seam-interface", Kind: KindSeamInterface},
	{Path: "pkg/gateway/streamproxy", Part: "stream-proxy", Kind: KindSeam},
	{Path: "pkg/gateway/uploadhandler", Part: "upload-handler", Kind: KindSeam},
	{Path: "pkg/gateway/platformtools", Part: PartPlatformTools, Kind: KindSeam},
	{Path: "pkg/gateway/meteringproxy", Part: "metering-proxy", Kind: KindSeam},
	{Path: "pkg/gateway/core", Part: "core", Kind: KindCore},
	{Path: "pkg/gateway/api", Part: "api-front", Kind: KindAPIFront},
	{Path: "pkg/gateway/drain", Part: "drain", Kind: KindReplica},
	{Path: "pkg/gateway/scaling", Part: "scaling", Kind: KindReplica},
	{Path: "pkg/gateway/opsendpoints", Part: "ops-endpoints", Kind: KindReplica},
	{Path: "pkg/gateway/replica", Part: "replica", Kind: KindReplica},
	{Path: "pkg/gateway/playground", Part: PartPlayground, Kind: KindReplica},
}

// within reports whether a repository-relative path is dir or a path
// under it.
func within(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, dir+"/")
}

// layoutDir returns the table row whose directory holds path, or false.
// Directories in the table do not nest, so at most one row matches.
func layoutDir(table []Dir, path string) (Dir, bool) {
	for _, d := range table {
		if within(path, d.Path) {
			return d, true
		}
	}
	return Dir{}, false
}

// partKind returns the kind of a part named in a register row: a transit
// part, or a part of the table. It returns false for a name neither knows.
func partKind(table []Dir, part string) (Kind, bool) {
	switch part {
	case PartDomain:
		return KindDomain, true
	case PartRemoved:
		return KindRemoved, true
	}
	for _, d := range table {
		if d.Part == part {
			return d.Kind, true
		}
	}
	return KindExternal, false
}
