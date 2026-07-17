// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos tests for data-store outages. The e2e Kind cluster runs
// real single-replica Postgres, Redis, and MinIO Deployments in
// lenny-system (datastores.yaml). Scaling one of those Deployments to
// zero replicas is a genuine store outage: the backing Service loses
// its endpoint and the gateway's connection to the store fails. Each
// test injects the outage, asserts the §10.1 / §4.4 / §12.8 documented
// degraded behavior (the gateway process survives, its liveness probe
// stays green, the §25.3 health report flags the store), then restores
// the store and asserts the health report returns to healthy.
//
// The injector is `kubectl scale deploy ... --replicas=0` followed by
// `--replicas=1`. Every test registers the restore as a t.Cleanup so
// the shared cluster is left healthy even on a mid-test failure.

package tier8_chaos_test

import (
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// Data-store Deployment names from tests/testinfra/k8s/datastores.yaml.
const (
	postgresDeployment = "lenny-postgres"
	redisDeployment    = "lenny-redis"
	minioDeployment    = "lenny-minio"
)

// gatewayDeployment is the client-facing gateway Deployment whose
// survival the store-outage tests assert.
const gatewayDeployment = "lenny-gateway"

// gatewaySelector matches the gateway Deployment's pods.
const gatewaySelector = "lenny.dev/component=gateway"

// storeOutageObserveBound is the time a test waits for the gateway's
// §25.3 health checker to observe a store transition. The Postgres and
// Redis health checkers ping their backend on a short interval; 90s
// covers the poll period plus the dial timeout on a connection-refused
// backend.
const storeOutageObserveBound = 90 * time.Second

// spec: 12.8
// diagnosis: §12.8 / §10.1 Postgres-unavailable degraded mode did not
// hold. The test scales lenny-postgres to zero. §10.1 keeps the gateway
// process alive through a Postgres outage (liveness /healthz stays 200,
// no crash-loop) and the §25.3 health report flags postgres unhealthy
// with the postgres-failover runbook; after restore the report returns
// to healthy. A failure means the gateway crash-looped on the outage,
// liveness went red, or the health report never recovered.
func TestPostgresUnavailable(t *testing.T) {
	c := kind.InstallLenny(t)
	probe := "chaos-pg-probe"
	assertStoreOutageDegrades(t, c, storeOutageScenario{
		store:          postgresDeployment,
		storeSelector:  "lenny.dev/e2e-datastore=postgres",
		probePod:       probe,
		healthComp:     "postgres",
		wantRunbookRef: "postgres-failover",
		// The e2e Postgres uses emptyDir; a restored pod comes back with
		// an empty database, so the schema is re-applied after restore.
		postRestore: func() { ensurePostgresSchema(t, c) },
	})
}

// spec: 12.8
// diagnosis: §12.8 / §11.6 single-node Redis outage degraded mode did
// not hold. Scaling the single-replica e2e Redis to zero is the §12.8
// "Redis cluster degraded" case for a non-HA topology. §11.6 keeps the
// gateway serving against its in-process circuit-breaker cache and
// reports the dependency degraded rather than crash. The test asserts
// the gateway survives (liveness 200, no crash-loop), the §25.3 report
// flags redis unhealthy, and the report recovers after restore.
func TestRedisClusterDegraded(t *testing.T) {
	c := kind.InstallLenny(t)
	probe := "chaos-redis-probe"
	assertStoreOutageDegrades(t, c, storeOutageScenario{
		store:          redisDeployment,
		storeSelector:  "lenny.dev/e2e-datastore=redis",
		probePod:       probe,
		healthComp:     "redis",
		wantRunbookRef: "redis-failure",
	})
}

// spec: 12.8
// diagnosis: §12.8 / §4.4 / §12.5 MinIO-unavailable degraded mode did
// not hold. The test scales lenny-minio to zero. §4.4 keeps the gateway
// process alive through a MinIO outage (only uploads degrade); §12.5's
// drain-readiness probe — a real bucket check — reports not-ready while
// MinIO is down so node drains are blocked. The test asserts liveness
// /healthz stays 200 with no crash-loop, /internal/drain-readiness is
// 503 during the outage, and returns to 200 after MinIO is restored.
func TestMinIOUnavailable(t *testing.T) {
	c := kind.InstallLenny(t)
	probe := "chaos-minio-probe"
	gatewayIP := startGatewayProbePod(t, c, probe)

	if !deploymentReady(t, c, gatewayDeployment) {
		t.Skipf("precondition not met: %s Deployment is not fully Ready (%s) before the chaos injection",
			gatewayDeployment, deploymentReadyState(t, c, gatewayDeployment))
	}
	if !deploymentReady(t, c, minioDeployment) {
		t.Skipf("precondition not met: %s Deployment is not Ready (%s) before the chaos injection",
			minioDeployment, deploymentReadyState(t, c, minioDeployment))
	}
	// Precondition: the drain-readiness probe (real MinIO bucket check)
	// is ready, so the not-ready assertion under outage is attributable
	// to the injected MinIO outage rather than a pre-existing condition.
	if p := curlGateway(t, c, probe, gatewayIP, "/internal/drain-readiness"); !p.ok(200) {
		t.Skipf("precondition not met: /internal/drain-readiness is not 200 before the injection "+
			"(curl exit %d, status %d, body %q); MinIO or its artifact bucket is not healthy",
			p.curlExit, p.statusCode, p.body)
	}
	restartsBefore := deploymentRestartCount(t, c, gatewaySelector)
	t.Logf("precondition: gateway Ready, MinIO Ready, drain-readiness 200, gateway restarts=%d",
		restartsBefore)

	// Inject: scale MinIO to zero. The artifact bucket lives on the
	// store pod's emptyDir, so a scaled pod loses it; the t.Cleanup
	// restores both the Deployment (to its original replica count) and
	// the bucket.
	minioReplicas := deploymentDesiredReplicas(t, c, minioDeployment)
	if minioReplicas == 0 {
		minioReplicas = 1
	}
	scaleDeployment(t, c, minioDeployment, 0)
	t.Cleanup(func() {
		restoreDeployment(t, c, minioDeployment, minioReplicas)
		ensureMinIOBucket(t, c)
	})
	if !waitDeploymentScaledDown(t, c, minioDeployment, storeRecoveryBound) {
		t.Fatalf("%s did not scale down to zero replicas after the scale command", minioDeployment)
	}
	t.Logf("injected: %s scaled to zero; MinIO is unreachable", minioDeployment)

	// Assert: the gateway liveness probe stays 200 throughout. /healthz
	// is the kubelet liveness target; if a MinIO outage flipped it red
	// the gateway pods would be killed, which §4.4 forbids.
	for i := 0; i < 5; i++ {
		if p := curlGateway(t, c, probe, gatewayIP, "/healthz"); !p.ok(200) {
			t.Errorf("gateway /healthz returned curl exit %d / status %d during the MinIO outage; "+
				"§4.4 requires the gateway process to survive a MinIO outage", p.curlExit, p.statusCode)
			break
		}
		time.Sleep(2 * time.Second)
	}

	// Assert: the drain-readiness probe (real MinIO bucket check) flips
	// to not-ready (503). §12.5 uses this signal to block node drains
	// while MinIO is unhealthy.
	notReady := pollUntil(storeOutageObserveBound, 3*time.Second, func() bool {
		return curlGateway(t, c, probe, gatewayIP, "/internal/drain-readiness").statusCode == 503
	})
	if !notReady {
		last := curlGateway(t, c, probe, gatewayIP, "/internal/drain-readiness")
		t.Errorf("/internal/drain-readiness did not report 503 within %s of the MinIO outage "+
			"(last curl exit %d, status %d, body %q); §12.5 drain-readiness must track MinIO health",
			storeOutageObserveBound, last.curlExit, last.statusCode, last.body)
	} else {
		t.Logf("drain-readiness reports 503 (MinIO outage observed; node drains blocked per §12.5)")
	}

	// Assert: the gateway Deployment did not crash-loop on the outage.
	assertNoCrashLoop(t, c, gatewayDeployment, gatewaySelector, restartsBefore)

	// Restore happens in the t.Cleanup above. Assert recovery: once
	// MinIO and its bucket are back, drain-readiness returns to 200.
	restoreDeployment(t, c, minioDeployment, minioReplicas)
	ensureMinIOBucket(t, c)
	recovered := pollUntil(storeRecoveryBound, 3*time.Second, func() bool {
		return curlGateway(t, c, probe, gatewayIP, "/internal/drain-readiness").ok(200)
	})
	if !recovered {
		last := curlGateway(t, c, probe, gatewayIP, "/internal/drain-readiness")
		t.Fatalf("/internal/drain-readiness did not return to 200 within %s after MinIO was restored "+
			"(last curl exit %d, status %d, body %q)",
			storeRecoveryBound, last.curlExit, last.statusCode, last.body)
	}
	t.Logf("recovery: MinIO restored, drain-readiness back to 200; MinIO outage verified end to end")
}

// spec: 12.8
// diagnosis: §12.8 / §10.1 dual-store (Postgres + Redis) unavailable
// degraded mode did not hold. The test scales both lenny-postgres and
// lenny-redis to zero — the §10.1 DualStoreUnavailable case. §10.1
// globally rejects new sessions but keeps the gateway process alive:
// liveness /healthz stays 200, no crash-loop. The §25.3 report flags
// both postgres and redis unhealthy, and returns to healthy after both
// stores are restored. A failure means a crash-loop or no recovery.
func TestDualStoreUnavailable(t *testing.T) {
	c := kind.InstallLenny(t)
	probe := "chaos-dual-probe"
	gatewayIP := startGatewayProbePod(t, c, probe)

	if !deploymentReady(t, c, gatewayDeployment) {
		t.Skipf("precondition not met: %s Deployment is not fully Ready (%s) before the chaos injection",
			gatewayDeployment, deploymentReadyState(t, c, gatewayDeployment))
	}
	for _, store := range []string{postgresDeployment, redisDeployment} {
		if !deploymentReady(t, c, store) {
			t.Skipf("precondition not met: %s Deployment is not Ready (%s) before the chaos injection",
				store, deploymentReadyState(t, c, store))
		}
	}
	report, ok := gatewayHealth(t, c, probe, gatewayIP)
	if !ok || report.Status != "healthy" {
		t.Skipf("precondition not met: /v1/admin/health is not healthy before the injection (ok=%v)", ok)
	}
	restartsBefore := deploymentRestartCount(t, c, gatewaySelector)
	t.Logf("precondition: gateway Ready and healthy, Postgres + Redis Ready, gateway restarts=%d",
		restartsBefore)

	// Inject: scale both stores to zero. scaleDownAndRestore registers
	// a cleanup per store that scales it back to its original replica
	// count; both cleanups run regardless of test outcome. The Postgres
	// emptyDir loses the schema on a pod restart, so its restore
	// re-applies the schema.
	pgReplicas := scaleDownAndRestore(t, c, postgresDeployment,
		func() { ensurePostgresSchema(t, c) })
	redisReplicas := scaleDownAndRestore(t, c, redisDeployment)
	for _, store := range []string{postgresDeployment, redisDeployment} {
		if !waitDeploymentScaledDown(t, c, store, storeRecoveryBound) {
			t.Fatalf("%s did not scale down to zero replicas after the scale command", store)
		}
	}
	t.Logf("injected: both %s and %s scaled to zero; dual-store outage in effect",
		postgresDeployment, redisDeployment)

	// Assert: the gateway liveness probe stays 200 throughout the
	// dual-store outage. §10.1 globally rejects new sessions but the
	// process must not be killed.
	for i := 0; i < 5; i++ {
		if p := curlGateway(t, c, probe, gatewayIP, "/healthz"); !p.ok(200) {
			t.Errorf("gateway /healthz returned curl exit %d / status %d during the dual-store outage; "+
				"§10.1 keeps the gateway process alive under DualStoreUnavailable", p.curlExit, p.statusCode)
			break
		}
		time.Sleep(2 * time.Second)
	}

	// Assert: the §25.3 health report flags both stores unhealthy.
	for _, comp := range []string{"postgres", "redis"} {
		reached, last := waitHealthComponent(t, c, probe, gatewayIP, comp,
			storeOutageObserveBound, "unhealthy", "degraded")
		if !reached {
			t.Errorf("/v1/admin/health did not flag component %q unhealthy within %s of the dual-store "+
				"outage (last status %q)", comp, storeOutageObserveBound, last)
		} else {
			t.Logf("health report: component %q is %q (dual-store outage observed)", comp, last)
		}
	}

	// Assert: the gateway Deployment did not crash-loop.
	assertNoCrashLoop(t, c, gatewayDeployment, gatewaySelector, restartsBefore)

	// Restore both stores (the t.Cleanups also restore; calling here
	// lets the test assert recovery before returning). Postgres is
	// re-migrated after restore so the recovery health check sees a
	// fully recovered store.
	restoreDeployment(t, c, postgresDeployment, pgReplicas,
		func() { ensurePostgresSchema(t, c) })
	restoreDeployment(t, c, redisDeployment, redisReplicas)

	// Assert recovery: the health report returns to fully healthy.
	recovered := pollUntil(storeRecoveryBound, 3*time.Second, func() bool {
		report, ok := gatewayHealth(t, c, probe, gatewayIP)
		return ok && report.Status == "healthy"
	})
	if !recovered {
		report, _ := gatewayHealth(t, c, probe, gatewayIP)
		t.Fatalf("/v1/admin/health did not return to healthy within %s after both stores were restored "+
			"(last overall status %q)", storeRecoveryBound, report.Status)
	}
	t.Logf("recovery: both stores restored, /v1/admin/health back to healthy; " +
		"dual-store outage verified end to end")
}

// storeOutageScenario parameterizes the shared store-outage body for
// the Postgres and Redis cases. The MinIO case is bespoke (it asserts
// against the drain-readiness probe) and does not use this struct.
type storeOutageScenario struct {
	// store is the data-store Deployment name to scale to zero.
	store string
	// storeSelector matches the store Deployment's pods.
	storeSelector string
	// probePod is the name of the curl probe pod to create.
	probePod string
	// healthComp is the §25.3 health component name backed by the store.
	healthComp string
	// wantRunbookRef is the §25.7 Path B runbook the health component's
	// suggestedAction must point at while the store is down.
	wantRunbookRef string
	// postRestore, when set, runs after the store Deployment is Ready
	// again. The e2e Postgres uses emptyDir, so a restored pod comes
	// back with an empty database; the Postgres scenario sets this to
	// re-apply the schema and leave the shared cluster healthy.
	postRestore func()
}

// assertStoreOutageDegrades runs the shared store-outage chaos body:
// inject the outage, assert the gateway survives (liveness 200, no
// crash-loop) and the §25.3 health report flags the store, restore the
// store, assert the health report recovers.
func assertStoreOutageDegrades(t *testing.T, c *kind.Cluster, s storeOutageScenario) {
	t.Helper()
	gatewayIP := startGatewayProbePod(t, c, s.probePod)

	// Precondition: the gateway and the store both start healthy.
	if !deploymentReady(t, c, gatewayDeployment) {
		t.Skipf("precondition not met: %s Deployment is not fully Ready (%s) before the chaos injection",
			gatewayDeployment, deploymentReadyState(t, c, gatewayDeployment))
	}
	if !deploymentReady(t, c, s.store) {
		t.Skipf("precondition not met: %s Deployment is not Ready (%s) before the chaos injection",
			s.store, deploymentReadyState(t, c, s.store))
	}
	report, ok := gatewayHealth(t, c, s.probePod, gatewayIP)
	if !ok {
		t.Skipf("precondition not met: /v1/admin/health is not reachable before the injection")
	}
	if comp, present := report.component(s.healthComp); !present || comp.Status != "healthy" {
		t.Skipf("precondition not met: health component %q is not healthy before the injection (present=%v)",
			s.healthComp, present)
	}
	restartsBefore := deploymentRestartCount(t, c, gatewaySelector)
	t.Logf("precondition: gateway Ready and healthy, %s Ready, gateway restarts=%d",
		s.store, restartsBefore)

	// Inject: scale the store to zero. scaleDownAndRestore registers the
	// cleanup that scales it back to its original replica count and runs
	// the scenario's postRestore re-seed (the Postgres emptyDir loses
	// the schema on a pod restart).
	var postRestore []func()
	if s.postRestore != nil {
		postRestore = append(postRestore, s.postRestore)
	}
	storeReplicas := scaleDownAndRestore(t, c, s.store, postRestore...)
	if !waitDeploymentScaledDown(t, c, s.store, storeRecoveryBound) {
		t.Fatalf("%s did not scale down to zero replicas after the scale command", s.store)
	}
	t.Logf("injected: %s scaled to zero; the store is unreachable", s.store)

	// Assert: the gateway liveness probe stays 200. /healthz is the
	// kubelet liveness target; a store outage flipping it red would get
	// the gateway pods killed, which §10.1 forbids for a single store.
	for i := 0; i < 5; i++ {
		if p := curlGateway(t, c, s.probePod, gatewayIP, "/healthz"); !p.ok(200) {
			t.Errorf("gateway /healthz returned curl exit %d / status %d during the %s outage; "+
				"the gateway process must survive a single-store outage",
				p.curlExit, p.statusCode, s.store)
			break
		}
		time.Sleep(2 * time.Second)
	}

	// Assert: the §25.3 health report flags the store unhealthy and
	// carries the documented runbookRef.
	reached, last := waitHealthComponent(t, c, s.probePod, gatewayIP, s.healthComp,
		storeOutageObserveBound, "unhealthy", "degraded")
	if !reached {
		t.Errorf("/v1/admin/health did not flag component %q unhealthy within %s of the %s outage "+
			"(last status %q)", s.healthComp, storeOutageObserveBound, s.store, last)
	} else {
		report, _ := gatewayHealth(t, c, s.probePod, gatewayIP)
		comp, _ := report.component(s.healthComp)
		t.Logf("health report: component %q is %q, runbook %q", s.healthComp, comp.Status, comp.runbook())
		if s.wantRunbookRef != "" && comp.runbook() != s.wantRunbookRef &&
			!strings.Contains(comp.Detail, s.wantRunbookRef) {
			t.Errorf("health component %q carries runbook %q, expected %q",
				s.healthComp, comp.runbook(), s.wantRunbookRef)
		}
		// Assert: the top-level §25.4 degradation envelope tracks the
		// outage. During the store outage the aggregate status is
		// non-healthy, so the envelope level is degraded or failed (never
		// healthy) and its thresholdSource is compiled-in-defaults.
		if report.Status == "healthy" {
			t.Errorf("aggregate health status is %q during the %s outage, expected non-healthy; "+
				"the degraded component must pull the aggregate off healthy", report.Status, s.store)
		}
		assertDegradationEnvelope(t, report, "during the "+s.store+" outage")
		if report.Degradation != nil {
			t.Logf("degradation envelope during outage: level=%q thresholdSource=%q",
				report.Degradation.Level, report.Degradation.ThresholdSource)
		}
	}

	// Assert: the gateway Deployment did not crash-loop on the outage.
	assertNoCrashLoop(t, c, gatewayDeployment, gatewaySelector, restartsBefore)

	// Restore the store (the t.Cleanup also restores). Re-seed runs
	// after the store is Ready so the health check below sees a fully
	// recovered store.
	restoreDeployment(t, c, s.store, storeReplicas, postRestore...)

	// Assert recovery: the health component returns to healthy.
	recovered, lastStatus := waitHealthComponent(t, c, s.probePod, gatewayIP, s.healthComp,
		storeRecoveryBound, "healthy")
	if !recovered {
		t.Fatalf("/v1/admin/health did not return component %q to healthy within %s after %s was restored "+
			"(last status %q)", s.healthComp, storeRecoveryBound, s.store, lastStatus)
	}
	// Assert: once the store recovers the top-level §25.4 degradation
	// envelope returns to level healthy (with thresholdSource still
	// compiled-in-defaults), so the response-level signal an agent reads
	// tracks recovery, not only the per-component status.
	if report, ok := gatewayHealth(t, c, s.probePod, gatewayIP); ok {
		assertDegradationEnvelope(t, report, "after "+s.store+" was restored")
	}
	t.Logf("recovery: %s restored, health component %q back to healthy; outage verified end to end",
		s.store, s.healthComp)
}

// expectedDegradationLevel maps a §25.3 aggregate health status to the
// §25.2 degradation-envelope level the report must carry alongside it:
// healthy→healthy, degraded→degraded, unhealthy→failed. spec: §25.2
// lines 210, 233.
func expectedDegradationLevel(status string) string {
	switch status {
	case "degraded":
		return "degraded"
	case "unhealthy":
		return "failed"
	default:
		return "healthy"
	}
}

// assertDegradationEnvelope decodes the top-level §25.2 degradation
// object from a live §25.3 /v1/admin/health report and asserts it
// tracks the aggregate status. The envelope is always present (the
// aggregate report stamps it unconditionally), its thresholdSource is
// always compiled-in-defaults (the gateway's in-process tracker
// evaluates the compiled-in thresholds, never the operator's Prometheus
// rules), and its level is the §25.2 mapping of the report's overall
// status. This pins the response-level envelope an agent reads against
// the live JSON during a real store outage, which unit tests exercise
// only in process. spec: §25.2 line 224 (thresholdSource is used for
// health responses); §25.3 line 529 (health Degradation); §25.13 line
// 4851 (compiled-in-defaults on the in-process fallback view).
func assertDegradationEnvelope(t *testing.T, report healthReport, when string) {
	t.Helper()
	if report.Degradation == nil {
		t.Errorf("/v1/admin/health carried no top-level degradation envelope %s; "+
			"the §25.4 canonical response-level signal must always be present on the aggregate report", when)
		return
	}
	if report.Degradation.ThresholdSource != "compiled-in-defaults" {
		t.Errorf("degradation.thresholdSource = %q %s, want %q; the gateway's in-process aggregate "+
			"view always evaluates the compiled-in thresholds",
			report.Degradation.ThresholdSource, when, "compiled-in-defaults")
	}
	if want := expectedDegradationLevel(report.Status); report.Degradation.Level != want {
		t.Errorf("degradation.level = %q %s for overall status %q, want %q; the envelope level "+
			"must track the aggregate health status",
			report.Degradation.Level, when, report.Status, want)
	}
}

// assertNoCrashLoop checks that the named Deployment is still Ready and
// that its pods' restart count has not climbed since restartsBefore. A
// stable restart count proves the component rode out a dependency
// failure without the kubelet killing its pods.
func assertNoCrashLoop(t *testing.T, c *kind.Cluster, deployment, selector string, restartsBefore int) {
	t.Helper()
	if !deploymentReady(t, c, deployment) {
		t.Errorf("%s Deployment is no longer fully Ready (%s) during the outage; "+
			"the component did not ride out the dependency failure",
			deployment, deploymentReadyState(t, c, deployment))
	}
	restartsNow := deploymentRestartCount(t, c, selector)
	if restartsBefore >= 0 && restartsNow > restartsBefore {
		t.Errorf("%s pods restarted during the outage (restart count %d -> %d); "+
			"the component crash-looped on the dependency failure rather than degrading",
			deployment, restartsBefore, restartsNow)
	} else {
		t.Logf("%s rode out the outage: Ready, restart count stable at %d", deployment, restartsNow)
	}
}

// ensureMinIOBucket recreates the lenny-artifacts bucket on the e2e
// MinIO. The e2e MinIO stores data on an emptyDir, so a scaled-down and
// restored MinIO pod comes back with an empty data directory and no
// bucket. The bucket is the steady-state the gateway's artifact store
// and drain-readiness probe expect, so a MinIO chaos test must
// recreate it to leave the shared cluster healthy. The recipe mirrors
// the `mc mb` step in tests/testinfra/kind/install.sh: a one-shot mc
// pod connecting straight to the MinIO pod IP (the lenny-system
// default-deny NetworkPolicy denies a throwaway pod's egress to
// CoreDNS, so the Service DNS name would not resolve).
func ensureMinIOBucket(t *testing.T, c *kind.Cluster) {
	t.Helper()
	podIP, err := c.KubectlOut(
		t,
		"-n", lennySystemNamespace, "get", "pod",
		"-l", "lenny.dev/e2e-datastore=minio",
		"-o", "jsonpath={.items[0].status.podIP}",
	)
	podIP = strings.TrimSpace(podIP)
	if err != nil || podIP == "" {
		t.Errorf("could not resolve the lenny-minio pod IP to recreate the artifact bucket: %v", err)
		return
	}
	_, _ = c.KubectlOut(t, "-n", lennySystemNamespace, "delete", "pod",
		"chaos-minio-mb", "--ignore-not-found", "--wait=true")
	script := "set -e; " +
		"mc alias set e2e http://" + podIP + ":9000 lennyminio lennyminio123 >/dev/null 2>&1; " +
		"mc mb --ignore-existing e2e/lenny-artifacts"
	if out, err := c.KubectlOut(
		t,
		"-n", lennySystemNamespace, "run", "chaos-minio-mb",
		"--image=minio/mc:RELEASE.2024-09-16T17-43-14Z",
		"--image-pull-policy=IfNotPresent", "--restart=Never",
		"--attach", "--rm", "--quiet", "--command", "--",
		"/bin/sh", "-c", script,
	); err != nil {
		t.Errorf("failed to recreate the MinIO artifact bucket: %v\n%s", err, out)
	}
}

// ensurePostgresSchema re-applies the database schema to the e2e
// Postgres by running `lenny-migrate up`. The e2e Postgres stores its
// data directory on an emptyDir, so a scaled-down and restored
// lenny-postgres pod comes back with an empty database — every table
// gone. The migrated schema is the steady state the gateway, the audit
// chain, and the rest of the platform expect, so a Postgres chaos test
// MUST re-migrate after restoring the store to leave the shared
// cluster healthy. `lenny-migrate up` is idempotent: on an
// already-migrated database it is a no-op.
func ensurePostgresSchema(t *testing.T, c *kind.Cluster) {
	t.Helper()
	pgIP := dataStorePodIP(t, c, "postgres")
	if pgIP == "" {
		t.Errorf("could not resolve the lenny-postgres pod IP to re-apply the schema")
		return
	}
	if out, err := runMigrateExpectErr(t, c, pgIP, "up"); err != nil {
		t.Errorf("failed to re-apply the Postgres schema after the outage: %v\n%s", err, out)
	}
}
