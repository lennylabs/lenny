// SPDX-License-Identifier: MIT

package diagnostics_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/diagnostics"
)

func TestClassifyPodFailureOOM(t *testing.T) {
	// §25.6: exit code 137 plus the OOM flag classifies as OOM_KILLED.
	got, ok := diagnostics.ClassifyPodFailure(diagnostics.Signals{ExitCode: 137, OOMKilled: true})
	if !ok || got != diagnostics.CategoryOOMKilled {
		t.Errorf("ClassifyPodFailure(OOM) = %q, %v; want OOM_KILLED, true", got, ok)
	}
}

func TestClassifyPodFailureSetupCommand(t *testing.T) {
	// §25.6: exit code 1 during the setup phase is a setup-command failure.
	got, ok := diagnostics.ClassifyPodFailure(diagnostics.Signals{ExitCode: 1, InSetupPhase: true})
	if !ok || got != diagnostics.CategorySetupCommandFailed {
		t.Errorf("ClassifyPodFailure(setup) = %q, %v; want SETUP_COMMAND_FAILED, true", got, ok)
	}
}

func TestClassifyPodFailureImagePullAndResourcePressure(t *testing.T) {
	got, ok := diagnostics.ClassifyPodFailure(diagnostics.Signals{ImagePullError: true})
	if !ok || got != diagnostics.CategoryImagePullFailure {
		t.Errorf("image-pull failure = %q, %v; want IMAGE_PULL_FAILURE", got, ok)
	}
	got, ok = diagnostics.ClassifyPodFailure(diagnostics.Signals{ResourcePressure: true})
	if !ok || got != diagnostics.CategoryResourcePressure {
		t.Errorf("resource pressure = %q, %v; want RESOURCE_PRESSURE", got, ok)
	}
}

func TestClassifyPodFailureGenericCrash(t *testing.T) {
	got, ok := diagnostics.ClassifyPodFailure(diagnostics.Signals{ExitCode: 2})
	if !ok || got != diagnostics.CategoryPodCrash {
		t.Errorf("ClassifyPodFailure(exit 2) = %q, %v; want POD_CRASH, true", got, ok)
	}
}

func TestClassifyPodFailureCleanExit(t *testing.T) {
	if _, ok := diagnostics.ClassifyPodFailure(diagnostics.Signals{ExitCode: 0}); ok {
		t.Error("a clean exit reported a failure cause, want none")
	}
}

func TestClassifyPodFailurePrecedence(t *testing.T) {
	// An OOM kill outranks every other signal, even a setup-phase
	// non-zero exit and an image-pull error.
	got, _ := diagnostics.ClassifyPodFailure(diagnostics.Signals{
		ExitCode: 1, OOMKilled: true, InSetupPhase: true, ImagePullError: true,
	})
	if got != diagnostics.CategoryOOMKilled {
		t.Errorf("precedence: got %q, want OOM_KILLED to outrank other signals", got)
	}
	// A pre-start image-pull failure outranks exit-code analysis.
	got, _ = diagnostics.ClassifyPodFailure(diagnostics.Signals{
		ExitCode: 1, InSetupPhase: true, ImagePullError: true,
	})
	if got != diagnostics.CategoryImagePullFailure {
		t.Errorf("precedence: got %q, want IMAGE_PULL_FAILURE to outrank exit-code analysis", got)
	}
}
