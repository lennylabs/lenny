// SPDX-License-Identifier: MIT

package opsservice

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
)

// CacheInvalidateHeader is the HTTP header the §25.5
// subscription_cache_invalidate RPC carries. spec: §25.5 line 2751.
const CacheInvalidateHeader = "X-Lenny-Cache-Invalidate"

// cacheInvalidateMessage is the fixed message the invalidate token
// authenticates. The RPC injects no data — it only kicks a cache
// refresh — so a single shared-secret-derived token is sufficient to
// authenticate a peer without minting an OIDC bearer.
const cacheInvalidateMessage = "subscription_cache_invalidate"

// InvalidateToken derives the §25.5 subscription_cache_invalidate RPC
// token from the shared HMAC key both peers already mount (the gateway
// Token Service / embedded OIDC signing key). Deriving the token from a
// key both replicas hold avoids a separate Secret and lets a peer
// authenticate the idempotent refresh kick without an OIDC bearer. An
// empty key yields an empty token, which disables the RPC. spec: §25.5
// line 2751.
func InvalidateToken(sharedKey []byte) string {
	if len(sharedKey) == 0 {
		return ""
	}
	mac := hmac.New(sha256.New, sharedKey)
	mac.Write([]byte(cacheInvalidateMessage))
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyInvalidateToken reports whether got matches want in constant
// time. An empty want never verifies (the RPC is disabled).
func VerifyInvalidateToken(want, got string) bool {
	if want == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(want), []byte(got)) == 1
}

// PeerLister returns the base URLs of the other lenny-ops replicas the
// invalidate RPC fans out to (this replica excluded).
type PeerLister interface {
	Peers(ctx context.Context) ([]string, error)
}

// HTTPDoer is the subset of *http.Client the broadcaster needs; tests
// substitute a fake.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// EndpointsPeerLister resolves peer replica base URLs from the lenny-ops
// Service Endpoints, reusing the same Endpoints object the §25.4 replica
// counter reads. It excludes the local replica by its own pod IP so a
// replica does not broadcast to itself. spec: §25.5 line 2751 — "over
// the lenny-ops headless Service".
type EndpointsPeerLister struct {
	endpoints corev1client.EndpointsGetter
	namespace string
	service   string
	scheme    string
	port      int
	selfIP    string
}

// EndpointsPeerListerConfig configures an EndpointsPeerLister.
type EndpointsPeerListerConfig struct {
	Endpoints corev1client.EndpointsGetter
	Namespace string
	Service   string
	// Scheme is "http" or "https" for the peer admin port. Defaults to
	// "http".
	Scheme string
	// Port is the lenny-ops admin port peers listen on.
	Port int
	// SelfIP is this pod's IP, excluded from the peer set.
	SelfIP string
}

// NewEndpointsPeerLister returns a peer lister over the named Service
// Endpoints.
func NewEndpointsPeerLister(cfg EndpointsPeerListerConfig) *EndpointsPeerLister {
	scheme := cfg.Scheme
	if scheme == "" {
		scheme = "http"
	}
	return &EndpointsPeerLister{
		endpoints: cfg.Endpoints,
		namespace: cfg.Namespace,
		service:   cfg.Service,
		scheme:    scheme,
		port:      cfg.Port,
		selfIP:    cfg.SelfIP,
	}
}

// Peers lists every ready replica address except this pod's own.
func (l *EndpointsPeerLister) Peers(ctx context.Context) ([]string, error) {
	ep, err := l.endpoints.Endpoints(l.namespace).Get(ctx, l.service, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	var peers []string
	for _, subset := range ep.Subsets {
		for _, addr := range subset.Addresses {
			if addr.IP == "" || addr.IP == l.selfIP {
				continue
			}
			peers = append(peers, fmt.Sprintf("%s://%s:%d", l.scheme, addr.IP, l.port))
		}
	}
	return peers, nil
}

// CacheInvalidateBroadcaster fans the §25.5 subscription_cache_invalidate
// RPC out to peer lenny-ops replicas so a subscription change made on one
// replica reaches every replica's delivery-worker cache within a few
// hundred milliseconds, regardless of the periodic refresh interval.
// spec: §25.5 lines 2751, 2756.
type CacheInvalidateBroadcaster struct {
	peers   PeerLister
	doer    HTTPDoer
	token   string
	path    string
	timeout time.Duration
	logger  *slog.Logger
}

// CacheInvalidateBroadcasterConfig configures a broadcaster.
type CacheInvalidateBroadcasterConfig struct {
	Peers PeerLister
	// Doer issues the peer requests. A nil Doer uses an http.Client bound
	// by Timeout.
	Doer HTTPDoer
	// Token is the shared-secret-derived invalidate token. An empty token
	// disables broadcasting (the RPC is unconfigured).
	Token string
	// Path is the peer invalidate route. Defaults to the canonical path.
	Path string
	// Timeout bounds each peer request when Doer is nil. Defaults to 3s.
	Timeout time.Duration
	Logger  *slog.Logger
}

// DefaultCacheInvalidatePath is the canonical §25.5 invalidate RPC route.
const DefaultCacheInvalidatePath = "/internal/v1/event-subscriptions/cache/invalidate"

// NewCacheInvalidateBroadcaster builds a broadcaster. It returns nil when
// no token is configured, so the caller wires nothing and the cache
// degrades to periodic-refresh-only.
func NewCacheInvalidateBroadcaster(cfg CacheInvalidateBroadcasterConfig) *CacheInvalidateBroadcaster {
	if cfg.Token == "" || cfg.Peers == nil {
		return nil
	}
	path := cfg.Path
	if path == "" {
		path = DefaultCacheInvalidatePath
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	doer := cfg.Doer
	if doer == nil {
		doer = &http.Client{Timeout: timeout}
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &CacheInvalidateBroadcaster{
		peers: cfg.Peers, doer: doer, token: cfg.Token,
		path: path, timeout: timeout, logger: logger,
	}
}

// Broadcast sends the invalidate RPC to every peer. It is best-effort:
// a peer that is unreachable or errors is logged and skipped (that
// replica still picks up the change at its next periodic refresh), so
// one slow peer never blocks the CRUD that triggered the broadcast.
func (b *CacheInvalidateBroadcaster) Broadcast(ctx context.Context) {
	if b == nil {
		return
	}
	peers, err := b.peers.Peers(ctx)
	if err != nil {
		b.logger.Warn("ops subscription cache invalidate: peer discovery failed", "error", err)
		return
	}
	for _, peer := range peers {
		b.post(ctx, peer)
	}
}

func (b *CacheInvalidateBroadcaster) post(ctx context.Context, peer string) {
	url := strings.TrimRight(peer, "/") + b.path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		b.logger.Warn("ops subscription cache invalidate: build request", "peer", peer, "error", err)
		return
	}
	req.Header.Set(CacheInvalidateHeader, b.token)
	resp, err := b.doer.Do(req)
	if err != nil {
		b.logger.Warn("ops subscription cache invalidate: peer unreachable", "peer", peer, "error", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		b.logger.Warn("ops subscription cache invalidate: peer rejected", "peer", peer, "status", resp.StatusCode)
	}
}
