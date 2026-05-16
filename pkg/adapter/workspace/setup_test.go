// SPDX-License-Identifier: MIT

package workspace_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

func cmd(c string, timeout int32) *adapterv1.SetupCommand {
	return &adapterv1.SetupCommand{Cmd: c, TimeoutSeconds: timeout}
}

func TestRunSetupExecutesCommandsInOrder(t *testing.T) {
	dir := t.TempDir()
	err := workspace.RunSetup(context.Background(), dir, []*adapterv1.SetupCommand{
		cmd("echo first > a.txt", 30),
		cmd("echo second > b.txt", 30),
	})
	if err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("setup command did not produce %s: %v", name, err)
		}
	}
}

func TestRunSetupRunsInWorkdir(t *testing.T) {
	dir := t.TempDir()
	if err := workspace.RunSetup(context.Background(), dir, []*adapterv1.SetupCommand{
		cmd("touch in-workdir.marker", 30),
	}); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	// A relative path resolves against the command's working directory;
	// finding the marker under dir confirms the command ran there.
	if _, err := os.Stat(filepath.Join(dir, "in-workdir.marker")); err != nil {
		t.Errorf("setup command did not run in the workspace directory: %v", err)
	}
}

func TestRunSetupStopsAtFirstFailure(t *testing.T) {
	dir := t.TempDir()
	err := workspace.RunSetup(context.Background(), dir, []*adapterv1.SetupCommand{
		cmd("exit 3", 30),
		cmd("echo unreached > reached.txt", 30),
	})
	if err == nil {
		t.Fatal("RunSetup should return an error when a command exits non-zero")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "reached.txt")); statErr == nil {
		t.Error("a command after the failing one was executed")
	}
}

func TestRunSetupEnforcesTimeout(t *testing.T) {
	dir := t.TempDir()
	err := workspace.RunSetup(context.Background(), dir, []*adapterv1.SetupCommand{
		cmd("sleep 30", 1),
	})
	if err == nil {
		t.Fatal("RunSetup should return an error when a command exceeds its timeout")
	}
}

func TestRunSetupRejectsEmptyCommand(t *testing.T) {
	if err := workspace.RunSetup(context.Background(), t.TempDir(), []*adapterv1.SetupCommand{
		cmd("", 30),
	}); err == nil {
		t.Fatal("RunSetup should reject an empty command")
	}
}

func TestRunSetupAcceptsNoCommands(t *testing.T) {
	if err := workspace.RunSetup(context.Background(), t.TempDir(), nil); err != nil {
		t.Errorf("RunSetup with no commands should succeed, got %v", err)
	}
}
