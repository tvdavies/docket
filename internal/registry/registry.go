// Package registry manages the machine-local list of Docket workspaces served
// by the user service. It stores paths and display names only; each workspace's
// files remain its source of truth.
package registry

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/tvdavies/docket/internal/store"
	"github.com/tvdavies/docket/internal/workspace"
	"gopkg.in/yaml.v3"
)

const DefaultListen = "127.0.0.1:7463"

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Config is machine-local service configuration.
type Config struct {
	Listen     string           `yaml:"listen,omitempty"`
	Workspaces []WorkspaceEntry `yaml:"workspaces,omitempty"`
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
			if existing.Name == entry.Name && samePath(existing.Path, entry.Path) {
				return nil
			}
			if existing.Name == entry.Name {
				return fmt.Errorf("workspace name %q is already registered at %s", entry.Name, existing.Path)
			}
			if samePath(existing.Path, entry.Path) {
				return fmt.Errorf("workspace %s is already registered as %q", entry.Path, existing.Name)
			}
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
			if entry.Name == name {
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

// Validate checks uniqueness and route-safe workspace names.
func (c *Config) Validate() error {
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

func samePath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}
