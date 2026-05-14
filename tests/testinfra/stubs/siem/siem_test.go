// SPDX-License-Identifier: MIT

package siem_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/stubs/siem"
)

// spec: 11.7 (SIEM forwarder happy path: batches arrive)
// diagnosis: A POST to the stub did not produce a recorded batch.
//
//	The handler or the recorder is dropping the request.
func TestStubRecordsBatch(t *testing.T) {
	t.Parallel()
	s := siem.New(t)
	batch := []map[string]any{{"event_id": "1"}}
	body, _ := json.Marshal(batch)
	resp, err := http.Post(s.URL(), "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status: want 204, got %d", resp.StatusCode)
	}
	if got := len(s.Batches()); got != 1 {
		t.Errorf("batch count: want 1, got %d", got)
	}
}

// spec: 11.7 (SIEM signature enforcement when CheckSignature is set)
// diagnosis: An unsigned POST was accepted after CheckSignature was
//
//	enabled. The HMAC gate is wrong.
func TestStubSignatureEnforced(t *testing.T) {
	t.Parallel()
	s := siem.New(t)
	s.CheckSignature("shh")

	resp, err := http.Post(s.URL(), "application/json", bytes.NewReader([]byte("[]")))
	if err != nil {
		t.Fatalf("unsigned post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("unsigned: want 401, got %d", resp.StatusCode)
	}

	body := []byte(`[{"event_id":"1"}]`)
	mac := hmac.New(sha256.New, []byte("shh"))
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))
	req, _ := http.NewRequest(http.MethodPost, s.URL(), bytes.NewReader(body))
	req.Header.Set("X-Lenny-SIEM-Signature", sig)
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("signed do: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNoContent {
		t.Errorf("signed: want 204, got %d", resp2.StatusCode)
	}
}

// spec: 11.7 (Events convenience flattens every batch)
// diagnosis: Events() did not flatten properly. The aggregation walks
//
//	the wrong field or skips batches.
func TestStubEventsFlat(t *testing.T) {
	t.Parallel()
	s := siem.New(t)
	body, _ := json.Marshal([]map[string]any{{"event_id": "1"}, {"event_id": "2"}})
	resp, _ := http.Post(s.URL(), "application/json", bytes.NewReader(body))
	resp.Body.Close()
	body2, _ := json.Marshal([]map[string]any{{"event_id": "3"}})
	resp2, _ := http.Post(s.URL(), "application/json", bytes.NewReader(body2))
	resp2.Body.Close()
	if got := len(s.Events()); got != 3 {
		t.Errorf("flat event count: want 3, got %d", got)
	}
}
