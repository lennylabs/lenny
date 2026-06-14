// SPDX-License-Identifier: MIT

// Package backends provides §25.3 health.Checkers for the gateway's
// external backing services. Each checker pings its backend within a
// bounded timeout and reports a healthy or unhealthy Component with
// the §25.3 operability hints (a suggested action and a runbook
// reference) an AI-DevOps agent can act on.
//
// The dependency-heavy backend probes live here, in a subpackage, so
// the parent health package stays free of the pgx and Redis imports.
package backends

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/health"
	"github.com/lennylabs/lenny/pkg/ops/conventions"
)

// probeTimeout bounds a health ping so a hung backend cannot stall the
// §25.3 health request path. spec: §25.3 line 441 — "Each probe has a
// hard timeout of 2 seconds."
const probeTimeout = 2 * time.Second

// certExpiryWarning is the lead time at which a cert-manager-issued
// certificate is reported degraded so an operator notices before it
// lapses. It matches the §16.5 CertExpiryImminent alert threshold
// (min(lenny_cert_expiry_seconds) < 3600). spec: §16.5; §25.3 line 441.
const certExpiryWarning = time.Hour

// ProbeFunc is a §25.3 single-query dependency probe. It returns nil when
// the backing dependency answered within the caller's deadline and an
// error otherwise. spec: §25.3 line 441 — "TCP connect + single-query
// probes".
type ProbeFunc func(ctx context.Context) error

// breakerCacheStaleAfter is the §11.7 staleness budget for the
// circuit-breaker cache: a snapshot older than this is reported
// degraded because the cache has stopped reconciling with Redis.
const breakerCacheStaleAfter = 5 * time.Second

// breakerCacheAction builds the §25.3 singular remediation hint for a
// degraded circuit-breaker cache. The remediation is to restore Redis
// connectivity; detail describes the gateway's fail-open behaviour
// while the snapshot is stale. spec: §25.3 lines 459-501.
func breakerCacheAction(detail string) *conventions.SuggestedAction {
	return &conventions.SuggestedAction{
		Action:    "INVESTIGATE_REDIS",
		Reasoning: "Verify Redis reachability; " + detail + ".",
		Runbook:   health.RunbookForIssue("REDIS_UNREACHABLE"),
	}
}

// Postgres returns a health.Checker that pings the Postgres pool.
func Postgres(pool *pgxpool.Pool, name string) health.Checker {
	return health.CheckerFunc{
		ComponentName: name,
		Fn: func(ctx context.Context) health.Component {
			pingCtx, cancel := context.WithTimeout(ctx, probeTimeout)
			defer cancel()
			if err := pool.Ping(pingCtx); err != nil {
				return health.Component{
					Name:   name,
					Status: health.StatusUnhealthy,
					Detail: "postgres ping failed: " + err.Error(),
					// spec: §25.3 lines 459-501 / §25.7 line 3226 — the
					// Issue selects both the Path B runbook and the
					// structured suggestedAction through the catalog the
					// aggregator applies; the checker does not duplicate
					// the hint inline.
					Issue: "POSTGRES_UNREACHABLE",
				}
			}
			return health.Component{
				Name:   name,
				Status: health.StatusHealthy,
				Detail: "postgres reachable",
			}
		},
	}
}

// Redis returns a health.Checker that pings the Redis client.
func Redis(client redis.UniversalClient, name string) health.Checker {
	return health.CheckerFunc{
		ComponentName: name,
		Fn: func(ctx context.Context) health.Component {
			pingCtx, cancel := context.WithTimeout(ctx, probeTimeout)
			defer cancel()
			if err := client.Ping(pingCtx).Err(); err != nil {
				return health.Component{
					Name:   name,
					Status: health.StatusUnhealthy,
					Detail: "redis ping failed: " + err.Error(),
					// spec: §25.3 lines 459-501 / §25.7 line 3227 — the
					// aggregator resolves the structured hint and runbook
					// from the Issue code.
					Issue: "REDIS_UNREACHABLE",
				}
			}
			return health.Component{
				Name:   name,
				Status: health.StatusHealthy,
				Detail: "redis reachable",
			}
		},
	}
}

// BreakerCache is the freshness view of the §11.6 circuit-breaker
// cache. *cachingstore.Store satisfies it.
type BreakerCache interface {
	LastRefresh() time.Time
}

// CircuitBreakerCache returns a health.Checker that reports the
// freshness of the §11.6 circuit-breaker cache. When the cache has not
// reconciled with Redis within the §11.7 5-second budget the component
// is degraded rather than unhealthy: the gateway keeps admitting
// requests against the last known snapshot, which §11.7 states is
// strictly better than reporting nothing during a Redis outage.
func CircuitBreakerCache(cache BreakerCache, name string) health.Checker {
	return health.CheckerFunc{
		ComponentName: name,
		Fn: func(context.Context) health.Component {
			last := cache.LastRefresh()
			if last.IsZero() {
				return health.Component{
					Name:   name,
					Status: health.StatusDegraded,
					Detail: "circuit-breaker cache has not completed its first refresh",
					// The breaker-cache staleness is a Redis-connectivity
					// symptom rather than a §25.7 Path B issue code, so the
					// checker carries the singular hint directly.
					// spec: §25.3 lines 459-501.
					SuggestedAction: breakerCacheAction("the gateway admits requests against an empty breaker snapshot until the first refresh succeeds"),
				}
			}
			if age := time.Since(last); age > breakerCacheStaleAfter {
				return health.Component{
					Name:            name,
					Status:          health.StatusDegraded,
					Detail:          fmt.Sprintf("circuit-breaker cache is %s stale; serving the last known snapshot", age.Round(time.Second)),
					SuggestedAction: breakerCacheAction("the gateway admits requests against a stale breaker snapshot until the cache refreshes"),
				}
			}
			return health.Component{
				Name:   name,
				Status: health.StatusHealthy,
				Detail: "circuit-breaker cache fresh",
			}
		},
	}
}

// SIEMForwarder is the §11.7 SIEM delivery view a health probe reads.
// *siem.Forwarder satisfies Healthy(); *siem.CountingMetrics satisfies
// FailureRate(). The probe stays free of the siem import by depending on
// these narrow readers.
type SIEMForwarder interface {
	// Healthy reports whether the most recent SIEM batch delivery
	// succeeded.
	Healthy() bool
}

// SIEMFailureRate reports the §11.7 SIEM delivery failure rate as a
// percentage so the probe can compare it against
// audit.siem.failureThresholdPercent.
type SIEMFailureRate interface {
	FailureRate() float64
}

// SIEM returns a §11.7 item 4 health.Checker for the SIEM forwarder. It
// reports degraded when the delivery failure rate exceeds
// thresholdPercent (default 5%) or the most recent batch delivery
// failed, matching the spec's "if the failure rate exceeds the
// configured threshold ... the /healthz endpoint reports degraded
// status". The status is StatusDegraded rather than StatusUnhealthy
// because audit rows continue to persist durably in Postgres during a
// SIEM outage — the external copy is impaired, not the audit pipeline.
//
// The gateway does not gate its liveness probe (/healthz) or readiness
// probe (/readyz) on this component: a SIEM outage is shared across
// every replica, so failing readiness would remove all replicas from
// the Service and turn an audit-integrity degradation into a full
// outage. The degraded verdict surfaces on the §25.3 health API and
// drives the §16.5 alert instead. spec: §11.7 item 4 line 372.
func SIEM(fwd SIEMForwarder, rate SIEMFailureRate, thresholdPercent float64, name string) health.Checker {
	if thresholdPercent <= 0 {
		thresholdPercent = 5
	}
	return health.CheckerFunc{
		ComponentName: name,
		Fn: func(context.Context) health.Component {
			fr := rate.FailureRate()
			if fr > thresholdPercent {
				return health.Component{
					Name:   name,
					Status: health.StatusDegraded,
					Detail: fmt.Sprintf("SIEM delivery failure rate %.1f%% exceeds the %.1f%% threshold; audit rows persist in Postgres but the external immutable copy is lagging", fr, thresholdPercent),
					// spec: §25.3 lines 459-501 — the aggregator resolves
					// the structured hint and runbook from the Issue code.
					Issue: "AUDIT_SIEM_DELIVERY_DEGRADED",
				}
			}
			if !fwd.Healthy() {
				return health.Component{
					Name:   name,
					Status: health.StatusDegraded,
					Detail: "the most recent SIEM batch delivery failed",
					// spec: §25.3 lines 459-501 — the aggregator resolves
					// the structured hint and runbook from the Issue code.
					Issue: "AUDIT_SIEM_DELIVERY_DEGRADED",
				}
			}
			return health.Component{
				Name:   name,
				Status: health.StatusHealthy,
				Detail: "SIEM delivery healthy",
			}
		},
	}
}

// ObjectStore returns the §25.3 MinIO/ArtifactStore dependency probe. It
// runs the supplied HeadBucket-equivalent probe within the 2-second
// timeout and reports the component name the §25.3 Degradation section
// uses ("objectStore"). On failure it stamps MINIO_UNREACHABLE so the
// aggregator resolves the singular suggestedAction and the minio-failure
// runbook. spec: §25.3 line 441 ("MinIO (HeadBucket)"), lines 527-528
// ("If MinIO is unreachable: objectStore.status reports unhealthy").
func ObjectStore(probe ProbeFunc, name string) health.Checker {
	return health.CheckerFunc{
		ComponentName: name,
		Fn: func(ctx context.Context) health.Component {
			pingCtx, cancel := context.WithTimeout(ctx, probeTimeout)
			defer cancel()
			if err := probe(pingCtx); err != nil {
				return health.Component{
					Name:   name,
					Status: health.StatusUnhealthy,
					Detail: "object store probe failed: " + err.Error(),
					Issue:  "MINIO_UNREACHABLE",
				}
			}
			return health.Component{
				Name:   name,
				Status: health.StatusHealthy,
				Detail: "object store reachable",
			}
		},
	}
}

// APIServer returns the §25.3 Kubernetes API server dependency probe. It
// runs the supplied GET /healthz probe within the 2-second timeout. The
// gateway depends on the API server to claim warm pods, so an
// unreachable API server is reported unhealthy with an inline
// investigate hint. spec: §25.3 line 441 ("K8s API server (/healthz)").
func APIServer(probe ProbeFunc, name string) health.Checker {
	return health.CheckerFunc{
		ComponentName: name,
		Fn: func(ctx context.Context) health.Component {
			pingCtx, cancel := context.WithTimeout(ctx, probeTimeout)
			defer cancel()
			if err := probe(pingCtx); err != nil {
				return health.Component{
					Name:   name,
					Status: health.StatusUnhealthy,
					Detail: "kubernetes API server /healthz probe failed: " + err.Error(),
					SuggestedAction: &conventions.SuggestedAction{
						Action:    "INVESTIGATE_KUBE_API",
						Reasoning: "The Kubernetes API server is unreachable; warm-pool claims and pod admission stall until it recovers.",
					},
				}
			}
			return health.Component{
				Name:   name,
				Status: health.StatusHealthy,
				Detail: "kubernetes API server reachable",
			}
		},
	}
}

// Connectors returns the §25.3 registered-connectors dependency probe. It
// runs a single-query reachability check against the connector registry
// within the 2-second timeout. A query failure means the registry store
// is unreachable, so the gateway cannot resolve a session's connectors.
// spec: §25.3 line 441 ("registered connectors").
func Connectors(probe ProbeFunc, name string) health.Checker {
	return health.CheckerFunc{
		ComponentName: name,
		Fn: func(ctx context.Context) health.Component {
			pingCtx, cancel := context.WithTimeout(ctx, probeTimeout)
			defer cancel()
			if err := probe(pingCtx); err != nil {
				return health.Component{
					Name:   name,
					Status: health.StatusUnhealthy,
					Detail: "connector registry probe failed: " + err.Error(),
					SuggestedAction: &conventions.SuggestedAction{
						Action:    "INVESTIGATE_CONNECTOR_REGISTRY",
						Reasoning: "The connector registry is unreachable; sessions that depend on registered connectors cannot be created until it recovers.",
					},
				}
			}
			return health.Component{
				Name:   name,
				Status: health.StatusHealthy,
				Detail: "connector registry reachable",
			}
		},
	}
}

// CertReader reads the expiry of the cert-manager-issued certificate the
// gateway depends on. FileCertReader satisfies it from a mounted PEM
// file. spec: §25.3 line 441 ("cert-manager (certificate status)").
type CertReader interface {
	// NotAfter returns the certificate's expiry instant. A non-nil error
	// means the certificate could not be read or parsed.
	NotAfter(ctx context.Context) (time.Time, error)
}

// CertManager returns the §25.3 cert-manager dependency probe. It reads
// the gateway's cert-manager-managed certificate status and reports
// unhealthy when the certificate is unreadable or already expired,
// degraded when it expires within certExpiryWarning, and healthy
// otherwise. An imminent or lapsed certificate stamps
// CERT_EXPIRY_IMMINENT so the aggregator resolves the renew-certificate
// suggestedAction and the cert-manager-outage runbook. spec: §25.3 line
// 441; §16.5 CertExpiryImminent.
func CertManager(reader CertReader, name string) health.Checker {
	return health.CheckerFunc{
		ComponentName: name,
		Fn: func(ctx context.Context) health.Component {
			pingCtx, cancel := context.WithTimeout(ctx, probeTimeout)
			defer cancel()
			notAfter, err := reader.NotAfter(pingCtx)
			if err != nil {
				return health.Component{
					Name:   name,
					Status: health.StatusUnhealthy,
					Detail: "certificate status unreadable: " + err.Error(),
					Issue:  "CERT_EXPIRY_IMMINENT",
				}
			}
			remaining := time.Until(notAfter)
			switch {
			case remaining <= 0:
				return health.Component{
					Name:   name,
					Status: health.StatusUnhealthy,
					Detail: fmt.Sprintf("certificate expired %s ago", (-remaining).Round(time.Second)),
					Issue:  "CERT_EXPIRY_IMMINENT",
				}
			case remaining < certExpiryWarning:
				return health.Component{
					Name:   name,
					Status: health.StatusDegraded,
					Detail: fmt.Sprintf("certificate expires in %s", remaining.Round(time.Second)),
					Issue:  "CERT_EXPIRY_IMMINENT",
				}
			default:
				return health.Component{
					Name:   name,
					Status: health.StatusHealthy,
					Detail: fmt.Sprintf("certificate valid for %s", remaining.Round(time.Second)),
				}
			}
		},
	}
}

// FileCertReader reads the leaf certificate's NotAfter from a PEM file on
// disk (the cert-manager-managed Secret mounted into the gateway pod). It
// re-reads on every probe so a cert-manager renewal is observed without a
// gateway restart; the §25.3 5-second probe cache bounds the read rate.
func FileCertReader(path string) CertReader { return fileCertReader{path: path} }

type fileCertReader struct{ path string }

func (r fileCertReader) NotAfter(context.Context) (time.Time, error) {
	data, err := os.ReadFile(r.path)
	if err != nil {
		return time.Time{}, err
	}
	for len(data) > 0 {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return time.Time{}, err
		}
		return cert.NotAfter, nil
	}
	return time.Time{}, fmt.Errorf("backends: no CERTIFICATE block in %s", r.path)
}
