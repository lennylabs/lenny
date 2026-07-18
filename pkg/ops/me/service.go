// SPDX-License-Identifier: MIT

// Package me implements the §4.0 / §25.4 caller identity discovery
// surface: `/v1/admin/me` and `/v1/admin/me/authorized-tools`. v1
// runs in two places — the gateway's admin router calls into this
// package directly so a logged-in agent can introspect their own
// principal, and `lenny-ops` re-exposes the same endpoints over its
// admin surface via the gateway client documented in §4.0.
//
// The package is transport-agnostic: callers pass a caller principal
// (the §10.2 auth.Principal) and receive a structured response. The
// admin handler in pkg/gateway/admin/me.go is a thin wrapper that
// renders the JSON.
package me

import (
	"sort"

	"github.com/lennylabs/lenny/pkg/auth"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// Identity is the §25.4 caller identity record returned by the
// `/v1/admin/me` endpoint.
type Identity struct {
	Subject    string      `json:"subject"`
	TenantID   string      `json:"tenantId"`
	SessionID  string      `json:"sessionId,omitempty"`
	CallerType string      `json:"callerType,omitempty"`
	Typ        string      `json:"typ,omitempty"`
	Roles      []auth.Role `json:"roles"`
}

// AuthorizedTool is one tool entry returned by the
// `/v1/admin/me/authorized-tools` endpoint.
type AuthorizedTool struct {
	Tool        string `json:"tool"`
	Scope       string `json:"scope"`
	Category    string `json:"category"`
	Description string `json:"description,omitempty"`
	// MinRole is the lowest role that grants access to the tool. It is
	// not rendered in the JSON response, but callers (the gateway admin
	// router, the lenny-ops gateway-client re-exposure) use it to gate
	// the per-caller filter in Authorized.
	MinRole auth.Role `json:"-"`
}

// Service is the §4.0 caller-identity discovery service. It owns no
// state; the methods are pure functions over the supplied principal
// and tool catalog.
type Service struct {
	catalog []AuthorizedTool
}

// NewService returns a Service backed by the supplied admin-tool
// catalog. The catalog is captured by reference — callers that
// mutate it after constructing the Service will see the change.
func NewService(catalog []AuthorizedTool) *Service {
	return &Service{catalog: catalog}
}

// Me renders the caller's identity from the supplied principal. The
// returned Roles slice is a copy so the caller can mutate it without
// touching the principal.
func (s *Service) Me(p authmw.Principal) Identity {
	return Identity{
		Subject:    p.Subject,
		TenantID:   p.TenantID,
		SessionID:  p.SessionID,
		CallerType: p.CallerType,
		Typ:        string(p.Typ),
		Roles:      append([]auth.Role(nil), p.Roles...),
	}
}

// Authorized returns every tool from the catalog whose MinRole is
// satisfied by the principal's role set and whose Scope the
// principal's §25.1 scope claim permits, sorted by Tool name.
//
// spec: §25.1 line 96 — "`/v1/admin/me/authorized-tools` — pre-filters
// the tool list to what the caller's scopes permit." The role check
// alone only enforces the role ceiling; a narrowly-scoped token must
// see a further-reduced list, not the full role-eligible catalog.
func (s *Service) Authorized(p authmw.Principal) []AuthorizedTool {
	out := make([]AuthorizedTool, 0, len(s.catalog))
	for _, t := range s.catalog {
		if !p.HasRole(t.MinRole) {
			continue
		}
		if t.Scope != "" && !p.HasScope(t.Scope) {
			continue
		}
		out = append(out, AuthorizedTool{
			Tool:        t.Tool,
			Scope:       t.Scope,
			Category:    t.Category,
			Description: t.Description,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tool < out[j].Tool })
	return out
}
