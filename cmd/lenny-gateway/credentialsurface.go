// SPDX-License-Identifier: MIT

package main

import (
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/connectoroauth"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/connectorcredstore"
	connectorcredpg "github.com/lennylabs/lenny/pkg/gateway/connectorcredstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/connectorsecret"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialserver"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialstore"
	credentialpg "github.com/lennylabs/lenny/pkg/gateway/credentials/credentialstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/usercreds"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/translator"
	"github.com/lennylabs/lenny/pkg/kms/rekey"
	"github.com/lennylabs/lenny/pkg/storerouter"
)

// buildCredentialSurface is the §4.1 composition-root build step (R1) for the
// §4.9 end-user credential surface and the §9.3 connector OAuth flow. It
// constructs the OpenAI Chat and Open Responses translators; selects the
// in-memory or §12.9-T4-envelope-encrypted Postgres backend for the §4.9
// end-user credential store and the §9.3 connector-credential store; builds the
// §4.9 pre-authorized user-source materializer (wiring it to the session server
// and the pod binder when the public proxy URL is set), the §4.9 credential
// server, the §4.9.1 KMS-rotation re-encryption job over the envelope-backed
// stores, and the §9.3 connector OAuth authorization-code flow with its
// session-state backing, client-secret resolver, and private-CA HTTP client.
// It records its outputs on the accumulator so the MCP fabric, the admin
// router, the HTTP surface, the LLM proxy, and the background workers read
// them back.
//
// spec: §4.1 gateway subsystem seams; §4.9 credential registry / pre-authorized
// flow; §9.3 connector OAuth.
func (w *gatewayWiring) buildCredentialSurface(sessionSrv *sessionserver.Server) {
	f := w.f
	llmProxyPublicURL := f.llmProxyPublicURL
	connectorOAuthCallbackURL := f.connectorOAuthCallbackURL
	connectorOAuthClientSecretKey := f.connectorOAuthClientSecretKey
	connectorOAuthCA := f.connectorOAuthCA

	auditSink := w.auditSink

	// ----- OpenAI Chat + Open Responses translators -----
	openaiHandler := translator.NewOpenAIChatHandler(w.sessions, w.exec, translator.OpenAIChatOptions{Clock: clockinject.Now})
	responsesHandler := translator.NewOpenResponsesHandler(w.sessions, w.exec, translator.OpenResponsesOptions{Clock: clockinject.Now})

	// ----- §4.9 end-user credential registry -----
	// The Postgres-backed store envelope-encrypts the §12.9 T4 secret
	// column under per-tenant KMS KEKs; the in-memory store keeps the
	// secret process-local and never persists it.
	var credentials credentialstore.Store = credentialstore.NewMemory(nil)
	// §4.9.1 re-encryption job: the envelope-backed stores re-key their
	// rows under the current KEK version after a rotation. Only the
	// Postgres stores have a KEK to rotate; the in-memory stores hold
	// plaintext.
	var credentialRekeyers []rekey.TenantRekeyer
	if w.pgPool != nil {
		pgCreds, perr := credentialpg.New(w.pgPool, w.kmsProvider)
		if perr != nil {
			log.Fatalf("lenny-gateway: credential store: %v", perr)
		}
		credentials = pgCreds
		credentialRekeyers = append(credentialRekeyers, pgCreds)
	}
	// ----- §4.9 Pre-Authorized Credential Flow (user-source delivery) -----
	// The user-source materializer resolves a user-registered credential
	// into a proxy-mode lease at session creation and serves the §4.9 LLM
	// proxy from it, sharing the lease store (llmLeases) and upstream-
	// credential cache (credCache) the pool path uses. User credentials are
	// delivered in proxy mode so the secret never reaches the pod and
	// rotation/revocation are gateway-local. It is wired only when the
	// public proxy URL is configured; otherwise Available reports every
	// provider unavailable and sessions fall through to pool.
	// spec: §4.9 lines 1340-1381.
	var userCredMaterializer *usercreds.Materializer
	if userLeaseStore, ok := w.llmLeases.(usercreds.LeaseStore); ok {
		userCredMaterializer = usercreds.New(usercreds.Config{
			Store:    credentials,
			Leases:   userLeaseStore,
			Creds:    w.credCache,
			ProxyURL: *llmProxyPublicURL,
		})
	}
	credServer := credentialserver.New(credentials).
		WithAudit(credentialAuditor{sink: auditSink})
	if userCredMaterializer != nil {
		// spec: §4.9 lines 1350-1351 — the PUT (rotate) and revoke endpoints
		// reach the active user leases through the materializer.
		credServer = credServer.WithLeasePropagator(userCredMaterializer)
		// spec: §4.9 lines 1347-1351 — the session-creation router resolves a
		// user source only when the materializer reports the credential
		// available and deliverable.
		sessionSrv.SetUserCredChecker(userCredMaterializer.Available)
		if w.podBinder != nil {
			// spec: §4.9 lines 1246-1262 — the §4.7 binder materializes each
			// user-source provider into a proxy-mode lease pushed to the pod.
			w.podBinder.UserCredentials = userCredMaterializer
		}
	}

	// ----- §9.3 connector OAuth 2.1 authorization-code flow -----
	// The connector-credential store holds the access/refresh tokens a
	// completed connector OAuth flow produces, keyed by the
	// (tenant, connector, user) triple. The in-memory store keeps the
	// tokens process-local; a Postgres-backed store envelope-encrypts
	// them under the same per-tenant KMS KEKs the credential store
	// uses. The flow is wired only when --connector-oauth-callback-url
	// is set: the OAuth provider needs an absolute redirect URI.
	// §4.3 line 200 / §13.3 connector OAuth tokens are T4 Restricted
	// and must be envelope-encrypted at rest. The Postgres-backed store
	// envelope-encrypts both access and refresh tokens under the
	// per-tenant KMS KEK; the in-memory store is for tests and the
	// minimal gateway. The §4.3 long-term trust-boundary tightening —
	// routing connector credential reads/writes through a Token Service
	// RPC so the gateway holds no KMS decrypt grant — is deferred (see
	// F-4.3.1 resolution note); today the gateway holds the same KMS
	// access for connector creds that it already holds for §4.9
	// user-credential rows.
	var connectorCreds connectorcredstore.Store = connectorcredstore.NewMemory(nil)
	if w.pgPool != nil {
		pgConnectorCreds, err := connectorcredpg.New(w.pgPool, w.kmsProvider, nil)
		if err != nil {
			log.Fatalf("lenny-gateway: connector-credential store: %v", err)
		}
		connectorCreds = pgConnectorCreds
		credentialRekeyers = append(credentialRekeyers, pgConnectorCreds)
		log.Printf("lenny-gateway: §4.3 connector credentials backed by Postgres (envelope-encrypted)")
	}
	// §4.9.1 KMS-key-rotation re-encryption job over every envelope-backed
	// credential store. Wired to the admin router below; absent in the
	// in-memory dev posture (no store to re-key).
	var credentialRekeyJob *rekey.Job
	if len(credentialRekeyers) > 0 {
		credentialRekeyJob = rekey.NewJob(credentialRekeyers...)
	}
	var connectorOAuth *admin.ConnectorOAuth
	var connectorStateStore *connectoroauth.MemoryStateStore
	if *connectorOAuthCallbackURL != "" {
		var stateSeed [32]byte
		if _, err := rand.Read(stateSeed[:]); err != nil {
			log.Fatalf("lenny-gateway: connector OAuth state signing key: %v", err)
		}
		stateSigner, err := connectoroauth.NewStateSigner(connectoroauth.SigningKey{
			KeyID: "boot", Secret: stateSeed[:],
		})
		if err != nil {
			log.Fatalf("lenny-gateway: connector OAuth state signer: %v", err)
		}
		// spec: §9.3 line 157 — pending state binds to (session, connector)
		// with TTL=10min. Production binds it to Redis so the flow survives
		// a gateway restart and a callback resolves on any replica
		// (F-9.3.5); the in-memory store is the single-process fallback and
		// alone needs the periodic Sweep scheduled below in the watchdog
		// group (Redis relies on native key expiry). F-9.3.16.
		var connectorStateBacking connectoroauth.StateStore
		if w.redisClient != nil {
			connectorStateBacking = connectoroauth.NewRedisStateStore(w.concernRedis.For(storerouter.RedisConcernCachePubSub))
			log.Printf("lenny-gateway: §9.3 connector OAuth state backed by Redis (TTL=10m, cross-replica)")
		} else {
			connectorStateStore = connectoroauth.NewMemoryStateStore()
			connectorStateBacking = connectorStateStore
		}
		connectorOAuth = &admin.ConnectorOAuth{
			StateSigner: stateSigner,
			StateStore:  connectorStateBacking,
			Credentials: connectorCreds,
			CallbackURL: *connectorOAuthCallbackURL,
		}
		// spec: §9.3 line 129 — resolve a confidential connector's client
		// secret from its auth.clientSecretRef Kubernetes Secret at
		// token-exchange time. Wired whenever the gateway holds a cluster
		// client (the production --agent-namespace path); without it a
		// confidential-client callback returns a clear "no client-secret
		// resolver is wired" error instead of failing on exchange. F-9.3.4.
		if w.clusterClient != nil {
			connectorOAuth.ClientSecrets = connectorsecret.NewKubeResolver(w.clusterClient, *connectorOAuthClientSecretKey)
		}
		// §9.3: when the provider's token endpoint is behind a private
		// CA, --connector-oauth-ca supplies the bundle that verifies it.
		if *connectorOAuthCA != "" {
			caPEM, err := os.ReadFile(*connectorOAuthCA)
			if err != nil {
				log.Fatalf("lenny-gateway: connector OAuth CA bundle: %v", err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(caPEM) {
				log.Fatalf("lenny-gateway: connector OAuth CA bundle %s contains no PEM certificates", *connectorOAuthCA)
			}
			connectorOAuth.HTTPClient = &http.Client{
				Timeout: 15 * time.Second,
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
				},
			}
		}
		log.Printf("lenny-gateway: §9.3 connector OAuth 2.1 flow enabled, callback %s", *connectorOAuthCallbackURL)
	}

	w.openaiHandler = openaiHandler
	w.responsesHandler = responsesHandler
	w.credentials = credentials
	w.userCredMaterializer = userCredMaterializer
	w.credServer = credServer
	w.connectorCreds = connectorCreds
	w.credentialRekeyJob = credentialRekeyJob
	w.connectorOAuth = connectorOAuth
	w.connectorStateStore = connectorStateStore
}
