// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// spec: §15.2 lines 1310-1315 — negotiate the highest mutually supported
// MCP spec version; reject too-old/retired versions with the structured
// lenny error. F-15.2.1, F-15.5.4.
func TestNegotiateVersion_spec_15_2_1310(t *testing.T) {
	cases := []struct {
		name           string
		requested      string
		wantNegotiated string
		wantDeprecated bool
		wantErrCode    string
	}{
		{"omitted defaults to current", "", CurrentProtocolVersion, false, ""},
		{"current pins current", CurrentProtocolVersion, CurrentProtocolVersion, false, ""},
		{"previous pins previous and deprecates", PreviousProtocolVersion, PreviousProtocolVersion, true, ""},
		{"newer than current offers current", "2099-01-01", CurrentProtocolVersion, false, ""},
		{"in-gap offers current", "2025-01-01", CurrentProtocolVersion, false, ""},
		{"older than oldest is unsupported", "2020-01-01", "", false, "MCP_VERSION_UNSUPPORTED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, deprecated, nerr := negotiateVersion(tc.requested)
			if tc.wantErrCode != "" {
				if nerr == nil {
					t.Fatalf("negotiateVersion(%q): want error %s, got nil", tc.requested, tc.wantErrCode)
				}
				if nerr.code != tc.wantErrCode {
					t.Fatalf("negotiateVersion(%q) code = %s, want %s", tc.requested, nerr.code, tc.wantErrCode)
				}
				return
			}
			if nerr != nil {
				t.Fatalf("negotiateVersion(%q): unexpected error %+v", tc.requested, nerr)
			}
			if got != tc.wantNegotiated {
				t.Errorf("negotiateVersion(%q) = %q, want %q", tc.requested, got, tc.wantNegotiated)
			}
			if deprecated != tc.wantDeprecated {
				t.Errorf("negotiateVersion(%q) deprecated = %v, want %v", tc.requested, deprecated, tc.wantDeprecated)
			}
		})
	}
}

// spec: §15.2 "Session-lifetime exception" — a new handshake on a retired
// version is rejected with MCP_PROTOCOL_VERSION_RETIRED, distinct from the
// generic unsupported rejection. F-15.5.4.
func TestNegotiateVersion_retired_spec_15_2(t *testing.T) {
	orig := retiredVersions
	retiredVersions = []string{"2024-05-01"}
	defer func() { retiredVersions = orig }()

	_, _, nerr := negotiateVersion("2024-05-01")
	if nerr == nil || nerr.code != "MCP_PROTOCOL_VERSION_RETIRED" {
		t.Fatalf("retired version: want MCP_PROTOCOL_VERSION_RETIRED, got %+v", nerr)
	}
}

// spec: §15.2 line 1316 — the deprecation warning header is set on a POST
// /mcp initialize that negotiates the previous version, and absent on the
// current version. F-15.5.4.
func TestInitializeDeprecationHeader_spec_15_2_1316(t *testing.T) {
	s := NewServer()
	h := s.Handler()

	rrCurrent := postInitialize(t, h, CurrentProtocolVersion)
	if got := rrCurrent.Header().Get(headerMCPVersionDeprecated); got != "" {
		t.Errorf("current version set deprecation header = %q, want empty", got)
	}

	rrPrev := postInitialize(t, h, PreviousProtocolVersion)
	if got := rrPrev.Header().Get(headerMCPVersionDeprecated); got != PreviousProtocolVersion {
		t.Errorf("previous version deprecation header = %q, want %q", got, PreviousProtocolVersion)
	}
	var resp map[string]any
	if err := json.Unmarshal(rrPrev.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	result := resp["result"].(map[string]any)
	if result["protocolVersion"] != PreviousProtocolVersion {
		t.Errorf("negotiated = %v, want %q", result["protocolVersion"], PreviousProtocolVersion)
	}
}

// spec: §15.2 line 1313 — an unsupported (too-old) initialize is rejected
// with the structured lenny error carrying the supported version list.
// F-15.2.1.
func TestInitializeUnsupportedRejected_spec_15_2_1313(t *testing.T) {
	s := NewServer()
	rr := postInitialize(t, s.Handler(), "2020-01-01")
	var resp jsonRPCResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error == nil {
		t.Fatalf("want JSON-RPC error, got %+v", resp)
	}
	data, _ := json.Marshal(resp.Error.Data)
	var env LennyErrorDetail
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("decode error data: %v", err)
	}
	if env.Code != "MCP_VERSION_UNSUPPORTED" {
		t.Errorf("error code = %q, want MCP_VERSION_UNSUPPORTED", env.Code)
	}
	if !strings.Contains(string(data), CurrentProtocolVersion) {
		t.Errorf("error details omit supported versions: %s", data)
	}
}

// spec: §15.2 lines 1310-1315 — the WebSocket transport negotiates with
// the same rules and surfaces the structured lenny error on rejection.
// F-15.2.1.
func TestWebSocketInitializeNegotiation_spec_15_2_1310(t *testing.T) {
	s := NewServer()
	ctx := context.Background()

	out, _ := s.dispatchFrameBytes(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`))
	var resp jsonRPCResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	result := resp.Result.(map[string]any)
	if result["protocolVersion"] != PreviousProtocolVersion {
		t.Errorf("WS negotiated = %v, want %q", result["protocolVersion"], PreviousProtocolVersion)
	}

	outRej, _ := s.dispatchFrameBytes(ctx, []byte(`{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":"2020-01-01"}}`))
	var rej jsonRPCResponse
	if err := json.Unmarshal(outRej, &rej); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rej.Error == nil {
		t.Fatalf("WS too-old: want error, got %+v", rej)
	}
	data, _ := json.Marshal(rej.Error.Data)
	if !strings.Contains(string(data), "MCP_VERSION_UNSUPPORTED") {
		t.Errorf("WS rejection missing MCP_VERSION_UNSUPPORTED: %s", data)
	}
}

func postInitialize(t *testing.T, h http.Handler, version string) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize"}`
	if version != "" {
		body = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + version + `"}}`
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}
