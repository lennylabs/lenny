// SPDX-License-Identifier: MIT

package connectorinvoke

import (
	"context"
	"crypto/tls"
	"net"
	"net/url"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorstore"
)

// StageStatus is the per-stage outcome of a §15.1 connector live test.
type StageStatus string

const (
	StagePassed  StageStatus = "passed"
	StageFailed  StageStatus = "failed"
	StageSkipped StageStatus = "skipped"
)

// Stage names match the §15.1 line 1163 connector-test response.
const (
	StageDNS  = "dns_resolution"
	StageTLS  = "tls_handshake"
	StageMCP  = "mcp_initialize"
	StageAuth = "auth_validation"
)

// TestStage is one row of the §15.1 connector-test response.
type TestStage struct {
	Name      string      `json:"name"`
	Status    StageStatus `json:"status"`
	LatencyMs int64       `json:"latencyMs"`
	Error     string      `json:"error,omitempty"`
}

// TestReport is the §15.1 line 1163 connector live-test response body.
type TestReport struct {
	Connector string      `json:"connector"`
	Stages    []TestStage `json:"stages"`
	Overall   StageStatus `json:"overall"`
}

// Tester runs the §15.1 connector live-connectivity check: DNS
// resolution, TLS handshake, MCP `initialize`, and authentication
// validation. The DNS resolver, TLS dialer, and MCP client are seams so
// the stage orchestration is unit-testable without a network.
//
// spec: §15.1 line 791, lines 1163-1180.
type Tester struct {
	client  *Client
	resolve func(ctx context.Context, host string) error
	dialTLS func(ctx context.Context, addr, serverName string) error
	now     func() time.Time
}

// NewTester wires a Tester with production network seams: the system DNS
// resolver and a TLS dialer with a bounded handshake timeout. client is
// the outbound MCP client used for the `initialize` stage.
func NewTester(client *Client) *Tester {
	return &Tester{
		client: client,
		resolve: func(ctx context.Context, host string) error {
			_, err := net.DefaultResolver.LookupHost(ctx, host)
			return err
		},
		dialTLS: func(ctx context.Context, addr, serverName string) error {
			d := tls.Dialer{Config: &tls.Config{ServerName: serverName}}
			conn, err := d.DialContext(ctx, "tcp", addr)
			if err != nil {
				return err
			}
			return conn.Close()
		},
		now: time.Now,
	}
}

// Test runs the four §15.1 stages against conn, carrying bearer as the
// credential for the MCP handshake and the authentication-validation
// stage. A stage whose prerequisite failed is reported as skipped. The
// overall status is failed when any stage failed and passed otherwise.
func (t *Tester) Test(ctx context.Context, conn connectorstore.Connector, bearer string) TestReport {
	report := TestReport{Connector: conn.ID, Overall: StagePassed}
	add := func(s TestStage) {
		report.Stages = append(report.Stages, s)
		if s.Status == StageFailed {
			report.Overall = StageFailed
		}
	}

	u, err := url.Parse(conn.MCPServerURL)
	if err != nil || u.Host == "" {
		add(TestStage{Name: StageDNS, Status: StageFailed, Error: "connector mcpServerUrl is not a valid URL"})
		add(TestStage{Name: StageTLS, Status: StageSkipped})
		add(TestStage{Name: StageMCP, Status: StageSkipped})
		add(TestStage{Name: StageAuth, Status: StageSkipped})
		return report
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = "443"
	}

	dns := t.timed(func() error { return t.resolve(ctx, host) })
	add(stage(StageDNS, dns))
	if dns.err != nil {
		add(TestStage{Name: StageTLS, Status: StageSkipped})
		add(TestStage{Name: StageMCP, Status: StageSkipped})
		add(TestStage{Name: StageAuth, Status: StageSkipped})
		return report
	}

	tlsRes := t.timed(func() error { return t.dialTLS(ctx, net.JoinHostPort(host, port), host) })
	add(stage(StageTLS, tlsRes))
	if tlsRes.err != nil {
		add(TestStage{Name: StageMCP, Status: StageSkipped})
		add(TestStage{Name: StageAuth, Status: StageSkipped})
		return report
	}

	var sess *Session
	mcp := t.timed(func() error {
		s, _, err := t.client.Initialize(ctx, conn.MCPServerURL, bearer)
		sess = s
		return err
	})
	add(stage(StageMCP, mcp))
	if mcp.err != nil {
		add(TestStage{Name: StageAuth, Status: StageSkipped})
		return report
	}

	add(t.authStage(ctx, conn, bearer, sess))
	return report
}

// WithSeams overrides the network seams. It is used by tests to drive
// the stage orchestration without a real network; production wires the
// defaults through NewTester.
func (t *Tester) WithSeams(resolve func(ctx context.Context, host string) error, dialTLS func(ctx context.Context, addr, serverName string) error, now func() time.Time) *Tester {
	if resolve != nil {
		t.resolve = resolve
	}
	if dialTLS != nil {
		t.dialTLS = dialTLS
	}
	if now != nil {
		t.now = now
	}
	return t
}

// authStage validates the stored credential. A connector with no auth
// block requires no validation (skipped). A connector with auth but no
// stored credential cannot be validated (skipped). Otherwise an
// authenticated tools/list confirms the credential is accepted.
func (t *Tester) authStage(ctx context.Context, conn connectorstore.Connector, bearer string, sess *Session) TestStage {
	if conn.Auth == nil {
		return TestStage{Name: StageAuth, Status: StageSkipped, Error: "connector requires no authentication"}
	}
	if bearer == "" {
		return TestStage{Name: StageAuth, Status: StageSkipped, Error: "no stored credential to validate"}
	}
	res := t.timed(func() error {
		_, err := sess.ListTools(ctx)
		return err
	})
	return stage(StageAuth, res)
}

type timedResult struct {
	latencyMs int64
	err       error
}

func (t *Tester) timed(fn func() error) timedResult {
	start := t.now()
	err := fn()
	d := t.now().Sub(start)
	ms := d.Milliseconds()
	if ms < 0 {
		ms = 0
	}
	return timedResult{latencyMs: ms, err: err}
}

func stage(name string, r timedResult) TestStage {
	s := TestStage{Name: name, LatencyMs: r.latencyMs, Status: StagePassed}
	if r.err != nil {
		s.Status = StageFailed
		s.Error = r.err.Error()
	}
	return s
}
