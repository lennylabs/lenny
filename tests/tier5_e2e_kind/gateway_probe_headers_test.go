// SPDX-License-Identifier: MIT

//go:build e2e_kind

package tier5_e2e_kind_test

import "testing"

// spec: 15.1 (REST API, the ETag a GET returns for the next conditional write)
// diagnosis: a failure means the tier-5 gateway probe no longer recovers the
// response headers from kubectl exec's combined output, so a probe-driven
// test that reads the §15.1 ETag (t5AllowSessionsWithNoEnvironment) fails
// with "returned no ETag" although the gateway sent one. The case that
// carries a stderr line before the status line is the one a kubectl stream
// warning produces.
func TestGatewayProbeHeaderSplitToleratesLeadingKubectlOutput(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		wantETag string
		wantBody string
	}{
		{
			name:     "status line first",
			in:       "HTTP/1.1 200 OK\r\nEtag: \"7\"\r\nContent-Type: application/json\r\n\r\n{\"a\":1}",
			wantETag: `"7"`,
			wantBody: `{"a":1}`,
		},
		{
			name:     "kubectl stderr line before the status line",
			in:       "E1030 12:00:00.000000 1 v2.go:167] websocket stream warning\nHTTP/1.1 200 OK\r\nEtag: \"9\"\r\n\r\n{\"b\":2}",
			wantETag: `"9"`,
			wantBody: `{"b":2}`,
		},
		{
			name:     "no header block",
			in:       "curl: (7) Failed to connect",
			wantETag: "",
			wantBody: "curl: (7) Failed to connect",
		},
		{
			name:     "empty output",
			in:       "",
			wantETag: "",
			wantBody: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			headers, body := splitT5Headers(tc.in)
			if got := headers["etag"]; got != tc.wantETag {
				t.Errorf("etag = %q, want %q", got, tc.wantETag)
			}
			if body != tc.wantBody {
				t.Errorf("body = %q, want %q", body, tc.wantBody)
			}
		})
	}
}
