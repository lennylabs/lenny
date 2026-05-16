// SPDX-License-Identifier: MIT

package erasure_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/erasure"
)

// countEraser is a §12.8 DeleteByUser adapter returning a fixed count.
func countEraser(n int) erasure.DeleteByUserFunc {
	return func(context.Context, string, string) (int, error) { return n, nil }
}

func TestDeleteByUserRunsEveryStore(t *testing.T) {
	o := erasure.New(
		erasure.Eraser{Name: "sessions", DeleteByUser: countEraser(3)},
		erasure.Eraser{Name: "interactions", DeleteByUser: countEraser(5)},
	)
	res, err := o.DeleteByUser(context.Background(), "acme", "alice")
	if err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	if res.Total != 8 {
		t.Errorf("Total = %d, want 8", res.Total)
	}
	if res.Deleted["sessions"] != 3 || res.Deleted["interactions"] != 5 {
		t.Errorf("Deleted = %v, want sessions=3 interactions=5", res.Deleted)
	}
}

func TestDeleteByUserRunsInDependencyOrder(t *testing.T) {
	var order []string
	track := func(name string) erasure.DeleteByUserFunc {
		return func(context.Context, string, string) (int, error) {
			order = append(order, name)
			return 0, nil
		}
	}
	o := erasure.New(
		erasure.Eraser{Name: "transcripts", DeleteByUser: track("transcripts")},
		erasure.Eraser{Name: "interactions", DeleteByUser: track("interactions")},
		erasure.Eraser{Name: "sessions", DeleteByUser: track("sessions")},
	)
	if _, err := o.DeleteByUser(context.Background(), "acme", "alice"); err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	want := []string{"transcripts", "interactions", "sessions"}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("erasure order = %v, want %v", order, want)
			break
		}
	}
}

func TestDeleteByUserIsFailFast(t *testing.T) {
	var reached []string
	o := erasure.New(
		erasure.Eraser{Name: "first", DeleteByUser: func(context.Context, string, string) (int, error) {
			reached = append(reached, "first")
			return 2, nil
		}},
		erasure.Eraser{Name: "broken", DeleteByUser: func(context.Context, string, string) (int, error) {
			reached = append(reached, "broken")
			return 0, errors.New("store down")
		}},
		erasure.Eraser{Name: "after", DeleteByUser: func(context.Context, string, string) (int, error) {
			reached = append(reached, "after")
			return 9, nil
		}},
	)
	res, err := o.DeleteByUser(context.Background(), "acme", "alice")
	if err == nil {
		t.Fatal("DeleteByUser should fail when a store errors")
	}
	// The store after the failure is not reached.
	for _, r := range reached {
		if r == "after" {
			t.Error("a store after the failed one was reached — the job is not fail-fast")
		}
	}
	// The partial result covers only the store erased before the failure.
	if res.Deleted["first"] != 2 {
		t.Errorf("partial result = %v, want first=2", res.Deleted)
	}
	if _, ok := res.Deleted["after"]; ok {
		t.Error("the partial result includes a store the job never reached")
	}
}

func TestDeleteByUserEmptyOrchestrator(t *testing.T) {
	res, err := erasure.New().DeleteByUser(context.Background(), "acme", "alice")
	if err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	if res.Total != 0 {
		t.Errorf("Total = %d, want 0", res.Total)
	}
}
