// SPDX-License-Identifier: MIT

package gatewayreader_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/drift"
	"github.com/lennylabs/lenny/pkg/ops/driftservice/gatewayreader"
)

// fakeAdmin serves canned list responses keyed by the request path's
// base (the part before "?"). Each base maps to a sequence of pages so a
// test can exercise cursor pagination; a single-element slice is one
// page. A path whose base has no entry returns an empty single page.
type fakeAdmin struct {
	pages map[string][]page
	calls []string
	err   error
}

type page struct {
	items   []map[string]any
	cursor  string
	hasMore bool
}

func (f *fakeAdmin) Get(_ context.Context, path string, out any) error {
	f.calls = append(f.calls, path)
	if f.err != nil {
		return f.err
	}
	base := path
	var query string
	if i := strings.IndexByte(path, '?'); i >= 0 {
		base, query = path[:i], path[i+1:]
	}
	pages := f.pages[base]
	idx := 0
	// Resolve the requested page from the cursor query param. The reader
	// passes cursor="" on the first page and the prior page's Cursor on
	// each subsequent page; the fake maps that cursor back to the next
	// page index.
	q, _ := url.ParseQuery(query)
	if c := q.Get("cursor"); c != "" {
		for i, p := range pages {
			if p.cursor == c {
				idx = i + 1
				break
			}
		}
	}
	resp := page{}
	if idx < len(pages) {
		resp = pages[idx]
	}
	env := map[string]any{
		"items":   resp.items,
		"cursor":  resp.cursor,
		"hasMore": resp.hasMore,
	}
	raw, _ := json.Marshal(env)
	return json.Unmarshal(raw, out)
}

func single(items ...map[string]any) []page {
	return []page{{items: items}}
}

// spec: §25.10 line 3770 — running state read from the four admin LIST
// endpoints and normalized into the snapshot's resource-keyed structure.
func TestRunningStateAllScopeCollectsEveryResourceType_spec_25_10_3770(t *testing.T) {
	f := &fakeAdmin{pages: map[string][]page{
		"/v1/admin/runtimes":         single(map[string]any{"name": "echo", "image": "echo:1"}),
		"/v1/admin/pools":            single(map[string]any{"name": "default-gvisor", "warmCount": float64(5)}),
		"/v1/admin/tenants":          single(map[string]any{"id": "acme", "workspaceTier": "T2"}),
		"/v1/admin/credential-pools": single(map[string]any{"name": "anthropic", "provider": "anthropic"}),
	}}
	r := gatewayreader.New(f)
	state, err := r.RunningState(context.Background(), gatewayreader.ScopeAll)
	if err != nil {
		t.Fatalf("RunningState: %v", err)
	}
	runtimes, ok := state["runtimes"].(map[string]any)
	if !ok || runtimes["echo"] == nil {
		t.Fatalf("runtimes not collected: %#v", state["runtimes"])
	}
	pools := state["pools"].(map[string]any)
	if pools["default-gvisor"] == nil {
		t.Fatalf("pools not collected: %#v", state["pools"])
	}
	tenants := state["tenants"].(map[string]any)
	if tenants["acme"] == nil {
		t.Fatalf("tenants not collected: %#v", state["tenants"])
	}
	// Credential pools are keyed "{tenantId}/{name}".
	creds := state["credential-pools"].(map[string]any)
	if creds["acme/anthropic"] == nil {
		t.Fatalf("credential-pools not keyed by tenantId/name: %#v", state["credential-pools"])
	}
}

// spec: §25.10 line 3828 — a narrow scope collects only its resource type.
func TestRunningStateNarrowScopeCollectsOnlyOneType_spec_25_10_3828(t *testing.T) {
	f := &fakeAdmin{pages: map[string][]page{
		"/v1/admin/pools":    single(map[string]any{"name": "p1"}),
		"/v1/admin/runtimes": single(map[string]any{"name": "echo"}),
	}}
	r := gatewayreader.New(f)
	state, err := r.RunningState(context.Background(), gatewayreader.ScopePools)
	if err != nil {
		t.Fatalf("RunningState: %v", err)
	}
	if _, ok := state["pools"]; !ok {
		t.Fatal("pools scope did not collect pools")
	}
	if _, ok := state["runtimes"]; ok {
		t.Fatal("pools scope collected runtimes — narrow scope leaked")
	}
	// The pools endpoint must be the only one hit.
	for _, c := range f.calls {
		if !strings.HasPrefix(c, "/v1/admin/pools") {
			t.Errorf("pools scope hit unexpected endpoint %q", c)
		}
	}
}

// spec: §25.10 line 3773 — observed/server fields must not surface as
// spurious drift, so normalization strips them.
func TestNormalizationStripsObservedFields_spec_25_10_3773(t *testing.T) {
	f := &fakeAdmin{pages: map[string][]page{
		"/v1/admin/pools": single(map[string]any{
			"name":             "p1",
			"warmCount":        float64(3),
			"isolationProfile": "gvisor",
			// Server-generated / observed fields that the desired snapshot
			// never declares:
			"etag":           `"42"`,
			"createdAt":      "2026-01-01T00:00:00Z",
			"updatedAt":      "2026-02-01T00:00:00Z",
			"syncStatus":     "synced",
			"phase":          "active",
			"activeSessions": float64(7),
			"idlePodCount":   float64(2),
			"poolCondition":  "PoolWarmingUp",
		}),
	}}
	r := gatewayreader.New(f)
	state, err := r.RunningState(context.Background(), gatewayreader.ScopePools)
	if err != nil {
		t.Fatalf("RunningState: %v", err)
	}
	p1 := state["pools"].(map[string]any)["p1"].(map[string]any)
	for _, dropped := range []string{"etag", "createdAt", "updatedAt", "syncStatus", "phase", "activeSessions", "idlePodCount", "poolCondition"} {
		if _, present := p1[dropped]; present {
			t.Errorf("observed field %q was not stripped from normalized pool", dropped)
		}
	}
	// Config fields are retained.
	if p1["warmCount"] != float64(3) || p1["isolationProfile"] != "gvisor" {
		t.Errorf("config fields lost during normalization: %#v", p1)
	}
}

// spec: §25.10 line 3770 — normalized running state diffs clean against a
// matching desired snapshot (no false drift). This is the core
// correctness invariant the reopened finding flagged: an unwired reader
// reported every desired field as removed drift.
func TestNormalizedStateDiffsCleanAgainstMatchingDesired_spec_25_10_3770(t *testing.T) {
	f := &fakeAdmin{pages: map[string][]page{
		"/v1/admin/pools": single(map[string]any{
			"name":      "default-gvisor",
			"warmCount": float64(5),
			// Observed fields present on the wire; the desired snapshot omits
			// them.
			"etag":       `"9"`,
			"syncStatus": "synced",
			"phase":      "active",
		}),
	}}
	r := gatewayreader.New(f)
	running, err := r.RunningState(context.Background(), gatewayreader.ScopePools)
	if err != nil {
		t.Fatalf("RunningState: %v", err)
	}
	// Desired carries only the config fields, as a refreshed snapshot would.
	desired := map[string]any{
		"pools": map[string]any{
			"default-gvisor": map[string]any{"warmCount": float64(5)},
		},
	}
	if changes := drift.Diff(desired, running); len(changes) != 0 {
		t.Fatalf("normalized running state drifted against matching desired: %#v", changes)
	}
	// A genuine config change is still detected.
	desired["pools"].(map[string]any)["default-gvisor"].(map[string]any)["warmCount"] = float64(8)
	if changes := drift.Diff(desired, running); len(changes) != 1 || changes[0].Kind != drift.Modified {
		t.Fatalf("genuine warmCount drift not detected: %#v", changes)
	}
}

// spec: §15.1 — the admin LIST endpoints paginate; the collector must
// follow the cursor until hasMore is false.
func TestRunningStateFollowsPagination_spec_15_1(t *testing.T) {
	f := &fakeAdmin{pages: map[string][]page{
		"/v1/admin/runtimes": {
			{items: []map[string]any{{"name": "r1"}}, cursor: "c1", hasMore: true},
			{items: []map[string]any{{"name": "r2"}}, cursor: "", hasMore: false},
		},
	}}
	r := gatewayreader.New(f)
	state, err := r.RunningState(context.Background(), gatewayreader.ScopeRuntimes)
	if err != nil {
		t.Fatalf("RunningState: %v", err)
	}
	runtimes := state["runtimes"].(map[string]any)
	if runtimes["r1"] == nil || runtimes["r2"] == nil {
		t.Fatalf("pagination dropped a page: %#v", runtimes)
	}
	if len(f.calls) != 2 {
		t.Errorf("expected 2 paged calls, got %d: %v", len(f.calls), f.calls)
	}
}

// spec: §25.10 line 3828 — an unrecognized scope collects nothing rather
// than erroring, preserving the empty-running-state posture.
func TestRunningStateUnknownScopeIsEmpty_spec_25_10_3828(t *testing.T) {
	f := &fakeAdmin{pages: map[string][]page{
		"/v1/admin/pools": single(map[string]any{"name": "p1"}),
	}}
	r := gatewayreader.New(f)
	state, err := r.RunningState(context.Background(), "quotas-typo")
	if err != nil {
		t.Fatalf("RunningState: %v", err)
	}
	if len(state) != 0 {
		t.Errorf("unknown scope collected %#v, want empty", state)
	}
	if len(f.calls) != 0 {
		t.Errorf("unknown scope issued admin calls: %v", f.calls)
	}
}

// spec: §25.10 — a gateway-client error propagates so the drift report
// surfaces the outage rather than silently reporting an empty state.
func TestRunningStatePropagatesClientError(t *testing.T) {
	f := &fakeAdmin{err: errors.New("connection refused")}
	r := gatewayreader.New(f)
	if _, err := r.RunningState(context.Background(), gatewayreader.ScopeRuntimes); err == nil {
		t.Fatal("expected client error to propagate, got nil")
	}
}

// An item missing its key field cannot be keyed into the resource map
// and is skipped rather than colliding on the empty key.
func TestListSkipsItemWithoutKeyField(t *testing.T) {
	f := &fakeAdmin{pages: map[string][]page{
		"/v1/admin/runtimes": single(
			map[string]any{"name": "good"},
			map[string]any{"image": "no-name:1"}, // no "name"
		),
	}}
	r := gatewayreader.New(f)
	state, err := r.RunningState(context.Background(), gatewayreader.ScopeRuntimes)
	if err != nil {
		t.Fatalf("RunningState: %v", err)
	}
	runtimes := state["runtimes"].(map[string]any)
	if len(runtimes) != 1 || runtimes["good"] == nil {
		t.Fatalf("nameless item was not skipped: %#v", runtimes)
	}
}

// Credential-pool collection enumerates each tenant and passes its
// tenantId on the LIST request (§4.9 tenant-scoped credential pools).
func TestCredentialPoolsEnumeratePerTenant_spec_4_9(t *testing.T) {
	f := &fakeAdmin{pages: map[string][]page{
		"/v1/admin/tenants": single(
			map[string]any{"id": "acme"},
			map[string]any{"id": "globex"},
		),
		"/v1/admin/credential-pools": single(map[string]any{"name": "anthropic"}),
	}}
	r := gatewayreader.New(f)
	state, err := r.RunningState(context.Background(), gatewayreader.ScopeCredentialPools)
	if err != nil {
		t.Fatalf("RunningState: %v", err)
	}
	creds := state["credential-pools"].(map[string]any)
	if creds["acme/anthropic"] == nil || creds["globex/anthropic"] == nil {
		t.Fatalf("credential pools not collected per tenant: %#v", creds)
	}
	// Every credential-pools call carries a tenantId.
	var sawTenantID bool
	for _, c := range f.calls {
		if strings.HasPrefix(c, "/v1/admin/credential-pools") {
			sawTenantID = true
			if !strings.Contains(c, "tenantId=") {
				t.Errorf("credential-pools call missing tenantId: %q", c)
			}
		}
	}
	if !sawTenantID {
		t.Fatal("no credential-pools call issued")
	}
}

// The first page request omits the cursor param and sets the §15.1 page
// limit; this guards the request-building contract.
func TestFirstPageRequestShape_spec_15_1(t *testing.T) {
	f := &fakeAdmin{pages: map[string][]page{
		"/v1/admin/runtimes": single(map[string]any{"name": "echo"}),
	}}
	r := gatewayreader.New(f)
	if _, err := r.RunningState(context.Background(), gatewayreader.ScopeRuntimes); err != nil {
		t.Fatalf("RunningState: %v", err)
	}
	got := f.calls[0]
	if !strings.Contains(got, "limit=200") {
		t.Errorf("first page did not request the max limit: %q", got)
	}
	if strings.Contains(got, "cursor=") {
		t.Errorf("first page sent a cursor: %q", got)
	}
}
