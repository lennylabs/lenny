// SPDX-License-Identifier: MIT

package main

// The size cap of the gate's Go build cache. Each gate run builds several
// build-tag sets and a -race build into the lenny-gate-gocache volume, and
// Go trims only the entries unused for five days, so the volume grows by
// tens of gigabytes a day under repeated gate runs. Before the host half of `lenny-test gate` starts the gate
// container, it measures the volume and empties it when it holds more
// than the cap, unless another container has the volume mounted. The cap
// only bounds disk use: the run that follows a cleanup compiles from cold,
// and the content-addressed cache yields the same build either way. The
// cap implements the testing model rather than a specification behavior,
// so nothing here carries a spec annotation.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	// gateEnvGoCacheCap overrides the default of --gocache-cap-gb.
	gateEnvGoCacheCap = "LENNY_GATE_GOCACHE_CAP_GB"
	// defaultGateGoCacheCapGB is the default cap of the build cache volume,
	// in gigabytes of 10^9 bytes. It is an operator-tunable harness default
	// (--gocache-cap-gb and LENNY_GATE_GOCACHE_CAP_GB), set after the volume
	// reached 96.8 GB in two days of gate runs.
	defaultGateGoCacheCapGB = 30
	// maxGateGoCacheCapGB is the largest cap the gate accepts. It keeps the
	// cap's conversion to KiB inside int64, and it is far above any disk the
	// gate runs on, so a cap of this size never empties the volume.
	maxGateGoCacheCapGB = 1_000_000
	// gateGoCacheStepSeconds bounds each short container inside the
	// container itself, with timeout(1), so a measurement or cleanup that
	// overruns ends on its own. Ending the docker CLI on the host would leave
	// a `docker run --rm` container running in the daemon, and a cleanup left
	// running would delete entries under the gate container that follows.
	gateGoCacheStepSeconds = 300
	// gateGoCacheTimeout bounds the docker CLI calls of the check on the host:
	// the in-use listing and the two short containers, each of which ends
	// within gateGoCacheStepSeconds plus timeout(1)'s kill grace. It does not
	// bound the gate container that follows.
	gateGoCacheTimeout = 12 * time.Minute
)

// gateCommandRunner runs a command and returns its standard output. Tests
// replace it to drive the cap's decision without Docker.
type gateCommandRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

// execGateCommand runs a command and returns its standard output, with its
// standard error in the error when it fails.
func execGateCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return out, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, msg)
		}
		return out, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return out, nil
}

// resolveGateGoCacheCap returns the cap of one gate run: the value of
// --gocache-cap-gb when the flag was given, else the value of
// LENNY_GATE_GOCACHE_CAP_GB when it is set, else defaultGateGoCacheCapGB.
// A value outside 0 to maxGateGoCacheCapGB, or an environment value that is
// not a whole number, is an error, so a mistyped override never runs the
// gate with a cap the operator did not choose. The caller resolves the cap
// only for a run that uses the volume, so a malformed environment value
// does not refuse a --here run or a run that passes the flag.
func resolveGateGoCacheCap(flagSet bool, flagGB int, getenv func(string) string) (int, error) {
	if flagSet {
		if err := checkGateGoCacheCap(flagGB); err != nil {
			return 0, fmt.Errorf("--gocache-cap-gb=%d %w", flagGB, err)
		}
		return flagGB, nil
	}
	raw := strings.TrimSpace(getenv(gateEnvGoCacheCap))
	if raw == "" {
		return defaultGateGoCacheCapGB, nil
	}
	gb, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s=%q is not a whole number of gigabytes", gateEnvGoCacheCap, raw)
	}
	if err := checkGateGoCacheCap(gb); err != nil {
		return 0, fmt.Errorf("%s=%q %w", gateEnvGoCacheCap, raw, err)
	}
	return gb, nil
}

// checkGateGoCacheCap refuses a cap outside 0 to maxGateGoCacheCapGB.
func checkGateGoCacheCap(gb int) error {
	if gb < 0 || gb > maxGateGoCacheCapGB {
		return fmt.Errorf("is outside 0 to %d GB; pass 0 to turn the check off", maxGateGoCacheCapGB)
	}
	return nil
}

// gateGoCacheInUseArgs is the argv of the `docker ps` that lists the
// containers with the build cache volume mounted. Every worktree's gate
// container mounts the same volume, so a gate running in another worktree
// shows up here.
func gateGoCacheInUseArgs() []string {
	return []string{"ps", "-q", "--filter", "volume=" + gateBuildVolume}
}

// gateGoCacheMeasureArgs is the argv of the `docker run` that measures the
// build cache volume: du -sk over the volume mounted where the gate mounts
// it, in the gate image. A container reads the volume on every host,
// including Docker Desktop, where the volume lives inside the VM.
func gateGoCacheMeasureArgs(imageRef string) []string {
	return gateGoCacheContainerArgs(imageRef, "du", "-sk", gateGoCache)
}

// gateGoCacheCleanArgs is the argv of the `docker run` that empties the
// build cache volume with `go clean -cache` in the gate image. The command
// removes the cache's entries and keeps the directory, so the directory
// keeps the owner the gate container gave it.
func gateGoCacheCleanArgs(imageRef string) []string {
	return gateGoCacheContainerArgs(imageRef, "go", "clean", "-cache")
}

// gateGoCacheContainerArgs is the argv of a short `docker run` of the gate
// image with the build cache volume mounted at GOCACHE, no network, and
// the command under timeout(1) (gateGoCacheStepSeconds).
func gateGoCacheContainerArgs(imageRef string, command ...string) []string {
	args := []string{
		"run", "--rm", "--network", "none",
		"-v", gateBuildVolume + ":" + gateGoCache,
		"-e", "GOCACHE=" + gateGoCache,
		imageRef, "timeout", "--kill-after=10", strconv.Itoa(gateGoCacheStepSeconds),
	}
	return append(args, command...)
}

// parseDuKilobytes reads the size in KiB from the first field of du -sk
// output.
func parseDuKilobytes(out []byte) (int64, error) {
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return 0, errors.New("du printed nothing")
	}
	kb, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil || kb < 0 {
		return 0, fmt.Errorf("du printed %q rather than a size", strings.TrimSpace(string(out)))
	}
	return kb, nil
}

// formatKBAsGB renders a size in KiB in gigabytes of 10^9 bytes, the unit
// of the cap and of `docker system df`.
func formatKBAsGB(kb int64) string {
	return strconv.FormatFloat(float64(kb)*1024/1e9, 'f', 1, 64) + " GB"
}

// capGateGoCache measures the build cache volume in the gate image named
// by imageRef and empties it when it holds more than capGB gigabytes and
// no container has the volume mounted. A cap of 0 turns the check off.
// It logs one line to logw when it empties the volume or defers the
// cleanup, and one warning when it cannot measure, list, or empty; none of
// these stops the gate, because the cap bounds disk use and decides
// nothing about the verdict. It reports whether it emptied the volume.
//
// label names the command the lines belong to, `lenny-test gate` or
// `lenny-test run`, because `run --container` caps the same volume
// (change 0172, decision 18).
func capGateGoCache(ctx context.Context, run gateCommandRunner, imageRef string, capGB int, label string, logw io.Writer) bool {
	if capGB <= 0 {
		return false
	}
	out, err := run(ctx, "docker", gateGoCacheMeasureArgs(imageRef)...)
	if err == nil {
		var kb int64
		if kb, err = parseDuKilobytes(out); err == nil {
			return emptyGateGoCacheOverCap(ctx, run, imageRef, kb, capGB, label, logw)
		}
	}
	fmt.Fprintf(logw, "%s: warning: measure the build cache volume %s: %v; the run goes ahead without the size check\n", label, gateBuildVolume, err)
	return false
}

// gateCacheCapKnob names the setting of the build cache volume's cap for
// the command label names: the gate's flag, or the variable `lenny-test
// run --container` reads.
func gateCacheCapKnob(label string) string {
	if label == "lenny-test gate" {
		return "--gocache-cap-gb"
	}
	return gateEnvGoCacheCap
}

// emptyGateGoCacheOverCap empties the build cache volume, which holds kb
// KiB, when that is more than capGB gigabytes. It first lists the
// containers that have the volume mounted, and defers the cleanup while
// any does, because `go clean -cache` deletes entries a running gate's
// builds may still read. The comparison runs in KiB with the cap rounded
// down, which cannot overflow under maxGateGoCacheCapGB.
func emptyGateGoCacheOverCap(ctx context.Context, run gateCommandRunner, imageRef string, kb int64, capGB int, label string, logw io.Writer) bool {
	if kb <= int64(capGB)*1e9/1024 {
		return false
	}
	size := formatKBAsGB(kb)
	users, err := run(ctx, "docker", gateGoCacheInUseArgs()...)
	if err != nil {
		fmt.Fprintf(logw, "%s: warning: the build cache volume %s holds %s, over the %d GB cap, and listing the containers that use it failed, so it was not emptied: %v\n", label, gateBuildVolume, size, capGB, err)
		return false
	}
	if ids := strings.Fields(string(users)); len(ids) > 0 {
		fmt.Fprintf(logw, "%s: the build cache volume %s holds %s, over the %d GB cap; not emptied, because %d running container(s) use it (%s)\n", label, gateBuildVolume, size, capGB, len(ids), strings.Join(ids, " "))
		return false
	}
	if _, err := run(ctx, "docker", gateGoCacheCleanArgs(imageRef)...); err != nil {
		fmt.Fprintf(logw, "%s: warning: the build cache volume %s holds %s, over the %d GB cap, and emptying it failed: %v\n", label, gateBuildVolume, size, capGB, err)
		return false
	}
	fmt.Fprintf(logw, "%s: the build cache volume %s held %s, over the %d GB cap (%s); emptied it, so this run compiles from cold\n", label, gateBuildVolume, size, capGB, gateCacheCapKnob(label))
	return true
}
