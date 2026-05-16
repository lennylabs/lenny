// SPDX-License-Identifier: MIT

package erasure_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/erasure"
)

// countByUser is a user-scoped erasure adapter returning a fixed count.
func countByUser(n int) erasure.DeleteByUserFunc {
	return func(context.Context, string, string) (int, error) { return n, nil }
}

func TestDeleteByUserRunsEveryUserScopedStore(t *testing.T) {
	o := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{
		{Name: "sessions", DeleteByUser: countByUser(3)},
		{Name: "interactions", DeleteByUser: countByUser(5)},
	}})
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

func TestDeleteByUserErasesSessionScopedStores(t *testing.T) {
	// The user owns two sessions; the transcript store is erased once
	// per session before the session rows themselves are deleted.
	var erasedSessions []string
	o := erasure.New(erasure.Config{
		Sessions: func(context.Context, string, string) ([]string, error) {
			return []string{"sess-1", "sess-2"}, nil
		},
		SessionScoped: []erasure.SessionEraser{{
			Name: "transcripts",
			DeleteBySession: func(_ context.Context, _, sid string) (int, error) {
				erasedSessions = append(erasedSessions, sid)
				return 4, nil
			},
		}},
		UserScoped: []erasure.Eraser{{Name: "sessions", DeleteByUser: countByUser(2)}},
	})
	res, err := o.DeleteByUser(context.Background(), "acme", "alice")
	if err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	if len(erasedSessions) != 2 {
		t.Errorf("transcript store erased %d sessions, want 2", len(erasedSessions))
	}
	if res.Deleted["transcripts"] != 8 { // 4 entries × 2 sessions
		t.Errorf("transcripts deleted = %d, want 8", res.Deleted["transcripts"])
	}
	if res.Total != 10 { // 8 transcript entries + 2 sessions
		t.Errorf("Total = %d, want 10", res.Total)
	}
}

func TestSessionScopedRunsBeforeUserScoped(t *testing.T) {
	var order []string
	o := erasure.New(erasure.Config{
		Sessions: func(context.Context, string, string) ([]string, error) {
			return []string{"sess-1"}, nil
		},
		SessionScoped: []erasure.SessionEraser{{
			Name: "transcripts",
			DeleteBySession: func(context.Context, string, string) (int, error) {
				order = append(order, "transcripts")
				return 0, nil
			},
		}},
		UserScoped: []erasure.Eraser{{
			Name: "sessions",
			DeleteByUser: func(context.Context, string, string) (int, error) {
				order = append(order, "sessions")
				return 0, nil
			},
		}},
	})
	if _, err := o.DeleteByUser(context.Background(), "acme", "alice"); err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	if len(order) != 2 || order[0] != "transcripts" || order[1] != "sessions" {
		t.Errorf("erasure order = %v, want [transcripts sessions] — session-scoped first", order)
	}
}

func TestDeleteByUserIsFailFast(t *testing.T) {
	var reached []string
	o := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{
		{Name: "first", DeleteByUser: func(context.Context, string, string) (int, error) {
			reached = append(reached, "first")
			return 2, nil
		}},
		{Name: "broken", DeleteByUser: func(context.Context, string, string) (int, error) {
			reached = append(reached, "broken")
			return 0, errors.New("store down")
		}},
		{Name: "after", DeleteByUser: func(context.Context, string, string) (int, error) {
			reached = append(reached, "after")
			return 9, nil
		}},
	}})
	res, err := o.DeleteByUser(context.Background(), "acme", "alice")
	if err == nil {
		t.Fatal("DeleteByUser should fail when a store errors")
	}
	for _, r := range reached {
		if r == "after" {
			t.Error("a store after the failed one was reached — the job is not fail-fast")
		}
	}
	if res.Deleted["first"] != 2 {
		t.Errorf("partial result = %v, want first=2", res.Deleted)
	}
	if _, ok := res.Deleted["after"]; ok {
		t.Error("the partial result includes a store the job never reached")
	}
}

func TestDeleteByUserPropagatesSessionListError(t *testing.T) {
	o := erasure.New(erasure.Config{
		Sessions: func(context.Context, string, string) ([]string, error) {
			return nil, errors.New("session store down")
		},
		SessionScoped: []erasure.SessionEraser{{
			Name:            "transcripts",
			DeleteBySession: func(context.Context, string, string) (int, error) { return 0, nil },
		}},
	})
	if _, err := o.DeleteByUser(context.Background(), "acme", "alice"); err == nil {
		t.Error("DeleteByUser should fail when the session lister errors")
	}
}

func TestDeleteByUserEmptyOrchestrator(t *testing.T) {
	res, err := erasure.New(erasure.Config{}).DeleteByUser(context.Background(), "acme", "alice")
	if err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	if res.Total != 0 {
		t.Errorf("Total = %d, want 0", res.Total)
	}
}
