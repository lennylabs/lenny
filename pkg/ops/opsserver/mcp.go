// SPDX-License-Identifier: MIT

package opsserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/lennylabs/lenny/pkg/ops/mcpmgmt"
)

// mcpInvoker returns the §25.12 Invoker that routes an MCP management
// tool call to its underlying lenny-ops endpoint. §25.12 has the
// management server dispatch ops-owned tools to local handlers and
// gateway-owned tools through GatewayClient; the v1 management server
// exposes the ops-owned operability tools, so the invoker dispatches
// them in-process by replaying the tool's mapped request against this
// Server's own mux.
func (s *Server) mcpInvoker() mcpmgmt.Invoker {
	return &opsInvoker{server: s}
}

// opsInvoker is the §25.12 Invoker for the lenny-ops-resident
// operability tools.
type opsInvoker struct {
	server *Server
}

// Invoke runs an MCP management tool by translating it into the HTTP
// request its §25.12 mapping names and replaying that request against
// the lenny-ops mux. The captured response becomes the tool result;
// §25.2 dry-run previews (200 with dryRun:true) are surfaced so the
// management server can apply the §25.12 dry-run mapping.
func (i *opsInvoker) Invoke(tool mcpmgmt.Tool, args json.RawMessage) (mcpmgmt.ToolResult, error) {
	path, query, body, err := buildToolRequest(tool, args)
	if err != nil {
		return mcpmgmt.ToolResult{}, err
	}
	url := path
	if query != "" {
		url += "?" + query
	}
	var reader *bytes.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(tool.Method, url, reader)
	req.Header.Set("Content-Type", "application/json")
	// §25.12: the management server is the caller; mark the invocation
	// so audit and identity attribution see the MCP path.
	req.Header.Set("X-Lenny-Caller", "mcp-management")
	rec := httptest.NewRecorder()
	i.server.ServeHTTP(rec, req)

	result := mcpmgmt.ToolResult{
		Status: rec.Code,
		Body:   append(json.RawMessage(nil), rec.Body.Bytes()...),
	}
	// §25.2 dry-run preview: a 200 carrying dryRun:true is a preview, not
	// a mutation. Surface the flag and the preview object.
	if rec.Code == http.StatusOK {
		var probe struct {
			DryRun  bool            `json:"dryRun"`
			Preview json.RawMessage `json:"preview"`
		}
		if json.Unmarshal(rec.Body.Bytes(), &probe) == nil && probe.DryRun {
			result.DryRun = true
			result.Preview = probe.Preview
		}
	}
	// §25.12: a 503 from an ops endpoint that is not configured maps to
	// the -32000 ENDPOINT_UNAVAILABLE error.
	if rec.Code == http.StatusServiceUnavailable {
		result.Unavailable = true
	}
	return result, nil
}

// buildToolRequest translates a §25.12 tool and its arguments into the
// concrete request path, query string, and body. Path parameters
// ({id}, {name}, {component}) are substituted from the arguments; for a
// GET the remaining arguments become query parameters, and for a body
// method they become the JSON request body.
func buildToolRequest(tool mcpmgmt.Tool, args json.RawMessage) (path, query string, body []byte, err error) {
	var argMap map[string]any
	if len(args) > 0 {
		if err := json.Unmarshal(args, &argMap); err != nil {
			return "", "", nil, err
		}
	}
	path = tool.Path
	consumed := make(map[string]bool)
	// Substitute {param} path segments from the arguments.
	for {
		open := strings.IndexByte(path, '{')
		if open < 0 {
			break
		}
		closeIdx := strings.IndexByte(path[open:], '}')
		if closeIdx < 0 {
			break
		}
		name := path[open+1 : open+closeIdx]
		value := stringArg(argMap[name])
		path = path[:open] + value + path[open+closeIdx+1:]
		consumed[name] = true
	}
	if tool.Method == http.MethodGet {
		query = encodeQuery(argMap, consumed)
		return path, query, nil, nil
	}
	// Body methods: the unconsumed arguments form the JSON request body.
	rest := make(map[string]any)
	for k, v := range argMap {
		if !consumed[k] {
			rest[k] = v
		}
	}
	if len(rest) > 0 {
		body, err = json.Marshal(rest)
		if err != nil {
			return "", "", nil, err
		}
	}
	return path, query, body, nil
}

// stringArg renders an argument value as a path/query string.
func stringArg(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	default:
		b, _ := json.Marshal(t)
		return strings.Trim(string(b), `"`)
	}
}

// encodeQuery encodes the unconsumed string-valued arguments as a query
// string; non-string and consumed arguments are skipped.
func encodeQuery(argMap map[string]any, consumed map[string]bool) string {
	values := make([]string, 0, len(argMap))
	for k, v := range argMap {
		if consumed[k] {
			continue
		}
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		values = append(values, queryEscape(k)+"="+queryEscape(s))
	}
	// Deterministic order keeps the replayed request reproducible.
	sortStrings(values)
	return strings.Join(values, "&")
}

// queryEscape percent-escapes a query component.
func queryEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isUnreserved(c) {
			b.WriteByte(c)
			continue
		}
		const hex = "0123456789ABCDEF"
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0x0f])
	}
	return b.String()
}

// isUnreserved reports whether c is an RFC 3986 unreserved character.
func isUnreserved(c byte) bool {
	switch {
	case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		return true
	case c == '-', c == '_', c == '.', c == '~':
		return true
	default:
		return false
	}
}

// sortStrings sorts s in place (small slices; insertion sort).
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
