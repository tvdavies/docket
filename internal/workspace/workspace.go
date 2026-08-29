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
	"slices"

	"github.com/tvdavies/docket/internal/plugin"
	"gopkg.in/yaml.v3"
)

// DirName is the workspace directory name placed inside a project root.
const DirName = ".docket"

// ErrNotFound is returned when no workspace can be discovered.
var ErrNotFound = errors.New("no docket workspace found (run `docket init`)")

// Workspace is an opened docket store rooted at a `.docket/` directory.
type Workspace struct {
	// Root is the absolute path to the `.docket` directory itself.
	Root string
	// Config is the effective config after plugin contribution composition.
	Config *Config
	// DeclaredConfig is the workspace-owned YAML before plugin contributions.
	DeclaredConfig *Config
	// Plugins contains enabled, validated plugin metadata in declaration order.
	Plugins []LoadedPlugin
}

// LoadedPlugin is one plugin composed into this workspace.
type LoadedPlugin struct {
	Manifest  *plugin.Manifest
	Effective plugin.EffectiveConfig
}

// Open discovers and loads a workspace, returning ErrNotFound if none exists.
func Open() (*Workspace, error) {
	root, err := discover()
	if err != nil {
		return nil, err
	}
	return openRoot(root)
}

// OpenRoot loads exactly the workspace rooted at path. Path may be the project
// directory or its .docket directory, but discovery never walks into a parent.
// Services use this to pin a registration to one store.
func OpenRoot(path string) (*Workspace, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	root := abs
	if filepath.Base(root) != DirName {
		root = filepath.Join(root, DirName)
	}
	if !isDir(root) {
		return nil, fmt.Errorf("%w (path=%s)", ErrNotFound, path)
	}
	return openRoot(root)
}

// LoadDeclaredRoot reads only workspace-owned config without resolving enabled
// plugins. Plugin update validation uses this so an unrelated unavailable
// plugin does not prevent checking the candidate's enabling workspaces.
func LoadDeclaredRoot(path string) (*Config, error) {
	root, err := declaredRoot(path)
	if err != nil {
		return nil, err
	}
	return loadConfig(filepath.Join(root, "config.yaml"))
}

// OpenAt discovers and loads the workspace containing path. Path may be the
// project directory, its .docket directory, or any descendant. It does not
// consult DOCKET_HOME and is used when registering a workspace from inside it.
func OpenAt(path string) (*Workspace, error) {
	root, err := FindRootAt(path)
	if err != nil {
		return nil, err
	}
	return openRoot(root)
}

// FindRootAt locates the workspace containing path without loading or composing
// its configuration. Recovery commands use this when the active plugin config
// is itself what made the workspace unavailable.
func FindRootAt(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("open workspace at %s: %w", path, err)
	}
	if !info.IsDir() {
		abs = filepath.Dir(abs)
	}
	for {
		if filepath.Base(abs) == DirName && isDir(abs) {
			return abs, nil
		}
		candidate := filepath.Join(abs, DirName)
		if isDir(candidate) {
			return candidate, nil
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", fmt.Errorf("%w (path=%s)", ErrNotFound, path)
		}
		abs = parent
	}
}

func openRoot(root string) (*Workspace, error) {
	declared, err := loadConfig(filepath.Join(root, "config.yaml"))
	if err != nil {
		return nil, err
	}
	effective, loaded, err := composePlugins(declared)
	if err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}
	return &Workspace{Root: root, Config: effective, DeclaredConfig: declared, Plugins: loaded}, nil
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

func declaredRoot(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	root := absolute
	if filepath.Base(root) != DirName {
		root = filepath.Join(root, DirName)
	}
	if !isDir(root) {
		return "", fmt.Errorf("%w (path=%s)", ErrNotFound, path)
	}
	return root, nil
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

// HandlerStateDir holds machine-local handler cursors and locks. Its dedicated
// subdirectory prevents a handler's DOCKET_ACTOR inbox from acknowledging its
// delivery cursor while remaining covered by the existing .cursors/ gitignore
// rule in workspaces created before handlers existed.
func (w *Workspace) HandlerStateDir() string { return w.Path(".cursors", "handlers") }

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
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}
	return &cfg, nil
}

func composePlugins(declared *Config) (*Config, []LoadedPlugin, error) {
	return composePluginsWithOverrides(declared, nil)
}

func composePluginsWithOverrides(declared *Config, overrides map[string]plugin.Installed) (*Config, []LoadedPlugin, error) {
	data, err := yaml.Marshal(declared)
	if err != nil {
		return nil, nil, err
	}
	var effective Config
	if err := yaml.Unmarshal(data, &effective); err != nil {
		return nil, nil, err
	}
	effective.applyDefaults()
	if len(declared.Plugins.Order) == 0 {
		return &effective, nil, nil
	}
	installed, err := plugin.LoadInstalled()
	if err != nil {
		return nil, nil, err
	}
	byName := plugin.InstalledByName(installed)
	for name, entry := range overrides {
		byName[name] = entry
	}
	contributed := map[string]string{}
	anchorTail := map[string]string{}
	loaded := make([]LoadedPlugin, 0, len(declared.Plugins.Order))
	for _, name := range declared.Plugins.Order {
		entry, ok := byName[name]
		if !ok {
			return nil, nil, fmt.Errorf("plugin %q is enabled but not installed (run: docket plugin add <path>)", name)
		}
		manifest, err := plugin.Load(entry.Path, plugin.EngineVersion)
		if err != nil {
			return nil, nil, fmt.Errorf("plugin %q: %w", name, err)
		}
		if manifest.Name != name {
			return nil, nil, fmt.Errorf("plugin registry name %q does not match manifest name %q", name, manifest.Name)
		}
		for _, status := range manifest.Statuses {
			if other, exists := contributed[status.Name]; exists && other != name {
				return nil, nil, fmt.Errorf("plugins %q and %q both contribute status %q", other, name, status.Name)
			}
			contributed[status.Name] = name
			if !slices.Contains(effective.Statuses, status.Name) {
				anchor := status.After
				if tail := anchorTail[anchor]; tail != "" {
					anchor = tail
				}
				index := slices.Index(effective.Statuses, anchor)
				if index < 0 {
					return nil, nil, fmt.Errorf("plugin %q status %q: anchor %q is absent", name, status.Name, status.After)
				}
				effective.Statuses = slices.Insert(effective.Statuses, index+1, status.Name)
				anchorTail[status.After] = status.Name
			}
			if status.Terminal && !slices.Contains(effective.Terminal, status.Name) {
				effective.Terminal = append(effective.Terminal, status.Name)
			}
		}
		use := declared.Plugins.Values[name]
		resolved, err := manifest.ResolveConfig(entry.Config, use.Config, use.Statuses, effective.Statuses)
		if err != nil {
			return nil, nil, fmt.Errorf("plugin %q: %w", name, err)
		}
		for handlerName, handler := range manifest.Handlers {
			identity := name + "/" + handlerName
			if _, exists := effective.Handlers[identity]; exists {
				return nil, nil, fmt.Errorf("handler %q is duplicated", identity)
			}
			if effective.Handlers == nil {
				effective.Handlers = map[string]HandlerConfig{}
			}
			effective.Handlers[identity] = HandlerConfig{
				On: handler.On, Match: handler.Match, Run: handler.Run, Lua: handler.Lua, Delivery: handler.Delivery,
				PluginName: name, PluginRoot: manifest.Root, PluginConfig: resolved.Values, PluginStatusConfig: resolved.Statuses,
			}
		}
		loaded = append(loaded, LoadedPlugin{Manifest: manifest, Effective: resolved})
	}
	if err := effective.Validate(); err != nil {
		return nil, nil, err
	}
	return &effective, loaded, nil
}
