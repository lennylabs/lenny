// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"sync/atomic"

	"github.com/lennylabs/lenny/pkg/gateway/derivelock"
)

// concurrentHoldCounter wraps a derivelock.Lock and records the maximum
// number of holders observed at any one time. Used by the
// MemoryLockSerializesConcurrentRequests test to prove serialization.
type concurrentHoldCounter struct {
	inner         derivelock.Lock
	held          atomic.Int32
	maxConcurrent atomic.Int32
}

func (c *concurrentHoldCounter) Acquire(ctx context.Context, sourceSessionID string) (derivelock.Releaser, error) {
	rel, err := c.inner.Acquire(ctx, sourceSessionID)
	if err != nil {
		return nil, err
	}
	now := c.held.Add(1)
	for {
		m := c.maxConcurrent.Load()
		if now <= m || c.maxConcurrent.CompareAndSwap(m, now) {
			break
		}
	}
	return func() {
		c.held.Add(-1)
		rel()
	}, nil
}
