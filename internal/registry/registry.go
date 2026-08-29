// Package registry manages machine-local Docket workspaces, installed plugins,
// and service settings. Each workspace's task files remain its source of truth.
package registry

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/tvdavies/docket/internal/store"
	"github.com/tvdavies/docket/internal/workspace"
	"gopkg.in/yaml.v3"
)

const DefaultListen = "127.0.0.1:7463"

// DefaultPruneAfter is how long a registered workspace directory must be
// continuously missing before the service unregisters it.
const DefaultPruneAfter = time.Hour

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Config is machine-local service configuration.
type Config struct {
	Listen     string           `yaml:"listen,omitempty"`
	PruneAfter string           `yaml:"prune_after,omitempty"`
	Workspaces []WorkspaceEntry `yaml:"workspaces,omitempty"`
	Plugins    []PluginEntry    `yaml:"plugins,omitempty"`
}

// PluginEntry identifies one trusted plugin installed for this Docket instance.
type PluginEntry struct {
	Name    string         `yaml:"name" json:"name"`
	Path    string         `yaml:"path" json:"path"`
	Source  PluginSource   `yaml:"source" json:"source"`
	Version string         `yaml:"version" json:"version"`
	Config  map[string]any `yaml:"config,omitempty" json:"config,omitempty"`
}

// PluginSource records whether an entry is linked to a local checkout or owned
// by Docket as a managed git clone.
type PluginSource struct {
	Type   string `yaml:"type" json:"type"`
	URL    string `yaml:"url,omitempty" json:"url,omitempty"`
	Ref    string `yaml:"ref,omitempty" json:"ref,omitempty"`
	Commit string `yaml:"commit,omitempty" json:"commit,omitempty"`
}

// PruneGrace resolves prune_after: empty uses DefaultPruneAfter and "never"
// disables pruning by returning zero.
func (c *Config) PruneGrace() (time.Duration, error) {
	value := strings.ToLower(strings.TrimSpace(c.PruneAfter))
	if value == "" {
		return DefaultPruneAfter, nil
	}
	if value == "never" {
		return 0, nil
	}
	grace, err := time.ParseDuration(value)
	if err != nil || grace <= 0 {
		return 0, fmt.Errorf("prune_after must be \"never\" or a positive duration such as 1h, not %q", c.PruneAfter)
	}
	return grace, nil
}

// WorkspaceEntry identifies one file-backed workspace. Path is the absolute
// project directory containing .docket/, not the .docket directory itself.
type WorkspaceEntry struct {
	Name string `yaml:"name" json:"name"`
	Path string `yaml:"path" json:"path"`
}

// ConfigPath resolves the registry path. DOCKET_CONFIG is primarily useful for
// tests and dedicated service installations; otherwise the platform user config
// directory is used (typically ~/.config/docket/config.yaml on Linux).
func ConfigPath() (string, error) {
	if path := os.Getenv("DOCKET_CONFIG"); path != "" {
		return filepath.Abs(path)
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(dir, "docket", "config.yaml"), nil
}

// Load reads the registry. A missing file is an empty registry with defaults.
func Load() (*Config, error) {
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}
	return loadPath(path)
}

func loadPath(path string) (*Config, error) {
	config := &Config{Listen: DefaultListen}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return config, nil
		}
		return nil, fmt.Errorf("read service config: %w", err)
	}
	if err := yaml.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("parse service config: %w", err)
	}
	if config.Listen == "" {
		config.Listen = DefaultListen
	}
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("validate service config: %w", err)
	}
	return config, nil
}

// Add registers a workspace. Re-adding the same name/path pair is idempotent.
func Add(path, name string) (WorkspaceEntry, error) {
	explicitName := name != ""
	entry, err := resolveEntry(path, name)
	if err != nil {
		return WorkspaceEntry{}, err
	}
	configPath, err := ConfigPath()
	if err != nil {
		return WorkspaceEntry{}, err
	}
	err = store.WithLock(configPath+".lock", func() error {
		config, err := loadPath(configPath)
		if err != nil {
			return err
		}
		for _, existing := range config.Workspaces {
			if samePath(existing.Path, entry.Path) {
				if explicitName && existing.Name != entry.Name {
					return fmt.Errorf("workspace %s is already registered as %q", entry.Path, existing.Name)
				}
				entry = existing
				return nil
			}
		}
		if explicitName {
			for _, existing := range config.Workspaces {
				if existing.Name == entry.Name {
					return fmt.Errorf("workspace name %q is already registered at %s", entry.Name, existing.Path)
				}
			}
		} else {
			entry.Name = availableName(entry.Name, config.Workspaces)
		}
		config.Workspaces = append(config.Workspaces, entry)
		sort.Slice(config.Workspaces, func(i, j int) bool {
			return config.Workspaces[i].Name < config.Workspaces[j].Name
		})
		return writePath(configPath, config)
	})
	return entry, err
}

// Remove unregisters a workspace by name and returns whether it existed.
func Remove(name string) (bool, error) {
	return remove(name, nil)
}

// RemoveMatching unregisters name only while it still points at path, so a
// concurrent re-registration under the same name is never removed by mistake.
func RemoveMatching(name, path string) (bool, error) {
	return remove(name, &path)
}

func remove(name string, path *string) (bool, error) {
	configPath, err := ConfigPath()
	if err != nil {
		return false, err
	}
	removed := false
	err = store.WithLock(configPath+".lock", func() error {
		config, err := loadPath(configPath)
		if err != nil {
			return err
		}
		kept := config.Workspaces[:0]
		for _, entry := range config.Workspaces {
			if entry.Name == name && (path == nil || samePath(entry.Path, *path)) {
				removed = true
				continue
			}
			kept = append(kept, entry)
		}
		if !removed {
			return nil
		}
		config.Workspaces = kept
		return writePath(configPath, config)
	})
	return removed, err
}

// Update applies one locked mutation to the machine registry and writes it
// atomically after validation.
func Update(mutate func(*Config) error) error {
	configPath, err := ConfigPath()
	if err != nil {
		return err
	}
	return store.WithLock(configPath+".lock", func() error {
		config, err := loadPath(configPath)
		if err != nil {
			return err
		}
		if err := mutate(config); err != nil {
			return err
		}
		if err := config.Validate(); err != nil {
			return err
		}
		return writePath(configPath, config)
	})
}

// PruneMissing unregisters workspaces whose project directories have been
// continuously missing for the configured grace period and returns the
// surviving entries. The caller owns the missing-since tracking map; only a
// confirmed not-exist counts as missing, so transient stat failures never
// unregister anything.
func PruneMissing(config *Config, missing map[string]time.Time, now time.Time, logf func(string, ...any)) []WorkspaceEntry {
	grace, err := config.PruneGrace()
	if err != nil || grace <= 0 {
		clear(missing)
		return config.Workspaces
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}

	active := make(map[string]bool, len(config.Workspaces))
	kept := make([]WorkspaceEntry, 0, len(config.Workspaces))
	for _, entry := range config.Workspaces {
		key := entry.Name + "\x00" + filepath.Clean(entry.Path)
		active[key] = true
		if _, err := os.Stat(entry.Path); err == nil || !os.IsNotExist(err) {
			delete(missing, key)
			kept = append(kept, entry)
			continue
		}
		since, tracked := missing[key]
		if !tracked {
			missing[key] = now
			kept = append(kept, entry)
			continue
		}
		if now.Sub(since) < grace {
			kept = append(kept, entry)
			continue
		}
		removed, err := RemoveMatching(entry.Name, entry.Path)
		if err != nil {
			logf("docket: prune workspace %q: %v", entry.Name, err)
			kept = append(kept, entry)
			continue
		}
		delete(missing, key)
		if removed {
			logf("docket: unregistered workspace %q; %s has been missing since %s (task files untouched)",
				entry.Name, entry.Path, since.UTC().Format(time.RFC3339))
		}
	}
	for key := range missing {
		if !active[key] {
			delete(missing, key)
		}
	}
	return kept
}

// Validate checks uniqueness and route-safe workspace/plugin names.
func (c *Config) Validate() error {
	if _, err := c.PruneGrace(); err != nil {
		return err
	}
	names := map[string]bool{}
	paths := map[string]string{}
	for _, entry := range c.Workspaces {
		if !namePattern.MatchString(entry.Name) {
			return fmt.Errorf("workspace %q: name must contain only lowercase letters, numbers, hyphens, and underscores", entry.Name)
		}
		if !filepath.IsAbs(entry.Path) {
			return fmt.Errorf("workspace %q: path must be absolute", entry.Name)
		}
		if names[entry.Name] {
			return fmt.Errorf("workspace name %q is duplicated", entry.Name)
		}
		names[entry.Name] = true
		clean := filepath.Clean(entry.Path)
		if other, exists := paths[clean]; exists {
			return fmt.Errorf("workspace path %s is registered as both %q and %q", clean, other, entry.Name)
		}
		paths[clean] = entry.Name
	}
	pluginNames := map[string]bool{}
	pluginPaths := map[string]string{}
	for _, entry := range c.Plugins {
		if !namePattern.MatchString(entry.Name) {
			return fmt.Errorf("plugin %q: name must contain only lowercase letters, numbers, hyphens, and underscores", entry.Name)
		}
		if !filepath.IsAbs(entry.Path) {
			return fmt.Errorf("plugin %q: path must be absolute", entry.Name)
		}
		if entry.Source.Type != "local" && entry.Source.Type != "git" {
			return fmt.Errorf("plugin %q: source.type must be local or git", entry.Name)
		}
		if pluginNames[entry.Name] {
			return fmt.Errorf("plugin name %q is duplicated", entry.Name)
		}
		pluginNames[entry.Name] = true
		clean := filepath.Clean(entry.Path)
		if other, exists := pluginPaths[clean]; exists {
			return fmt.Errorf("plugin path %s is registered as both %q and %q", clean, other, entry.Name)
		}
		pluginPaths[clean] = entry.Name
	}
	return nil
}

func resolveEntry(path, name string) (WorkspaceEntry, error) {
	ws, err := workspace.OpenAt(path)
	if err != nil {
		return WorkspaceEntry{}, err
	}
	projectRoot := filepath.Dir(ws.Root)
	if resolved, err := filepath.EvalSymlinks(projectRoot); err == nil {
		projectRoot = resolved
	}
	projectRoot, err = filepath.Abs(projectRoot)
	if err != nil {
		return WorkspaceEntry{}, err
	}
	if name == "" {
		name = slug(filepath.Base(projectRoot))
	}
	if !namePattern.MatchString(name) {
		return WorkspaceEntry{}, fmt.Errorf("workspace name %q must contain only lowercase letters, numbers, hyphens, and underscores", name)
	}
	return WorkspaceEntry{Name: name, Path: projectRoot}, nil
}

func writePath(path string, config *Config) error {
	data, err := yaml.Marshal(config)
	if err != nil {
		return err
	}
	header := []byte("# Docket user service configuration. Workspace task data remains in each .docket/.\n")
	return store.WriteAtomic(path, append(header, data...), 0o644)
}

func slug(value string) string {
	value = strings.ToLower(value)
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		isAlpha := r >= 'a' && r <= 'z'
		isDigit := r >= '0' && r <= '9'
		if isAlpha || isDigit || r == '_' {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func availableName(base string, entries []WorkspaceEntry) string {
	used := map[string]bool{}
	for _, entry := range entries {
		used[entry.Name] = true
	}
	if !used[base] {
		return base
	}
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s-%d", base, suffix)
		if !used[candidate] {
			return candidate
		}
	}
}

func samePath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}
