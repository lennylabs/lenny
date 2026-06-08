// SPDX-License-Identifier: MIT

package delegation

import (
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/delegationpolicystore"
)

// spec: §8.3 lines 157-188 — resolveChildContentPolicy applies the
// four-axis contentPolicy inheritance and monotonicity rules. A child
// lease may only make the policy stricter; any weakening rejects. The
// returned effective policy is the per-axis narrowest. F-13.5.10.

const (
	defInput = delegationpolicystore.DefaultMaxInputSize        // 128 KiB
	defFile  = delegationpolicystore.DefaultMaxExportedFileSize // 10 MiB
)

func TestResolveChildContentPolicy_inheritWhenNoChildPolicy_spec_8_3_240(t *testing.T) {
	parent := effContentPolicy{MaxInputSize: 65536, InterceptorRef: "scrub", ScanExportedFiles: true, MaxExportedFileSize: 4096}
	got, err := resolveChildContentPolicy(parent, delegationpolicystore.ContentPolicy{}, false)
	if err != nil {
		t.Fatalf("inherit path returned error: %v", err)
	}
	if got != parent {
		t.Errorf("child without its own policy must inherit the parent's effective policy verbatim: got %+v want %+v", got, parent)
	}
}

func TestResolveChildContentPolicy_permittedAxes_spec_8_3_157(t *testing.T) {
	parent := effContentPolicy{MaxInputSize: defInput, MaxExportedFileSize: defFile}
	cases := []struct {
		name  string
		child delegationpolicystore.ContentPolicy
		want  effContentPolicy
	}{
		{
			name:  "child tightens maxInputSize",
			child: delegationpolicystore.ContentPolicy{MaxInputSize: 4096},
			want:  effContentPolicy{MaxInputSize: 4096, MaxExportedFileSize: defFile},
		},
		{
			name:  "child adds interceptor over null parent",
			child: delegationpolicystore.ContentPolicy{InterceptorRef: "scrub"},
			want:  effContentPolicy{MaxInputSize: defInput, InterceptorRef: "scrub", MaxExportedFileSize: defFile},
		},
		{
			name:  "child enables scanExportedFiles",
			child: delegationpolicystore.ContentPolicy{InterceptorRef: "scrub", ScanExportedFiles: true},
			want:  effContentPolicy{MaxInputSize: defInput, InterceptorRef: "scrub", ScanExportedFiles: true, MaxExportedFileSize: defFile},
		},
		{
			name:  "child tightens maxExportedFileSize",
			child: delegationpolicystore.ContentPolicy{MaxExportedFileSize: 4096},
			want:  effContentPolicy{MaxInputSize: defInput, MaxExportedFileSize: 4096},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveChildContentPolicy(parent, tc.child, true)
			if err != nil {
				t.Fatalf("unexpected rejection: %v", err)
			}
			if got != tc.want {
				t.Errorf("effective = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestResolveChildContentPolicy_sameInterceptorPermitted_spec_8_3_185(t *testing.T) {
	parent := effContentPolicy{MaxInputSize: defInput, InterceptorRef: "scrub", MaxExportedFileSize: defFile}
	got, err := resolveChildContentPolicy(parent, delegationpolicystore.ContentPolicy{InterceptorRef: "scrub"}, true)
	if err != nil {
		t.Fatalf("same interceptorRef must be permitted (condition 1): %v", err)
	}
	if got.InterceptorRef != "scrub" {
		t.Errorf("InterceptorRef = %q, want scrub", got.InterceptorRef)
	}
}

func TestResolveChildContentPolicy_weakeningRejections_spec_8_3_177_187(t *testing.T) {
	cases := []struct {
		name     string
		parent   effContentPolicy
		child    delegationpolicystore.ContentPolicy
		wantAxis string
	}{
		{
			name:     "maxInputSize widened",
			parent:   effContentPolicy{MaxInputSize: 4096, MaxExportedFileSize: defFile},
			child:    delegationpolicystore.ContentPolicy{MaxInputSize: 8192},
			wantAxis: "maxInputSize",
		},
		{
			name:     "maxExportedFileSize widened",
			parent:   effContentPolicy{MaxInputSize: defInput, MaxExportedFileSize: 4096},
			child:    delegationpolicystore.ContentPolicy{MaxExportedFileSize: 8192},
			wantAxis: "maxExportedFileSize",
		},
		{
			name:     "scanExportedFiles dropped",
			parent:   effContentPolicy{MaxInputSize: defInput, InterceptorRef: "scrub", ScanExportedFiles: true, MaxExportedFileSize: defFile},
			child:    delegationpolicystore.ContentPolicy{InterceptorRef: "scrub", ScanExportedFiles: false},
			wantAxis: "scanExportedFiles",
		},
		{
			name:     "interceptorRef set back to null",
			parent:   effContentPolicy{MaxInputSize: defInput, InterceptorRef: "scrub", MaxExportedFileSize: defFile},
			child:    delegationpolicystore.ContentPolicy{InterceptorRef: ""},
			wantAxis: "interceptorRef",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolveChildContentPolicy(tc.parent, tc.child, true)
			var w *ContentPolicyWeakeningError
			if !errors.As(err, &w) {
				t.Fatalf("want *ContentPolicyWeakeningError, got %v", err)
			}
			if w.Axis != tc.wantAxis {
				t.Errorf("axis = %q, want %q", w.Axis, tc.wantAxis)
			}
		})
	}
}

func TestResolveChildContentPolicy_interceptorSubstitution_spec_8_3_188(t *testing.T) {
	parent := effContentPolicy{MaxInputSize: defInput, InterceptorRef: "scrub", MaxExportedFileSize: defFile}
	_, err := resolveChildContentPolicy(parent, delegationpolicystore.ContentPolicy{InterceptorRef: "redactor"}, true)
	var s *ContentPolicyInterceptorSubstitutionError
	if !errors.As(err, &s) {
		t.Fatalf("want *ContentPolicyInterceptorSubstitutionError, got %v", err)
	}
	if s.ParentRef != "scrub" || s.ChildRef != "redactor" {
		t.Errorf("substitution error refs = (%q,%q), want (scrub,redactor)", s.ParentRef, s.ChildRef)
	}
}

func TestEffContentPolicy_tighterThanDefault_spec_8_3_157(t *testing.T) {
	cases := []struct {
		name string
		eff  effContentPolicy
		want bool
	}{
		{"pure default", platformDefaultContentPolicy(), false},
		{"interceptor set", effContentPolicy{MaxInputSize: defInput, InterceptorRef: "scrub", MaxExportedFileSize: defFile}, true},
		{"scan on", effContentPolicy{MaxInputSize: defInput, ScanExportedFiles: true, MaxExportedFileSize: defFile}, true},
		{"input tightened", effContentPolicy{MaxInputSize: 4096, MaxExportedFileSize: defFile}, true},
		{"file tightened", effContentPolicy{MaxInputSize: defInput, MaxExportedFileSize: 4096}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.eff.tighterThanDefault(); got != tc.want {
				t.Errorf("tighterThanDefault() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNormalizeContentPolicy_fillsDefaults_spec_8_3_157(t *testing.T) {
	got := normalizeContentPolicy(delegationpolicystore.ContentPolicy{InterceptorRef: "scrub"})
	if got.MaxInputSize != defInput || got.MaxExportedFileSize != defFile {
		t.Errorf("size defaults not filled: %+v", got)
	}
	if got.InterceptorRef != "scrub" {
		t.Errorf("InterceptorRef = %q, want scrub", got.InterceptorRef)
	}
}
