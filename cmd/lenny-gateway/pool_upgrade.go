// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
)

// poolSpecReader adapts the §15.1 pool catalog to the
// runtimeupgrade.PoolReader seam. Start uses it to confirm the target
// pool exists and to capture its current configuration as
// previousPoolSpec, which §10.5 line 507 preserves for rollback until
// the upgrade reaches Complete. A missing pool returns ok=false so Start
// maps it to 404. spec: §10.5 lines 466-540.
type poolSpecReader struct {
	store poolstore.Store
}

func (p poolSpecReader) PoolSpec(ctx context.Context, pool string) ([]byte, bool, error) {
	rec, err := p.store.Get(ctx, pool)
	if errors.Is(err, poolstore.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	spec, err := json.Marshal(rec)
	if err != nil {
		return nil, false, err
	}
	return spec, true, nil
}
