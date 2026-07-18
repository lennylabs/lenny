//go:build integration

// SPDX-License-Identifier: MIT

// Tier-4 integration test for the §25.17 multi-cluster watchdog model: a
// single agent that maintains independent SSE connections to more than one
// Lenny installation's lenny-ops. The suite stands up two full
// installations (each its own Postgres, Redis, and cmd/lenny-ops
// subprocess, mirroring two independently deployed clusters), subscribes a
// single driver to both event streams, then takes one installation down and
// asserts the driver keeps receiving events from the surviving one.
package tier4_integration_test

import (
	"bufio"
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/opsprocess"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// installation is one independently deployed Lenny installation for the
// purposes of this test: its own Postgres, its own Redis, and its own
// cmd/lenny-ops subprocess standing in for that installation's lenny-ops
// Ingress. Nothing is shared between installations, matching §25.17's "each
// Lenny installation has its own lenny-ops Ingress."
type installation struct {
	base string
	ops  *opsprocess.Process
}

// startInstallation boots one independent Postgres + Redis + cmd/lenny-ops
// stack. Each installation gets its own containers so the two installations
// share no state; stopping one installation's lenny-ops (or its stores)
// cannot affect the other.
func startInstallation(t *testing.T) installation {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ops := opsprocess.StartWith(
		t,
		"--postgres-dsn="+pg.DSN,
		"--redis-url=redis://"+rd.Addr+"/0",
		"--redis-allow-insecure",
	)
	return installation{base: ops.BaseURL(), ops: ops}
}

// openEventStream opens the §25.5 SSE stream against one installation's
// lenny-ops, filtered to escalation_created, and returns a reader positioned
// to consume frames.
func openEventStream(t *testing.T, ctx context.Context, base string) (*http.Response, *bufio.Reader) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		base+"/v1/admin/events/stream?eventType=escalation_created", nil)
	if err != nil {
		t.Fatalf("build SSE request against %s: %v", base, err)
	}
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("X-Lenny-User-ID", "alice@acme.com")
	req.Header.Set("X-Lenny-Roles", "platform-admin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open SSE stream against %s: %v", base, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("SSE stream against %s: status %d, want 200", base, resp.StatusCode)
	}
	return resp, bufio.NewReader(resp.Body)
}

// spec: §25.17 (Multi-Cluster Note) — "In a multi-cluster deployment, each
// Lenny installation has its own lenny-ops Ingress. A multi-cluster
// watchdog agent maintains SSE connections to each installation's
// lenny-ops and performs the same operational loop independently per
// cluster."
//
// diagnosis: a failure here means a single driver's independent SSE
// subscriptions to two installations are not actually independent. Either
// an event created on one installation leaked onto the other installation's
// stream (the two lenny-ops surfaces are not observing distinct sources), or
// stopping one installation's lenny-ops disrupted the loop against the
// surviving installation (a shared resource — client, goroutine, or
// connection pool — was not scoped per installation the way §25.17 requires
// "independently per cluster" to mean).
func TestMultiInstallationWatchdogIndependentPerInstallation(t *testing.T) {
	opsprocess.SkipUnlessAvailable(t)

	instA := startInstallation(t)
	instB := startInstallation(t)

	escType := events.EventEscalationCreated.CloudEventsType() // dev.lenny.escalation_created

	streamCtx, cancelStreams := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelStreams()

	respA, readerA := openEventStream(t, streamCtx, instA.base)
	defer respA.Body.Close()
	respB, readerB := openEventStream(t, streamCtx, instB.base)
	defer respB.Body.Close()

	// ---- each installation's stream carries only its own events ----
	// A watchdog subscribed to both installations must see installation A's
	// escalation only on A's stream and installation B's escalation only on
	// B's stream: the two operational loops are independent per §25.17.
	idA1 := createEscalation(t, instA.base, "critical", "installation A: warm pool exhausted")
	framesA, err := readSSEFrames(streamCtx, readerA, escType, 1)
	if err != nil || len(framesA) != 1 {
		t.Fatalf("installation A stream: got %d frames, err %v (want the escalation just created on A)", len(framesA), err)
	}
	assertEscalationFrame(t, framesA[0], escType, idA1)

	idB1 := createEscalation(t, instB.base, "critical", "installation B: warm pool exhausted")
	framesB, err := readSSEFrames(streamCtx, readerB, escType, 1)
	if err != nil || len(framesB) != 1 {
		t.Fatalf("installation B stream: got %d frames, err %v (want the escalation just created on B)", len(framesB), err)
	}
	assertEscalationFrame(t, framesB[0], escType, idB1)

	// ---- take installation A down ----
	// §25.17: the watchdog "performs the same operational loop independently
	// per cluster." Stopping A's lenny-ops must not touch B's connection or
	// event delivery.
	instA.ops.Stop(t)

	// A's SSE connection must actually break: the outage is genuine, not a
	// no-op, so the surviving-installation assertion below is attributable
	// to the outage rather than to a stream that was never disrupted.
	brokenCtx, brokenCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer brokenCancel()
	if _, err := readSSEFrames(brokenCtx, readerA, escType, 1); err == nil {
		t.Fatalf("installation A stream still delivered a frame after its lenny-ops was stopped; the outage did not take effect")
	}

	// ---- the surviving installation's loop is unaffected ----
	// A second escalation on B must still arrive on B's already-open stream,
	// proving the driver's loop against the surviving installation continued
	// unaffected by A's outage.
	idB2 := createEscalation(t, instB.base, "warning", "installation B: post-outage escalation")
	framesB2, err := readSSEFrames(streamCtx, readerB, escType, 1)
	if err != nil || len(framesB2) != 1 {
		t.Fatalf("installation B stream after A's outage: got %d frames, err %v "+
			"(want the escalation created on B after A went down; §25.17 requires B's loop to be unaffected)",
			len(framesB2), err)
	}
	assertEscalationFrame(t, framesB2[0], escType, idB2)
}
