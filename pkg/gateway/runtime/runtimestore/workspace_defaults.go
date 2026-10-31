// SPDX-License-Identifier: MIT

package runtimestore

import (
	"context"
	"errors"
	"fmt"

	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// ErrWorkspaceDefaultsLookup marks a failure to read a runtime, or the base
// of a derived runtime, while composing its workspaceDefaults into a plan
// that is binding. It wraps a store error that is not a definite
// not-found answer, so a binding request maps it to the retryable
// SERVICE_UNAVAILABLE rather than binding the plan without its defaults.
//
// spec: §14.1 WorkspacePlan Schema Versioning (Runtime workspace defaults).
var ErrWorkspaceDefaultsLookup = errors.New("runtimestore: workspace defaults lookup failed")

// ComposeInto composes the workspaceDefaults block into plan. Each default
// file becomes an inlineFile source and each default setup command a
// setupCommands entry that keeps its timeoutSeconds, and both precede the
// plan's own sources and setup commands, so the §14.1 path collision rule
// settles a default and a client source that write one path.
//
// It returns (plan, false, nil) unchanged for a nil block or a block with no
// files and no setup commands. Otherwise it returns the composed plan,
// re-parsed in its stored form, with true. A file entry carrying a ref, or a
// default that is not a valid §14 source, returns an error.
//
// spec: §14.1 WorkspacePlan Schema Versioning (Runtime workspace defaults);
// §5.1 Derived Runtime Instantiation.
func (w *WorkspaceDefaults) ComposeInto(plan workspaceplan.Plan) (workspaceplan.Plan, bool, error) {
	if w == nil || (len(w.Files) == 0 && len(w.SetupCommands) == 0) {
		return plan, false, nil
	}
	sources, err := w.inlineSources()
	if err != nil {
		return plan, false, err
	}
	composed := workspaceplan.Plan{
		SchemaVersion: plan.SchemaVersion,
		Sources:       append(sources, plan.Sources...),
		SetupCommands: append(w.planSetupCommands(), plan.SetupCommands...),
	}
	if composed.SchemaVersion == 0 {
		composed.SchemaVersion = workspaceplan.SchemaVersion
	}
	raw, err := workspaceplan.Marshal(composed)
	if err != nil {
		return plan, false, fmt.Errorf("runtimestore: marshal composed workspace plan: %w", err)
	}
	parsed, _, err := workspaceplan.ParseStored(raw)
	if err != nil {
		return plan, false, fmt.Errorf("runtimestore: composed workspace plan is invalid: %w", err)
	}
	return parsed, true, nil
}

// inlineSources converts the default files to inlineFile sources in their
// declared order. A file that carries a ref has no inline content to place,
// so it is an error: registration rejects it, and this catches a row stored
// before that check.
func (w *WorkspaceDefaults) inlineSources() ([]workspaceplan.Source, error) {
	sources := make([]workspaceplan.Source, 0, len(w.Files))
	for i, f := range w.Files {
		if f.Ref != "" {
			return nil, fmt.Errorf("files[%d].ref: a workspace default file carries its content inline", i)
		}
		sources = append(sources, workspaceplan.Source{
			Type:    workspaceplan.TypeInlineFile,
			Variant: workspaceplan.InlineFile{PathField: f.Path, Content: f.Content},
			Raw: map[string]any{
				"type":    workspaceplan.TypeInlineFile,
				"path":    f.Path,
				"content": f.Content,
			},
		})
	}
	return sources, nil
}

// planSetupCommands converts the default setup commands to plan entries,
// keeping each command's timeoutSeconds.
func (w *WorkspaceDefaults) planSetupCommands() []workspaceplan.SetupCommand {
	cmds := make([]workspaceplan.SetupCommand, 0, len(w.SetupCommands))
	for _, c := range w.SetupCommands {
		cmds = append(cmds, workspaceplan.SetupCommand{Cmd: c.Cmd, TimeoutSeconds: c.TimeoutSeconds})
	}
	return cmds
}

// ComposeWorkspaceDefaults reads the effective runtime registered under
// runtimeRef (the §5.1 base-then-derived merge) and composes its
// workspaceDefaults into plan through ComposeInto.
//
// A nil store, an empty runtimeRef, or a runtime with no registry entry
// composes nothing and returns (plan, false, nil). A store error on the
// runtime or on a derived runtime's base that is not a not-found answer is
// wrapped with ErrWorkspaceDefaultsLookup. A derived runtime whose base is
// missing returns the resolution error unwrapped, because composing nothing
// would drop the derived runtime's own defaults.
//
// spec: §14.1 WorkspacePlan Schema Versioning (Runtime workspace defaults);
// §5.1 Derived Runtime Instantiation.
func ComposeWorkspaceDefaults(ctx context.Context, s Store, runtimeRef string, plan workspaceplan.Plan) (workspaceplan.Plan, bool, error) {
	if s == nil || runtimeRef == "" {
		return plan, false, nil
	}
	rt, registered, err := ResolveRegistered(ctx, s, runtimeRef)
	if err != nil || !registered {
		return plan, false, err
	}
	return rt.WorkspaceDefaults.ComposeInto(plan)
}

// ResolveRegistered reads the effective runtime registered under name (the
// §5.1 base-then-derived merge) for a caller that admits a request naming a
// runtime with no registry entry. It returns registered=false and a nil error
// when name has no entry. A store error that is not a not-found answer is
// wrapped with ErrWorkspaceDefaultsLookup, and a derived runtime whose base is
// missing returns the resolution error unwrapped. name is read once, so a
// runtime deleted during the call reads as unregistered.
//
// spec: §14.1 WorkspacePlan Schema Versioning (Runtime workspace defaults,
// rule 4); §5.1 Derived Runtime Instantiation.
func ResolveRegistered(ctx context.Context, s Store, name string) (Runtime, bool, error) {
	rt, err := s.Get(ctx, name)
	if errors.Is(err, ErrNotFound) {
		return Runtime{}, false, nil
	}
	if err != nil {
		return Runtime{}, false, fmt.Errorf("%w: runtime %q: %w", ErrWorkspaceDefaultsLookup, name, err)
	}
	merged, err := mergeBase(ctx, s, name, rt)
	if errors.Is(err, ErrNotFound) {
		return Runtime{}, false, err
	}
	if err != nil {
		return Runtime{}, false, fmt.Errorf("%w: runtime %q: %w", ErrWorkspaceDefaultsLookup, name, err)
	}
	return merged, true, nil
}
