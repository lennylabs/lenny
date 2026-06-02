// SPDX-License-Identifier: MIT

package opsidem

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/lennylabs/lenny/pkg/ops/conventions"
)

// encodeJSON writes v as JSON to w. Errors are unrecoverable mid-write
// and discarded, matching the conventions.WriteError idiom.
func encodeJSON(w http.ResponseWriter, v any) error {
	return json.NewEncoder(w).Encode(v)
}

// captureWriter tees the inner handler's response into a buffer so the
// middleware can store it for §25.4 replay, while still writing it
// through to the client. The status defaults to 200 for a handler that
// writes a body without an explicit WriteHeader.
type captureWriter struct {
	http.ResponseWriter
	status      int
	body        bytes.Buffer
	wroteHeader bool
}

func (c *captureWriter) WriteHeader(status int) {
	if c.wroteHeader {
		return
	}
	c.status = status
	c.wroteHeader = true
	c.ResponseWriter.WriteHeader(status)
}

func (c *captureWriter) Write(b []byte) (int, error) {
	if !c.wroteHeader {
		c.WriteHeader(http.StatusOK)
	}
	c.body.Write(b)
	return c.ResponseWriter.Write(b)
}

// degradingWriter injects the §25.4 degradation envelope into an optional
// endpoint's JSON response when the idempotency store is unavailable, so
// the agent learns retry-safety was not guaranteed for this call. It
// merges a `degradation` field into a top-level JSON object; a non-object
// body (or non-JSON) is passed through unchanged with the header set.
//
// spec: §25.4 lines 2069-2070 — "the response includes degradation.warnings
// noting that retry-safety is not guaranteed".
type degradingWriter struct {
	http.ResponseWriter
	status      int
	body        bytes.Buffer
	wroteHeader bool
	buffering   bool
}

func (d *degradingWriter) WriteHeader(status int) {
	if d.wroteHeader {
		return
	}
	d.status = status
	d.wroteHeader = true
	d.ResponseWriter.Header().Set("X-Lenny-Idempotency-Degraded", "true")
	// Only attempt the JSON merge for a 2xx JSON object response; pass
	// through any other status or content type verbatim.
	ct := d.ResponseWriter.Header().Get("Content-Type")
	if status >= 200 && status < 300 && (ct == "" || jsonContentType(ct)) {
		d.buffering = true
		return
	}
	d.ResponseWriter.WriteHeader(status)
}

func (d *degradingWriter) Write(b []byte) (int, error) {
	if !d.wroteHeader {
		d.WriteHeader(http.StatusOK)
	}
	if d.buffering {
		return d.body.Write(b)
	}
	return d.ResponseWriter.Write(b)
}

// flush merges the degradation envelope and writes the buffered body. The
// middleware calls it after the inner handler returns.
func (d *degradingWriter) flush() {
	if !d.buffering {
		return
	}
	merged, ok := mergeDegradation(d.body.Bytes())
	if !ok {
		// Not a JSON object after all: write the raw body verbatim.
		d.ResponseWriter.WriteHeader(d.status)
		_, _ = d.ResponseWriter.Write(d.body.Bytes())
		return
	}
	d.ResponseWriter.WriteHeader(d.status)
	_, _ = d.ResponseWriter.Write(merged)
}

// mergeDegradation adds a `degradation` field to a top-level JSON object.
// Returns (merged, true) on success, (nil, false) when body is not a JSON
// object.
func mergeDegradation(body []byte) ([]byte, bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, false
	}
	deg := conventions.Degradation{
		Level:    conventions.DegradationDegraded,
		Warnings: []string{"idempotency store unavailable: retry-safety is not guaranteed for this request"},
	}
	raw, err := json.Marshal(deg)
	if err != nil {
		return nil, false
	}
	obj["degradation"] = raw
	out, err := json.Marshal(obj)
	if err != nil {
		return nil, false
	}
	return out, true
}

func jsonContentType(ct string) bool {
	for i := 0; i+4 <= len(ct); i++ {
		if ct[i:i+4] == "json" {
			return true
		}
	}
	return false
}
