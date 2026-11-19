// SPDX-License-Identifier: MIT

package uploadhandler

import (
	"flag"

	"github.com/lennylabs/lenny/pkg/common/envdefault"
)

// Flags holds the flags only the upload handler reads. Options copies their
// values.
type Flags struct {
	// MaxConcurrentPerSession is Options.MaxConcurrentUploadsPerSession.
	MaxConcurrentPerSession *int
	// MaxConcurrentGlobal is Options.MaxConcurrentUploadsGlobal.
	MaxConcurrentGlobal *int
	// MaxBytesPerSession is Options.MaxUploadBytesPerSession.
	MaxBytesPerSession *int64
	// MidSessionUpload is Options.MidSessionUploadEnabled.
	MidSessionUpload *bool
}

// RegisterFlags registers the upload handler's flags on fs and returns their
// values, which fs fills when it parses. Each flag's default is read from
// its environment variable.
//
// The §11.1 concurrent-upload and per-session upload-size admission caps
// are distinct from the §4.1 upload handler back-pressure semaphore. Zero
// leaves each scope unlimited; operators opt in.
func RegisterFlags(fs *flag.FlagSet) *Flags {
	f := &Flags{}
	f.MaxConcurrentPerSession = fs.Int("upload-max-concurrent-per-session",
		envdefault.Int("LENNY_UPLOAD_MAX_CONCURRENT_PER_SESSION", 0),
		"§11.1 per-session concurrent-upload admission cap. Excess in-flight uploads against one session are rejected with 429 RATE_LIMITED. Zero disables the per-session concurrency cap. Override via LENNY_UPLOAD_MAX_CONCURRENT_PER_SESSION. F-11.1.5.")
	f.MaxConcurrentGlobal = fs.Int("upload-max-concurrent-global",
		envdefault.Int("LENNY_UPLOAD_MAX_CONCURRENT_GLOBAL", 0),
		"§11.1 global (per-replica) concurrent-upload admission cap. Excess in-flight uploads across all sessions are rejected with 429 RATE_LIMITED. Zero disables the global concurrency cap. Override via LENNY_UPLOAD_MAX_CONCURRENT_GLOBAL. F-11.1.5.")
	f.MaxBytesPerSession = fs.Int64("upload-max-bytes-per-session",
		envdefault.Int64("LENNY_UPLOAD_MAX_BYTES_PER_SESSION", 0),
		"§11.1 per-session cumulative upload-size cap (bytes). The sum of all uploads in a session is rejected with 429 QUOTA_EXCEEDED past this value; the per-file cap is the separate 64 MiB body ceiling. Zero disables the per-session size cap. Override via LENNY_UPLOAD_MAX_BYTES_PER_SESSION. F-11.1.6.")
	f.MidSessionUpload = fs.Bool("mid-session-upload",
		envdefault.Bool("LENNY_MID_SESSION_UPLOAD", false),
		"§7.4 deployer policy: admit uploads into an already-running session (POST /v1/sessions/{id}/upload-to-session) when the bound runtime also declares capabilities.midSessionUpload. Off by default; override via LENNY_MID_SESSION_UPLOAD. F-7.4.6.")
	return f
}
