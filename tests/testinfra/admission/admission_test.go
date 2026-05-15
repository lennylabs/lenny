// SPDX-License-Identifier: MIT

package admission

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

// spec: 13.1 (admission webhook protocol)
// diagnosis: The fake server must return a well-formed
//
//	admissionv1.AdmissionReview envelope so the k8s API
//	server accepts it.
func TestServerEnvelopeShape(t *testing.T) {
	t.Parallel()
	s := NewServer(t, func(r *Request) Response {
		if r.Name == "blocked" {
			return Deny("LabelImmutable", "lenny labels cannot be edited")
		}
		return Allow()
	})

	body, err := MarshalReview(&Request{
		UID:       NewUID(),
		Kind:      "Pod",
		Resource:  "pods",
		Namespace: "lenny-runtime",
		Name:      "blocked",
		Operation: "UPDATE",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp, err := http.Post(s.URL(), "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var got struct {
		Response struct {
			UID     string `json:"uid"`
			Allowed bool   `json:"allowed"`
			Status  struct {
				Code    int32  `json:"code"`
				Reason  string `json:"reason"`
				Message string `json:"message"`
			} `json:"status"`
		} `json:"response"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, raw)
	}
	if got.Response.Allowed {
		t.Fatalf("blocked pod should be denied; got allowed=true")
	}
	if got.Response.Status.Reason != "LabelImmutable" {
		t.Errorf("reason: got %q; want LabelImmutable", got.Response.Status.Reason)
	}
	if got.Response.Status.Code != 403 {
		t.Errorf("code: got %d; want 403", got.Response.Status.Code)
	}
	if s.Hits() != 1 {
		t.Errorf("hits: got %d; want 1", s.Hits())
	}
}

// spec: 13.1 (admission webhook — allow path)
// diagnosis: A name that doesn't match the policy should be
//
//	allowed; verifies the happy path round-trip.
func TestServerAllow(t *testing.T) {
	t.Parallel()
	s := NewServer(t, func(r *Request) Response {
		return Allow()
	})
	body, _ := MarshalReview(&Request{UID: NewUID(), Kind: "Pod", Name: "ok", Operation: "CREATE"})
	resp, err := http.Post(s.URL(), "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var got struct {
		Response struct {
			Allowed bool `json:"allowed"`
		} `json:"response"`
	}
	_ = json.Unmarshal(raw, &got)
	if !got.Response.Allowed {
		t.Fatalf("happy-path pod should be allowed: %s", raw)
	}
}

// spec: 13.1 (admission webhook — request capture)
// diagnosis: LastRequest must surface the UserInfo / Operation /
//
//	Name fields the policy needs to make its decision.
func TestServerLastRequest(t *testing.T) {
	t.Parallel()
	s := NewServer(t, func(r *Request) Response { return Allow() })
	body, _ := MarshalReview(&Request{
		UID:       NewUID(),
		Kind:      "Pod",
		Name:      "test-pod",
		Operation: "UPDATE",
		UserInfo:  map[string]string{"username": "alice"},
	})
	resp, _ := http.Post(s.URL(), "application/json", bytes.NewReader(body))
	resp.Body.Close()
	last := s.LastRequest()
	if last == nil {
		t.Fatal("LastRequest should not be nil after a review")
	}
	if last.Name != "test-pod" {
		t.Errorf("name: got %q; want test-pod", last.Name)
	}
	if last.Operation != "UPDATE" {
		t.Errorf("operation: got %q; want UPDATE", last.Operation)
	}
}

// spec: 13.1 (admission webhook — UID uniqueness)
// diagnosis: NewUID must produce distinct values; correlation
//
//	IDs collide otherwise.
func TestNewUIDUnique(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		u := NewUID()
		if seen[u] {
			t.Fatalf("duplicate UID: %q", u)
		}
		seen[u] = true
		if len(u) != 32 {
			t.Fatalf("UID length: got %d; want 32 (16 bytes hex)", len(u))
		}
	}
}
