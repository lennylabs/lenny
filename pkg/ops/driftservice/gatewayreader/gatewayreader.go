// SPDX-License-Identifier: MIT

// Package gatewayreader implements the §25.10 running-state collector:
// the driftservice.RunningStateReader that reads the live platform
// configuration from the gateway admin API and normalizes it into the
// same resource-keyed structure the bootstrap_seed_snapshot desired
// state uses, so the §25.10 field-by-field diff compares configuration
// against configuration.
//
// spec: §25.10 line 3770 ("Running state — read via GatewayClient calls
// to GET /v1/admin/runtimes, GET /v1/admin/pools, etc.").
//
// The admin LIST endpoints return more per-resource fields than the
// desired-state snapshot carries: an optimistic-concurrency etag,
// create/update/delete timestamps, and observed cluster status
// (pool sync status, lifecycle phase, idle/active counts). The
// normalization strips those server-generated and observed fields so
// they do not surface as spurious "added" drift against a desired
// snapshot that never declared them. F-25.10.4.
package gatewayreader

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

// §25.10 line 3828 comparison scopes. The empty string and "all" select
// every resource type; the narrow scopes restrict collection to one.
const (
	ScopeAll             = "all"
	ScopePools           = "pools"
	ScopeRuntimes        = "runtimes"
	ScopeTenants         = "tenants"
	ScopeCredentialPools = "credential-pools"
)

// adminPageLimit is the §15.1 admin LIST page-size cap (1-200). The
// collector requests the maximum so a Tier-3 platform pages as few
// times as possible.
const adminPageLimit = 200

// maxPages bounds the pagination loop so a gateway that returns
// hasMore=true with a non-advancing cursor cannot wedge the collector.
// At 200 items per page this covers 2,000,000 resources of one type,
// far past any real platform.
const maxPages = 10000

// AdminGetter is the subset of the §25.4 gateway admin-API client the
// running-state collector needs: an authenticated GET that decodes a
// JSON response into out. The production *pkg/ops/gateway.Client
// satisfies it; tests supply a fake.
type AdminGetter interface {
	Get(ctx context.Context, path string, out any) error
}

// listEnvelope is the §15.1 canonical paginated list response every
// admin LIST endpoint returns: an items array, an opaque continuation
// cursor, and the hasMore flag. Decoding items into map[string]any
// keeps each field as the same JSON-typed value the desired-state
// snapshot carries (numbers as float64), so drift.Diff compares like
// with like.
type listEnvelope struct {
	Items   []map[string]any `json:"items"`
	Cursor  string           `json:"cursor"`
	HasMore bool             `json:"hasMore"`
}

// commonObservedFields are the server-generated provenance fields every
// admin payload carries that the operator's desired-state snapshot does
// not declare: the §15.1 optimistic-concurrency etag (lines 1207-1209)
// and the server-clock create/update/delete timestamps. Stripping them
// keeps them out of the diff as spurious "added" drift.
var commonObservedFields = []string{"etag", "createdAt", "updatedAt", "deletedAt"}

// poolObservedFields are the live pool-status fields the admin GET
// populates from the running cluster rather than from the desired pool
// config: the bootstrap condition and idle count (§5.2 line 629), the
// active-session count and lifecycle phase (§15.1 line 797), the CRD
// reconciliation status (§4.6.2 line 559), and the cold-start bootstrap
// status object (§17.8.2 step 3).
var poolObservedFields = []string{
	"poolCondition", "idlePodCount", "activeSessions", "syncStatus", "phase", "bootstrapStatus",
}

// tenantObservedFields are the tenant-status fields populated from the
// running platform rather than from the desired tenant config: the last
// successful Tier-4 KMS probe timestamp (§10.2 / §12.7 residency-KMS
// liveness).
var tenantObservedFields = []string{"t4KmsLastProbeSuccessAt"}

// Reader is the §25.10 gateway-backed running-state collector. It is
// stateless beyond its admin client, so it is safe for concurrent use.
type Reader struct {
	client AdminGetter
}

// New returns a running-state reader over the given admin client.
func New(client AdminGetter) *Reader {
	return &Reader{client: client}
}

// RunningState collects the §25.10 running platform configuration for
// the comparison scope and returns it normalized into the resource-keyed
// structure the desired-state snapshot uses: a top-level key per
// resource type ("runtimes", "pools", "tenants", "credential-pools")
// mapping resource identity to its normalized config. The "all" scope
// (and any unrecognized scope) drives the wide collection; a narrow
// scope collects only its one resource type. §25.10 lines 3770, 3828.
func (r *Reader) RunningState(ctx context.Context, scope string) (map[string]any, error) {
	out := map[string]any{}
	switch scope {
	case "", ScopeAll:
		if err := r.collectRuntimes(ctx, out); err != nil {
			return nil, err
		}
		if err := r.collectPools(ctx, out); err != nil {
			return nil, err
		}
		if err := r.collectTenants(ctx, out); err != nil {
			return nil, err
		}
		if err := r.collectCredentialPools(ctx, out); err != nil {
			return nil, err
		}
	case ScopeRuntimes:
		return out, r.collectRuntimes(ctx, out)
	case ScopePools:
		return out, r.collectPools(ctx, out)
	case ScopeTenants:
		return out, r.collectTenants(ctx, out)
	case ScopeCredentialPools:
		return out, r.collectCredentialPools(ctx, out)
	default:
		// §25.10 line 3828 enumerates the valid scopes. An unrecognized
		// value collects nothing rather than erroring, so a caller typo
		// reports an empty running state (no drift) instead of failing the
		// request — the pre-wiring empty-running-state posture.
	}
	return out, nil
}

// collectRuntimes reads GET /v1/admin/runtimes and stores the normalized
// runtimes under out["runtimes"], keyed by runtime name.
func (r *Reader) collectRuntimes(ctx context.Context, out map[string]any) error {
	m, err := r.listInto(ctx, "/v1/admin/runtimes", nil, "name", nil)
	if err != nil {
		return err
	}
	out[ScopeRuntimes] = m
	return nil
}

// collectPools reads GET /v1/admin/pools and stores the normalized pools
// under out["pools"], keyed by pool name. Observed pool-status fields
// are stripped so a synced pool reports no drift against its desired
// config.
func (r *Reader) collectPools(ctx context.Context, out map[string]any) error {
	m, err := r.listInto(ctx, "/v1/admin/pools", nil, "name", poolObservedFields)
	if err != nil {
		return err
	}
	out[ScopePools] = m
	return nil
}

// collectTenants reads GET /v1/admin/tenants and stores the normalized
// tenants under out["tenants"], keyed by tenant id.
func (r *Reader) collectTenants(ctx context.Context, out map[string]any) error {
	m, err := r.listInto(ctx, "/v1/admin/tenants", nil, "id", tenantObservedFields)
	if err != nil {
		return err
	}
	out[ScopeTenants] = m
	return nil
}

// collectCredentialPools reads the §4.9 credential pools of every
// tenant and stores them under out["credential-pools"], keyed by
// "{tenantId}/{name}" so two tenants' identically-named pools do not
// collide. The admin LIST endpoint requires a tenantId, so the
// collector first enumerates tenant ids, then lists each tenant's pools.
func (r *Reader) collectCredentialPools(ctx context.Context, out map[string]any) error {
	ids, err := r.tenantIDs(ctx)
	if err != nil {
		return err
	}
	pools := map[string]any{}
	for _, id := range ids {
		// tenantId is part of the composite "{tenantId}/{name}" key, so it
		// is dropped from the value to avoid repeating identity as config.
		m, listErr := r.listInto(ctx, "/v1/admin/credential-pools", url.Values{"tenantId": {id}}, "name", []string{"tenantId"})
		if listErr != nil {
			return listErr
		}
		for name, cfg := range m {
			pools[id+"/"+name] = cfg
		}
	}
	out[ScopeCredentialPools] = pools
	return nil
}

// tenantIDs lists every tenant id so credential-pool collection can
// enumerate the tenant-scoped pools.
func (r *Reader) tenantIDs(ctx context.Context) ([]string, error) {
	cursor := ""
	var ids []string
	for page := 0; page < maxPages; page++ {
		var env listEnvelope
		if err := r.client.Get(ctx, pagePath("/v1/admin/tenants", nil, cursor), &env); err != nil {
			return nil, fmt.Errorf("gateway running-state: GET /v1/admin/tenants: %w", err)
		}
		for _, item := range env.Items {
			if id, ok := item["id"].(string); ok && id != "" {
				ids = append(ids, id)
			}
		}
		if !env.HasMore || env.Cursor == "" || env.Cursor == cursor {
			break
		}
		cursor = env.Cursor
	}
	return ids, nil
}

// listInto pages through an admin LIST endpoint and returns a map from
// each item's keyField value to its normalized config. base carries any
// fixed query parameters (e.g. tenantId); observed names the per-type
// observed fields stripped in addition to the common provenance fields.
func (r *Reader) listInto(ctx context.Context, basePath string, base url.Values, keyField string, observed []string) (map[string]any, error) {
	drop := make(map[string]bool, len(commonObservedFields)+len(observed)+1)
	for _, f := range commonObservedFields {
		drop[f] = true
	}
	for _, f := range observed {
		drop[f] = true
	}
	// The identity field becomes the resource map key, so drop it from the
	// value — a desired snapshot keys resources by identity and does not
	// repeat the name/id inside the config subtree. F-25.10.4.
	drop[keyField] = true
	result := map[string]any{}
	cursor := ""
	for page := 0; page < maxPages; page++ {
		var env listEnvelope
		if err := r.client.Get(ctx, pagePath(basePath, base, cursor), &env); err != nil {
			return nil, fmt.Errorf("gateway running-state: GET %s: %w", basePath, err)
		}
		for _, item := range env.Items {
			key, ok := item[keyField].(string)
			if !ok || key == "" {
				// An item without a usable identity cannot be keyed into the
				// resource map; skip it rather than collide on the empty key.
				continue
			}
			result[key] = stripFields(item, drop)
		}
		if !env.HasMore || env.Cursor == "" || env.Cursor == cursor {
			break
		}
		cursor = env.Cursor
	}
	return result, nil
}

// pagePath builds the admin LIST request path with the page-size cap,
// any fixed base parameters, and the continuation cursor (omitted on the
// first page).
func pagePath(basePath string, base url.Values, cursor string) string {
	q := url.Values{}
	for k, vs := range base {
		q[k] = append([]string(nil), vs...)
	}
	q.Set("limit", strconv.Itoa(adminPageLimit))
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	return basePath + "?" + q.Encode()
}

// stripFields returns a copy of item with the drop keys removed, so the
// normalized running config omits the server-generated provenance and
// observed-status fields the desired snapshot does not carry.
func stripFields(item map[string]any, drop map[string]bool) map[string]any {
	out := make(map[string]any, len(item))
	for k, v := range item {
		if drop[k] {
			continue
		}
		out[k] = v
	}
	return out
}
