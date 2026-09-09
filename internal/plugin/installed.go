package plugin

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Installed describes one instance-wide plugin registration. It mirrors the
// plugin portion of the machine registry without importing registry, avoiding a
// workspace <-> registry package cycle during composition.
type Installed struct {
	Name    string         `yaml:"name" json:"name"`
	Path    string         `yaml:"path" json:"path"`
	Source  Source         `yaml:"source" json:"source"`
	Version string         `yaml:"version" json:"version"`
	Config  map[string]any `yaml:"config,omitempty" json:"config,omitempty"`
}

type Source struct {
	Type   string `yaml:"type" json:"type"`
	URL    string `yaml:"url,omitempty" json:"url,omitempty"`
	Ref    string `yaml:"ref,omitempty" json:"ref,omitempty"`
	Commit string `yaml:"commit,omitempty" json:"commit,omitempty"`
}

// RegistryPath resolves the shared machine registry path.
func RegistryPath() (string, error) {
	if path := os.Getenv("DOCKET_CONFIG"); path != "" {
		return filepath.Abs(path)
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(dir, "docket", "config.yaml"), nil
}

// LoadInstalled reads only the lenient plugin portion of the machine registry.
// Unknown registry keys intentionally remain accepted for old/new compatibility.
func LoadInstalled() ([]Installed, error) {
	path, err := RegistryPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read service config: %w", err)
	}
	var value struct {
		Plugins []Installed `yaml:"plugins,omitempty"`
	}
	if err := yaml.Unmarshal(data, &value); err != nil {
		return nil, fmt.Errorf("parse service config: %w", err)
	}
	names := map[string]bool{}
	paths := map[string]bool{}
	for _, entry := range value.Plugins {
		if !namePattern.MatchString(entry.Name) || !filepath.IsAbs(entry.Path) {
			return nil, fmt.Errorf("invalid installed plugin entry %q", entry.Name)
		}
		clean := filepath.Clean(entry.Path)
		if names[entry.Name] || paths[clean] {
			return nil, fmt.Errorf("duplicate installed plugin entry %q", entry.Name)
		}
		names[entry.Name] = true
		paths[clean] = true
	}
	return value.Plugins, nil
}

func InstalledByName(entries []Installed) map[string]Installed {
	result := make(map[string]Installed, len(entries))
	for _, entry := range entries {
		result[entry.Name] = entry
	}
	return result
}
