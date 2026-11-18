// SPDX-License-Identifier: MIT

package metering

import (
	"flag"
	"time"

	"github.com/lennylabs/lenny/pkg/common/envdefault"
)

// Flags holds the flags only the usage recorder reads.
type Flags struct {
	// ExtensionWaitTimeout is the in-path wait for a budget extension the
	// recorder applies through SetExtensionWaitTimeout.
	ExtensionWaitTimeout *time.Duration
}

// RegisterFlags registers the usage recorder's flags on fs and returns their
// values, which fs fills when it parses.
func RegisterFlags(fs *flag.FlagSet) *Flags {
	f := &Flags{}
	f.ExtensionWaitTimeout = fs.Duration("proxy-extension-wait-timeout", envdefault.Duration("LENNY_PROXY_EXTENSION_WAIT_TIMEOUT", DefaultExtensionWaitTimeout),
		"§8.6 proxyExtensionWaitTimeout: how long the gateway LLM Proxy waits in-path for a budget-exhaustion lease extension to resolve before falling through to a recover-on-next-request BUDGET_EXHAUSTED. A fast auto or quick-approval extension resolves within this window and is transparent; a slower elicitation continues out-of-band. §8.6 does not fix this value; it is operator-tunable. Default 5s. Override via LENNY_PROXY_EXTENSION_WAIT_TIMEOUT.")
	return f
}
