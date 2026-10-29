// SPDX-License-Identifier: MIT

// Package auditquery decodes the §25.9 audit-events list response for the
// Kind-backed audit-chain tests, which read the §11.7 chain verdict
// through the list endpoint. §25.9 defines no separate verify route: the
// list response carries the verifier verdict in its top-level
// `chainIntegrityReport` tally and in each OCSF record's
// `unmapped.lenny_chain.integrity`, computed over the tenant's full
// ordered chain.
//
// It is test infrastructure: no product path calls it.
//
// spec: §25.9 (Audit Log Query API), §11.7 (Audit Logging)
package auditquery

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
)

// MaxLimit is the §25.9 upper bound on the list endpoint's `limit`.
const MaxLimit = 1000

// ListPath returns the §25.9 audit-events list path for one tenant. The
// since and until bounds are optional RFC 3339 timestamps; when both are
// empty the endpoint applies its default 24-hour look-back. cursor is the
// opaque `nextCursor` of the previous page, or empty for the first page.
func ListPath(tenant, since, until, cursor string) string {
	q := url.Values{}
	q.Set("tenantId", tenant)
	q.Set("limit", strconv.Itoa(MaxLimit))
	if since != "" {
		q.Set("since", since)
	}
	if until != "" {
		q.Set("until", until)
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	return "/v1/admin/audit-events?" + q.Encode()
}

// Report is the §25.9 `chainIntegrityReport` envelope field.
type Report struct {
	Verified            int `json:"verified"`
	Broken              int `json:"broken"`
	GapSuspected        int `json:"gap_suspected"`
	RechainedPostOutage int `json:"rechained_post_outage"`
	RedactedGDPR        int `json:"redacted_gdpr"`
	Unchecked           int `json:"unchecked"`
}

// add accumulates another page's tally.
func (r *Report) add(o Report) {
	r.Verified += o.Verified
	r.Broken += o.Broken
	r.GapSuspected += o.GapSuspected
	r.RechainedPostOutage += o.RechainedPostOutage
	r.RedactedGDPR += o.RedactedGDPR
	r.Unchecked += o.Unchecked
}

// Event is the subset of one OCSF record the chain tests read.
type Event struct {
	// Seq is the row's per-tenant sequence number (metadata.sequence).
	Seq uint64
	// TenantUID is the chain's tenant (metadata.tenant_uid).
	TenantUID string
	// Integrity is the row's §11.7 verdict (unmapped.lenny_chain.integrity).
	Integrity string
	// PrevHash is the row's chain link (unmapped.lenny_chain.prev_hash).
	PrevHash string
}

// Page is one decoded list response.
type Page struct {
	// TenantID is the envelope's tenant.
	TenantID string
	// Events are the page's records in response order.
	Events []Event
	// Report is the page's chainIntegrityReport. A response without one
	// is a decode error, because every §25.9 list response carries it.
	Report Report
	// NextCursor is the opaque cursor of the next page, empty on the last.
	NextCursor string
}

type wireRecord struct {
	Metadata struct {
		Sequence  uint64 `json:"sequence"`
		TenantUID string `json:"tenant_uid"`
	} `json:"metadata"`
	Unmapped struct {
		Chain struct {
			PrevHash  string `json:"prev_hash"`
			Integrity string `json:"integrity"`
		} `json:"lenny_chain"`
	} `json:"unmapped"`
}

type wireEnvelope struct {
	TenantID             string       `json:"tenantId"`
	Items                []wireRecord `json:"items"`
	ChainIntegrityReport *Report      `json:"chainIntegrityReport"`
	NextCursor           string       `json:"nextCursor"`
}

// ParsePage decodes one §25.9 list response body.
func ParsePage(body []byte) (Page, error) {
	var env wireEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return Page{}, fmt.Errorf("decode the audit-events list response: %w", err)
	}
	if env.ChainIntegrityReport == nil {
		return Page{}, fmt.Errorf("the audit-events list response carries no chainIntegrityReport")
	}
	p := Page{TenantID: env.TenantID, Report: *env.ChainIntegrityReport, NextCursor: env.NextCursor}
	for _, it := range env.Items {
		p.Events = append(p.Events, Event{
			Seq:       it.Metadata.Sequence,
			TenantUID: it.Metadata.TenantUID,
			Integrity: it.Unmapped.Chain.Integrity,
			PrevHash:  it.Unmapped.Chain.PrevHash,
		})
	}
	return p, nil
}

// Chain is the verdict over every page of one tenant's list.
type Chain struct {
	// TenantID is the tenant the pages were requested for.
	TenantID string
	// Events are every page's records, sorted by sequence number.
	Events []Event
	// Report is the sum of the pages' chainIntegrityReport tallies.
	Report Report
}

// Fetch is one page request: it returns the HTTP status and body of the
// list path it is given.
type Fetch func(path string) (status int, body []byte, err error)

// maxPages bounds the pagination walk so a cursor that never empties
// fails the read instead of looping.
const maxPages = 100

// ReadChain walks every page of the tenant's list through fetch and
// returns the combined verdict. A non-200 page, an undecodable page, an
// envelope naming another tenant, or a record naming another tenant is an
// error.
func ReadChain(tenant, since, until string, fetch Fetch) (Chain, error) {
	out := Chain{TenantID: tenant}
	cursor := ""
	for page := 0; page < maxPages; page++ {
		status, body, err := fetch(ListPath(tenant, since, until, cursor))
		if err != nil {
			return Chain{}, err
		}
		if status != 200 {
			return Chain{}, &StatusError{Status: status, Body: string(body)}
		}
		p, err := ParsePage(body)
		if err != nil {
			return Chain{}, err
		}
		if p.TenantID != tenant {
			return Chain{}, fmt.Errorf("the audit-events list for tenant %s names tenant %q in its envelope", tenant, p.TenantID)
		}
		for _, ev := range p.Events {
			if ev.TenantUID != tenant {
				return Chain{}, fmt.Errorf("the audit-events list for tenant %s returned seq %d of tenant %q", tenant, ev.Seq, ev.TenantUID)
			}
		}
		out.Events = append(out.Events, p.Events...)
		out.Report.add(p.Report)
		if p.NextCursor == "" {
			sort.Slice(out.Events, func(i, j int) bool { return out.Events[i].Seq < out.Events[j].Seq })
			return out, nil
		}
		cursor = p.NextCursor
	}
	return Chain{}, fmt.Errorf("the audit-events list for tenant %s did not end within %d pages", tenant, maxPages)
}

// StatusError is a non-200 list response.
type StatusError struct {
	Status int
	Body   string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("the audit-events list returned status %d (body %q)", e.Status, e.Body)
}

// Integrity returns the verdict of the row with sequence number seq, and
// whether that row was listed.
func (c Chain) Integrity(seq uint64) (string, bool) {
	for _, ev := range c.Events {
		if ev.Seq == seq {
			return ev.Integrity, true
		}
	}
	return "", false
}

// NotVerified returns the listed rows whose verdict is anything other than
// "verified", in sequence order.
func (c Chain) NotVerified() []Event {
	var out []Event
	for _, ev := range c.Events {
		if ev.Integrity != "verified" {
			out = append(out, ev)
		}
	}
	return out
}

// After returns the listed rows with a sequence number above seq.
func (c Chain) After(seq uint64) []Event {
	var out []Event
	for _, ev := range c.Events {
		if ev.Seq > seq {
			out = append(out, ev)
		}
	}
	return out
}

// MaxSeq returns the highest listed sequence number, or 0 for an empty
// list.
func (c Chain) MaxSeq() uint64 {
	if len(c.Events) == 0 {
		return 0
	}
	return c.Events[len(c.Events)-1].Seq
}
