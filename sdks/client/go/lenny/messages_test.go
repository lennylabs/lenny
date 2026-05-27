// SPDX-License-Identifier: MIT

package lenny

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// spec: §15.1 messages endpoint + §15.4 delivery_receipt — the SDK
// SendMessages call POSTs the batch and parses the receipt envelope.
func TestSendMessagesPostsBatchAndDecodesReceipt(t *testing.T) {
	var lastBody []byte
	var lastPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastPath = r.URL.Path
		lastBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(SendMessagesResponse{
			DeliveryReceipt: DeliveryReceipt{
				MessageID:   "msg_abc",
				Status:      "delivered",
				DeliveredAt: "2026-05-26T00:00:00Z",
			},
			Output: []OutputPart{{Type: "text", Text: "ok"}},
		})
	}))
	defer srv.Close()

	c, err := New(srv.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := c.SendMessages(context.Background(), "sess_1", SendMessagesRequest{
		Messages: []MessagePayload{
			{Role: "user", Content: "hello", Delivery: "immediate"},
		},
	})
	if err != nil {
		t.Fatalf("SendMessages: %v", err)
	}
	if lastPath != "/v1/sessions/sess_1/messages" {
		t.Errorf("path = %q", lastPath)
	}
	if !strings.Contains(string(lastBody), `"content":"hello"`) ||
		!strings.Contains(string(lastBody), `"delivery":"immediate"`) {
		t.Errorf("body = %s", lastBody)
	}
	if resp.DeliveryReceipt.Status != "delivered" || resp.DeliveryReceipt.MessageID != "msg_abc" {
		t.Errorf("receipt = %+v", resp.DeliveryReceipt)
	}
	if len(resp.Output) != 1 || resp.Output[0].Text != "ok" {
		t.Errorf("output = %+v", resp.Output)
	}
}

// spec: §15.1 — SDK must reject an empty batch before round-tripping.
func TestSendMessagesRejectsEmptyBatch(t *testing.T) {
	c, _ := New("http://example.test")
	_, err := c.SendMessages(context.Background(), "sess_1", SendMessagesRequest{})
	if err == nil {
		t.Fatal("expected error on empty messages batch")
	}
}

// spec: §15.1 transcript — the SDK forwards afterSeq and limit, decodes
// entries.
func TestGetTranscriptForwardsCursorAndLimit(t *testing.T) {
	var lastURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastURL = r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(TranscriptResponse{
			SessionID: "sess_2",
			Entries: []TranscriptEntry{
				{Seq: 10, Role: "user", Content: "hi"},
			},
		})
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	out, err := c.GetTranscript(context.Background(), "sess_2", TranscriptOptions{AfterSeq: 5, Limit: 20})
	if err != nil {
		t.Fatalf("GetTranscript: %v", err)
	}
	if !strings.Contains(lastURL, "afterSeq=5") || !strings.Contains(lastURL, "limit=20") {
		t.Errorf("url = %s", lastURL)
	}
	if out.SessionID != "sess_2" || len(out.Entries) != 1 || out.Entries[0].Seq != 10 {
		t.Errorf("transcript = %+v", out)
	}
}

// spec: §7.2 table line 124 / §15.1 — approve hits /approve and decodes
// the resolution envelope.
func TestApproveToolUsePostsApproveAndDecodesResolution(t *testing.T) {
	var lastPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(InteractionResolution{
			ID:         "tc_1",
			Phase:      "approved",
			ResolvedAt: "2026-05-26T00:00:00Z",
		})
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	out, err := c.ApproveToolUse(context.Background(), "sess_x", "tc_1")
	if err != nil {
		t.Fatalf("ApproveToolUse: %v", err)
	}
	if lastPath != "/v1/sessions/sess_x/tool-use/tc_1/approve" {
		t.Errorf("path = %q", lastPath)
	}
	if out.Phase != "approved" || out.ID != "tc_1" {
		t.Errorf("resolution = %+v", out)
	}
}

// spec: §7.2 table line 125 — deny forwards the reason in the body.
func TestDenyToolUseForwardsReasonInBody(t *testing.T) {
	var lastBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(InteractionResolution{ID: "tc_2", Phase: "denied"})
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	_, err := c.DenyToolUse(context.Background(), "sess_y", "tc_2", "policy violation")
	if err != nil {
		t.Fatalf("DenyToolUse: %v", err)
	}
	if !strings.Contains(string(lastBody), `"reason":"policy violation"`) {
		t.Errorf("body = %s", lastBody)
	}
}

// spec: §7.2 table line 126 — respond forwards the response value.
func TestRespondElicitationForwardsResponse(t *testing.T) {
	var lastBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(InteractionResolution{ID: "el_1", Phase: "responded"})
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	_, err := c.RespondElicitation(context.Background(), "sess_z", "el_1", map[string]any{"value": 42})
	if err != nil {
		t.Fatalf("RespondElicitation: %v", err)
	}
	if !strings.Contains(string(lastBody), `"response"`) || !strings.Contains(string(lastBody), `"value":42`) {
		t.Errorf("body = %s", lastBody)
	}
}

// spec: §7.2 table line 127 — dismiss hits /dismiss with an empty body.
func TestDismissElicitationPostsDismiss(t *testing.T) {
	var lastPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(InteractionResolution{ID: "el_2", Phase: "dismissed"})
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	out, err := c.DismissElicitation(context.Background(), "sess_q", "el_2")
	if err != nil {
		t.Fatalf("DismissElicitation: %v", err)
	}
	if lastPath != "/v1/sessions/sess_q/elicitations/el_2/dismiss" {
		t.Errorf("path = %q", lastPath)
	}
	if out.Phase != "dismissed" {
		t.Errorf("resolution = %+v", out)
	}
}
