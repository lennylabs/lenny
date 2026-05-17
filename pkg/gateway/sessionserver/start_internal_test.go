// SPDX-License-Identifier: MIT

package sessionserver

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
)

func TestExperimentContextToProtoNil(t *testing.T) {
	// An unenrolled session has no experimentContext to deliver.
	if got := experimentContextToProto(nil); got != nil {
		t.Errorf("experimentContextToProto(nil) = %v, want nil", got)
	}
}

func TestExperimentContextToProtoMapsFields(t *testing.T) {
	got := experimentContextToProto(&sessionstore.ExperimentContext{
		ExperimentID: "exp_1",
		VariantID:    "treatment",
		Inherited:    true,
	})
	if got == nil {
		t.Fatal("experimentContextToProto returned nil for an enrolled session")
	}
	if got.GetExperimentId() != "exp_1" || got.GetVariantId() != "treatment" || !got.GetInherited() {
		t.Errorf("proto experimentContext = %+v, want exp_1/treatment inherited", got)
	}
}
