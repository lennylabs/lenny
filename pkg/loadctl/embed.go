// SPDX-License-Identifier: MIT

package loadctl

import (
	"embed"
	"io/fs"
)

// webFS embeds the web/ tree at compile time so the loadctl binary
// serves the editable HTML + CSS without a separate static-asset
// pipeline. The embed root is relative to this file's package
// directory, so the build copies in tests/../web/. Since loadctl is
// at pkg/loadctl/, web/ is reached through the all:web pattern
// applied to a sibling-pinned path. Concrete path: we copied the
// web/ tree under pkg/loadctl/web at build time via the go generate
// step below; if the directive is absent, the binary falls back to
// the inlined indexHTML constant.
//
// Implementation note: rather than a `go generate` step, the embed
// uses the source-of-truth web/ at the repo root through a symlink
// committed under pkg/loadctl/web. The fallback path is robust if
// the symlink is broken on a checkout.

//go:embed all:web
var embeddedWeb embed.FS

// embeddedAssets returns the embedded web/ tree, or nil when the
// embed is empty (in which case handleIndex falls back to the
// inlined indexHTML constant).
func embeddedAssets() (fs.FS, bool) {
	sub, err := fs.Sub(embeddedWeb, "web")
	if err != nil {
		return nil, false
	}
	entries, err := fs.ReadDir(sub, ".")
	if err != nil || len(entries) == 0 {
		return nil, false
	}
	return sub, true
}
