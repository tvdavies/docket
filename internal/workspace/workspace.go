// Package workspace handles discovery and configuration of a docket workspace —
// a `.docket/` directory holding all tasks, projects, config, and attachments.
//
// Discovery walks up from the current directory like git finds `.git`. The
// environment variable DOCKET_HOME overrides discovery with an explicit path.
package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// DirName is the workspace directory name placed inside a project root.
const DirName = ".docket"

// ErrNotFound is returned when no workspace can be discovered.
var ErrNotFound = errors.New("no docket workspace found (run `docket init`)")

// Workspace is an opened docket store rooted at a `.docket/` directory.
type Workspace struct {
	// Root is the absolute path to the `.docket` directory itself.
	Root   string
	Config *Config
}

// Open discovers and loads a workspace, returning ErrNotFound if none exists.
func Open() (*Workspace, error) {
	root, err := discover()
	if err != nil {
		return nil, err
	}
	cfg, err := loadConfig(filepath.Join(root, "config.yaml"))
	if err != nil {
		return nil, err
	}
	return &Workspace{Root: root, Config: cfg}, nil
}

// discover locates the `.docket` directory, honouring DOCKET_HOME first, then
// walking up from the current working directory.
func discover() (string, error) {
	if home := os.Getenv("DOCKET_HOME"); home != "" {
		abs, err := filepath.Abs(home)
		if err != nil {
			return "", err
		}
		// DOCKET_HOME may point either at the project root or the `.docket` dir.
		if filepath.Base(abs) == DirName {
			if isDir(abs) {
				return abs, nil
			}
		}
		candidate := filepath.Join(abs, DirName)
		if isDir(candidate) {
			return candidate, nil
		}
		return "", fmt.Errorf("%w (DOCKET_HOME=%s)", ErrNotFound, home)
	}

	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(dir, DirName)
		if isDir(candidate) {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ErrNotFound
		}
		dir = parent
	}
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// Path joins parts onto the workspace root.
func (w *Workspace) Path(parts ...string) string {
	return filepath.Join(append([]string{w.Root}, parts...)...)
}

// TasksDir is the directory holding per-task folders.
func (w *Workspace) TasksDir() string { return w.Path("tasks") }

// ProjectsDir is the directory holding project files.
func (w *Workspace) ProjectsDir() string { return w.Path("projects") }

// TaskDir returns the directory for a single task id.
func (w *Workspace) TaskDir(id string) string { return filepath.Join(w.TasksDir(), id) }

// EventsFile is the append-only event log path.
func (w *Workspace) EventsFile() string { return w.Path("events.jsonl") }

// CursorsDir holds per-actor inbox cursors.
func (w *Workspace) CursorsDir() string { return w.Path(".cursors") }

// loadConfig reads and parses config.yaml.
func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	cfg.applyDefaults()
	return &cfg, nil
}
