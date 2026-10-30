// SPDX-License-Identifier: MIT

// Package crds is authored source that sits beside the copied manifests
// in the same directory. No producer writes it, and it carries a line
// citation the line pass has to be able to rewrite.
//
// spec: §10 line 437
package crds

import "embed"

// FS holds the copied manifests.
//
//go:embed *.yaml
var FS embed.FS
