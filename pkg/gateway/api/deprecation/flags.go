// SPDX-License-Identifier: MIT

package deprecation

import (
	"flag"
	"os"
)

// Flags holds the flag only the deprecation stage of the request pipeline
// reads.
type Flags struct {
	// Versions is the --deprecated-api-versions flag.
	Versions *string
}

// RegisterFlags registers these flags on fs and returns their values, which
// fs fills when it parses. Each flag's default is read from its environment
// variable, as the composition root read it before the flags moved here.
func RegisterFlags(fs *flag.FlagSet) *Flags {
	f := &Flags{}
	// §15.5 item 1 + docs/api/index.md — when a REST URL
	// version prefix enters its 6-month sunset window, the gateway adds
	// the `X-Lenny-Deprecated-Version` response header to every response
	// served under that prefix. The list defaults empty: v1 is current
	// and no /v2/ has shipped yet, so the middleware is inert. When the
	// first /v2/ surface lands, operators set
	// `gateway.deprecatedAPIVersions: [v1]` in the Helm values (rendered
	// as the flag below) and the middleware begins stamping the header
	// without further code changes. F-15.5.11.
	f.Versions = fs.String("deprecated-api-versions",
		os.Getenv("LENNY_DEPRECATED_API_VERSIONS"),
		"§15.5 item 1 / docs/api/index.md — comma-separated REST URL version prefixes currently in their 6-month sunset window. Each match stamps the `X-Lenny-Deprecated-Version` response header. Empty disables the header (v1 default). Override via LENNY_DEPRECATED_API_VERSIONS. F-15.5.11.")
	return f
}
