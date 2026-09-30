package pluginmgr

import (
	"errors"
	"fmt"

	"github.com/tvdavies/docket/internal/plugin"
	"github.com/tvdavies/docket/internal/registry"
	"github.com/tvdavies/docket/internal/workspace"
)

// ConfigScope names where a plugin setting is stored. Instance values live in
// the machine registry; workspace and status values live in one workspace's
// declared config.yaml.
type ConfigScope string

const (
	ScopeInstance  ConfigScope = "instance"
	ScopeWorkspace ConfigScope = "workspace"
	ScopeStatus    ConfigScope = "status"
)

// ConfigTarget selects the settings a SetConfig call patches.
type ConfigTarget struct {
	Plugin string
	Scope  ConfigScope
	// WorkspacePath locates the workspace for workspace and status scopes.
	WorkspacePath string
	// Status names the composed status for status scope.
	Status string
}

// PluginSettings describes one installed plugin's schemas and stored values.
// Instance values are default-resolved and never include secret fields, which
// can only be supplied through the environment. Workspace and status values
// are the stored keys only, so a literal value is distinguishable from a
// default.
type PluginSettings struct {
	Name           string                       `json:"name"`
	Version        string                       `json:"version"`
	Description    string                       `json:"description,omitempty"`
	Source         registry.PluginSource        `json:"source"`
	Schemas        plugin.ConfigSchemas         `json:"schemas"`
	InstanceValues map[string]any               `json:"instance_values"`
	Workspaces     map[string]WorkspaceSettings `json:"workspace_values"`
}

// WorkspaceSettings is the stored configuration of one enabling workspace.
type WorkspaceSettings struct {
	Path     string                    `json:"path"`
	Config   map[string]any            `json:"config"`
	Statuses map[string]map[string]any `json:"statuses"`
}

// DescribeConfig reports schemas and values for every installed plugin, or
// only name when it is non-empty. Registered workspaces that cannot be read
// are skipped rather than failing the whole report.
func DescribeConfig(name string) ([]PluginSettings, error) {
	config, err := registry.Load()
	if err != nil {
		return nil, err
	}
	result := []PluginSettings{}
	found := false
	for _, entry := range config.Plugins {
		if name != "" && entry.Name != name {
			continue
		}
		found = true
		manifest, err := plugin.Load(entry.Path, plugin.EngineVersion)
		if err != nil {
			return nil, fmt.Errorf("plugin %q: %w", entry.Name, err)
		}
		resolved, err := manifest.ResolveInstanceConfig(entry.Config)
		if err != nil {
			return nil, fmt.Errorf("plugin %q: %w", entry.Name, err)
		}
		values := map[string]any{}
		for key, value := range resolved {
			if field := manifest.Config.Instance[key]; field.Secret {
				continue
			}
			values[key] = value
		}
		workspaces := map[string]WorkspaceSettings{}
		for _, workspaceEntry := range config.Workspaces {
			declared, err := workspace.LoadDeclaredRoot(workspaceEntry.Path)
			if err != nil {
				continue
			}
			use, enabled := declared.Plugins.Values[entry.Name]
			if !enabled {
				continue
			}
			settings := WorkspaceSettings{Path: workspaceEntry.Path, Config: use.Config, Statuses: use.Statuses}
			if settings.Config == nil {
				settings.Config = map[string]any{}
			}
			if settings.Statuses == nil {
				settings.Statuses = map[string]map[string]any{}
			}
			workspaces[workspaceEntry.Name] = settings
		}
		result = append(result, PluginSettings{
			Name: manifest.Name, Version: manifest.Version, Description: manifest.Description,
			Source: entry.Source, Schemas: manifest.Config, InstanceValues: values, Workspaces: workspaces,
		})
	}
	if name != "" && !found {
		return nil, fmt.Errorf("plugin %q is not installed", name)
	}
	return result, nil
}

// SetConfig merges values into the target scope. Keys not named in values are
// retained; a list or map value replaces the stored value whole. Values are
// stored literally: an empty string, zero or false is a real value, not a
// deletion. The candidate is validated against the manifest and, for
// instance scope, every enabling workspace before anything is written, and a
// rejected update leaves every file byte-for-byte unchanged.
func SetConfig(target ConfigTarget, values map[string]any) error {
	switch target.Scope {
	case ScopeInstance:
		if target.WorkspacePath != "" || target.Status != "" {
			return fmt.Errorf("instance config takes no workspace or status")
		}
		return setInstanceConfig(target.Plugin, values)
	case ScopeWorkspace:
		if target.Status != "" {
			return fmt.Errorf("workspace config takes no status; use status scope")
		}
		return setWorkspaceConfig(target.WorkspacePath, target.Plugin, "", values)
	case ScopeStatus:
		if target.Status == "" {
			return fmt.Errorf("status config requires a status name")
		}
		return setWorkspaceConfig(target.WorkspacePath, target.Plugin, target.Status, values)
	default:
		return fmt.Errorf("unknown config scope %q (use instance, workspace, or status)", target.Scope)
	}
}

// setInstanceConfig holds every registered workspace's config lock while it
// validates and flips the registry, so no enabling workspace can change
// between validation and publication. A registry workspace change during the
// attempt retries with the new set.
func setInstanceConfig(name string, values map[string]any) error {
	for attempt := 0; attempt < 3; attempt++ {
		snapshot, err := registry.Load()
		if err != nil {
			return err
		}
		err = workspace.WithDeclaredConfigLocks(workspacePaths(snapshot.Workspaces), func() error {
			return registry.Update(func(latest *registry.Config) error {
				if !sameWorkspaces(snapshot.Workspaces, latest.Workspaces) {
					return errRegistryChanged
				}
				for index := range latest.Plugins {
					entry := &latest.Plugins[index]
					if entry.Name != name {
						continue
					}
					manifest, err := plugin.Load(entry.Path, plugin.EngineVersion)
					if err != nil {
						return err
					}
					candidate := map[string]any{}
					for key, value := range entry.Config {
						candidate[key] = value
					}
					for key, value := range values {
						candidate[key] = value
					}
					if _, err := manifest.ResolveInstanceConfig(candidate); err != nil {
						return err
					}
					if err := validateEnablingWorkspaces(latest.Workspaces, name, manifest, candidate); err != nil {
						return err
					}
					entry.Config = candidate
					return nil
				}
				return fmt.Errorf("plugin %q is not installed", name)
			})
		})
		if errors.Is(err, errRegistryChanged) {
			continue
		}
		return err
	}
	return fmt.Errorf("plugin registry kept changing during config update")
}

func setWorkspaceConfig(workspacePath, name, status string, values map[string]any) error {
	if workspacePath == "" {
		workspacePath = "."
	}
	root, err := workspace.FindRootAt(workspacePath)
	if err != nil {
		return err
	}
	// MutateDeclaredConfig recomposes and validates every plugin before the
	// atomic write, so an invalid value or unknown status writes nothing.
	return workspace.MutateDeclaredConfig(root, func(declared *workspace.Config) error {
		use, enabled := declared.Plugins.Values[name]
		if !enabled {
			return fmt.Errorf("plugin %q is not enabled", name)
		}
		if status == "" {
			if use.Config == nil {
				use.Config = map[string]any{}
			}
			for key, value := range values {
				use.Config[key] = value
			}
		} else {
			if use.Statuses == nil {
				use.Statuses = map[string]map[string]any{}
			}
			current := use.Statuses[status]
			if current == nil {
				current = map[string]any{}
			}
			for key, value := range values {
				current[key] = value
			}
			use.Statuses[status] = current
		}
		declared.Plugins.Values[name] = use
		return nil
	})
}
