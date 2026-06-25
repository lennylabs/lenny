// SPDX-License-Identifier: MIT

package generators_test

import (
	"testing"

	"pgregory.net/rapid"

	"github.com/lennylabs/lenny/tests/testinfra/fixtures/generators"
)

// spec: 18.2 (generated WorkspacePlan has expected shape)
// diagnosis: The generator emitted a plan without sources or with a
//
//	wrong schemaVersion. The shape contract is broken; tests
//	that consume the generator will see arbitrary inputs.
func TestWorkspacePlanShape(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		plan := generators.WorkspacePlan().Draw(rt, "plan")
		if plan["schemaVersion"] != 1 {
			rt.Errorf("schemaVersion: want 1, got %v", plan["schemaVersion"])
		}
		if _, ok := plan["sources"].([]map[string]any); !ok {
			rt.Errorf("sources missing or wrong type")
		}
	})
}

// spec: 18.2 (TaskRecord generator)
// diagnosis: The TaskRecord generator dropped a required field.
func TestTaskRecordShape(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		task := generators.TaskRecord().Draw(rt, "task")
		for _, key := range []string{"id", "state", "runtime", "prompt"} {
			if _, ok := task[key]; !ok {
				rt.Errorf("TaskRecord missing %q", key)
			}
		}
	})
}

// spec: 18.2 (MessagePart generator covers every kind)
// diagnosis: A kind beyond the documented enum slipped through.
func TestMessagePartKinds(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		part := generators.MessagePart().Draw(rt, "part")
		kind := part["type"].(string)
		switch kind {
		case "text", "tool_use", "tool_result", "reasoning", "file":
		default:
			rt.Errorf("MessagePart emitted unknown kind %q", kind)
		}
	})
}
