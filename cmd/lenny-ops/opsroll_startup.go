// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"log"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"sigs.k8s.io/yaml"

	"github.com/lennylabs/lenny/pkg/ops/upgradeservice"
	"github.com/lennylabs/lenny/pkg/upgrade"
)

// configMapsGetter returns the CoreV1 ConfigMaps getter for cs, or nil for
// a nil clientset. Returning an untyped nil avoids the typed-nil interface
// trap a direct cs.CoreV1() on a nil *kubernetes.Clientset would create.
func configMapsGetter(cs *kubernetes.Clientset) corev1client.ConfigMapsGetter {
	if cs == nil {
		return nil
	}
	return cs.CoreV1()
}

// opsRollHeartbeater stamps the §25.8 metadata.opsRollHeartbeat: the new
// lenny-ops pod calls it on startup while the upgrade is in OpsRoll to
// signal it is alive (spec line 3511). upgradeservice.Service satisfies
// it.
type opsRollHeartbeater interface {
	// RecordOpsHeartbeat stamps the ops_healthy heartbeat and returns the
	// current state. It is a no-op outside OpsRoll.
	RecordOpsHeartbeat(ctx context.Context) (upgradeservice.State, error)
	// Status returns the current upgrade state; ok is false when no upgrade
	// has ever been recorded.
	Status(ctx context.Context) (upgradeservice.State, bool, error)
	// AdvanceOpsRoll self-advances the active upgrade from OpsRoll to
	// CRDUpdate on the new pod, the §25.8 line 3508 transition the new
	// binary performs after it becomes Ready.
	AdvanceOpsRoll(ctx context.Context) (upgradeservice.State, error)
}

// targetSnapshotWriter writes the §25.10 bootstrap_seed_snapshot_target
// row from the rendered Helm values (spec line 3788). driftservice.Service
// satisfies it. A nil writer skips the write (cold-start posture for a
// deployment whose drift service is not wired).
type targetSnapshotWriter interface {
	WriteTargetSnapshot(ctx context.Context, upgradeID, writtenBy string, desired map[string]any) error
}

// helmValuesReader reads the rendered Helm values the new lenny-ops binary
// writes into bootstrap_seed_snapshot_target. The new binary understands
// the new version's configuration structure; the old binary cannot
// compute the snapshot (spec line 3788). A reader that returns no values
// (an unwired ConfigMap source) leaves the target write skipped so the
// upgrade still self-advances.
type helmValuesReader interface {
	// RenderedValues returns the rendered chart values, or ok=false when no
	// source is configured.
	RenderedValues(ctx context.Context) (values map[string]any, ok bool, err error)
}

// opsRollStartupHook is the §25.8 new-pod OpsRoll startup path (spec lines
// 3508, 3511) joined with the §25.10 target-snapshot write (spec line
// 3788). On the new lenny-ops pod's startup, when the persisted upgrade is
// in OpsRoll and its target_version matches this binary's compiled-in
// version, the hook stamps the ops_healthy heartbeat, writes the target
// snapshot from the rendered Helm values, then self-advances
// OpsRoll→CRDUpdate. Until the hook runs no target row exists, so
// GET /v1/admin/drift?against=target returns DRIFT_NO_TARGET_SNAPSHOT;
// after it runs, against=target and against=both resolve.
//
// The hook is idempotent and safe to call once at startup: it is a no-op
// when no upgrade is active, when the phase is not OpsRoll, or when the
// persisted target_version does not match this binary (the version gate
// prevents an old binary that was rolled into OpsRoll from self-advancing
// before the new pod takes over).
//
// spec: §25.8 lines 3508 (self-advance OpsRoll→CRDUpdate), 3511
// (ops_healthy heartbeat); §25.10 line 3788 (write
// bootstrap_seed_snapshot_target early in OpsRoll).
type opsRollStartupHook struct {
	upgrades  opsRollHeartbeater
	snapshot  targetSnapshotWriter
	values    helmValuesReader
	version   string
	writtenBy string
}

// run executes the §25.8 new-pod OpsRoll startup path. It returns whether
// it advanced the upgrade (false on any no-op branch) and the first error
// it hit. The caller logs the outcome; a hook error does not abort
// startup, since a transient store outage at startup is retried by the
// operator's next proceed and the watchdog still governs the timeout.
func (h opsRollStartupHook) run(ctx context.Context) (advanced bool, err error) {
	st, ok, err := h.upgrades.Status(ctx)
	if err != nil {
		return false, fmt.Errorf("read upgrade state: %w", err)
	}
	if !ok || st.Phase != upgrade.OpsRoll {
		// No upgrade in flight, or this pod started outside an OpsRoll: the
		// ordinary startup case. Nothing to do.
		return false, nil
	}
	// §25.8 line 3508: the new pod only self-advances when the persisted
	// target_version matches its own compiled-in version. An old binary
	// that K8s rolled into OpsRoll (target_version != its version) must not
	// advance; it leaves the roll for the new pod and the watchdog times it
	// out if the new pod never arrives. Fail closed on a mismatch.
	if st.TargetVersion != h.version {
		log.Printf("lenny-ops: OpsRoll startup: persisted target_version %q != binary version %q; not self-advancing (old binary)",
			st.TargetVersion, h.version)
		return false, nil
	}

	// §25.8 line 3511: stamp the ops_healthy heartbeat so the watchdog
	// suppresses its OpsRoll-timeout rollback while this new pod is alive.
	if _, err := h.upgrades.RecordOpsHeartbeat(ctx); err != nil {
		return false, fmt.Errorf("record ops_healthy heartbeat: %w", err)
	}

	// §25.10 line 3788: write bootstrap_seed_snapshot_target from the
	// rendered Helm values. The new binary is required because only it
	// understands the new configuration structure. A missing values source
	// (unwired ConfigMap reader) leaves the target write skipped rather than
	// blocking the self-advance, so the upgrade still progresses; the
	// against=target drift query then reports DRIFT_NO_TARGET_SNAPSHOT until
	// a values source is configured.
	if err := h.writeTarget(ctx, st.OperationID); err != nil {
		return false, err
	}

	// §25.8 line 3508: self-advance OpsRoll→CRDUpdate now that the heartbeat
	// and target snapshot are recorded.
	next, err := h.upgrades.AdvanceOpsRoll(ctx)
	if err != nil {
		return false, fmt.Errorf("self-advance OpsRoll→CRDUpdate: %w", err)
	}
	log.Printf("lenny-ops: §25.8 OpsRoll startup: advanced upgrade %s to %s (target_version %q)",
		st.OperationID, next.Phase, st.TargetVersion)
	return true, nil
}

// writeTarget writes the §25.10 target snapshot from the rendered Helm
// values when both a writer and a values source are configured. A nil
// writer or an unconfigured values source skips the write (the cold-start
// posture), which the caller treats as non-fatal.
func (h opsRollStartupHook) writeTarget(ctx context.Context, upgradeID string) error {
	if h.snapshot == nil || h.values == nil {
		return nil
	}
	values, ok, err := h.values.RenderedValues(ctx)
	if err != nil {
		return fmt.Errorf("read rendered Helm values: %w", err)
	}
	if !ok || values == nil {
		log.Printf("lenny-ops: §25.10 OpsRoll startup: no rendered Helm values source configured; skipping bootstrap_seed_snapshot_target write")
		return nil
	}
	if err := h.snapshot.WriteTargetSnapshot(ctx, upgradeID, h.writtenBy, values); err != nil {
		return fmt.Errorf("write bootstrap_seed_snapshot_target: %w", err)
	}
	log.Printf("lenny-ops: §25.10 OpsRoll startup: wrote bootstrap_seed_snapshot_target for upgrade %s", upgradeID)
	return nil
}

// upgradeStartupHook holds the §25.8 OpsRoll startup hook's dependencies
// as the main wiring supplies them. runOpsRollStartupHook assembles the
// hook from it.
type upgradeStartupHook struct {
	// Upgrades drives the heartbeat, status read, and OpsRoll→CRDUpdate
	// self-advance. Required.
	Upgrades opsRollHeartbeater
	// Snapshot writes the §25.10 target snapshot. A nil writer skips the
	// write.
	Snapshot targetSnapshotWriter
	// ConfigMaps reads the rendered-values ConfigMap. A nil getter leaves
	// the values source unconfigured.
	ConfigMaps corev1client.ConfigMapsGetter
	// Namespace is the release namespace the values ConfigMap lives in.
	Namespace string
	// Version is this binary's compiled-in version, gating the self-advance.
	Version string
	// ValuesCM and ValuesKey name the rendered-values ConfigMap and the key
	// holding the values document. An empty ValuesCM disables the write.
	ValuesCM  string
	ValuesKey string
	// WrittenBy is the provenance recorded on the target snapshot row.
	WrittenBy string
}

// runOpsRollStartupHook assembles the §25.8 OpsRoll startup hook from cfg
// and runs it once, logging the outcome. A nil ConfigMaps getter or empty
// ValuesCM leaves the values source unconfigured (the target write is
// skipped). The hook is a no-op outside an in-flight OpsRoll whose
// target_version matches this binary, so an ordinary start invokes it
// harmlessly. A hook error is logged and swallowed: it must not abort
// process startup.
func runOpsRollStartupHook(ctx context.Context, cfg upgradeStartupHook) {
	if cfg.Upgrades == nil {
		return
	}
	var values helmValuesReader
	if cfg.ConfigMaps != nil && cfg.ValuesCM != "" {
		values = configMapValuesReader{
			cms:       cfg.ConfigMaps,
			namespace: cfg.Namespace,
			name:      cfg.ValuesCM,
			key:       cfg.ValuesKey,
		}
	}
	hook := opsRollStartupHook{
		upgrades:  cfg.Upgrades,
		snapshot:  cfg.Snapshot,
		values:    values,
		version:   cfg.Version,
		writtenBy: cfg.WrittenBy,
	}
	if _, err := hook.run(ctx); err != nil {
		log.Printf("lenny-ops: §25.8 OpsRoll startup hook failed (continuing startup): %v", err)
	}
}

// configMapValuesReader reads the rendered Helm values the new binary
// writes into the §25.10 target snapshot from a chart-rendered ConfigMap.
// The new lenny-ops binary is the only component that understands the new
// version's configuration structure, so the new pod reads its own
// rendered values at startup (spec line 3788). The ConfigMap name and the
// key holding the YAML/JSON values document are operator-tunable
// (ops.drift.helmValuesConfigMap / .helmValuesKey); an empty name leaves
// the source unconfigured and the target write skipped.
type configMapValuesReader struct {
	cms       corev1client.ConfigMapsGetter
	namespace string
	name      string
	key       string
}

// RenderedValues reads and parses the rendered Helm values document from
// the configured ConfigMap key. A missing ConfigMap or key returns ok=false
// so the startup hook skips the target write rather than failing the
// upgrade. The document is parsed as YAML, which also accepts JSON.
func (r configMapValuesReader) RenderedValues(ctx context.Context) (map[string]any, bool, error) {
	if r.name == "" || r.cms == nil {
		return nil, false, nil
	}
	cm, err := r.cms.ConfigMaps(r.namespace).Get(ctx, r.name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("get rendered-values ConfigMap %s/%s: %w", r.namespace, r.name, err)
	}
	raw, ok := cm.Data[r.key]
	if !ok || raw == "" {
		return nil, false, nil
	}
	var values map[string]any
	if err := yaml.Unmarshal([]byte(raw), &values); err != nil {
		return nil, false, fmt.Errorf("parse rendered Helm values from ConfigMap %s/%s key %q: %w", r.namespace, r.name, r.key, err)
	}
	return values, true, nil
}
