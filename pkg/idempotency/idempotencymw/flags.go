// SPDX-License-Identifier: MIT

package idempotencymw

import (
	"flag"

	"github.com/lennylabs/lenny/pkg/common/envdefault"
)

// Flags holds the flag only the idempotency stage of the request pipeline
// reads.
type Flags struct {
	// MaxBodyBytes is the --idempotency-max-body-bytes flag.
	MaxBodyBytes *int64
}

// RegisterFlags registers these flags on fs and returns their values, which
// fs fills when it parses. Each flag's default is read from its environment
// variable, as the composition root read it before the flags moved here.
func RegisterFlags(fs *flag.FlagSet) *Flags {
	f := &Flags{}
	f.MaxBodyBytes = fs.Int64("idempotency-max-body-bytes",
		envdefault.Int64("LENNY_IDEMPOTENCY_MAX_BODY_BYTES", 8<<20),
		"§11.5 cap on the request body the idempotency middleware buffers and hashes. A request larger than this is rejected with 413 BODY_TOO_LARGE before reaching the inner handler. Default 8 MiB covers the §11.5 critical operations (CreateSession ~10KB, FinalizeWorkspace and StartSession ~KB, Resume body that may carry a TaskResult). Operators raise it when their delegation payloads (taskInput) or replay/derive bodies exceed the default. Override via LENNY_IDEMPOTENCY_MAX_BODY_BYTES.")
	return f
}
