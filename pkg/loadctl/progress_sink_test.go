// SPDX-License-Identifier: MIT

package loadctl

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"testing/iotest"
)

func TestFileSinkRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sink, err := newProgressSink(filepath.Join(dir, "progress"))
	if err != nil {
		t.Fatal(err)
	}
	events := []RunnerProgress{
		{RunID: "r1", Scenario: "s", ElapsedSeconds: 0.5, Iterations: 50},
		{RunID: "r1", Scenario: "s", ElapsedSeconds: 1.0, Iterations: 100},
		{RunID: "r1", Scenario: "s", ElapsedSeconds: 1.5, Iterations: 150},
	}
	for _, e := range events {
		if err := sink.Append(e.RunID, e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	rc, err := sink.Open("r1")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rc.Close()
	body, _ := io.ReadAll(iotest.OneByteReader(rc))
	dec := json.NewDecoder(bytes.NewReader(body))
	got := []RunnerProgress{}
	for {
		var p RunnerProgress
		if err := dec.Decode(&p); err != nil {
			break
		}
		got = append(got, p)
	}
	if len(got) != 3 {
		t.Fatalf("got %d events, want 3: %+v", len(got), got)
	}
	if got[2].Iterations != 150 {
		t.Errorf("last event iters=%d want 150", got[2].Iterations)
	}
}

func TestFileSinkConcurrentAppend(t *testing.T) {
	dir := t.TempDir()
	sink, err := newProgressSink(filepath.Join(dir, "p"))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	const N = 200
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = sink.Append("r1", RunnerProgress{RunID: "r1", Iterations: int64(i)})
		}(i)
	}
	wg.Wait()
	rc, err := sink.Open("r1")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	dec := json.NewDecoder(rc)
	count := 0
	for {
		var p RunnerProgress
		if err := dec.Decode(&p); err != nil {
			break
		}
		count++
	}
	if count != N {
		t.Errorf("persisted %d, want %d", count, N)
	}
}

func TestNoopSinkOpenReportsNoProgress(t *testing.T) {
	s, _ := newProgressSink("")
	_, err := s.Open("anything")
	if !errors.Is(err, ErrNoProgress) {
		t.Errorf("err=%v want ErrNoProgress", err)
	}
}
