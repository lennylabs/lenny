// SPDX-License-Identifier: MIT

package sessionserver

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/uploadhandler"
)

// attachUploads attaches an upload handler built from opts to srv, as the
// composition root attaches it: every seam field of opts takes srv's own
// side of the operation, the handler registers its routes with
// srv, and srv closes upload windows through it. The internal tests use it
// rather than tests/testinfra/uploadwire, which imports this package. It is
// test wiring and carries no specification annotation.
func attachUploads(t *testing.T, srv *Server, opts uploadhandler.Options) *uploadhandler.Handler {
	t.Helper()
	core := srv.UploadSeam()
	opts.Sessions, opts.Identity, opts.Forwarding, opts.Workspaces = core, core, core, core
	opts.Tokens, opts.Quota, opts.Audit = core, core, core
	up, err := uploadhandler.New(opts)
	if err != nil {
		t.Fatalf("build the upload handler: %v", err)
	}
	if err := up.RegisterRoutes(srv); err != nil {
		t.Fatalf("register the upload routes: %v", err)
	}
	srv.SetUploadWindows(up)
	return up
}
