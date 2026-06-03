// SPDX-License-Identifier: MIT

package prodsource

import (
	"context"
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/ops/diagnostics"
	"github.com/lennylabs/lenny/pkg/ops/gateway"
)

// gatewayGetter is the subset of the gateway admin client the pool-config
// reader uses, extracted so the reader is unit-testable against a stub.
type gatewayGetter interface {
	Get(ctx context.Context, path string, out any) error
}

// GatewayPoolReader is the §25.6 warm-pool config reader over the gateway
// admin API. It reads GET /v1/admin/pools/{name} for the pool's config
// summary and CRD sync status. spec: §25.6 line 2906 (gateway admin
// GetPoolConfig / GetPoolSyncStatus). F-25.6.1.
type GatewayPoolReader struct {
	client gatewayGetter
}

// NewGatewayPoolReader returns a GatewayPoolReader over the gateway admin
// client.
func NewGatewayPoolReader(client *gateway.Client) *GatewayPoolReader {
	return &GatewayPoolReader{client: client}
}

// Compile-time assertion that *GatewayPoolReader satisfies the seam.
var _ PoolConfig = (*GatewayPoolReader)(nil)

// poolConfigPayload mirrors the fields of the gateway admin pool GET
// response (pkg/gateway/admin.PoolPayload) the §25.6 pool diagnosis
// reads. Unread fields are omitted so the reader does not couple to the
// full admin contract.
type poolConfigPayload struct {
	Name       string `json:"name"`
	RuntimeRef string `json:"runtimeRef"`
	WarmCount  int    `json:"warmCount"`
	SyncStatus string `json:"syncStatus"`
}

// PoolConfig reads the pool's config summary and CRD sync status. A 404
// from the gateway returns found=false (the §25.6 POOL_NOT_FOUND path);
// any other transport error is returned so the caller marks the
// diagnosis degraded.
func (r *GatewayPoolReader) PoolConfig(ctx context.Context, poolName string) (diagnostics.PoolConfigSummary, bool, string, bool, error) {
	var payload poolConfigPayload
	err := r.client.Get(ctx, "/v1/admin/pools/"+poolName, &payload)
	if err != nil {
		var herr *gateway.HTTPError
		if errors.As(err, &herr) && herr.Status == http.StatusNotFound {
			return diagnostics.PoolConfigSummary{}, false, "", false, nil
		}
		return diagnostics.PoolConfigSummary{}, false, "", false, err
	}
	cfg := diagnostics.PoolConfigSummary{
		MinWarm: payload.WarmCount,
		Runtime: payload.RuntimeRef,
	}
	// spec/04 line 559 — "synced" means the CRD generation matches the
	// Postgres generation. Any other value ("pending", "unknown") is a
	// negative or unknown sync state; the gateway carries the raw token
	// as the detail.
	synced := payload.SyncStatus == "synced"
	return cfg, synced, payload.SyncStatus, true, nil
}
