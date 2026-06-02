// SPDX-License-Identifier: MIT

package eventsubscription

import (
	"context"
	"net"
	"net/netip"
)

// defaultResolver is the production Resolver: it delegates to
// net.DefaultResolver. spec: §25.5 line 2741.
type defaultResolver struct{}

func (defaultResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, network, host)
}
