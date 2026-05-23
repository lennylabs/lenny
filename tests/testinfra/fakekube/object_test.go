// SPDX-License-Identifier: MIT

package fakekube

import (
	"errors"
	"sync"
	"testing"
)

func TestObjectStoreCreateGet(t *testing.T) {
	s := NewObjectStore()
	obj := &Object{Kind: "Sandbox", Namespace: "lenny-agents", Name: "pod-1"}
	if err := s.Create(obj); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := s.Get("Sandbox", "lenny-agents", "pod-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ResourceVersion == "" {
		t.Error("ResourceVersion not assigned on Create")
	}
}

func TestObjectStoreCreateDuplicateRejected(t *testing.T) {
	s := NewObjectStore()
	obj := &Object{Kind: "X", Name: "a"}
	_ = s.Create(obj)
	if err := s.Create(obj); err == nil {
		t.Error("expected duplicate-create error")
	}
}

func TestObjectStoreUpdateConflictOnStaleRV(t *testing.T) {
	s := NewObjectStore()
	obj := &Object{Kind: "X", Name: "a"}
	_ = s.Create(obj)
	got, _ := s.Get("X", "", "a")
	got.Labels = map[string]string{"v": "1"}
	if err := s.Update(got); err != nil {
		t.Fatalf("first Update: %v", err)
	}
	// got now holds the *old* RV; the second Update must conflict.
	got.Labels["v"] = "2"
	if err := s.Update(got); !errors.Is(err, ErrConflict) {
		t.Errorf("expected ErrConflict, got %v", err)
	}
}

// TestObjectStoreSSAConflictUnderRace asserts that when N goroutines
// race to Update the same object with the same observed RV, exactly
// one succeeds and the others see ErrConflict. This is the §5.2
// invariant SSA enforces.
func TestObjectStoreSSAConflictUnderRace(t *testing.T) {
	s := NewObjectStore()
	_ = s.Create(&Object{Kind: "Sandbox", Name: "pod-x"})
	observed, _ := s.Get("Sandbox", "", "pod-x")
	const N = 50
	var wg sync.WaitGroup
	var success, conflicts int
	var mu sync.Mutex
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			localObj := *observed
			localObj.Labels = map[string]string{"writer": "x"}
			err := s.Update(&localObj)
			mu.Lock()
			if err == nil {
				success++
			} else if errors.Is(err, ErrConflict) {
				conflicts++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if success != 1 {
		t.Errorf("§5.2 violated: %d successful updates with same observed RV (want exactly 1)", success)
	}
	if conflicts != N-1 {
		t.Errorf("§5.2 violated: %d conflicts (want %d)", conflicts, N-1)
	}
}

func TestObjectStoreHookFires(t *testing.T) {
	s := NewObjectStore()
	var ops []string
	var mu sync.Mutex
	s.AddHook(func(op string, _ *Object) {
		mu.Lock()
		ops = append(ops, op)
		mu.Unlock()
	})
	_ = s.Create(&Object{Kind: "X", Name: "a"})
	got, _ := s.Get("X", "", "a")
	_ = s.Update(got)
	_ = s.Delete("X", "", "a")
	mu.Lock()
	defer mu.Unlock()
	if len(ops) != 3 || ops[0] != "create" || ops[1] != "update" || ops[2] != "delete" {
		t.Errorf("ops=%v want [create update delete]", ops)
	}
}
