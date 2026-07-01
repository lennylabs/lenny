// SPDX-License-Identifier: MIT

package upgradeservice

import (
	"context"
	"encoding/json"
	"time"

	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/upgrade"
)

// CodeOpsRollTimeout is the §25.8 line 3509 failure code the watchdog
// stamps on State.Error when OpsRoll exceeds opsRollTimeoutSeconds and the
// new lenny-ops pod never wrote a heartbeat: the old pod auto-rolls-back.
const CodeOpsRollTimeout = "OPS_ROLL_TIMEOUT"

// Default §25.8 watchdog timeouts (spec lines 3509, 3529, 3533, 3510).
const (
	// DefaultOpsRollTimeout is platform.upgrade.opsRollTimeoutSeconds.
	DefaultOpsRollTimeout = 600 * time.Second
	// DefaultGatewayRollTimeout is platform.upgrade.gatewayRollTimeoutSeconds.
	DefaultGatewayRollTimeout = 1200 * time.Second
	// DefaultControllerRollTimeout is platform.upgrade.controllerRollTimeoutSeconds.
	DefaultControllerRollTimeout = 600 * time.Second
	// DefaultObservationWindow is the §25.8 line 3510 pod-watch window
	// within which a stuck ImagePullBackOff / CrashLoopBackOff is surfaced.
	DefaultObservationWindow = 60 * time.Second
)

// WatchdogConfig holds the §25.8 per-phase roll timeouts and the
// image-pull observation window. A zero field falls back to its default.
type WatchdogConfig struct {
	OpsRollTimeout        time.Duration
	GatewayRollTimeout    time.Duration
	ControllerRollTimeout time.Duration
	ObservationWindow     time.Duration
}

func (c WatchdogConfig) withDefaults() WatchdogConfig {
	if c.OpsRollTimeout <= 0 {
		c.OpsRollTimeout = DefaultOpsRollTimeout
	}
	if c.GatewayRollTimeout <= 0 {
		c.GatewayRollTimeout = DefaultGatewayRollTimeout
	}
	if c.ControllerRollTimeout <= 0 {
		c.ControllerRollTimeout = DefaultControllerRollTimeout
	}
	if c.ObservationWindow <= 0 {
		c.ObservationWindow = DefaultObservationWindow
	}
	return c
}

// rollTimeout returns the §25.8 timeout for a roll phase and whether the
// phase is one the watchdog watches (OpsRoll, GatewayRoll, ControllerRoll).
func (c WatchdogConfig) rollTimeout(p upgrade.Phase) (time.Duration, bool) {
	switch p {
	case upgrade.OpsRoll:
		return c.OpsRollTimeout, true
	case upgrade.GatewayRoll:
		return c.GatewayRollTimeout, true
	case upgrade.ControllerRoll:
		return c.ControllerRollTimeout, true
	default:
		return 0, false
	}
}

// PodStatus is the §25.8 line 3510 observation of the new pod for the
// rolling component during a roll phase.
type PodStatus struct {
	// Stuck reports that the new pod is in ImagePullBackOff or
	// CrashLoopBackOff.
	Stuck bool
	// Reason is the K8s waiting reason ("ImagePullBackOff" or
	// "CrashLoopBackOff") when Stuck.
	Reason string
	// ImageRef is the image reference the stuck pod is trying to pull.
	ImageRef string
	// Description is the human-readable pod description for the
	// platform_upgrade_image_pull_failed payload.
	Description string
}

// PodObserver reports the status of the rolling component's new pod during
// a §25.8 roll phase. lenny-ops supplies a Kubernetes pod-watch
// implementation; a nil observer disables the image-pull observation
// (only the timeout rollback remains). The watchdog passes the phase so a
// single observer can select the right Deployment (lenny-ops, gateway,
// controllers).
type PodObserver interface {
	ObserveNewPod(ctx context.Context, phase upgrade.Phase) (PodStatus, error)
}

// ImagePullCheckRecorder records the latency of one image-pull
// observation into the §25.8 lenny_platform_image_pull_check_duration_seconds
// histogram (spec line 3619), labelled by component. A nil recorder drops it.
type ImagePullCheckRecorder func(component string, d time.Duration)

// WatchdogResult reports what one Evaluate did, for observability and
// tests.
type WatchdogResult struct {
	// Active reports whether an upgrade was in a watched roll phase.
	Active bool
	// Phase is the roll phase observed (empty when not active).
	Phase upgrade.Phase
	// Elapsed is the time spent in the phase at evaluation.
	Elapsed time.Duration
	// ImagePullFailed reports that a platform_upgrade_image_pull_failed
	// event was emitted this evaluation.
	ImagePullFailed bool
	// RolledBack reports that the watchdog auto-rolled-back on timeout.
	RolledBack bool
	// HeartbeatSuppressed reports that the OpsRoll timeout was suppressed
	// because the new pod's heartbeat is fresh.
	HeartbeatSuppressed bool
}

// Watchdog is the §25.8 OpsRoll watchdog: a goroutine the old lenny-ops
// pod runs during a roll phase that observes the new pod and auto-rolls-
// back on timeout. In this codebase's operator-paced orchestrator the
// watchdog owns coordination, observation, and the audit/operational-event
// trail; the actual Deployment re-patch on rollback is performed by the
// operator or a Kubernetes seam, consistent with the rest of the
// orchestrator (which records phase transitions rather than mutating the
// cluster directly).
//
// spec: §25.8 lines 3509-3511 (OpsRoll timeout, image-pull failure event,
// heartbeat), line 3619 (image-pull-check histogram).
type Watchdog struct {
	svc      *Service
	cfg      WatchdogConfig
	observer PodObserver
	emitter  events.EventEmitter
	record   ImagePullCheckRecorder
	now      func() time.Time

	// emitted dedups the platform_upgrade_image_pull_failed event so a
	// repeated evaluation of the same stuck pod fires it once per
	// operation+phase. Keyed by operationID+phase.
	emitted map[string]bool
}

// WatchdogOptions configures a Watchdog.
type WatchdogOptions struct {
	// Service is the orchestrator the watchdog drives. Required.
	Service *Service
	// Config holds the per-phase timeouts; zero fields use the defaults.
	Config WatchdogConfig
	// Observer reports the new pod's status. A nil observer disables
	// image-pull observation.
	Observer PodObserver
	// Emitter publishes the platform_upgrade_image_pull_failed operational
	// event. A nil emitter is a no-op.
	Emitter events.EventEmitter
	// Record records the image-pull-check histogram. A nil recorder drops it.
	Record ImagePullCheckRecorder
	// Now supplies the current time; nil defaults to time.Now.
	Now func() time.Time
}

// NewWatchdog returns a Watchdog over opts. It panics when Service is nil
// (a wiring error).
func NewWatchdog(opts WatchdogOptions) *Watchdog {
	if opts.Service == nil {
		panic("upgradeservice: WatchdogOptions.Service is required")
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Watchdog{
		svc:      opts.Service,
		cfg:      opts.Config.withDefaults(),
		observer: opts.Observer,
		emitter:  opts.Emitter,
		record:   opts.Record,
		now:      now,
		emitted:  map[string]bool{},
	}
}

// Evaluate runs one watchdog pass. It is a no-op unless an upgrade is in a
// watched roll phase (OpsRoll, GatewayRoll, ControllerRoll). It observes
// the new pod (emitting platform_upgrade_image_pull_failed once per
// operation+phase when the pod is stuck) and, when the phase has exceeded
// its timeout, auto-rolls-back a rollbackable phase (OpsRoll) with
// OPS_ROLL_TIMEOUT. The OpsRoll timeout is suppressed while the new pod's
// heartbeat is fresh (the roll succeeded; the operator simply has not
// proceeded). Post-SchemaMigration roll phases (GatewayRoll,
// ControllerRoll) are past the §25.8 point of no return, so a timeout
// there surfaces the image-pull event and leaves rollback to the operator
// (a restore-from-backup decision per spec line 3549).
//
// A lenny-ops cron calls Evaluate periodically; it is safe to call
// concurrently with operator-driven transitions (the Service serializes
// the read-modify-write).
func (w *Watchdog) Evaluate(ctx context.Context) (WatchdogResult, error) {
	st, ok, err := w.svc.Status(ctx)
	if err != nil {
		return WatchdogResult{}, err
	}
	if !ok || upgrade.IsTerminal(st.Phase) {
		return WatchdogResult{}, nil
	}
	timeout, watched := w.cfg.rollTimeout(st.Phase)
	if !watched {
		return WatchdogResult{}, nil
	}
	res := WatchdogResult{Active: true, Phase: st.Phase, Elapsed: w.now().Sub(st.UpdatedAt)}

	// §25.8 line 3510: observe the new pod; emit the image-pull-failed
	// event once when it is stuck. The observation latency feeds the
	// image-pull-check histogram.
	if w.observer != nil {
		start := w.now()
		status, obErr := w.observer.ObserveNewPod(ctx, st.Phase)
		if w.record != nil {
			w.record(componentForPhase(st.Phase), w.now().Sub(start))
		}
		if obErr == nil && status.Stuck {
			if w.emitImagePullFailed(ctx, st, status) {
				res.ImagePullFailed = true
			}
		}
	}

	if res.Elapsed < timeout {
		return res, nil
	}

	// Timeout exceeded. OpsRoll auto-rolls-back unless a fresh heartbeat
	// says the new pod is alive (spec line 3511).
	if st.Phase == upgrade.OpsRoll && w.heartbeatFresh(st) {
		res.HeartbeatSuppressed = true
		return res, nil
	}
	if !upgrade.CanRollBack(st.Phase) {
		// Post-migration roll phase: no auto-rollback. The image-pull event
		// (if any) and the PlatformUpgradeStuck alert carry the signal.
		return res, nil
	}
	if _, err := w.svc.RollbackOnTimeout(ctx, CodeOpsRollTimeout,
		"OpsRoll exceeded the configured timeout without an ops_healthy heartbeat"); err != nil {
		return res, err
	}
	res.RolledBack = true
	return res, nil
}

// heartbeatFresh reports whether the OpsRoll heartbeat was written after
// the phase was entered and within the observation window of now. A stale
// or absent heartbeat means the new pod never came up.
func (w *Watchdog) heartbeatFresh(st State) bool {
	if st.OpsHeartbeat.IsZero() {
		return false
	}
	if st.OpsHeartbeat.Before(st.UpdatedAt) {
		return false
	}
	return w.now().Sub(st.OpsHeartbeat) <= w.cfg.ObservationWindow
}

// emitImagePullFailed publishes the §16.6 platform_upgrade_image_pull_failed
// operational event once per operation+phase. It returns whether it
// emitted (false on a duplicate or a nil emitter).
func (w *Watchdog) emitImagePullFailed(ctx context.Context, st State, status PodStatus) bool {
	if w.emitter == nil {
		return false
	}
	key := st.OperationID + "|" + string(st.Phase)
	if w.emitted[key] {
		return false
	}
	payload, _ := json.Marshal(map[string]any{
		"operationId": st.OperationID,
		"phase":       string(st.Phase),
		"component":   componentForPhase(st.Phase),
		"reason":      status.Reason,
		"imageRef":    status.ImageRef,
		"description": status.Description,
	})
	if err := w.emitter.Emit(ctx, events.OperationalEvent{
		Source:          "//lenny.dev/ops",
		Type:            events.EventPlatformUpgradeImagePullFailed.CloudEventsType(),
		Subject:         "platform",
		Severity:        "error",
		DataContentType: "application/json",
		Data:            payload,
	}); err != nil {
		return false
	}
	w.emitted[key] = true
	return true
}

// componentForPhase maps a roll phase to the component short name whose
// Deployment is rolling, for the image-pull event/histogram labels.
func componentForPhase(p upgrade.Phase) string {
	switch p {
	case upgrade.OpsRoll:
		return "ops"
	case upgrade.GatewayRoll:
		return "gateway"
	case upgrade.ControllerRoll:
		return "controllers"
	default:
		return string(p)
	}
}
