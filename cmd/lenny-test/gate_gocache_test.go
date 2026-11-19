// SPDX-License-Identifier: MIT

package main

// Tests for the size cap of the gate's build cache volume. The cap
// implements the testing model rather than a specification behavior, so
// these tests carry no spec annotation.

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// fakeGateCommands records the commands capGateGoCache runs and answers
// the measurement with du output or an error.
type fakeGateCommands struct {
	duOut    string
	duErr    error
	cleanErr error
	calls    [][]string
}

func (f *fakeGateCommands) run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	if reflect.DeepEqual(args, gateGoCacheMeasureArgs("img")) {
		return []byte(f.duOut), f.duErr
	}
	return nil, f.cleanErr
}

// cleaned reports whether the fake ran the cleanup command.
func (f *fakeGateCommands) cleaned() bool {
	want := append([]string{"docker"}, gateGoCacheCleanArgs("img")...)
	for _, c := range f.calls {
		if reflect.DeepEqual(c, want) {
			return true
		}
	}
	return false
}

// duLine renders du -sk output for a size in KiB.
func duLine(kb int64) string {
	return strconv.FormatInt(kb, 10) + "\t" + gateGoCache + "\n"
}

// capKB is the size in KiB at which a 30 GB cap is reached exactly:
// 30*10^9 bytes is 29,296,875 KiB.
const capKB = 29296875

func TestGateGoCacheUnderTheCapIsKept(t *testing.T) {
	for _, kb := range []int64{0, 1000, capKB} {
		f := &fakeGateCommands{duOut: duLine(kb)}
		var log strings.Builder
		if capGateGoCache(context.Background(), f.run, "img", 30, &log) {
			t.Errorf("%d KiB: emptied; want kept at or under the 30 GB cap", kb)
		}
		if f.cleaned() || len(f.calls) != 1 {
			t.Errorf("%d KiB: commands %v; want only the measurement", kb, f.calls)
		}
		if log.Len() != 0 {
			t.Errorf("%d KiB: logged %q; want nothing", kb, log.String())
		}
	}
}

func TestGateGoCacheOverTheCapIsEmptiedAndLogged(t *testing.T) {
	for _, kb := range []int64{capKB + 1, 94531250} { // 94,531,250 KiB is 96.8 GB
		f := &fakeGateCommands{duOut: duLine(kb)}
		var log strings.Builder
		if !capGateGoCache(context.Background(), f.run, "img", 30, &log) {
			t.Errorf("%d KiB: kept; want emptied over the 30 GB cap", kb)
		}
		if !f.cleaned() {
			t.Errorf("%d KiB: commands %v; want the cleanup to run", kb, f.calls)
		}
		got := log.String()
		if strings.Count(got, "\n") != 1 || !strings.Contains(got, "emptied") || !strings.Contains(got, gateBuildVolume) {
			t.Errorf("%d KiB: logged %q; want one line naming the volume and saying it was emptied", kb, got)
		}
	}
	f := &fakeGateCommands{duOut: duLine(94531250)}
	var log strings.Builder
	capGateGoCache(context.Background(), f.run, "img", 30, &log)
	if !strings.Contains(log.String(), "96.8 GB") || !strings.Contains(log.String(), "30 GB cap") {
		t.Errorf("logged %q; want the size 96.8 GB and the 30 GB cap", log.String())
	}
}

func TestGateGoCacheCapZeroRunsNothing(t *testing.T) {
	f := &fakeGateCommands{duOut: duLine(1 << 40)}
	var log strings.Builder
	if capGateGoCache(context.Background(), f.run, "img", 0, &log) {
		t.Error("emptied with the cap at 0; want the check off")
	}
	if len(f.calls) != 0 || log.Len() != 0 {
		t.Errorf("commands %v, log %q; want no command and no log with the cap at 0", f.calls, log.String())
	}
}

func TestGateGoCacheMeasurementErrorLogsAndKeepsTheCache(t *testing.T) {
	cases := map[string]*fakeGateCommands{
		"docker fails":     {duErr: errors.New("daemon not reachable")},
		"empty output":     {duOut: ""},
		"not a size":       {duOut: "du: cannot read directory\n"},
		"a negative count": {duOut: "-5\t/var/cache/go-build\n"},
	}
	for name, f := range cases {
		t.Run(name, func(t *testing.T) {
			var log strings.Builder
			if capGateGoCache(context.Background(), f.run, "img", 30, &log) {
				t.Error("emptied after a failed measurement; want the cache kept")
			}
			if f.cleaned() {
				t.Errorf("commands %v; want no cleanup after a failed measurement", f.calls)
			}
			if got := log.String(); !strings.Contains(got, "warning: measure the build cache volume") || strings.Count(got, "\n") != 1 {
				t.Errorf("logged %q; want one warning that the measurement failed", got)
			}
		})
	}
}

func TestGateGoCacheCleanupErrorLogsAndProceeds(t *testing.T) {
	f := &fakeGateCommands{duOut: duLine(94531250), cleanErr: errors.New("exit status 1")}
	var log strings.Builder
	if capGateGoCache(context.Background(), f.run, "img", 30, &log) {
		t.Error("reported the cache emptied after the cleanup failed")
	}
	if got := log.String(); !strings.Contains(got, "emptying it failed") || !strings.Contains(got, "exit status 1") {
		t.Errorf("logged %q; want a warning naming the cleanup failure", got)
	}
}

func TestGateGoCacheCommandsMountTheVolumeWhereTheGateDoes(t *testing.T) {
	ref := "sha256:abc"
	wantMeasure := []string{"run", "--rm", "--network", "none", "-v", "lenny-gate-gocache:/var/cache/go-build", ref, "du", "-sk", "/var/cache/go-build"}
	if got := gateGoCacheMeasureArgs(ref); !reflect.DeepEqual(got, wantMeasure) {
		t.Errorf("measure args\n got %q\nwant %q", got, wantMeasure)
	}
	wantClean := []string{"run", "--rm", "--network", "none", "-v", "lenny-gate-gocache:/var/cache/go-build", "-e", "GOCACHE=/var/cache/go-build", ref, "go", "clean", "-cache"}
	if got := gateGoCacheCleanArgs(ref); !reflect.DeepEqual(got, wantClean) {
		t.Errorf("clean args\n got %q\nwant %q", got, wantClean)
	}
	// The cleanup must empty the volume the gate run mounts, at the path
	// the gate run gives GOCACHE.
	run := strings.Join(gateDockerRunArgs(gateHost{root: "/r", image: "img"}), " ")
	if !strings.Contains(run, "-v "+gateBuildVolume+":"+gateGoCache) {
		t.Errorf("gate run args %q do not mount %s at %s", run, gateBuildVolume, gateGoCache)
	}
}

func TestGateHostImageRefPrefersTheRecordedID(t *testing.T) {
	if got := (gateHost{image: "lenny-gate:t", imageID: "sha256:x"}).imageRef(); got != "sha256:x" {
		t.Errorf("imageRef %q; want the recorded ID", got)
	}
	if got := (gateHost{image: "lenny-gate:t"}).imageRef(); got != "lenny-gate:t" {
		t.Errorf("imageRef %q; want the tag without an ID", got)
	}
}

func TestGateGoCacheCapDefaultReadsTheEnvironment(t *testing.T) {
	cases := []struct {
		env     string
		want    int
		wantErr bool
	}{
		{env: "", want: defaultGateGoCacheCapGB},
		{env: "12", want: 12},
		{env: " 50 ", want: 50},
		{env: "0", want: 0},
		{env: "-1", wantErr: true},
		{env: "30GB", wantErr: true},
		{env: "2.5", wantErr: true},
	}
	for _, c := range cases {
		got, err := gateGoCacheCapDefault(func(k string) string {
			if k == gateEnvGoCacheCap {
				return c.env
			}
			return ""
		})
		if c.wantErr {
			if err == nil || !strings.Contains(err.Error(), gateEnvGoCacheCap) {
				t.Errorf("%q: err %v; want an error naming %s", c.env, err, gateEnvGoCacheCap)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%q: got %d, %v; want %d", c.env, got, err, c.want)
		}
	}
}

func TestGateRefusesANegativeOrMalformedCacheCap(t *testing.T) {
	if code := runGate([]string{"--gocache-cap-gb", "-1"}); code != 2 {
		t.Errorf("--gocache-cap-gb -1: exit %d; want 2", code)
	}
	t.Setenv(gateEnvGoCacheCap, "lots")
	if code := runGate(nil); code != 2 {
		t.Errorf("%s=lots: exit %d; want 2", gateEnvGoCacheCap, code)
	}
}

func TestExecGateCommandReturnsStdoutAndStderrOnFailure(t *testing.T) {
	out, err := execGateCommand(context.Background(), "sh", "-c", "echo 42")
	if err != nil || strings.TrimSpace(string(out)) != "42" {
		t.Errorf("got %q, %v; want 42 and no error", out, err)
	}
	_, err = execGateCommand(context.Background(), "sh", "-c", "echo boom >&2; exit 3")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("err %v; want the failure with its standard error", err)
	}
	_, err = execGateCommand(context.Background(), "sh", "-c", "exit 4")
	if err == nil || !strings.Contains(err.Error(), "exit status 4") {
		t.Errorf("err %v; want the exit status", err)
	}
}
