// SPDX-License-Identifier: MIT

package main

// The size cap of the gate's Go build cache. Each gate run builds several
// build-tag sets, a -race build, and a coverage build into the
// lenny-gate-gocache volume, and Go trims only the entries unused for five
// days, so the volume grows by tens of gigabytes a day under repeated gate
// runs. Before the host half of `lenny-test gate` starts the gate
// container, it measures the volume and empties it when it holds more
// than the cap. The cap only bounds disk use: an emptied cache makes the
// next run compile from cold and changes no verdict. The cap implements
// the testing model rather than a specification behavior, so nothing here
// carries a spec annotation.

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
	// (--gocache-cap-gb and LENNY_GATE_GOCACHE_CAP_GB) that the owner set on
	// 2026-11-19, after the volume reached 96.8 GB in two days of runs.
	defaultGateGoCacheCapGB = 30
	// gateGoCacheTimeout bounds the measurement and the cleanup together,
	// so a daemon that does not answer cannot hold the gate indefinitely.
	gateGoCacheTimeout = 10 * time.Minute
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

// gateGoCacheCapDefault returns the default of --gocache-cap-gb: the value
// of LENNY_GATE_GOCACHE_CAP_GB when it is set, and defaultGateGoCacheCapGB
// otherwise. A value that is not a non-negative integer is an error, so a
// mistyped override never runs the gate with a cap the operator did not
// choose.
func gateGoCacheCapDefault(getenv func(string) string) (int, error) {
	raw := strings.TrimSpace(getenv(gateEnvGoCacheCap))
	if raw == "" {
		return defaultGateGoCacheCapGB, nil
	}
	gb, err := strconv.Atoi(raw)
	if err != nil || gb < 0 {
		return 0, fmt.Errorf("%s=%q is not a non-negative whole number of gigabytes", gateEnvGoCacheCap, raw)
	}
	return gb, nil
}

// gateGoCacheMeasureArgs is the argv of the `docker run` that measures the
// build cache volume: du -sk over the volume mounted where the gate mounts
// it, in the gate image. A container reads the volume on every host,
// including Docker Desktop, where the volume lives inside the VM.
func gateGoCacheMeasureArgs(imageRef string) []string {
	return []string{
		"run", "--rm", "--network", "none",
		"-v", gateBuildVolume + ":" + gateGoCache,
		imageRef, "du", "-sk", gateGoCache,
	}
}

// gateGoCacheCleanArgs is the argv of the `docker run` that empties the
// build cache volume with `go clean -cache` in the gate image. The command
// removes the cache's entries and keeps the directory, so the directory
// keeps the owner the gate container gave it.
func gateGoCacheCleanArgs(imageRef string) []string {
	return []string{
		"run", "--rm", "--network", "none",
		"-v", gateBuildVolume + ":" + gateGoCache,
		"-e", "GOCACHE=" + gateGoCache,
		imageRef, "go", "clean", "-cache",
	}
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

// formatGB renders a byte count in gigabytes of 10^9 bytes, the unit of
// the cap and of `docker system df`.
func formatGB(n int64) string {
	return strconv.FormatFloat(float64(n)/1e9, 'f', 1, 64) + " GB"
}

// capGateGoCache measures the build cache volume in the gate image named
// by imageRef and empties it when it holds more than capGB gigabytes. A
// cap of 0 turns the check off. It logs one line to logw when it empties
// the volume, and one warning when it cannot measure or empty it; neither
// failure stops the gate, because the cap bounds disk use and decides
// nothing about the verdict. It reports whether it emptied the volume.
func capGateGoCache(ctx context.Context, run gateCommandRunner, imageRef string, capGB int, logw io.Writer) bool {
	if capGB <= 0 {
		return false
	}
	out, err := run(ctx, "docker", gateGoCacheMeasureArgs(imageRef)...)
	if err != nil {
		fmt.Fprintf(logw, "lenny-test gate: warning: measure the build cache volume %s: %v; the gate runs without the size check\n", gateBuildVolume, err)
		return false
	}
	kb, err := parseDuKilobytes(out)
	if err != nil {
		fmt.Fprintf(logw, "lenny-test gate: warning: measure the build cache volume %s: %v; the gate runs without the size check\n", gateBuildVolume, err)
		return false
	}
	size, limit := kb*1024, int64(capGB)*1e9
	if size <= limit {
		return false
	}
	if _, err := run(ctx, "docker", gateGoCacheCleanArgs(imageRef)...); err != nil {
		fmt.Fprintf(logw, "lenny-test gate: warning: the build cache volume %s holds %s, over the %d GB cap, and emptying it failed: %v\n", gateBuildVolume, formatGB(size), capGB, err)
		return false
	}
	fmt.Fprintf(logw, "lenny-test gate: the build cache volume %s held %s, over the %d GB cap (--gocache-cap-gb); emptied it, so this run compiles from cold\n", gateBuildVolume, formatGB(size), capGB)
	return true
}
