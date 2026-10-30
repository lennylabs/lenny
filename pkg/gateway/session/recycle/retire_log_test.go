// SPDX-License-Identifier: MIT

package recycle_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/session/recycle"
	"github.com/lennylabs/lenny/pkg/sandbox/podscrub"
)

// loggingDispositionDriver builds a claim disposition driver whose terminal
// status write succeeds without a cluster and whose logger writes JSON records
// to the returned buffer.
func loggingDispositionDriver(t *testing.T) (leasecontrol.ClaimDispositionDriver, *bytes.Buffer) {
	t.Helper()
	cl := interceptor.NewClient(fake.NewClientBuilder().Build(), interceptor.Funcs{
		SubResourcePatch: func(context.Context, client.Client, string, client.Object, client.Patch, ...client.SubResourcePatchOption) error {
			return nil
		},
	})
	var buf bytes.Buffer
	d, err := recycle.NewClaimDispositionDriver(recycle.ClaimDispositionDriverOptions{
		Client: cl, Namespace: testNS, Now: func() time.Time { return time.Unix(0, 0) },
		Logger: slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	if err != nil {
		t.Fatalf("NewClaimDispositionDriver: %v", err)
	}
	return d, &buf
}

// logRecords decodes every JSON log record in buf.
func logRecords(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var recs []map[string]any
	dec := json.NewDecoder(buf)
	for dec.More() {
		var rec map[string]any
		if err := dec.Decode(&rec); err != nil {
			t.Fatalf("decode log record: %v", err)
		}
		recs = append(recs, rec)
	}
	return recs
}

// TestClaimDispositionReleasedRetireLogsReasonAtInfo_spec_5_2 verifies that a
// released recycle-boundary retire is recorded at Info with the pod
// identifier, the reason, and the pod's lifetime session count and uptime. The
// §16.1 retirement counter does not carry runtime_not_live, so this record is
// where an operator sees that retire.
// spec: 5.2 (Pod retirement policy), 4.6.3 (released terminal)
//
// diagnosis: a failure means a non-counting retire such as runtime_not_live
// leaves no record naming why the pod left the pool.
func TestClaimDispositionReleasedRetireLogsReasonAtInfo_spec_5_2(t *testing.T) {
	d, buf := loggingDispositionDriver(t)
	life := leasecontrol.PodLifetime{SessionsServed: 3, UptimeSeconds: 900}
	if err := d.Retire(context.Background(), "pod-1", false, false, podscrub.ReasonRuntimeNotLive, life, ""); err != nil {
		t.Fatalf("Retire: %v", err)
	}
	recs := logRecords(t, buf)
	if len(recs) != 1 {
		t.Fatalf("log records = %v, want exactly one", recs)
	}
	rec := recs[0]
	want := map[string]any{
		"level": "INFO", "pod_id": "pod-1", "reason": "runtime_not_live",
		"sessions_served": float64(3), "uptime_seconds": float64(900),
	}
	for k, v := range want {
		if rec[k] != v {
			t.Errorf("record %s = %v, want %v (record %v)", k, rec[k], v, rec)
		}
	}
}

// TestClaimDispositionFailedRetireKeepsWarnRecordOnly_spec_5_2 verifies that a
// failed retire keeps its single Warn audit record and emits no Info record.
// spec: 5.2 (failed pod's metadata retained in the audit log; Pod retirement
// policy)
//
// diagnosis: a failure means the fail-policy termination's audit record
// changed level or is duplicated by the released-retire Info record.
func TestClaimDispositionFailedRetireKeepsWarnRecordOnly_spec_5_2(t *testing.T) {
	d, buf := loggingDispositionDriver(t)
	if err := d.Retire(context.Background(), "pod-1", true, false, podscrub.ReasonCleanupFailPolicy, leasecontrol.PodLifetime{}, "shred"); err != nil {
		t.Fatalf("Retire: %v", err)
	}
	recs := logRecords(t, buf)
	if len(recs) != 1 || recs[0]["level"] != "WARN" || recs[0]["detail"] != "shred" {
		t.Fatalf("log records = %v, want one WARN record carrying the detail", recs)
	}
}
