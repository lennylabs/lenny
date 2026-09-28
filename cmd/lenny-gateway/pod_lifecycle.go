// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/adapter"
	agentpodstatepg "github.com/lennylabs/lenny/pkg/agentpodstate/pgstore"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointer"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointretention"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/serviceidentity"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/recycle"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
	"github.com/lennylabs/lenny/pkg/storerouter"
)

// adapterGRPCPort is the TCP port a Sandbox pod's §4.7 adapter listens
// on. §13.2 fixes the gateway↔adapter link to TCP 50051.
const adapterGRPCPort = 50051

// buildPodLifecycle constructs the §15.1 pod-placement surfaces when
// --agent-namespace is set: the cluster client, the §25.3 apiserver health
// probe, the §10.2 SA-token verifier, the §5.2 slot counter, the reserved-hold
// reserved-hold and recycle-boundary coordinators, the §4.7 pod binder, the
// §4.6.1 Postgres-backed fallback claim, the §4.4/§12.5 checkpointer, and the
// §7.1 seal-and-export sealer, recording each on the accumulator. Without
// --agent-namespace every field stays nil and the in-process / subprocess
// executor recorded by buildExecutorAndCredentials remains in force.
//
// checkpointRetention is threaded in by buildStores because it has no
// accumulator field and is consumed only by the checkpointer built here.
//
// spec: §4.1 gateway subsystem seams; §15.1 pod placement; §4.7 binder;
// §5.2 slot counter; recycle.
func (w *gatewayWiring) buildPodLifecycle(checkpointRetention checkpointretention.Store) {
	f := w.f
	adapterCA := f.adapterCA
	adapterKeepaliveTimeMs := f.adapterKeepaliveTimeMs
	adapterKeepaliveTimeoutMs := f.adapterKeepaliveTimeoutMs
	adapterTLSCert := f.adapterTLSCert
	adapterTLSKey := f.adapterTLSKey
	agentNamespace := f.agentNamespace
	checkpointInterval := f.checkpointInterval
	checkpointJitterFraction := f.checkpointJitterFraction
	checkpointGrantWindow := f.checkpointGrantWindow
	checkpointCapabilityTTLSeconds := f.checkpointCapabilityTTLSeconds
	claimHoldTTLSeconds := f.claimHoldTTLSeconds
	clusterBurst := f.clusterBurst
	clusterQPS := f.clusterQPS
	saTokenAudience := f.saTokenAudience
	adminSATokenAudience := f.adminSATokenAudience
	slotCounterPostgresFallbackMaxSeconds := f.slotCounterPostgresFallbackMaxSeconds

	// The stores, Redis client, executor, credential assigner, and pool
	// store were recorded on the accumulator by the earlier build steps.
	pgPool := w.pgPool
	redisClient := w.redisClient
	concernRedis := w.concernRedis
	sessions := w.sessions
	pools := w.pools
	blobs := w.blobs
	credAssign := w.credAssign
	exec := w.exec
	// §10.1 / §11.2 / §12.5 — the object-store, manifest, catalog, and
	// storage-quota seams the checkpoint driver produces against. Recorded
	// on the accumulator by buildStores/buildRedisAndQuota, they are read
	// here into the §15.1 checkpointer construction below.
	objectStore := w.objectStore
	partialManifests := w.partialManifests
	blobsCataloged := w.blobsCataloged
	minioStore := w.minioStore
	storageCounter := w.storageCounter
	tenants := w.tenants

	// §15.1 pod placement: with --agent-namespace the gateway claims a
	// §5 warm pod for each started session and dispatches its messages
	// to the pod's §4.7 adapter. The in-process and subprocess
	// executors stay available for local development.
	var (
		podBinder     *podsession.Binder
		podRegistry   *podsession.Registry
		checkpointSvc *checkpointer.Checkpointer
		// holdCoordinator runs the reserved-hold expiry timers on this
		// replica. It is shared between the Binder (whose acquisition-path
		// rebind cancels the local timer) and the scrub-report service (whose
		// recycle disposition driver arms the timer after a reserved patch). It
		// stays nil when --agent-namespace is unset (single-process dev) so
		// the recycle path and the rebind branch are inert there.
		holdCoordinator *recycle.HoldCoordinator
		// recycleBoundary runs the gateway-side recycle-boundary timers on
		// this replica: the missing-report timeout armed at the bound → recycling
		// patch (cancelled by ReportPodScrub, retiring the pod if no report
		// arrives within cleanupTimeoutSeconds plus a grace) and the preConnect
		// re-warm completion poll that drives the claim recycling → reserved once
		// the SDK re-warm makes the pod Ready. It is shared between the Binder /
		// SlotClaimer (which arm the timeout at the recycle patch) and the
		// scrub-report disposition driver (which cancels it and starts the
		// re-warm poll). It stays nil when --agent-namespace is unset so the
		// recycle path is inert there; the §4.6.1 orphan GC remains the
		// coordinator-crash backstop.
		recycleBoundary *recycle.RecycleBoundaryCoordinator
		// clusterClient is the controller-runtime client used by the
		// session-start path and by the §10.4 PDB poller (F-10.4.4). It
		// stays nil when --agent-namespace is unset (single-process dev).
		clusterClient client.Client
		// kubeHealthzProbe is the §25.3 Kubernetes API server dependency
		// probe (GET /healthz). It stays nil when --agent-namespace is
		// unset, so the §25.3 health surface omits the component on a
		// Postgres-only deployment with no cluster client.
		kubeHealthzProbe func(context.Context) error
		// saTokenVerifier validates the projected SA token's signature and
		// audience on every pod→gateway GatewayControl request via a
		// Kubernetes TokenReview (§10.2). It stays nil when there
		// is no in-cluster client or no configured audience, in which case
		// the SA-token interceptor degrades to the audience-only decode.
		saTokenVerifier leasecontrol.TokenVerifier
		// saUserVerifier is the same TokenReview verifier in the form the
		// §25.4 admin-API service-identity resolver needs: it reports the
		// ServiceAccount username the apiserver authenticated, so the resolver
		// can grant a role to a named account rather than to any authentic
		// token.
		saUserVerifier serviceidentity.SATokenAuthenticator
	)
	if *agentNamespace != "" {
		cfg, err := ctrl.GetConfig()
		if err != nil {
			log.Fatalf("lenny-gateway: resolve cluster config for --agent-namespace: %v", err)
		}
		// The gateway's session-start path issues 5+ Kubernetes API
		// calls per request (list pools, get template, list sandboxes,
		// patch sandbox, create claim). client-go's default of QPS=5 /
		// Burst=10 saturates at trivial load; the gateway logs spam
		// "Waited Ns due to client-side throttling" and each
		// session-start picks up >1s of added latency. The spec
		// mandates explicit QPS for the controller (§4.6.1) but leaves
		// the gateway-side throttle to operator tuning, so --cluster-qps
		// / --cluster-burst (defaults 100 / 200) are configurable like
		// the controller's --create-qps / --status-qps flags. The
		// kube-apiserver's own priority+fairness shaping remains the
		// production-bounded gate; this client-side limit is the safety
		// net against runaway clients.
		cfg.QPS = float32(*clusterQPS)
		cfg.Burst = *clusterBurst
		scheme := k8sruntime.NewScheme()
		utilruntime.Must(clientgoscheme.AddToScheme(scheme))
		utilruntime.Must(lennyv1.AddToScheme(scheme))
		k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
		if err != nil {
			log.Fatalf("lenny-gateway: build cluster client: %v", err)
		}
		clusterClient = k8sClient
		// spec: §25.3 — the §25.3 health service probes the
		// Kubernetes API server with a GET /healthz over the cluster
		// transport. Built here where the rest config is in scope; wired
		// onto the health aggregator below.
		probe, err := podsession.NewHealthzProbe(cfg)
		if err != nil {
			log.Fatalf("lenny-gateway: build apiserver /healthz probe: %v", err)
		}
		kubeHealthzProbe = probe
		// spec: §10.2 — "Pods cannot forge or extend this token.
		// The gateway validates the signature on every pod→gateway
		// request." When an audience is configured the gateway validates
		// the projected SA token via a Kubernetes TokenReview (the
		// apiserver checks the SA-issuer signature and expiry), binding the
		// deployment-specific audience at the same time. Built from a
		// clientset here where the rest config is in scope; wired onto the
		// §8.6 GatewayControl listener below. F-10.2.10.
		// The same verifier backs the §25.4 admin-API service-identity
		// resolver, whose audience is configured separately from the §10.3
		// GatewayControl audience, so it is built whenever either is set.
		if *saTokenAudience != "" || *adminSATokenAudience != "" {
			cs, err := kubernetes.NewForConfig(cfg)
			if err != nil {
				log.Fatalf("lenny-gateway: §10.2 build TokenReview client: %v", err)
			}
			verifier := leasecontrol.TokenReviewVerifier{
				Reviews: cs.AuthenticationV1().TokenReviews(),
			}
			saUserVerifier = verifier
			if *saTokenAudience != "" {
				saTokenVerifier = verifier
			}
		}
		dialOpt, err := adapter.TLSClientOption(*adapterTLSCert, *adapterTLSKey, *adapterCA)
		if err != nil {
			log.Fatalf("lenny-gateway: adapter TLS: %v", err)
		}
		// spec: §11.3 — gateway→pod keepalive: 10s interval
		// and 5s timeout. Without these the gRPC client library default
		// (no keepalive) leaves a half-open TCP connection holding
		// adapter state past the §11.3 timeout. F-11.3.12.
		keepaliveOpt := grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                time.Duration(*adapterKeepaliveTimeMs) * time.Millisecond,
			Timeout:             time.Duration(*adapterKeepaliveTimeoutMs) * time.Millisecond,
			PermitWithoutStream: true,
		})
		podRegistry = podsession.NewRegistry()
		// §5.2 atomic slot counter. When Redis is wired, every
		// concurrent-mode slot reservation goes through the Redis Lua
		// GET-compare-INCR sequence so two gateway replicas racing on
		// the same pod cannot transiently exceed maxConcurrent. The
		// counter (with its §12.4 Postgres fallback) is the only
		// intra-pod capacity gate; a nil counter makes SlotClaimer
		// ClaimSlot and ReleaseSlot fail closed rather than overrun or
		// over-release the pod, so a concurrent-mode pool requires Redis.
		var slotCounter *slotcounter.Counter
		if redisClient != nil {
			// §5.2: the SessionStore is the post-recovery
			// rehydration seed source. After a Redis restart the first
			// slot reservation on each concurrent-mode pod re-seeds the
			// pod's active_slots counter from GetActiveSlotsByPod before
			// any new slot is allowed, closing the over-commit race.
			// §12.4: the SessionStore is also the Redis-outage
			// capacity-gate fallback. When Redis is unreachable the counter
			// gates intra-pod slot admission on GetActiveSlotsByPod under a
			// per-pod Postgres advisory lock (ReserveSlotUnderLock), failing
			// closed only after the bounded outage window.
			// §12.4: the Postgres-fallback window is operator-tunable
			// (gateway.slotCounterPostgresFallbackMaxSeconds) so the spec-default
			// 60s bounded outage window is not hardcoded; a non-positive value
			// keeps the default.
			slotCounter = slotcounter.New(concernRedis.For(storerouter.RedisConcernCoordination),
				slotcounter.WithSlotSource(sessions),
				slotcounter.WithFallbackSource(sessions),
				slotcounter.WithFallbackMaxWindow(time.Duration(*slotCounterPostgresFallbackMaxSeconds)*time.Second))
		}
		// reserved-hold coordinator: arms the per-claim hold-TTL expiry
		// timer after a recycle reserves the claim, and is cancelled on an
		// acquisition-path rebind. Constructed here (with the cluster client and
		// agent namespace available) and shared with the scrub-report service so
		// the disposition driver and the rebind branch use one timer registry.
		holdCoordinator, err = recycle.NewHoldCoordinator(recycle.HoldCoordinatorOptions{
			Client:    k8sClient,
			Namespace: *agentNamespace,
			Now:       clockinject.Now,
		})
		if err != nil {
			log.Fatalf("lenny-gateway: reserved-hold coordinator: %v", err)
		}
		// recycle-boundary coordinator: arms the missing-report timeout at
		// the bound → recycling patch and drives the preConnect re-warm
		// completion (recycling → reserved) once the SDK re-warm makes the pod
		// Ready. It hands the re-warm-completion reserve token to the
		// holdCoordinator so the hold-TTL expiry timer is armed on the same
		// registry the non-preConnect reserve uses.
		recycleBoundary, err = recycle.NewRecycleBoundaryCoordinator(recycle.RecycleBoundaryCoordinatorOptions{
			Client:    k8sClient,
			Namespace: *agentNamespace,
			Pools:     pools,
			HoldTTL:   time.Duration(*claimHoldTTLSeconds) * time.Second,
			Holds:     holdCoordinator,
			Now:       clockinject.Now,
		})
		if err != nil {
			log.Fatalf("lenny-gateway: recycle-boundary coordinator: %v", err)
		}
		podBinder = &podsession.Binder{
			Client:           k8sClient,
			Namespace:        *agentNamespace,
			AdapterPort:      adapterGRPCPort,
			AcceptedVersions: []string{adapter.ProtocolVersionV1},
			HoldCanceller:    holdCoordinator,
			RecycleBoundary:  recycleBoundary,
			Now:              clockinject.Now,
			DialAdapter: func(addr string) (*adapterclient.Client, error) {
				return adapterclient.Dial(addr, dialOpt, keepaliveOpt)
			},
			Blobs: blobs,
			// §4.9: the binder mints a session's credential leases and
			// pushes them to the pod via AssignCredentials before
			// StartSession. A BindRequest that names no credential pools
			// assigns nothing.
			Credentials: credAssign,
			// §5.2 atomic slot counter (Redis-backed) with its §12.4
			// Postgres-outage fallback; nil makes concurrent-mode slot
			// assignment and release fail closed (see SlotClaimer).
			SlotCounter: slotCounter,
			// §5.1 — log the runtime.integrationLevel.underdeclared
			// warning when the adapter handshake observes a higher level
			// than the runtime declared, so the author can raise the
			// declared level in a future release.
			IntegrationLevelUnderdeclared: func(runtime, declared, observed string) {
				log.Printf("lenny-gateway: runtime.integrationLevel.underdeclared runtime=%s declaredLevel=%s observedLevel=%s",
					runtime, declared, observed)
			},
		}
		// §4.6.1 Postgres-backed fallback claim: when Postgres is
		// configured the binder reads the agent_pod_state mirror to
		// claim a pod after the Kubernetes-API claim finds none. Without
		// Postgres the Fallback field stays nil and the no-idle-pod
		// result surfaces directly.
		if pgPool != nil {
			podBinder.Fallback = agentpodstatepg.New(pgPool)
			// §4.6.1 precondition 2: probe API-server reachability (GET
			// /readyz) before each fallback claim and skip when it fails.
			probe, err := podsession.NewReadyzProbe(cfg)
			if err != nil {
				log.Fatalf("lenny-gateway: build pod-claim fallback readyz probe: %v", err)
			}
			podBinder.APIServerReachable = probe
			log.Printf("lenny-gateway: §4.6.1 Postgres-backed pod-claim fallback enabled")
		}
		exec = executor.NewPodExecutor(podRegistry, podBinder)
		// §10.1 / §13.2 — the checkpoint driver mints one presigned PUT
		// capability per chunk against the resolved object store. The §13.2
		// startup assertion above already failed boot unless that store
		// implements Presigner whenever --agent-namespace is set, so the
		// assertion holds on this path.
		checkpointPresigner, _ := objectStore.(blobstore.Presigner)
		// §12.5 — the confirmed-chunk artifact_store catalog (RecordPut) and
		// the rotation-side chunk release (SoftDeleteRow / ListByPrefix +
		// HardDeleteObject) run only against the durable Postgres+MinIO
		// backends. A pod deployment without them leaves each interface field
		// nil rather than wrapping a nil concrete pointer, which the driver's
		// nil guards then skip.
		var checkpointChunkRecorder checkpointer.ChunkRecorder
		var checkpointChunkCatalog checkpointer.ChunkCatalogReleaser
		if blobsCataloged != nil {
			checkpointChunkRecorder = blobsCataloged
			checkpointChunkCatalog = blobsCataloged
		}
		var checkpointChunkObjects checkpointer.ChunkObjectStore
		if minioStore != nil {
			checkpointChunkObjects = minioStore
		}
		checkpointSvc = &checkpointer.Checkpointer{
			Sessions:       sessions,
			Registry:       podRegistry,
			Interval:       *checkpointInterval,
			JitterFraction: *checkpointJitterFraction,
			OnError: func(sessionID string, err error) {
				log.Printf("lenny-gateway: checkpoint of session %s failed: %v", sessionID, err)
			},
			// §4.4 / §12.5 — record the snapshot ref to the
			// retention catalog and Rotate so the table never holds
			// more than the latest-2 active rows per session. The
			// Metrics field is wired after gatewaymetrics.New() below
			// so the §4.4 duration histogram is emitted.
			Retention: checkpointRetention,
			// §10.1.7 / §17.8.1 / §5.2 — the deployment-wide
			// checkpointGrantWindow default and the presigned-capability
			// TTL the grant/confirm loop signs into each grant. GrantWindow
			// is the fallback the driver applies when a pool declares no
			// per-pool override; PoolGrantWindow resolves that override from
			// the §5.2 poolstore mirror.
			GrantWindow:     *checkpointGrantWindow,
			CapabilityTTL:   time.Duration(*checkpointCapabilityTTLSeconds) * time.Second,
			PoolGrantWindow: sessionserver.NewPoolPolicyReader(pools),
			// §10.1 — the produce → store pipeline seams: the
			// gateway writes the checkpoint_manifest intent/confirm/finalise
			// rows, signs per-chunk presigned PUT capabilities, confirms each
			// committed chunk's bytes-actually-written with a StatObject, and
			// catalogs it. Without these the driver aborts every non-empty
			// checkpoint at its first ChunkReady, so seal-and-export never
			// completes and a completed session's pod never reaches the §6.2
			// occupancy-zero recycle branch.
			Manifests:    partialManifests,
			Presigner:    checkpointPresigner,
			ObjectStore:  objectStore,
			Cataloging:   checkpointChunkRecorder,
			ChunkCatalog: checkpointChunkCatalog,
			ChunkObjects: checkpointChunkObjects,
			// §11.2 — reserve the probed workspace size against the tenant's
			// storage counter before any grant and reconcile it against the
			// confirmed total on every terminal arm. QuotaLimitFor resolves
			// the tenant's storageQuotaBytes at reservation time; a tenant
			// with no configured limit is unlimited and the driver skips the
			// reservation.
			Quota:         storageCounter,
			QuotaLimitFor: tenantStorageQuotaResolver(tenants),
		}
		log.Printf("lenny-gateway: placing sessions on warm pods in namespace %q", *agentNamespace)
	}

	// §7.1 seal-and-export uses the same checkpointer; an untyped-nil
	// Sealer keeps seal-and-export disabled without --agent-namespace.
	var sessionSealer sessionserver.Sealer
	if checkpointSvc != nil {
		sessionSealer = checkpointSvc
	}

	// spec: §4.1 — record the §15.1 pod-placement surfaces (and the
	// pod-executor that replaced the dev executor) on the accumulator for
	// the messaging step and the later subsystem and worker steps.
	w.exec = exec
	w.podBinder = podBinder
	w.podRegistry = podRegistry
	w.checkpointSvc = checkpointSvc
	w.holdCoordinator = holdCoordinator
	w.recycleBoundary = recycleBoundary
	w.clusterClient = clusterClient
	w.kubeHealthzProbe = kubeHealthzProbe
	w.saTokenVerifier = saTokenVerifier
	w.saUserVerifier = saUserVerifier
	w.sessionSealer = sessionSealer
}

// tenantStorageQuotaResolver returns the §11.2 checkpointer QuotaLimitFor seam:
// it resolves a tenant's configured storageQuotaBytes at reservation time so
// the driver reserves the probed workspace size against the tenant's counter
// before minting any grant. A tenant lookup failure is wrapped and named so a
// checkpoint fails closed rather than reserving against a limit it could not
// read; a tenant with no configured limit resolves to 0, which the driver
// treats as unlimited and skips the reservation. Extracted from
// buildPodLifecycle so the resolve-then-wrap path has a single canonical
// implementation the tier-1 test pins rather than an inline closure only a live
// cluster exercises.
//
// spec: §11.2; §12.4 storageQuotaBytes.
func tenantStorageQuotaResolver(tenants tenantstore.Store) func(ctx context.Context, tenantID string) (int64, error) {
	return func(ctx context.Context, tenantID string) (int64, error) {
		t, err := tenants.Get(ctx, tenantID)
		if err != nil {
			return 0, fmt.Errorf("resolve storage quota for tenant %s: %w", tenantID, err)
		}
		return t.StorageQuotaBytes, nil
	}
}
