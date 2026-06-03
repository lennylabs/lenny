// SPDX-License-Identifier: MIT

package opsservice

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// TestInvalidateTokenDeterministicAndVerifiable is the §25.5 line 2751
// contract: peers that mount the same shared key derive the same
// invalidate token, and VerifyInvalidateToken accepts it while rejecting
// a mismatch and an empty want.
func TestInvalidateTokenDeterministic_spec_25_5_2751(t *testing.T) {
	key := []byte("shared-hmac-key-bytes")
	a := InvalidateToken(key)
	b := InvalidateToken(key)
	if a == "" || a != b {
		t.Fatalf("InvalidateToken not deterministic: %q vs %q", a, b)
	}
	if InvalidateToken([]byte("other")) == a {
		t.Error("different keys produced the same token")
	}
	if InvalidateToken(nil) != "" {
		t.Error("an empty key must yield an empty (disabled) token")
	}
	if !VerifyInvalidateToken(a, b) {
		t.Error("VerifyInvalidateToken rejected a matching token")
	}
	if VerifyInvalidateToken(a, "wrong") {
		t.Error("VerifyInvalidateToken accepted a mismatched token")
	}
	if VerifyInvalidateToken("", "") {
		t.Error("VerifyInvalidateToken accepted an empty (disabled) want")
	}
}

// TestEndpointsPeerListerExcludesSelf is the §25.5 line 2751 peer
// discovery: the lister returns every ready replica address except this
// pod's own, as scheme://ip:port URLs.
func TestEndpointsPeerListerExcludesSelf_spec_25_5_2751(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{Name: "lenny-ops", Namespace: "lenny-system"},
		Subsets: []corev1.EndpointSubset{{
			Addresses: []corev1.EndpointAddress{
				{IP: "10.0.0.1"}, {IP: "10.0.0.2"}, {IP: "10.0.0.3"},
			},
		}},
	})
	l := NewEndpointsPeerLister(EndpointsPeerListerConfig{
		Endpoints: cs.CoreV1(), Namespace: "lenny-system", Service: "lenny-ops",
		Port: 8090, SelfIP: "10.0.0.2",
	})
	peers, err := l.Peers(context.Background())
	if err != nil {
		t.Fatalf("Peers: %v", err)
	}
	if len(peers) != 2 {
		t.Fatalf("peers = %v, want 2 (self excluded)", peers)
	}
	want := map[string]bool{"http://10.0.0.1:8090": true, "http://10.0.0.3:8090": true}
	for _, p := range peers {
		if !want[p] {
			t.Errorf("unexpected peer %q", p)
		}
	}
}

// fakeDoer records the requests the broadcaster issues and returns a
// scripted response (or error) per call.
type fakeDoer struct {
	mu       sync.Mutex
	requests []*http.Request
	status   int
	err      error
}

func (d *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.requests = append(d.requests, req)
	if d.err != nil {
		return nil, d.err
	}
	status := d.status
	if status == 0 {
		status = http.StatusNoContent
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(""))}, nil
}

// staticPeers is a fixed PeerLister.
type staticPeers []string

func (p staticPeers) Peers(context.Context) ([]string, error) { return p, nil }

// TestCacheInvalidateBroadcastPostsToEachPeer is the §25.5 line 2751 RPC
// fan-out: the broadcaster POSTs the invalidate route to every peer with
// the shared token header.
func TestCacheInvalidateBroadcastPostsToEachPeer_spec_25_5_2751(t *testing.T) {
	doer := &fakeDoer{}
	b := NewCacheInvalidateBroadcaster(CacheInvalidateBroadcasterConfig{
		Peers: staticPeers{"http://10.0.0.1:8090", "http://10.0.0.3:8090"},
		Doer:  doer,
		Token: "tok",
	})
	if b == nil {
		t.Fatal("broadcaster nil with a token and peers configured")
	}
	b.Broadcast(context.Background())
	if len(doer.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(doer.requests))
	}
	for _, r := range doer.requests {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, DefaultCacheInvalidatePath) {
			t.Errorf("path = %s, want suffix %s", r.URL.Path, DefaultCacheInvalidatePath)
		}
		if got := r.Header.Get(CacheInvalidateHeader); got != "tok" {
			t.Errorf("token header = %q, want tok", got)
		}
	}
}

// TestCacheInvalidateBroadcastToleratesPeerError confirms one unreachable
// peer does not abort the fan-out to the others. spec: §25.5 line 2751
// (best-effort propagation; periodic refresh is the backstop).
func TestCacheInvalidateBroadcastToleratesPeerError(t *testing.T) {
	doer := &fakeDoer{err: io.ErrUnexpectedEOF}
	b := NewCacheInvalidateBroadcaster(CacheInvalidateBroadcasterConfig{
		Peers: staticPeers{"http://10.0.0.1:8090", "http://10.0.0.3:8090"},
		Doer:  doer,
		Token: "tok",
	})
	b.Broadcast(context.Background()) // must not panic
	if len(doer.requests) != 2 {
		t.Fatalf("requests = %d, want 2 (every peer attempted despite errors)", len(doer.requests))
	}
}

// TestCacheInvalidateBroadcasterDisabledWithoutToken confirms an empty
// token (dev / no shared key) disables the RPC entirely.
func TestCacheInvalidateBroadcasterDisabledWithoutToken(t *testing.T) {
	if b := NewCacheInvalidateBroadcaster(CacheInvalidateBroadcasterConfig{
		Peers: staticPeers{"http://x"}, Token: "",
	}); b != nil {
		t.Error("broadcaster non-nil with no token")
	}
	// A nil broadcaster's Broadcast is a safe no-op.
	var nilB *CacheInvalidateBroadcaster
	nilB.Broadcast(context.Background())
}
