// SPDX-License-Identifier: MIT

package adapter

import (
	"testing"
	"time"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

func TestSetupOptionsFromProtoNil(t *testing.T) {
	// No policy means no aggregate cap on the setup phase.
	got := setupOptionsFromProto(nil)
	if got.AggregateTimeout != 0 || got.FailOnAggregateTimeout {
		t.Errorf("setupOptionsFromProto(nil) = %+v, want a zero SetupOptions", got)
	}
}

func TestSetupOptionsFromProtoFail(t *testing.T) {
	got := setupOptionsFromProto(&adapterv1.SetupPolicy{TimeoutSeconds: 300, OnTimeout: "fail"})
	if got.AggregateTimeout != 300*time.Second || !got.FailOnAggregateTimeout {
		t.Errorf("setupOptionsFromProto(fail) = %+v, want 300s / fail", got)
	}
}

func TestSetupOptionsFromProtoWarn(t *testing.T) {
	got := setupOptionsFromProto(&adapterv1.SetupPolicy{TimeoutSeconds: 120, OnTimeout: "warn"})
	if got.AggregateTimeout != 120*time.Second || got.FailOnAggregateTimeout {
		t.Errorf("setupOptionsFromProto(warn) = %+v, want 120s / proceed", got)
	}
}

func TestSetupOptionsFromProtoEmptyDispositionIsFail(t *testing.T) {
	// §5.1: an empty onTimeout is the conservative fail default.
	got := setupOptionsFromProto(&adapterv1.SetupPolicy{TimeoutSeconds: 60})
	if !got.FailOnAggregateTimeout {
		t.Error("an empty onTimeout must be treated as fail")
	}
}
