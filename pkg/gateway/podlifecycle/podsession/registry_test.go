// SPDX-License-Identifier: MIT

package podsession_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
)

func TestRegistryPutGet(t *testing.T) {
	r := podsession.NewRegistry()
	bind := &podsession.BindResult{SessionID: "sess-1", SandboxName: "sbx-1", PodIP: "10.0.0.1"}
	r.Put(bind)

	got, ok := r.Get("sess-1")
	if !ok {
		t.Fatal("Get returned ok=false for a stored binding")
	}
	if got.SandboxName != "sbx-1" {
		t.Errorf("binding sandbox = %q, want sbx-1", got.SandboxName)
	}
}

func TestRegistryGetAbsent(t *testing.T) {
	r := podsession.NewRegistry()
	if _, ok := r.Get("sess-absent"); ok {
		t.Error("Get returned ok=true for an unstored session")
	}
}

func TestRegistryRemove(t *testing.T) {
	r := podsession.NewRegistry()
	r.Put(&podsession.BindResult{SessionID: "sess-1", SandboxName: "sbx-1"})

	got, ok := r.Remove("sess-1")
	if !ok || got.SandboxName != "sbx-1" {
		t.Fatalf("Remove returned (%v, %t), want the stored binding", got, ok)
	}
	if _, ok := r.Get("sess-1"); ok {
		t.Error("Get returned ok=true after Remove")
	}
}

func TestRegistryRemoveAbsent(t *testing.T) {
	r := podsession.NewRegistry()
	if _, ok := r.Remove("sess-absent"); ok {
		t.Error("Remove returned ok=true for an unstored session")
	}
}

func TestRegistryPutReplacesPriorBinding(t *testing.T) {
	r := podsession.NewRegistry()
	r.Put(&podsession.BindResult{SessionID: "sess-1", SandboxName: "old"})
	r.Put(&podsession.BindResult{SessionID: "sess-1", SandboxName: "new"})

	got, _ := r.Get("sess-1")
	if got.SandboxName != "new" {
		t.Errorf("binding sandbox = %q, want the replacement new", got.SandboxName)
	}
	if r.Len() != 1 {
		t.Errorf("registry length = %d, want 1 after replacing one session", r.Len())
	}
}

func TestRegistryConcurrentAccess(t *testing.T) {
	r := podsession.NewRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := fmt.Sprintf("sess-%d", n)
			r.Put(&podsession.BindResult{SessionID: id})
			r.Get(id)
			r.Len()
			r.Remove(id)
		}(i)
	}
	wg.Wait()
	if r.Len() != 0 {
		t.Errorf("registry length = %d, want 0 after every session was removed", r.Len())
	}
}
