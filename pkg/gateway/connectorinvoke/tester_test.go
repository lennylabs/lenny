// SPDX-License-Identifier: MIT

package connectorinvoke

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
)

// fakeClock advances a fixed step on each read so timed() reports a
// deterministic positive latency.
type fakeClock struct {
	t    time.Time
	step time.Duration
}

func (c *fakeClock) now() time.Time {
	cur := c.t
	c.t = c.t.Add(c.step)
	return cur
}

func okClient() *Client {
	return New(&fakeDoer{
		responses: []*http.Response{
			jsonResp(200, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18"}}`, nil),
			jsonResp(202, ``, nil),
			jsonResp(200, `{"jsonrpc":"2.0","id":2,"result":{"tools":[]}}`, nil),
		},
	})
}

func passSeams(t *Tester) *Tester {
	clk := &fakeClock{t: time.Unix(0, 0), step: time.Millisecond}
	return t.WithSeams(
		func(context.Context, string) error { return nil },
		func(context.Context, string, string) error { return nil },
		clk.now,
	)
}

// TestTesterAllStagesPass_spec_15_1_1163 verifies a reachable connector
// with a stored credential reports every stage passed.
func TestTesterAllStagesPass_spec_15_1_1163(t *testing.T) {
	conn := connectorstore.Connector{ID: "github", MCPServerURL: "https://mcp.github.example", Auth: &connectorstore.ConnectorAuth{Type: "oauth2"}}
	tester := passSeams(NewTester(okClient()))
	rep := tester.Test(context.Background(), conn, "tok")
	if rep.Overall != StagePassed {
		t.Fatalf("overall = %q, want passed; stages=%+v", rep.Overall, rep.Stages)
	}
	want := []string{StageDNS, StageTLS, StageMCP, StageAuth}
	if len(rep.Stages) != len(want) {
		t.Fatalf("got %d stages, want %d", len(rep.Stages), len(want))
	}
	for i, n := range want {
		if rep.Stages[i].Name != n {
			t.Errorf("stage %d name = %q, want %q", i, rep.Stages[i].Name, n)
		}
		if rep.Stages[i].Status != StagePassed {
			t.Errorf("stage %q status = %q, want passed", n, rep.Stages[i].Status)
		}
		if rep.Stages[i].LatencyMs <= 0 {
			t.Errorf("stage %q latency = %d, want > 0", n, rep.Stages[i].LatencyMs)
		}
	}
}

// TestTesterDNSFailureSkipsDownstream_spec_15_1_1163 verifies a DNS
// failure fails that stage and skips TLS, MCP, and auth.
func TestTesterDNSFailureSkipsDownstream_spec_15_1_1163(t *testing.T) {
	conn := connectorstore.Connector{ID: "x", MCPServerURL: "https://nope.example"}
	tester := NewTester(okClient()).WithSeams(
		func(context.Context, string) error { return errors.New("no such host") },
		func(context.Context, string, string) error { return nil },
		(&fakeClock{step: time.Millisecond}).now,
	)
	rep := tester.Test(context.Background(), conn, "")
	if rep.Overall != StageFailed {
		t.Fatalf("overall = %q, want failed", rep.Overall)
	}
	if rep.Stages[0].Name != StageDNS || rep.Stages[0].Status != StageFailed {
		t.Errorf("dns stage = %+v, want failed", rep.Stages[0])
	}
	for _, s := range rep.Stages[1:] {
		if s.Status != StageSkipped {
			t.Errorf("stage %q = %q after dns failure, want skipped", s.Name, s.Status)
		}
	}
}

// TestTesterTLSFailureSkipsMCPAndAuth_spec_15_1_1163 verifies a TLS
// handshake failure skips the MCP and auth stages.
func TestTesterTLSFailureSkipsMCPAndAuth_spec_15_1_1163(t *testing.T) {
	conn := connectorstore.Connector{ID: "x", MCPServerURL: "https://bad-tls.example"}
	tester := NewTester(okClient()).WithSeams(
		func(context.Context, string) error { return nil },
		func(context.Context, string, string) error { return errors.New("x509: certificate expired") },
		(&fakeClock{step: time.Millisecond}).now,
	)
	rep := tester.Test(context.Background(), conn, "")
	if rep.Stages[1].Name != StageTLS || rep.Stages[1].Status != StageFailed {
		t.Errorf("tls stage = %+v, want failed", rep.Stages[1])
	}
	if rep.Stages[2].Status != StageSkipped || rep.Stages[3].Status != StageSkipped {
		t.Errorf("mcp/auth not skipped: %+v", rep.Stages[2:])
	}
}

// TestTesterAuthSkippedForPublicConnector_spec_15_1_1163 verifies a
// connector with no auth block reports auth_validation skipped, not
// failed, so the overall result stays passed.
func TestTesterAuthSkippedForPublicConnector_spec_15_1_1163(t *testing.T) {
	conn := connectorstore.Connector{ID: "pub", MCPServerURL: "https://pub.example"}
	rep := passSeams(NewTester(okClient())).Test(context.Background(), conn, "")
	auth := rep.Stages[3]
	if auth.Name != StageAuth || auth.Status != StageSkipped {
		t.Errorf("auth stage = %+v, want skipped", auth)
	}
	if rep.Overall != StagePassed {
		t.Errorf("overall = %q, want passed (skipped auth must not fail overall)", rep.Overall)
	}
}

// TestTesterAuthSkippedWithoutCredential_spec_15_1_1163 verifies an
// auth-configured connector with no stored credential reports
// auth_validation skipped.
func TestTesterAuthSkippedWithoutCredential_spec_15_1_1163(t *testing.T) {
	conn := connectorstore.Connector{ID: "gh", MCPServerURL: "https://gh.example", Auth: &connectorstore.ConnectorAuth{Type: "oauth2"}}
	rep := passSeams(NewTester(okClient())).Test(context.Background(), conn, "")
	if rep.Stages[3].Status != StageSkipped {
		t.Errorf("auth stage = %+v, want skipped (no credential)", rep.Stages[3])
	}
}

// TestTesterInvalidURLFailsDNS_spec_15_1_1163 verifies an unparseable
// mcpServerUrl fails the dns stage rather than panicking.
func TestTesterInvalidURLFailsDNS_spec_15_1_1163(t *testing.T) {
	conn := connectorstore.Connector{ID: "bad", MCPServerURL: "://missing-scheme"}
	rep := passSeams(NewTester(okClient())).Test(context.Background(), conn, "")
	if rep.Stages[0].Status != StageFailed || rep.Overall != StageFailed {
		t.Errorf("expected dns failure on invalid url, got %+v", rep)
	}
}
