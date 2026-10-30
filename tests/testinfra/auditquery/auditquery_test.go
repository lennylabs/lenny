// SPDX-License-Identifier: MIT

package auditquery

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func record(seq int, tenant, integrity string) string {
	return fmt.Sprintf(`{"class_uid":6003,"metadata":{"uid":"u%d","sequence":%d,"tenant_uid":%q},`+
		`"unmapped":{"lenny_chain":{"prev_hash":"h%d","integrity":%q}}}`, seq, seq, tenant, seq-1, integrity)
}

func envelope(tenant, cursor string, report string, records ...string) []byte {
	return []byte(fmt.Sprintf(`{"tenantId":%q,"items":[%s],"ocsfVersion":"1.1.0",`+
		`"chainIntegrityReport":%s,"nextCursor":%q}`, tenant, strings.Join(records, ","), report, cursor))
}

// TestListPathCarriesTenantLimitAndBounds pins the list query the chain
// tests send: the tenant, the §25.9 maximum page size, and the optional
// since, until, and cursor parameters.
//
// spec: 25.9 (Audit Log Query API)
func TestListPathCarriesTenantLimitAndBounds(t *testing.T) {
	p := ListPath("acme", "", "", "")
	if p != "/v1/admin/audit-events?limit=1000&tenantId=acme" {
		t.Errorf("ListPath without bounds = %q", p)
	}
	u, err := url.Parse(ListPath("acme", "2026-05-01T11:00:00Z", "2026-05-01T13:00:00Z", "c1"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Path != "/v1/admin/audit-events" || q.Get("tenantId") != "acme" || q.Get("since") != "2026-05-01T11:00:00Z" ||
		q.Get("until") != "2026-05-01T13:00:00Z" || q.Get("cursor") != "c1" || q.Get("limit") != "1000" {
		t.Errorf("ListPath with bounds = %q", u.String())
	}
}

// TestReadChainWalksPagesAndSumsTheReport pins the pagination walk and the
// decoded verdict: every page's records are combined in sequence order,
// the reports are summed, and each row's integrity is read from
// unmapped.lenny_chain.
//
// spec: 25.9 (Audit Log Query API), 11.7 (Audit Logging)
func TestReadChainWalksPagesAndSumsTheReport(t *testing.T) {
	pages := map[string][]byte{
		"": envelope("acme", "c2", `{"verified":2,"broken":0,"gap_suspected":0,"rechained_post_outage":0,"redacted_gdpr":0}`,
			record(2, "acme", "verified"), record(1, "acme", "verified")),
		"c2": envelope("acme", "", `{"verified":0,"broken":1,"gap_suspected":1,"rechained_post_outage":0,"redacted_gdpr":0}`,
			record(4, "acme", "broken"), record(3, "acme", "gap_suspected")),
	}
	var seen []string
	fetch := func(path string) (int, []byte, error) {
		u, _ := url.Parse(path)
		c := u.Query().Get("cursor")
		seen = append(seen, c)
		return 200, pages[c], nil
	}
	c, err := ReadChain("acme", "", "", fetch)
	if err != nil {
		t.Fatalf("ReadChain: %v", err)
	}
	if strings.Join(seen, ",") != ",c2" {
		t.Errorf("cursors requested = %q, want the first page then c2", seen)
	}
	if len(c.Events) != 4 || c.Events[0].Seq != 1 || c.MaxSeq() != 4 {
		t.Fatalf("events = %+v, want seq 1..4 in order", c.Events)
	}
	if c.Report != (Report{Verified: 2, Broken: 1, GapSuspected: 1}) {
		t.Errorf("report = %+v", c.Report)
	}
	if got, ok := c.Integrity(4); !ok || got != "broken" {
		t.Errorf("Integrity(4) = %q, %v", got, ok)
	}
	if _, ok := c.Integrity(9); ok {
		t.Error("Integrity(9) reported a row that was not listed")
	}
	if nv := c.NotVerified(); len(nv) != 2 || nv[0].Seq != 3 || nv[1].Seq != 4 {
		t.Errorf("NotVerified = %+v", nv)
	}
	if a := c.After(2); len(a) != 2 || a[0].Seq != 3 {
		t.Errorf("After(2) = %+v", a)
	}
	if c.Events[1].PrevHash != "h1" {
		t.Errorf("prev_hash = %q, want h1", c.Events[1].PrevHash)
	}
}

// TestReadChainErrors pins the read failures: a non-200 page, an
// undecodable body, a missing chainIntegrityReport, an envelope or a
// record naming another tenant, a transport error, and a cursor that
// never empties.
//
// spec: 25.9 (Audit Log Query API)
func TestReadChainErrors(t *testing.T) {
	okReport := `{"verified":1,"broken":0,"gap_suspected":0,"rechained_post_outage":0,"redacted_gdpr":0}`
	cases := []struct {
		name    string
		status  int
		body    []byte
		err     error
		wantErr string
	}{
		{"status 400", 400, []byte(`{"error":"seq must be a positive integer"}`), nil, "status 400"},
		{"undecodable", 200, []byte(`not json`), nil, "decode the audit-events list response"},
		{"no report", 200, []byte(`{"tenantId":"acme","items":[]}`), nil, "no chainIntegrityReport"},
		{"other envelope tenant", 200, envelope("globex", "", okReport, record(1, "globex", "verified")), nil, `names tenant "globex"`},
		{"other record tenant", 200, envelope("acme", "", okReport, record(1, "globex", "verified")), nil, `of tenant "globex"`},
		{"transport", 0, nil, errors.New("curl exit 7"), "curl exit 7"},
		{"endless cursor", 200, envelope("acme", "again", okReport), nil, "did not end within"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ReadChain("acme", "", "", func(string) (int, []byte, error) { return tc.status, tc.body, tc.err })
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
	var se *StatusError
	_, err := ReadChain("acme", "", "", func(string) (int, []byte, error) { return 503, []byte("x"), nil })
	if !errors.As(err, &se) || se.Status != 503 {
		t.Errorf("a non-200 page did not return a *StatusError: %v", err)
	}
}

// TestReadChainEmptyList pins that an empty chain decodes to no events
// and a zero report.
//
// spec: 25.9 (Audit Log Query API)
func TestReadChainEmptyList(t *testing.T) {
	c, err := ReadChain("acme", "", "", func(string) (int, []byte, error) {
		return 200, envelope("acme", "", `{"verified":0,"broken":0,"gap_suspected":0,"rechained_post_outage":0,"redacted_gdpr":0}`), nil
	})
	if err != nil {
		t.Fatalf("ReadChain: %v", err)
	}
	if len(c.Events) != 0 || c.MaxSeq() != 0 || c.Report != (Report{}) {
		t.Errorf("empty chain = %+v", c)
	}
}
