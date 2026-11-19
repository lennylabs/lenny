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
// the measurement with du output, the in-use listing with container IDs,
// and the cleanup with an error or success.
type fakeGateCommands struct {
	duOut    string
	duErr    error
	psOut    string
	psErr    error
	cleanErr error
	calls    [][]string
}

func (f *fakeGateCommands) run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	switch {
	case reflect.DeepEqual(args, gateGoCacheMeasureArgs("img")):
		return []byte(f.duOut), f.duErr
	case reflect.DeepEqual(args, gateGoCacheInUseArgs()):
		return []byte(f.psOut), f.psErr
	case reflect.DeepEqual(args, gateGoCacheCleanArgs("img")):
		return nil, f.cleanErr
	}
	return nil, errors.New("unexpected command")
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

// kb96_8GB is 96.8 GB (10^9 bytes each) in KiB.
const kb96_8GB = 94531250

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
	for _, kb := range []int64{capKB + 1, kb96_8GB} {
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
	f := &fakeGateCommands{duOut: duLine(kb96_8GB)}
	var log strings.Builder
	capGateGoCache(context.Background(), f.run, "img", 30, &log)
	if !strings.Contains(log.String(), "96.8 GB") || !strings.Contains(log.String(), "30 GB cap") {
		t.Errorf("logged %q; want the size 96.8 GB and the 30 GB cap", log.String())
	}
}

func TestGateGoCacheInUseByAnotherContainerIsNotEmptied(t *testing.T) {
	f := &fakeGateCommands{duOut: duLine(kb96_8GB), psOut: "1a2b3c\n4d5e6f\n"}
	var log strings.Builder
	if capGateGoCache(context.Background(), f.run, "img", 30, &log) {
		t.Error("emptied while other containers use the volume")
	}
	if f.cleaned() {
		t.Errorf("commands %v; want no cleanup while the volume is in use", f.calls)
	}
	got := log.String()
	if strings.Count(got, "\n") != 1 || !strings.Contains(got, "not emptied") || !strings.Contains(got, "1a2b3c 4d5e6f") {
		t.Errorf("logged %q; want one line saying the volume was not emptied and naming the containers", got)
	}
}

func TestGateGoCacheInUseListingErrorKeepsTheCache(t *testing.T) {
	f := &fakeGateCommands{duOut: duLine(kb96_8GB), psErr: errors.New("daemon gone")}
	var log strings.Builder
	if capGateGoCache(context.Background(), f.run, "img", 30, &log) {
		t.Error("emptied when the in-use listing failed")
	}
	if f.cleaned() {
		t.Errorf("commands %v; want no cleanup when the in-use listing failed", f.calls)
	}
	if got := log.String(); !strings.Contains(got, "warning") || !strings.Contains(got, "daemon gone") {
		t.Errorf("logged %q; want a warning naming the listing failure", got)
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

func TestGateGoCacheComparisonHoldsAtTheExtremes(t *testing.T) {
	// 10^15 bytes, the largest cap, is 976,562,500,000 KiB.
	const maxCapKB = 976562500000
	cases := []struct {
		kb   int64
		want bool
	}{
		{kb: maxCapKB, want: false},
		{kb: maxCapKB + 1, want: true},
		// A size whose byte count does not fit in int64 still compares as
		// over the cap rather than wrapping to a negative number.
		{kb: 1 << 62, want: true},
	}
	for _, c := range cases {
		f := &fakeGateCommands{duOut: duLine(c.kb)}
		var log strings.Builder
		if got := capGateGoCache(context.Background(), f.run, "img", maxGateGoCacheCapGB, &log); got != c.want {
			t.Errorf("%d KiB under a cap of %d GB: emptied %v; want %v", c.kb, maxGateGoCacheCapGB, got, c.want)
		}
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
			if f.cleaned() || len(f.calls) != 1 {
				t.Errorf("commands %v; want only the measurement", f.calls)
			}
			if got := log.String(); !strings.Contains(got, "warning: measure the build cache volume") || strings.Count(got, "\n") != 1 {
				t.Errorf("logged %q; want one warning that the measurement failed", got)
			}
		})
	}
}

func TestGateGoCacheCleanupErrorLogsAndProceeds(t *testing.T) {
	f := &fakeGateCommands{duOut: duLine(kb96_8GB), cleanErr: errors.New("exit status 1")}
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
	prefix := []string{
		"run", "--rm", "--network", "none",
		"-v", "lenny-gate-gocache:/var/cache/go-build",
		"-e", "GOCACHE=/var/cache/go-build",
		ref, "timeout", "--kill-after=10", "300",
	}
	wantMeasure := append(append([]string{}, prefix...), "du", "-sk", "/var/cache/go-build")
	if got := gateGoCacheMeasureArgs(ref); !reflect.DeepEqual(got, wantMeasure) {
		t.Errorf("measure args\n got %q\nwant %q", got, wantMeasure)
	}
	wantClean := append(append([]string{}, prefix...), "go", "clean", "-cache")
	if got := gateGoCacheCleanArgs(ref); !reflect.DeepEqual(got, wantClean) {
		t.Errorf("clean args\n got %q\nwant %q", got, wantClean)
	}
	wantPS := []string{"ps", "-q", "--filter", "volume=lenny-gate-gocache"}
	if got := gateGoCacheInUseArgs(); !reflect.DeepEqual(got, wantPS) {
		t.Errorf("in-use args\n got %q\nwant %q", got, wantPS)
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

func TestResolveGateGoCacheCapPrefersTheFlagThenTheEnvironment(t *testing.T) {
	cases := []struct {
		name    string
		flagSet bool
		flagGB  int
		env     string
		want    int
		wantErr string
	}{
		{name: "default", want: defaultGateGoCacheCapGB},
		{name: "environment", env: "12", want: 12},
		{name: "environment with spaces", env: " 50 ", want: 50},
		{name: "environment zero", env: "0", want: 0},
		{name: "environment negative", env: "-1", wantErr: gateEnvGoCacheCap},
		{name: "environment with a unit", env: "30GB", wantErr: gateEnvGoCacheCap},
		{name: "environment fraction", env: "2.5", wantErr: gateEnvGoCacheCap},
		{name: "environment over the maximum", env: "99999999999", wantErr: gateEnvGoCacheCap},
		{name: "flag", flagSet: true, flagGB: 40, env: "12", want: 40},
		{name: "flag over a malformed environment", flagSet: true, flagGB: 40, env: "lots", want: 40},
		{name: "flag zero", flagSet: true, flagGB: 0, want: 0},
		{name: "flag at the maximum", flagSet: true, flagGB: maxGateGoCacheCapGB, want: maxGateGoCacheCapGB},
		{name: "flag negative", flagSet: true, flagGB: -1, wantErr: "--gocache-cap-gb"},
		{name: "flag over the maximum", flagSet: true, flagGB: maxGateGoCacheCapGB + 1, wantErr: "--gocache-cap-gb"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := resolveGateGoCacheCap(c.flagSet, c.flagGB, func(k string) string {
				if k == gateEnvGoCacheCap {
					return c.env
				}
				return ""
			})
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Errorf("err %v; want an error naming %s", err, c.wantErr)
				}
				return
			}
			if err != nil || got != c.want {
				t.Errorf("got %d, %v; want %d", got, err, c.want)
			}
		})
	}
}

func TestGateRefusesAnOutOfRangeOrMalformedCacheCap(t *testing.T) {
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
