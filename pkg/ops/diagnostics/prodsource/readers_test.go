// SPDX-License-Identifier: MIT

package prodsource

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/lennylabs/lenny/pkg/ops/gateway"
)

// TestHotKeysRankAndCap orders credentials by lease count, breaks ties by
// id, drops zero-lease credentials, and caps the list. spec: §25.6
// hot-key analysis.
func TestHotKeysRankAndCap_spec_25_6(t *testing.T) {
	got := hotKeys(map[string]int{
		"c1": 1, "c2": 9, "c3": 9, "c4": 5, "c5": 7, "c6": 3, "c0": 0,
	})
	want := []string{"c2", "c3", "c5", "c4", "c6"} // 9,9 (tie→id),7,5,3; c1 dropped by cap; c0 dropped (zero)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hotKeys = %v, want %v", got, want)
	}
	if len(hotKeys(map[string]int{})) != 0 {
		t.Fatalf("empty load → empty hot keys")
	}
}

// TestSignalsFromPodOOM maps a container terminated by the OOM killer to
// the OOM signal and carries the exit code. spec: §25.6 lines 2899-2906.
func TestSignalsFromPodOOM_spec_25_6_2899(t *testing.T) {
	pod := &corev1.Pod{Status: corev1.PodStatus{
		ContainerStatuses: []corev1.ContainerStatus{{
			State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
				ExitCode: 137, Reason: "OOMKilled",
			}},
		}},
	}}
	sig := signalsFromPod(pod)
	if sig.ExitCode != 137 || !sig.OOMKilled {
		t.Fatalf("want OOM signals, got %+v", sig)
	}
}

// TestSignalsFromPodImagePull maps an image-pull waiting reason to the
// image-pull signal. spec: §25.6 lines 2899-2906.
func TestSignalsFromPodImagePull_spec_25_6_2899(t *testing.T) {
	pod := &corev1.Pod{Status: corev1.PodStatus{
		ContainerStatuses: []corev1.ContainerStatus{{
			State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"}},
		}},
	}}
	if sig := signalsFromPod(pod); !sig.ImagePullError {
		t.Fatalf("want image-pull error, got %+v", sig)
	}
}

// TestSignalsFromPodUnschedulable maps an Unschedulable PodScheduled
// condition to the resource-pressure signal. spec: §25.6 lines 2899-2906.
func TestSignalsFromPodUnschedulable_spec_25_6_2899(t *testing.T) {
	pod := &corev1.Pod{Status: corev1.PodStatus{
		Conditions: []corev1.PodCondition{{
			Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: "Unschedulable",
		}},
	}}
	if sig := signalsFromPod(pod); !sig.ResourcePressure {
		t.Fatalf("want resource pressure, got %+v", sig)
	}
}

// TestSignalsFromPodLastTermination reads the last-termination exit code
// for a crash-looping container whose current state is waiting. spec:
// §25.6 lines 2899-2906.
func TestSignalsFromPodLastTermination_spec_25_6_2899(t *testing.T) {
	pod := &corev1.Pod{Status: corev1.PodStatus{
		ContainerStatuses: []corev1.ContainerStatus{{
			State:                corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
			LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1}},
		}},
	}}
	if sig := signalsFromPod(pod); sig.ExitCode != 1 {
		t.Fatalf("want last-termination exit 1, got %+v", sig)
	}
}

// stubGetter is a gatewayGetter that returns a fixed error or decodes a
// fixed payload into out.
type stubGetter struct {
	err     error
	payload poolConfigPayload
}

func (s stubGetter) Get(_ context.Context, _ string, out any) error {
	if s.err != nil {
		return s.err
	}
	if p, ok := out.(*poolConfigPayload); ok {
		*p = s.payload
	}
	return nil
}

// TestGatewayPoolReaderFound maps the admin pool GET response onto the
// config summary and CRD sync status. spec: §25.6 line 2906.
func TestGatewayPoolReaderFound_spec_25_6_2906(t *testing.T) {
	r := &GatewayPoolReader{client: stubGetter{payload: poolConfigPayload{
		Name: "p1", RuntimeRef: "claude", WarmCount: 5, SyncStatus: "synced",
	}}}
	cfg, synced, detail, found, err := r.PoolConfig(context.Background(), "p1")
	if err != nil || !found {
		t.Fatalf("want found, got found=%v err=%v", found, err)
	}
	if cfg.MinWarm != 5 || cfg.Runtime != "claude" || !synced || detail != "synced" {
		t.Fatalf("unexpected mapping: cfg=%+v synced=%v detail=%q", cfg, synced, detail)
	}
}

// TestGatewayPoolReaderNotFound maps a 404 from the gateway to
// found=false (the §25.6 POOL_NOT_FOUND path) rather than an error.
func TestGatewayPoolReaderNotFound_spec_25_6_2885(t *testing.T) {
	r := &GatewayPoolReader{client: stubGetter{err: &gateway.HTTPError{Status: http.StatusNotFound}}}
	_, _, _, found, err := r.PoolConfig(context.Background(), "p1")
	if err != nil || found {
		t.Fatalf("want found=false nil-err on 404, got found=%v err=%v", found, err)
	}
}

// TestGatewayPoolReaderPendingSync reports a non-"synced" status as not
// synced, carrying the raw token as the detail. spec/04 line 559.
func TestGatewayPoolReaderPendingSync_spec_04_559(t *testing.T) {
	r := &GatewayPoolReader{client: stubGetter{payload: poolConfigPayload{Name: "p1", SyncStatus: "pending"}}}
	_, synced, detail, found, err := r.PoolConfig(context.Background(), "p1")
	if err != nil || !found {
		t.Fatalf("want found, got found=%v err=%v", found, err)
	}
	if synced || detail != "pending" {
		t.Fatalf("want not-synced with pending detail, got synced=%v detail=%q", synced, detail)
	}
}

// TestGatewayPoolReaderTransportError propagates a non-404 transport
// error so the caller marks the diagnosis degraded.
func TestGatewayPoolReaderTransportError(t *testing.T) {
	r := &GatewayPoolReader{client: stubGetter{err: errors.New("connection refused")}}
	if _, _, _, _, err := r.PoolConfig(context.Background(), "p1"); err == nil {
		t.Fatalf("want transport error propagated")
	}
}
