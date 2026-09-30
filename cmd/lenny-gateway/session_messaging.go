// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"log"

	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/evalstore"
	evalpg "github.com/lennylabs/lenny/pkg/gateway/experiment/evalstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/experimentstore"
	experimentpg "github.com/lennylabs/lenny/pkg/gateway/experiment/experimentstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treearchive"
	treearchivepg "github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treearchive/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treebudget"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	interactionpg "github.com/lennylabs/lenny/pkg/gateway/session/interactionstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/memorystore"
	memorypg "github.com/lennylabs/lenny/pkg/gateway/session/memorystore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/toolapproval"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/storerouter"
)

// buildSessionMessaging constructs the §7.3 session-event bus (with its
// §12.4 (lenny:events: relay row) cross-replica relay and §7.3 durable
// last_seq hooks), the §7.2 session inbox and DLQ coordinator, the §8.10
// tree archive, the §8.2 tree
// budget reserver, the §9.2 interaction store, the §7.2 tool-approval gate
// registry, the §10.7 eval store, the §10.7 experiment store, and the §9.4
// memory store, recording each on the accumulator.
//
// spec: §4.1 gateway subsystem seams; §7.2/§7.3 messaging; §8.10 tree
// archive; §8.2 tree budget; §9.2 interactions; §9.4 memory; §10.7 evals.
func (w *gatewayWiring) buildSessionMessaging() {
	f := w.f
	evalAggregationRefreshSeconds := f.evalAggregationRefreshSeconds
	memoryEnabled := f.memoryEnabled
	memoryMaxPerUser := f.memoryMaxPerUser
	messagingDurableInbox := f.messagingDurableInbox
	messagingMaxDLQSize := f.messagingMaxDLQSize
	messagingMaxInboxSize := f.messagingMaxInboxSize
	sessionEventReplayBufferDepth := f.sessionEventReplayBufferDepth
	toolApprovalTimeout := f.toolApprovalTimeout
	treeArchiveCacheEntries := f.treeArchiveCacheEntries

	// The stores, Redis client, and executor were recorded on the
	// accumulator by the earlier build steps.
	pgPool := w.pgPool
	readPool := w.readPool
	redisClient := w.redisClient
	concernRedis := w.concernRedis
	sessions := w.sessions
	exec := w.exec

	// spec: §10.4 — replay buffer depth is operator-tunable
	// via gateway.sessionEventReplayBufferDepth. F-10.4.5.
	eventBus := sessionevents.NewBus(*sessionEventReplayBufferDepth)
	// §4.4 / §12.4 (lenny:events: relay row): when Redis is
	// wired, attach the cross-replica relay so a client reconnecting via
	// Last-Event-ID to a different replica sees prior events (the §15.1
	// streaming-reconnect contract). Single-replica dev mode keeps the
	// Bus's in-memory-only behaviour.
	if redisClient != nil {
		// §12.4 Cache/Pub-Sub concern: session-event relay.
		eventBus = eventBus.WithRedisRelay(sessionevents.NewRedisRelay(concernRedis.For(storerouter.RedisConcernCachePubSub)))
		log.Printf("lenny-gateway: §4.4 session SSE event bus relay attached to Redis (cross-replica replay enabled)")
	}
	// spec: §7.3 — wire the durable last_seq writer + the
	// coordinator-handoff seed loader so per-session SeqNum survives
	// replica restart and handoff without rewinds. The sessions
	// store carries the persisted last_seq column; both hooks are
	// best-effort (a Postgres outage degrades to the local counter).
	// F-7.3.3.
	if sessions != nil {
		lastSeq := lastSeqStore{sessions: sessions}
		eventBus = eventBus.WithLastSeqPersister(lastSeq).WithLastSeqLoader(lastSeq)
	}
	// spec: §7.2 — the session inbox + DLQ coordinator.
	// The DLQ is a Redis sorted set, so durability requires Redis; the
	// dev / no-Redis posture leaves messagingCoord nil and the gateway
	// no-ops the inbox-to-DLQ migration and the terminal DLQ drain. The
	// inbox is in-memory by default and Redis-list-backed when
	// durableInbox is set (§7.2). F-7.2.4, F-12.4.6, F-7.3.12.
	var messagingCoord *sessioninbox.Coordinator
	if redisClient != nil {
		// §12.4 Session-data concern: durable inbox + DLQ.
		sessionDataClient := concernRedis.For(storerouter.RedisConcernSessionData)
		var inbox sessioninbox.Inbox
		if *messagingDurableInbox {
			inbox = sessioninbox.NewRedisInbox(sessionDataClient, *messagingMaxInboxSize)
		} else {
			inbox = sessioninbox.NewMemoryInbox(*messagingMaxInboxSize)
		}
		messagingCoord = sessioninbox.NewCoordinator(sessioninbox.Config{
			Inbox:   inbox,
			DLQ:     sessioninbox.NewDLQ(sessionDataClient, *messagingMaxDLQSize),
			Emitter: sessionserver.NewBusEmitter(eventBus, clockinject.Now),
			Now:     clockinject.Now,
			Durable: *messagingDurableInbox,
		})
	}
	// One §8.10 tree archive shared by the sessionserver (which archives
	// children on terminal transitions) and the platform MCP tools. In
	// production the durable record lives in Postgres (migration 0100);
	// a per-replica LRU cache (§8.10, default 128 entries)
	// fronts it so a parent re-reading a child's result does not hit
	// Postgres every time. Developer mode keeps the in-memory archive,
	// which is durable for the lifetime of the single replica.
	var treeArchive treearchive.Store = treearchive.NewMemory()
	if pgPool != nil {
		treeArchive = treearchive.NewCached(treearchivepg.New(pgPool, nil, treearchivepg.WithReadPool(readPool)), *treeArchiveCacheEntries)
	}
	// §8.2 / §12.4: the Redis-backed
	// per-tree delegation budget counters gate every admission and are
	// decremented when a child settles (the §8.2 completed-
	// subtree offload). Shared by the delegation service (admission
	// Reserve) and the sessionserver (terminal-state Return). When Redis
	// is not configured the reserver stays nil and only the static
	// ValidateChildSlice ceiling is enforced. F-8.2.18 / F-8.2.12 / F-8.2.13.
	var treeBudgetReserver delegation.TreeBudgetReserver
	// hwmReader is the concrete *treebudget.Reserver kept alongside the
	// interface so the §8.3 high-watermark read (not part of the
	// narrow Reserve/Return interfaces) is reachable from the
	// sessionserver. Nil when Redis is not configured, which leaves the
	// nil interface genuinely nil so the typed-nil-in-interface trap is
	// avoided. F-8.9.6.
	var hwmReader sessionserver.DelegationHighWatermarkReader
	// treeBudgetConcrete keeps the concrete *treebudget.Reserver so the
	// §11.2 delegation-budget checkpoint/reconstruction reconciler can
	// call Snapshot/Restore (not part of the narrow Reserve/Return
	// interfaces). Nil when Redis is not configured. F-11.2.5.
	var treeBudgetConcrete *treebudget.Reserver
	if redisClient != nil {
		// §12.4 Delegation concern: tree-budget keys {root_session_id}:dlg:*.
		r := treebudget.New(concernRedis.For(storerouter.RedisConcernDelegation), 0)
		treeBudgetReserver = r
		hwmReader = r
		treeBudgetConcrete = r
	}
	// One §9.2 interaction store shared by the sessionserver (which
	// serves the respond/dismiss endpoints) and the platform MCP tools
	// (lenny/request_elicitation), so an elicitation a tool records is
	// resolvable through the REST surface.
	var interactions interactionstore.Store = interactionstore.NewMemory()
	if pgPool != nil {
		interactions = interactionpg.New(pgPool)
	}
	// spec: §7.2 — the tool-use approval loop. When a
	// runtime emits a tool_call(approvalRequired) over the §4.7 Attach
	// stream the pod executor consults the gate, which records the
	// KindToolUse interaction, publishes the tool_use_requested SSE
	// event, and blocks until the §15.1 approve/deny endpoint delivers
	// the verdict onto this shared waiter registry. The pod executor is
	// the only producer of approval-required frames, so the gate is wired
	// only when exec is a *PodExecutor (the echo / subprocess dev posture
	// never blocks on approval). F-7.2.9, F-7.2.18.
	toolApprovalWaits := toolapproval.NewRegistry()
	if pe, ok := exec.(*executor.PodExecutor); ok {
		pe.SetApprovalGate(sessionserver.NewToolApprovalGate(
			sessions, interactions, eventBus, toolApprovalWaits, clockinject.Now, *toolApprovalTimeout,
		))
	}
	var evals evalstore.Store = evalstore.NewMemory(0, nil)
	if pgPool != nil {
		evals = evalpg.New(pgPool)
	}
	// spec: §10.7 — the lenny_eval_aggregates materialized-view
	// read + REFRESH path requires a Postgres backend. Enable it only when
	// a positive refresh interval is configured against Postgres; with the
	// default 0 (or no Postgres) the §10.7 results API aggregates on read.
	evalMatviewEnabled := false
	if *evalAggregationRefreshSeconds > 0 {
		if pgPool != nil {
			evalMatviewEnabled = true
		} else {
			log.Printf("warning: --eval-aggregation-refresh-seconds=%d ignored: the §10.7 lenny_eval_aggregates materialized view requires a Postgres backend; aggregating on read", *evalAggregationRefreshSeconds)
		}
	} else if *evalAggregationRefreshSeconds < 0 {
		log.Printf("warning: --eval-aggregation-refresh-seconds=%d is negative; treating as 0 (matview disabled)", *evalAggregationRefreshSeconds)
	}
	var experiments experimentstore.Store = experimentstore.NewMemory()
	if pgPool != nil {
		experiments = experimentpg.New(pgPool)
	}
	// spec: §9.4 / §12.8 — when the feature flag
	// disables the MemoryStore, construct no store. The MCP tools
	// short-circuit on a nil Memory in mcptools.Deps. F-9.4.7.
	var memories memorystore.Store
	var memoryBackendLabel string
	if *memoryEnabled {
		// spec: §9.4 — the Embedder seam advertised for custom
		// providers is preflighted at startup so a misconfigured
		// dimension width (e.g., a provider that returns 1536-wide
		// vectors against the migration-0044 vector(256) column) fails
		// at boot rather than corrupting every Write with an unfriendly
		// pgvector dimension-mismatch error. The built-in
		// HashingEmbedder is correct by construction; the preflight is
		// still cheap and pins the contract for future seam swaps.
		// F-9.4.8.
		if err := memorystore.ValidateEmbedder(memorystore.NewHashingEmbedder()); err != nil {
			log.Fatalf("lenny-gateway: memorystore embedder preflight failed (§9.4): %v", err)
		}
		if pgPool != nil {
			pgmem := memorypg.NewWithMaxPerUser(pgPool, *memoryMaxPerUser)
			memories = pgmem
			memoryBackendLabel = "postgres"
		} else {
			inMem := memorystore.NewInMemory(*memoryMaxPerUser, nil)
			memories = inMem
			memoryBackendLabel = "memory"
		}
	}

	// spec: §4.1 — record the §7.2/§7.3 messaging surfaces and the §8.10 /
	// §8.2 / §9.2 / §9.4 / §10.7 per-session shared stores on the
	// accumulator for the §16.1 metrics step and the later subsystem,
	// admin, and worker steps. Each field is the local this step produced.
	w.eventBus = eventBus
	w.messagingCoord = messagingCoord
	w.treeArchive = treeArchive
	w.treeBudgetReserver = treeBudgetReserver
	w.hwmReader = hwmReader
	w.treeBudgetConcrete = treeBudgetConcrete
	w.interactions = interactions
	w.toolApprovalWaits = toolApprovalWaits
	w.evals = evals
	w.evalMatviewEnabled = evalMatviewEnabled
	w.experiments = experiments
	w.memories = memories
	w.memoryBackendLabel = memoryBackendLabel
}

// lastSeqStore adapts the §4.2 session store to the §7.3
// sessions.last_seq durability hooks on the session event bus. It
// satisfies both sessionevents.LastSeqPersister (advance on every
// publish) and sessionevents.LastSeqLoader (seed the in-memory
// counter on first publish for the session). Both methods are
// best-effort — a Postgres outage degrades to the local counter
// without dropping events. F-7.3.3.
type lastSeqStore struct{ sessions sessionstore.Store }

// LoadLastSeq returns the persisted §7.3 sessions.last_seq
// counter so the Bus seeds its local counter on the first publish for
// the session (the coordinator-handoff "primed from Postgres at
// handoff step 0" contract). A missing row reads as zero so a fresh
// session starts at 1.
func (l lastSeqStore) LoadLastSeq(ctx context.Context, tenantID, sessionID string) (int64, error) {
	sess, err := l.sessions.Get(ctx, tenantID, sessionID)
	if errors.Is(err, sessionstore.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return sess.LastSeq, nil
}

// AdvanceLastSeq persists the new per-session SeqNum to Postgres. The
// store's Update mutate-callback applies the new value and the
// pgstore's GREATEST floor in updateSQL keeps the persisted value
// monotonic against late writers from sibling replicas.
func (l lastSeqStore) AdvanceLastSeq(ctx context.Context, tenantID, sessionID string, seq int64) error {
	_, err := l.sessions.Update(ctx, tenantID, sessionID, func(row *sessionstore.Session) error {
		if seq > row.LastSeq {
			row.LastSeq = seq
		}
		return nil
	})
	if errors.Is(err, sessionstore.ErrNotFound) {
		return nil
	}
	return err
}
