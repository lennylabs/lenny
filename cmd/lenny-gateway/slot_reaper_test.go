// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
	"github.com/lennylabs/lenny/tests/testinfra/helm"
)

// registerReaperFlags registers the storage flags on a fresh command line and
// parses args.
func registerReaperFlags(t *testing.T, args ...string) (*gatewayFlags, error) {
	t.Helper()
	savedCmdLine, savedArgs := flag.CommandLine, os.Args
	t.Cleanup(func() { flag.CommandLine, os.Args = savedCmdLine, savedArgs })
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	flag.CommandLine.SetOutput(&strings.Builder{})
	os.Args = []string{savedArgs[0]}
	f := &gatewayFlags{}
	f.registerStorageFlags()
	return f, flag.CommandLine.Parse(args)
}

// TestSlotReaperFlagDefaults_spec_5_2 pins the stale-slot reaper's flag
// defaults: a 60-second interval, a rate of 50, and a 15-minute terminal age.
//
// spec: §5.2 (Slot assignment atomicity), §4.6.1 (Warm Pool Controller pod
// lifecycle)
func TestSlotReaperFlagDefaults_spec_5_2(t *testing.T) {
	for _, env := range []string{"LENNY_SLOT_REAPER_INTERVAL", "LENNY_SLOT_REAPER_RATE", "LENNY_SLOT_REAPER_TERMINAL_AGE"} {
		t.Setenv(env, "")
	}
	f, err := registerReaperFlags(t)
	if err != nil {
		t.Fatal(err)
	}
	if *f.slotReaperInterval != 60*time.Second || *f.slotReaperRate != 50 || *f.slotReaperTerminalAge != 15*time.Minute {
		t.Errorf("defaults = %s, %d, %s, want 60s, 50, 15m", *f.slotReaperInterval, *f.slotReaperRate, *f.slotReaperTerminalAge)
	}
	if f, err = registerReaperFlags(t, "--slot-reaper-terminal-age=20m", "--slot-reaper-rate=7"); err != nil ||
		*f.slotReaperTerminalAge != 20*time.Minute || *f.slotReaperRate != 7 {
		t.Errorf("overrides parsed %s, %d, %v", *f.slotReaperTerminalAge, *f.slotReaperRate, err)
	}
}

// defaultReaperSettings is the reaper gate's input at every default.
func defaultReaperSettings() slotReaperSettings {
	return slotReaperSettings{
		interval:                   60 * time.Second,
		rate:                       50,
		terminalAge:                15 * time.Minute,
		maxFinalizingSeconds:       600,
		maxResumingSeconds:         300,
		sealSeconds:                300,
		streamFailureReportTimeout: 30 * time.Second,
	}
}

// TestSlotReaperStartupGate_spec_5_2 pins the startup gate: the terminal age
// must be at least one second more than the largest of the finalizing and
// resuming deadlines in effect and the terminal pipeline budget (the seal
// window plus the release allowance, --stream-failure-report-timeout), and
// the rate and interval must be positive. A non-positive deadline or
// allowance resolves to its default before the comparison, as the watchdog
// and the session server resolve it. With the defaults the floor is 601 s.
//
// spec: §5.2 (Slot assignment atomicity), §7.1 (Terminal pipeline context)
func TestSlotReaperStartupGate_spec_5_2(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*slotReaperSettings)
		ok     bool
	}{
		{"the defaults", func(*slotReaperSettings) {}, true},
		{"a terminal age equal to the finalizing deadline", func(c *slotReaperSettings) { c.terminalAge = 600 * time.Second }, false},
		{"a terminal age one second above it", func(c *slotReaperSettings) { c.terminalAge = 601 * time.Second }, true},
		{"a seal window of 900s", func(c *slotReaperSettings) { c.sealSeconds = 900 }, false},
		{"a resuming deadline of 900s", func(c *slotReaperSettings) { c.maxResumingSeconds = 900 }, false},
		{"an unset finalizing deadline is 600s in effect", func(c *slotReaperSettings) {
			c.maxFinalizingSeconds = 0
			c.terminalAge = 600 * time.Second
		}, false},
		{"an unset seal window is 300s in effect", func(c *slotReaperSettings) {
			c.sealSeconds = 0
			c.terminalAge = 601 * time.Second
		}, true},
		{"the defaults floor the terminal age at 601s", func(c *slotReaperSettings) { c.terminalAge = 601 * time.Second }, true},
		{"a seal window of 580s with the allowance floors the terminal age at 611s", func(c *slotReaperSettings) {
			c.sealSeconds = 580
			c.terminalAge = 610 * time.Second
		}, false},
		{"a terminal age of 611s clears a 580s seal window with the allowance", func(c *slotReaperSettings) {
			c.sealSeconds = 580
			c.terminalAge = 611 * time.Second
		}, true},
		{"an allowance of 400s sets the floor at 701s", func(c *slotReaperSettings) {
			c.streamFailureReportTimeout = 400 * time.Second
			c.terminalAge = 700 * time.Second
		}, false},
		{"a terminal age of 701s clears an allowance of 400s", func(c *slotReaperSettings) {
			c.streamFailureReportTimeout = 400 * time.Second
			c.terminalAge = 701 * time.Second
		}, true},
		{"a zero allowance is 30s in effect", func(c *slotReaperSettings) {
			c.streamFailureReportTimeout = 0
			c.sealSeconds = 580
			c.terminalAge = 610 * time.Second
		}, false},
		{"a negative allowance is 30s in effect", func(c *slotReaperSettings) {
			c.streamFailureReportTimeout = -time.Second
			c.sealSeconds = 580
			c.terminalAge = 611 * time.Second
		}, true},
		{"a zero rate", func(c *slotReaperSettings) { c.rate = 0 }, false},
		{"a negative rate", func(c *slotReaperSettings) { c.rate = -5 }, false},
		{"a zero interval", func(c *slotReaperSettings) { c.interval = 0 }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := defaultReaperSettings()
			tc.mutate(&c)
			if err := validateSlotReaperFlags(c); (err == nil) != tc.ok {
				t.Errorf("validateSlotReaperFlags(%+v) = %v, want ok=%v", c, err, tc.ok)
			}
		})
	}
}

// TestSlotReaperStartupGateNamesTheReleaseAllowance_spec_5_2 pins the
// rejection text: a terminal age below the terminal pipeline budget names
// --stream-failure-report-timeout and the budget's floor, so an operator can
// see which flag set it.
//
// spec: §5.2 (Slot assignment atomicity), §7.1 (Terminal pipeline context)
func TestSlotReaperStartupGateNamesTheReleaseAllowance_spec_5_2(t *testing.T) {
	c := defaultReaperSettings()
	c.sealSeconds = 580
	c.terminalAge = 610 * time.Second
	err := validateSlotReaperFlags(c)
	if err == nil {
		t.Fatal("validateSlotReaperFlags accepted a terminal age below the terminal pipeline budget")
	}
	for _, want := range []string{"--stream-failure-report-timeout 30s", "must be at least 10m11s", "terminal pipeline budget (10m10s"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("rejection %q lacks %q", err, want)
		}
	}
}

// TestStaleSlotReaperRegistersUnderTheLeaderGate_spec_5_2 pins the reaper's
// registration on the gateway-leader gate: it registers when the pod binder,
// the slot counter, and the session store are wired, and not otherwise.
// The source check pins that startLeaderElectedSweeps routes it through the
// leader gate and that the startup gates run its validation.
//
// spec: §5.2 (Slot assignment atomicity), §12.5 (Artifact Store)
func TestStaleSlotReaperRegistersUnderTheLeaderGate_spec_5_2(t *testing.T) {
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	interval, rate, age := 60*time.Second, 50, 15*time.Minute
	full := func() *gatewayWiring {
		w := &gatewayWiring{}
		w.f = &gatewayFlags{slotReaperInterval: &interval, slotReaperRate: &rate, slotReaperTerminalAge: &age}
		w.podBinder = &podsession.Binder{}
		w.slotCounter = slotcounter.New(rc)
		w.sessions = memstore.New()
		return w
	}
	for _, tc := range []struct {
		name   string
		mutate func(*gatewayWiring)
		want   bool
	}{
		{"all wired", func(*gatewayWiring) {}, true},
		{"no slot counter", func(w *gatewayWiring) { w.slotCounter = nil }, false},
		{"no pod binder", func(w *gatewayWiring) { w.podBinder = nil }, false},
		{"no session store", func(w *gatewayWiring) { w.sessions = nil }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := full()
			tc.mutate(w)
			var names []string
			w.addStaleSlotReaper(func(name string, _ func(context.Context)) { names = append(names, name) })
			if got := len(names) == 1 && names[0] == "stale-slot-reaper"; got != tc.want {
				t.Errorf("registered %v, want registration %v", names, tc.want)
			}
		})
	}

	space := regexp.MustCompile(`\s+`)
	for _, tc := range []struct{ file, want, seam string }{
		{"leader_sweeps.go", "w.addStaleSlotReaper(func(name string, run func(context.Context)) { leaderGate.Add(name, run) })", "the reaper's leader-gate registration"},
		{"startupgates.go", "validateSlotReaperFlags(", "the reaper's startup gate"},
		{"sessionsrv.go", "MaxFinalizing: time.Duration(*f.maxFinalizingTimeoutSeconds) * time.Second,", "the launch bound"},
	} {
		b, err := os.ReadFile(tc.file)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(space.ReplaceAllString(string(b), " "), tc.want) {
			t.Errorf("%s: %s is not wired (%q missing)", tc.file, tc.seam, tc.want)
		}
	}
}

// chartRenderBase is the value set a stock chart render needs.
var chartRenderBase = []string{"coredns.clusterIP=10.96.0.53"}

// chartDir is the chart path from this package.
func chartDir() string { return filepath.Join("..", "..", "charts", "lenny") }

// renderArgs is every argument the named container renders, joined.
func renderArgs(t *testing.T, ms helm.Manifests, deployment string) string {
	t.Helper()
	m := ms.MustFind(t, "Deployment", deployment)
	spec, _ := m.Raw["spec"].(map[string]any)
	tmpl, _ := spec["template"].(map[string]any)
	podSpec, _ := tmpl["spec"].(map[string]any)
	containers, _ := podSpec["containers"].([]any)
	var all []string
	for _, c := range containers {
		cm, _ := c.(map[string]any)
		args, _ := cm["args"].([]any)
		for _, a := range args {
			if s, ok := a.(string); ok {
				all = append(all, s)
			}
		}
	}
	return strings.Join(all, "\n")
}

// TestChartRendersOneClaimOrphanTimeoutIntoBothBinaries_spec_5_2 pins the
// chart: controller.claimOrphanTimeoutSeconds renders into both the
// controller's --claim-orphan-timeout and the gateway's
// --slot-reaper-terminal-age, so the two copies cannot differ, and the
// render fails on a non-positive reaper rate or a timeout that does not
// exceed the post-terminal slot activity windows, whose seal term is the seal
// window plus the gateway's 30s release allowance.
//
// spec: §5.2 (Slot assignment atomicity), §4.6.1 (Warm Pool Controller pod
// lifecycle)
func TestChartRendersOneClaimOrphanTimeoutIntoBothBinaries_spec_5_2(t *testing.T) {
	helm.SkipUnlessAvailable(t)
	for _, tc := range []struct {
		name    string
		set     []string
		seconds string
	}{
		{"the defaults", nil, "900s"},
		{"a changed timeout", []string{"controller.claimOrphanTimeoutSeconds=1200"}, "1200s"},
		{"a seal window of 580s with a timeout of 611s, at the allowance floor", []string{"gateway.workspaceSealMaxDurationSeconds=580", "controller.claimOrphanTimeoutSeconds=611"}, "611s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ms := helm.Render(t, helm.Options{Chart: chartDir(), Set: append(append([]string(nil), chartRenderBase...), tc.set...)})
			if args := renderArgs(t, ms, "lenny-controller"); !strings.Contains(args, "--claim-orphan-timeout="+tc.seconds) {
				t.Errorf("lenny-controller args lack --claim-orphan-timeout=%s:\n%s", tc.seconds, args)
			}
			args := renderArgs(t, ms, "lenny-gateway")
			for _, want := range []string{"--slot-reaper-terminal-age=" + tc.seconds, "--slot-reaper-interval=60s", "--slot-reaper-rate=50"} {
				if !strings.Contains(args, want) {
					t.Errorf("lenny-gateway args lack %s", want)
				}
			}
		})
	}
	for _, tc := range []struct{ name, set string }{
		{"a zero reaper rate", "gateway.slotReaper.ratePerSecond=0"},
		{"a timeout equal to the finalizing deadline", "controller.claimOrphanTimeoutSeconds=600"},
		{"a seal window of 900s with the default timeout", "gateway.workspaceSealMaxDurationSeconds=900"},
		{"a seal window of 580s with a timeout of 610s, below the allowance", "gateway.workspaceSealMaxDurationSeconds=580,controller.claimOrphanTimeoutSeconds=610"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"template", "lenny", chartDir(), "--set", "global.spiffeTrustDomain=lenny-test", "--set", tc.set}
			for _, kv := range chartRenderBase {
				args = append(args, "--set", kv)
			}
			cmd := exec.Command("helm", args...)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Run(); err == nil {
				t.Fatalf("helm template with %s rendered; want a render failure", tc.set)
			}
			if !strings.Contains(stderr.String(), "§5.2") {
				t.Errorf("render failure %q does not name §5.2", stderr.String())
			}
		})
	}
}
