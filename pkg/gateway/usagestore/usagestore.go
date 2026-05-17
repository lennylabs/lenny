// SPDX-License-Identifier: MIT

// Package usagestore is the §15.1 usage / metering accumulator. It
// records per-tenant and per-runtime session and token counts so
// `GET /v1/usage` can return the §15.1 aggregated usage object.
//
// The store is an in-memory accumulator; production swaps in the
// §11.2.1 billing-event-stream-backed aggregator behind the same
// Record / Aggregate surface.
package usagestore

import (
	"context"
	"sort"
	"sync"
)

// Tokens is the §15.1 input/output token pair.
type Tokens struct {
	Input  int64 `json:"input"`
	Output int64 `json:"output"`
}

// Record is one usage event — a session's accumulated consumption.
type Record struct {
	TenantID   string
	Runtime    string
	Sessions   int64
	Tokens     Tokens
	PodMinutes float64
}

// TenantUsage is the §15.1 per-tenant usage rollup.
type TenantUsage struct {
	TenantID string `json:"tenantId"`
	Sessions int64  `json:"sessions"`
	Tokens   Tokens `json:"tokens"`
}

// RuntimeUsage is the §15.1 per-runtime usage rollup.
type RuntimeUsage struct {
	Runtime  string `json:"runtime"`
	Sessions int64  `json:"sessions"`
	Tokens   Tokens `json:"tokens"`
}

// Report is the §15.1 GET /v1/usage aggregated object.
type Report struct {
	TotalSessions   int64          `json:"totalSessions"`
	TotalTokens     Tokens         `json:"totalTokens"`
	TotalPodMinutes float64        `json:"totalPodMinutes"`
	ByTenant        []TenantUsage  `json:"byTenant"`
	ByRuntime       []RuntimeUsage `json:"byRuntime"`
}

// Store is the §15.1 usage accumulator contract.
type Store interface {
	// Record adds a usage event to the accumulator.
	Record(ctx context.Context, r Record) error

	// Aggregate returns the §15.1 usage report. When tenantFilter is
	// non-empty the report is scoped to that one tenant.
	Aggregate(ctx context.Context, tenantFilter string) (Report, error)
}

// Memory is the in-memory Store implementation.
type Memory struct {
	mu      sync.Mutex
	records []Record
}

// NewMemory returns an empty Memory store.
func NewMemory() *Memory { return &Memory{} }

// Record implements Store.
func (m *Memory) Record(_ context.Context, r Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records = append(m.records, r)
	return nil
}

// Aggregate implements Store.
func (m *Memory) Aggregate(_ context.Context, tenantFilter string) (Report, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var report Report
	byTenant := map[string]*TenantUsage{}
	byRuntime := map[string]*RuntimeUsage{}

	for _, rec := range m.records {
		if tenantFilter != "" && rec.TenantID != tenantFilter {
			continue
		}
		report.TotalSessions += rec.Sessions
		report.TotalTokens.Input += rec.Tokens.Input
		report.TotalTokens.Output += rec.Tokens.Output
		report.TotalPodMinutes += rec.PodMinutes

		tu := byTenant[rec.TenantID]
		if tu == nil {
			tu = &TenantUsage{TenantID: rec.TenantID}
			byTenant[rec.TenantID] = tu
		}
		tu.Sessions += rec.Sessions
		tu.Tokens.Input += rec.Tokens.Input
		tu.Tokens.Output += rec.Tokens.Output

		if rec.Runtime != "" {
			ru := byRuntime[rec.Runtime]
			if ru == nil {
				ru = &RuntimeUsage{Runtime: rec.Runtime}
				byRuntime[rec.Runtime] = ru
			}
			ru.Sessions += rec.Sessions
			ru.Tokens.Input += rec.Tokens.Input
			ru.Tokens.Output += rec.Tokens.Output
		}
	}

	report.ByTenant = make([]TenantUsage, 0, len(byTenant))
	for _, tu := range byTenant {
		report.ByTenant = append(report.ByTenant, *tu)
	}
	sort.Slice(report.ByTenant, func(i, j int) bool {
		return report.ByTenant[i].TenantID < report.ByTenant[j].TenantID
	})

	report.ByRuntime = make([]RuntimeUsage, 0, len(byRuntime))
	for _, ru := range byRuntime {
		report.ByRuntime = append(report.ByRuntime, *ru)
	}
	sort.Slice(report.ByRuntime, func(i, j int) bool {
		return report.ByRuntime[i].Runtime < report.ByRuntime[j].Runtime
	})
	return report, nil
}
