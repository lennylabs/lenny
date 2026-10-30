// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Cluster-free coverage of the journey test's pool reclaim path. The
// journey test creates a SandboxWarmPool per run, and a delete whose
// failure is discarded leaves a Ready member pod carrying
// lenny.dev/managed=true that later tier-5 cases have to reconcile
// against a Runtime the run already removed. These cases pin that every
// outcome leaving the pool behind is reported.
package tier5_e2e_kind_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// recordingReclaim builds a reclaimer whose cluster and admin calls are
// stubbed, collecting what it logged.
func recordingReclaim(
	deletePool func(string) (string, error),
	poolPresent func(string) (bool, error),
) (journeyPoolReclaim, *[]string) {
	var logged []string
	return journeyPoolReclaim{
		deletePool:  deletePool,
		poolPresent: poolPresent,
		removeObjects: func(string) (string, error) {
			return "", errors.New("the reclaimer removed objects the case did not expect it to")
		},
		logf: func(format string, args ...any) {
			logged = append(logged, fmt.Sprintf(format, args...))
		},
		sleep:    func(time.Duration) {},
		attempts: 3,
		interval: time.Millisecond,
	}, &logged
}

// TestJourneyPoolReclaimReportsAFailedDelete pins that a pool delete
// that errors is surfaced rather than discarded, which is what leaves a
// warm pool on the shared cluster with no signal.
//
// diagnosis: a failure here means the journey test can leak a
// SandboxWarmPool and its member pod without saying so, and the leak
// only surfaces later as an unrelated tier-5 case failing on a pod whose
// Runtime is gone.
//
// spec: §4.6
func TestJourneyPoolReclaimReportsAFailedDelete(t *testing.T) {
	t.Parallel()
	r, logged := recordingReclaim(
		func(string) (string, error) { return "pool row not found", errors.New("exit status 1") },
		func(string) (bool, error) { t.Fatal("the cluster was read after the delete failed"); return false, nil },
	)

	if r.reclaim("bob-agent-1-pool") {
		t.Fatal("the reclaimer reported success for a delete that failed")
	}
	if len(*logged) != 1 || !strings.Contains((*logged)[0], "exit status 1") {
		t.Errorf("the failed delete was not reported: %v", *logged)
	}
}

// TestJourneyPoolReclaimRemovesAPoolThatOutlivesItsDelete pins that a
// pool whose CRD is still present after the admin delete is reported and
// then removed directly, rather than the delete being taken at its word
// and the residue left for a later run.
//
// diagnosis: a failure here means the journey test treats a pool as gone
// while the pool controller still holds it, so its member pod stays on
// the cluster unreported.
//
// spec: §4.6
func TestJourneyPoolReclaimRemovesAPoolThatOutlivesItsDelete(t *testing.T) {
	t.Parallel()
	r, logged := recordingReclaim(
		func(string) (string, error) { return "deleted", nil },
		func(string) (bool, error) { return true, nil },
	)

	removed := ""
	r.removeObjects = func(pool string) (string, error) {
		removed = pool
		return "sandboxwarmpool.lenny.dev \"" + pool + "\" deleted", nil
	}

	if !r.reclaim("bob-agent-2-pool") {
		t.Fatalf("the reclaimer left a pool the admin delete did not remove: %v", *logged)
	}
	if removed != "bob-agent-2-pool" {
		t.Errorf("the reconciled objects of the surviving pool were not removed (removed %q)", removed)
	}
	if len(*logged) != 1 || !strings.Contains((*logged)[0], "still present") {
		t.Errorf("the surviving pool was not reported: %v", *logged)
	}
}

// TestJourneyPoolReclaimReportsObjectsItCannotRemove pins that a
// surviving pool whose reconciled objects also resist deletion is
// reported as a leak rather than as a clean reclaim.
//
// diagnosis: a failure here means residue the sweep could not remove is
// recorded as reclaimed, so nothing in the run names the pool a later
// tier-5 case will trip over.
//
// spec: §4.6
func TestJourneyPoolReclaimReportsObjectsItCannotRemove(t *testing.T) {
	t.Parallel()
	r, logged := recordingReclaim(
		func(string) (string, error) { return "deleted", nil },
		func(string) (bool, error) { return true, nil },
	)
	r.removeObjects = func(string) (string, error) { return "", errors.New("connection refused") }

	if r.reclaim("bob-agent-5-pool") {
		t.Fatal("the reclaimer reported success for objects it could not remove")
	}
	if len(*logged) != 2 || !strings.Contains((*logged)[1], "connection refused") {
		t.Errorf("the failed object removal was not reported: %v", *logged)
	}
}

// TestJourneyPoolReclaimConfirmsAPoolThatLeaves pins the clean path: a
// delete the cluster honours is reported as reclaimed and logs nothing.
//
// diagnosis: a failure here means a successful cleanup is reported as a
// leak, and the sweep will skip deleting the Runtime that pairs with it.
//
// spec: §4.6
func TestJourneyPoolReclaimConfirmsAPoolThatLeaves(t *testing.T) {
	t.Parallel()
	reads := 0
	r, logged := recordingReclaim(
		func(string) (string, error) { return "deleted", nil },
		func(string) (bool, error) {
			reads++
			return reads < 2, nil
		},
	)

	if !r.reclaim("bob-agent-3-pool") {
		t.Fatalf("the reclaimer reported a leak for a pool that left the cluster: %v", *logged)
	}
	if len(*logged) != 0 {
		t.Errorf("a clean reclaim logged: %v", *logged)
	}
}

// TestJourneyPoolReclaimReportsAnUnreadableCluster pins that a cluster
// read that errors is reported rather than read as absence.
//
// diagnosis: a failure here means an unreachable API server is taken as
// proof the pool is gone, so the leak is recorded as a clean cleanup.
//
// spec: §4.6
func TestJourneyPoolReclaimReportsAnUnreadableCluster(t *testing.T) {
	t.Parallel()
	r, logged := recordingReclaim(
		func(string) (string, error) { return "deleted", nil },
		func(string) (bool, error) { return false, errors.New("connection refused") },
	)

	if r.reclaim("bob-agent-4-pool") {
		t.Fatal("the reclaimer reported success without confirming the pool left the cluster")
	}
	if len(*logged) != 1 || !strings.Contains((*logged)[0], "connection refused") {
		t.Errorf("the unreadable cluster was not reported: %v", *logged)
	}
}
