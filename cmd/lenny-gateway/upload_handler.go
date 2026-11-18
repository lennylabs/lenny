// SPDX-License-Identifier: MIT

package main

import (
	"log"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/core/subsystem"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/uploadhandler"
)

// attachUploadHandler builds the §4.1 upload handler seam and wires it to
// the session core through the seam interface: the handler reaches the core
// through the core's upload seam operations, registers its routes with the
// session server, which serves the route registration interface until the
// API front holds the request pipeline, and serves the core and the pod
// binder the operations that close a session's upload window and count an
// archive extraction abort. It runs before the session server serves. A
// wiring failure stops the process, as every other build step's does.
func attachUploadHandler(
	srv *sessionserver.Server,
	binder *podsession.Binder,
	blobs blobstore.Store,
	runtimes runtimestore.Store,
	gate *subsystem.Subsystem,
	metrics *uploadhandler.PromUploadMetrics,
	flags *uploadhandler.Flags,
) {
	core := srv.UploadSeam()
	up, err := uploadhandler.New(uploadhandler.Options{
		Sessions:   core,
		Identity:   core,
		Forwarding: core,
		Workspaces: core,
		Tokens:     core,
		Quota:      core,
		Audit:      core,
		Blobs:      blobs,
		Runtimes:   runtimes,
		Subsystem:  gate,
		Metrics:    metrics,
		// The §11.1 concurrent-upload and per-session upload-size
		// admission caps. F-11.1.5, F-11.1.6.
		MaxConcurrentUploadsPerSession: *flags.MaxConcurrentPerSession,
		MaxConcurrentUploadsGlobal:     *flags.MaxConcurrentGlobal,
		MaxUploadBytesPerSession:       *flags.MaxBytesPerSession,
		// The §7.4 mid-session upload deployer policy. F-7.4.6.
		MidSessionUploadEnabled: *flags.MidSessionUpload,
	})
	if err != nil {
		log.Fatalf("lenny-gateway: upload handler: %v", err)
	}
	if err := up.RegisterRoutes(srv); err != nil {
		log.Fatalf("lenny-gateway: upload handler routes: %v", err)
	}
	srv.SetUploadWindows(up)
	if binder != nil {
		binder.UploadWindows = up
	}
}
