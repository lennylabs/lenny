// SPDX-License-Identifier: MIT

package podregistry

import "time"

// watchTickerInterval is the §12.6 PodEvent polling cadence the v1
// implementation uses in lieu of a controller-runtime informer.
// 500ms keeps the §12.6 documented event-latency budget under the
// 500ms P99 target while avoiding a Kubernetes API hot loop.
const watchTickerInterval = 500 * time.Millisecond

// watchTicker wraps a time.Ticker so the registry can be exercised
// against a faster cadence from tests without exposing time.Ticker
// internals through the public API.
type watchTicker struct{ t *time.Ticker }

func newWatchTicker(interval time.Duration) *watchTicker {
	if interval <= 0 {
		interval = watchTickerInterval
	}
	return &watchTicker{t: time.NewTicker(interval)}
}

func (w *watchTicker) C() <-chan time.Time { return w.t.C }
func (w *watchTicker) Stop()               { w.t.Stop() }
