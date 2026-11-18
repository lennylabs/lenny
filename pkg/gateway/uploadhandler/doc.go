// SPDX-License-Identifier: MIT

// Package uploadhandler is the gateway's upload handler seam. It serves the
// client upload endpoints: the plain and archive uploads, which stage a
// blob for a session in the created state, the mid-session upload, which
// writes files into a running session's workspace, and the blob read. It
// applies the upload handler's subsystem gate, which the session core's
// archive extraction shares, and owns the per-session and global upload
// admission limits, the abort registry that closes a session's upload
// window, and the upload metrics.
//
// The package reaches the session core only through pkg/gateway/seam: it
// looks sessions up, forwards a mid-session upload to the session's holder
// replica, verifies upload tokens, holds an upload against the tenant's
// storage quota, records the upload audit event, and writes a mid-session
// upload into the workspace through the core's operations, and it serves
// the core the operations that close a session's upload window and count
// an archive extraction abort. It registers its routes through the route
// registration interface. The archive extraction that runs while a
// session's workspace is materialized stays in the session core.
//
// spec: GWUPLOAD, UPLOAD
package uploadhandler
