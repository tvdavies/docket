package registry

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tvdavies/docket/internal/workspace"
)

// EnvWorkspace selects a workspace by registered name or path, like -w.
const EnvWorkspace = "DOCKET_WORKSPACE"

// Source records why a workspace was selected.
type Source string

// Resolution sources, highest precedence first.
const (
	SourceFlag    Source = "flag"
	SourceEnv     Source = "env"
	SourceCwd     Source = "cwd"
	SourceContext Source = "context"
)

// Resolution is the workspace in effect for one command.
type Resolution struct {
	// Root is the absolute .docket directory.
	Root string `json:"root"`
	// Project is the directory containing Root.
	Project string `json:"path"`
	// Name is the registered name, or empty for an unregistered workspace.
	Name   string `json:"name"`
	Source Source `json:"source"`
	// Selector is the -w value, the environment variable's value or the saved
	// context name that selected the workspace; empty for cwd discovery.
	Selector string `json:"selector,omitempty"`
	// Variable names the environment variable for SourceEnv.
	Variable string `json:"variable,omitempty"`
}

// Resolve selects the workspace for a command in precedence order:
//
//  1. selector (the -w/--workspace flag), a registered name or a path
//  2. DOCKET_HOME (set by handler and plugin subprocesses to pin a store),
//     then DOCKET_WORKSPACE, a registered name or a path
//  3. the nearest .docket found by walking up from the working directory
//  4. the saved context from "docket workspace use"
//
// Only the root is located; the caller opens and validates the config, so
// recovery commands can still reach a workspace whose config is broken.
func Resolve(selector string) (Resolution, error) {
	if selector != "" {
		return resolveSelector(selector, SourceFlag, "")
	}
	if root, ok, err := workspace.HomeRoot(); ok || err != nil {
		if err != nil {
			return Resolution{}, err
		}
		resolution := newResolution(root, SourceEnv)
		resolution.Variable = "DOCKET_HOME"
		resolution.Selector = os.Getenv("DOCKET_HOME")
		return resolution, nil
	}
	if value := os.Getenv(EnvWorkspace); value != "" {
		return resolveSelector(value, SourceEnv, EnvWorkspace)
	}
	root, err := workspace.DiscoverFromCwd()
	if err == nil {
		return newResolution(root, SourceCwd), nil
	}
	if !errors.Is(err, workspace.ErrNotFound) {
		return Resolution{}, err
	}
	config, loadErr := Load()
	if loadErr != nil {
		return Resolution{}, fmt.Errorf("%w; also could not read the saved workspace context: %v", notFound(), loadErr)
	}
	if config.CurrentWorkspace == "" {
		return Resolution{}, notFound()
	}
	entry, ok := config.Lookup(config.CurrentWorkspace)
	if !ok {
		return Resolution{}, fmt.Errorf("saved workspace context %q is no longer registered (run `docket workspace use NAME` or `docket workspace use --clear`)", config.CurrentWorkspace)
	}
	root, err = rootOf(entry)
	if err != nil {
		return Resolution{}, fmt.Errorf("saved workspace context %q: %w", entry.Name, err)
	}
	resolution := newResolution(root, SourceContext)
	resolution.Name = entry.Name
	resolution.Selector = entry.Name
	return resolution, nil
}

func notFound() error {
	return fmt.Errorf("%w; outside a workspace, select one with `-w NAME` or set a default with `docket workspace use NAME`", workspace.ErrNotFound)
}

// resolveSelector treats value as a registered name first and otherwise as a
// path to a project directory, its .docket directory or any descendant.
func resolveSelector(value string, source Source, variable string) (Resolution, error) {
	label := "-w " + value
	if variable != "" {
		label = variable + "=" + value
	}
	config, loadErr := Load()
	if loadErr == nil {
		if entry, ok := config.Lookup(value); ok {
			root, err := rootOf(entry)
			if err != nil {
				return Resolution{}, fmt.Errorf("%s: %w", label, err)
			}
			resolution := newResolution(root, source)
			resolution.Name, resolution.Selector, resolution.Variable = entry.Name, value, variable
			return resolution, nil
		}
	}
	path := expandHome(value)
	if !looksLikePath(value) {
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			if loadErr != nil {
				return Resolution{}, fmt.Errorf("%s: read workspace registry: %w", label, loadErr)
			}
			return Resolution{}, fmt.Errorf("unknown workspace %q: not a registered name or a workspace path (see `docket workspace list`)", value)
		}
	}
	root, err := workspace.FindRootAt(path)
	if err != nil {
		return Resolution{}, fmt.Errorf("%s: %w", label, err)
	}
	resolution := newResolution(root, source)
	resolution.Selector, resolution.Variable = value, variable
	if loadErr == nil {
		if entry, ok := config.LookupPath(resolution.Project); ok {
			resolution.Name = entry.Name
		}
	}
	return resolution, nil
}

func newResolution(root string, source Source) Resolution {
	return Resolution{Root: root, Project: filepath.Dir(root), Source: source}
}

// rootOf returns the .docket directory of a registered workspace without
// walking into parent directories.
func rootOf(entry WorkspaceEntry) (string, error) {
	root := filepath.Join(entry.Path, workspace.DirName)
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("%w (path=%s)", workspace.ErrNotFound, entry.Path)
	}
	return root, nil
}

func looksLikePath(value string) bool {
	return filepath.IsAbs(value) || strings.ContainsRune(value, filepath.Separator) ||
		strings.ContainsRune(value, '/') || value == "." || value == ".." || strings.HasPrefix(value, "~")
}

func expandHome(value string) string {
	if value != "~" && !strings.HasPrefix(value, "~/") {
		return value
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return value
	}
	return filepath.Join(home, strings.TrimPrefix(value, "~"))
}

// Lookup returns the workspace registered under name.
func (c *Config) Lookup(name string) (WorkspaceEntry, bool) {
	for _, entry := range c.Workspaces {
		if entry.Name == name {
			return entry, true
		}
	}
	return WorkspaceEntry{}, false
}

// LookupPath returns the workspace registered for a project directory,
// comparing symlink-resolved paths because registration resolves symlinks.
func (c *Config) LookupPath(project string) (WorkspaceEntry, bool) {
	want := canonical(project)
	for _, entry := range c.Workspaces {
		if canonical(entry.Path) == want {
			return entry, true
		}
	}
	return WorkspaceEntry{}, false
}

func canonical(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return filepath.Clean(path)
}

// Use saves the workspace context selected by a registered name or by the path
// of a registered workspace, and returns the selected entry.
func Use(selector string) (WorkspaceEntry, error) {
	var selected WorkspaceEntry
	err := Update(func(config *Config) error {
		entry, ok := config.Lookup(selector)
		if !ok {
			if root, err := workspace.FindRootAt(expandHome(selector)); err == nil {
				entry, ok = config.LookupPath(filepath.Dir(root))
			}
		}
		if !ok {
			return fmt.Errorf("unknown workspace %q: not a registered name or the path of a registered workspace (see `docket workspace list`; register with `docket workspace add PATH`)", selector)
		}
		config.CurrentWorkspace = entry.Name
		selected = entry
		return nil
	})
	return selected, err
}

// ClearCurrent removes the saved workspace context and returns the name it
// held, or "" when none was set.
func ClearCurrent() (string, error) {
	previous := ""
	err := Update(func(config *Config) error {
		previous = config.CurrentWorkspace
		config.CurrentWorkspace = ""
		return nil
	})
	return previous, err
}
