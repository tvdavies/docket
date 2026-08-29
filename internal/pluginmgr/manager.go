// Package pluginmgr implements instance installation and workspace enablement of
// trusted Docket plugins.
package pluginmgr

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tvdavies/docket/internal/handlers"
	"github.com/tvdavies/docket/internal/plugin"
	"github.com/tvdavies/docket/internal/registry"
	"github.com/tvdavies/docket/internal/workspace"
)

func Add(spec, overrideName, engineVersion string) (registry.PluginEntry, error) {
	if info, err := os.Stat(spec); err == nil && info.IsDir() {
		return addLocal(spec, overrideName, engineVersion)
	}
	return addGit(spec, overrideName, engineVersion)
}

func addLocal(path, overrideName, engineVersion string) (registry.PluginEntry, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return registry.PluginEntry{}, err
	}
	manifest, err := plugin.Load(absolute, engineVersion)
	if err != nil {
		return registry.PluginEntry{}, err
	}
	if overrideName != "" && overrideName != manifest.Name {
		return registry.PluginEntry{}, fmt.Errorf("--name %q does not match manifest name %q", overrideName, manifest.Name)
	}
	entry := registry.PluginEntry{
		Name: manifest.Name, Path: absolute, Version: manifest.Version,
		Source: registry.PluginSource{Type: "local"},
	}
	if err := register(entry); err != nil {
		return registry.PluginEntry{}, err
	}
	return entry, nil
}

func addGit(spec, overrideName, engineVersion string) (registry.PluginEntry, error) {
	remote, requestedRef, err := parseGitSpec(spec)
	if err != nil {
		return registry.PluginEntry{}, err
	}
	managed, err := managedRoot()
	if err != nil {
		return registry.PluginEntry{}, err
	}
	if err := os.MkdirAll(managed, 0o755); err != nil {
		return registry.PluginEntry{}, err
	}
	staging, err := os.MkdirTemp(managed, ".staging-")
	if err != nil {
		return registry.PluginEntry{}, err
	}
	_ = os.Remove(staging)
	defer os.RemoveAll(staging)
	resolvedRef, commit, err := cloneCandidate(remote, requestedRef, staging)
	if err != nil {
		return registry.PluginEntry{}, err
	}
	manifest, err := plugin.Load(staging, engineVersion)
	if err != nil {
		return registry.PluginEntry{}, err
	}
	if overrideName != "" && overrideName != manifest.Name {
		return registry.PluginEntry{}, fmt.Errorf("--name %q does not match manifest name %q", overrideName, manifest.Name)
	}
	final := filepath.Join(managed, manifest.Name)
	config, err := registry.Load()
	if err != nil {
		return registry.PluginEntry{}, err
	}
	for _, existing := range config.Plugins {
		if existing.Name == manifest.Name {
			if existing.Source.Type == "git" && existing.Source.URL == remote {
				return existing, nil
			}
			return registry.PluginEntry{}, fmt.Errorf("plugin %q is already installed from another source", manifest.Name)
		}
	}
	if _, err := os.Stat(final); err == nil {
		return registry.PluginEntry{}, fmt.Errorf("managed plugin path already exists: %s", final)
	}
	if err := os.Rename(staging, final); err != nil {
		return registry.PluginEntry{}, err
	}
	entry := registry.PluginEntry{
		Name: manifest.Name, Path: final, Version: manifest.Version,
		Source: registry.PluginSource{Type: "git", URL: remote, Ref: resolvedRef, Commit: commit},
	}
	if err := register(entry); err != nil {
		_ = os.RemoveAll(final)
		return registry.PluginEntry{}, err
	}
	return entry, nil
}

func register(entry registry.PluginEntry) error {
	return registry.Update(func(config *registry.Config) error {
		for _, existing := range config.Plugins {
			if existing.Name == entry.Name {
				if filepath.Clean(existing.Path) == filepath.Clean(entry.Path) {
					return nil
				}
				return fmt.Errorf("plugin %q is already installed at %s", entry.Name, existing.Path)
			}
			if filepath.Clean(existing.Path) == filepath.Clean(entry.Path) {
				return fmt.Errorf("plugin path %s is already registered as %q", entry.Path, existing.Name)
			}
		}
		config.Plugins = append(config.Plugins, entry)
		sort.Slice(config.Plugins, func(i, j int) bool { return config.Plugins[i].Name < config.Plugins[j].Name })
		return nil
	})
}

func Remove(name string) (bool, error) {
	var removed *registry.PluginEntry
	err := registry.Update(func(config *registry.Config) error {
		kept := config.Plugins[:0]
		for index := range config.Plugins {
			entry := config.Plugins[index]
			if entry.Name == name {
				copy := entry
				removed = &copy
				continue
			}
			kept = append(kept, entry)
		}
		config.Plugins = kept
		return nil
	})
	if err != nil || removed == nil {
		return removed != nil, err
	}
	if removed.Source.Type == "git" {
		managed, err := managedRoot()
		if err != nil {
			return true, err
		}
		if !pathInside(managed, removed.Path) {
			return true, fmt.Errorf("refusing to remove managed plugin outside %s: %s", managed, removed.Path)
		}
		if err := os.RemoveAll(removed.Path); err != nil {
			return true, err
		}
	}
	return true, nil
}

func Update(name, engineVersion string) ([]registry.PluginEntry, error) {
	config, err := registry.Load()
	if err != nil {
		return nil, err
	}
	var targets []registry.PluginEntry
	for _, entry := range config.Plugins {
		if name == "" || entry.Name == name {
			targets = append(targets, entry)
		}
	}
	if name != "" && len(targets) == 0 {
		return nil, fmt.Errorf("plugin %q is not installed", name)
	}
	updated := make([]registry.PluginEntry, 0, len(targets))
	for _, entry := range targets {
		if entry.Source.Type == "local" {
			updated = append(updated, entry)
			continue
		}
		candidate, err := updateOne(config, entry, engineVersion)
		if err != nil {
			return nil, err
		}
		updated = append(updated, candidate)
	}
	return updated, nil
}

func updateOne(config *registry.Config, current registry.PluginEntry, engineVersion string) (registry.PluginEntry, error) {
	managed, err := managedRoot()
	if err != nil {
		return registry.PluginEntry{}, err
	}
	if !pathInside(managed, current.Path) {
		return registry.PluginEntry{}, fmt.Errorf("refusing to update managed plugin outside %s: %s", managed, current.Path)
	}
	staging, err := os.MkdirTemp(managed, ".update-")
	if err != nil {
		return registry.PluginEntry{}, err
	}
	_ = os.Remove(staging)
	defer os.RemoveAll(staging)
	requested := ""
	if current.Source.Ref != "" && !looksLikeVersion(current.Source.Ref) {
		requested = current.Source.Ref
	}
	resolvedRef, commit, err := cloneCandidate(current.Source.URL, requested, staging)
	if err != nil {
		return registry.PluginEntry{}, err
	}
	manifest, err := plugin.Load(staging, engineVersion)
	if err != nil {
		return registry.PluginEntry{}, err
	}
	if manifest.Name != current.Name {
		return registry.PluginEntry{}, fmt.Errorf("updated manifest changed plugin name from %q to %q", current.Name, manifest.Name)
	}
	for _, workspaceEntry := range config.Workspaces {
		declared, err := workspace.LoadDeclaredRoot(workspaceEntry.Path)
		if err != nil {
			return registry.PluginEntry{}, fmt.Errorf("validate workspace %q: %w", workspaceEntry.Name, err)
		}
		if _, enabled := declared.Plugins.Values[current.Name]; !enabled {
			continue
		}
		if err := workspace.ValidatePluginCandidate(declared, manifest, current.Config); err != nil {
			return registry.PluginEntry{}, fmt.Errorf("candidate invalid for workspace %q: %w", workspaceEntry.Name, err)
		}
	}
	if current.Source.Commit == commit {
		return current, nil
	}
	shortCommit := commit
	if len(shortCommit) > 12 {
		shortCommit = shortCommit[:12]
	}
	candidatePath := filepath.Join(managed, current.Name+"-"+shortCommit)
	if !pathInside(managed, candidatePath) {
		return registry.PluginEntry{}, fmt.Errorf("invalid candidate path %s", candidatePath)
	}
	if _, err := os.Stat(candidatePath); err == nil {
		if err := os.RemoveAll(candidatePath); err != nil {
			return registry.PluginEntry{}, err
		}
	}
	if err := os.Rename(staging, candidatePath); err != nil {
		return registry.PluginEntry{}, err
	}
	candidate := current
	candidate.Path = candidatePath
	candidate.Version = manifest.Version
	candidate.Source.Ref = resolvedRef
	candidate.Source.Commit = commit
	if err := registry.Update(func(latest *registry.Config) error {
		for index := range latest.Plugins {
			if latest.Plugins[index].Name == current.Name {
				if latest.Plugins[index].Path != current.Path || latest.Plugins[index].Source.Commit != current.Source.Commit {
					return fmt.Errorf("plugin %q changed during update", current.Name)
				}
				latest.Plugins[index] = candidate
				return nil
			}
		}
		return fmt.Errorf("plugin %q was removed during update", current.Name)
	}); err != nil {
		_ = os.RemoveAll(candidatePath)
		return registry.PluginEntry{}, err
	}
	// The registry flip is atomic and points at a fully validated candidate;
	// only after it succeeds is the inactive prior clone removed.
	_ = os.RemoveAll(current.Path)
	return candidate, nil
}

// Enable composes a plugin into one workspace and prepares its cursors before
// atomically publishing the config change.
func Enable(workspacePath, name string, values map[string]any, adopt, fromStart bool, engineVersion string) error {
	if adopt && fromStart {
		return errors.New("--adopt-cursors and --from-start are mutually exclusive")
	}
	config, err := registry.Load()
	if err != nil {
		return err
	}
	var entry *registry.PluginEntry
	for index := range config.Plugins {
		if config.Plugins[index].Name == name {
			entry = &config.Plugins[index]
			break
		}
	}
	if entry == nil {
		return fmt.Errorf("plugin %q is not installed", name)
	}
	manifest, err := plugin.Load(entry.Path, engineVersion)
	if err != nil {
		return err
	}
	ws, err := workspace.OpenAt(workspacePath)
	if err != nil {
		return err
	}
	return workspace.MutateDeclaredConfig(ws.Root, func(declared *workspace.Config) error {
		use := declared.Plugins.Values[name]
		if use.Config == nil {
			use.Config = map[string]any{}
		}
		for key, value := range values {
			use.Config[key] = value
		}
		// The reference dispatch plugin's required server_root naturally defaults
		// to its linked checkout, while generic plugins still require explicit keys.
		if field, ok := manifest.Config.Workspace["server_root"]; ok && field.Required {
			if _, exists := use.Config["server_root"]; !exists {
				use.Config["server_root"] = manifest.Root
			}
		}
		if err := workspace.EnablePlugin(declared, manifest, use, adopt); err != nil {
			return err
		}
		if err := workspace.ValidatePluginCandidate(declared, manifest, entry.Config); err != nil {
			return err
		}
		for handlerName := range manifest.Handlers {
			identity := name + "/" + handlerName
			switch {
			case adopt:
				if err := handlers.AdoptCursor(ws, handlerName, identity); err != nil {
					return err
				}
			case fromStart:
				if err := handlers.ResetCursor(ws, identity); err != nil {
					return err
				}
			default:
				if err := handlers.SeedCursorAtEnd(ws, identity); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func Disable(workspacePath, name string) error {
	ws, err := workspace.OpenAt(workspacePath)
	if err != nil {
		return err
	}
	return workspace.MutateDeclaredConfig(ws.Root, func(declared *workspace.Config) error {
		if !workspace.DisablePlugin(declared, name) {
			return fmt.Errorf("plugin %q is not enabled", name)
		}
		return nil
	})
}

func managedRoot() (string, error) {
	if root := os.Getenv("DOCKET_PLUGIN_DIR"); root != "" {
		return filepath.Abs(root)
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		data = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(data, "docket", "plugins"), nil
}

func parseGitSpec(spec string) (string, string, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", "", errors.New("plugin source is required")
	}
	ref := ""
	if index := strings.LastIndex(spec, "@"); index > strings.LastIndex(spec, "/") {
		ref = spec[index+1:]
		spec = spec[:index]
	}
	remote := spec
	if !strings.Contains(spec, "://") && !strings.HasPrefix(spec, "git@") {
		parts := strings.Split(spec, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return "", "", fmt.Errorf("git plugin source must be owner/repo[@ref] or a git URL")
		}
		remote = "https://github.com/" + spec
	}
	return remote, ref, nil
}

func cloneCandidate(remote, requestedRef, destination string) (string, string, error) {
	if err := runGit("clone", "--quiet", remote, destination); err != nil {
		return "", "", err
	}
	resolved := requestedRef
	if resolved == "" {
		output, err := gitOutput(destination, "tag", "--list", "--sort=-version:refname")
		if err != nil {
			return "", "", err
		}
		for _, tag := range strings.Fields(output) {
			if looksLikeVersion(tag) {
				resolved = tag
				break
			}
		}
	}
	if resolved != "" {
		if err := runGitAt(destination, "checkout", "--quiet", resolved); err != nil {
			return "", "", err
		}
	} else {
		branch, err := gitOutput(destination, "symbolic-ref", "--short", "HEAD")
		if err == nil {
			resolved = strings.TrimSpace(branch)
		}
	}
	commit, err := gitOutput(destination, "rev-parse", "HEAD")
	if err != nil {
		return "", "", err
	}
	return resolved, strings.TrimSpace(commit), nil
}

func runGit(args ...string) error {
	command := exec.Command("git", args...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func runGitAt(root string, args ...string) error {
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("git -C %s %s: %w: %s", root, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func gitOutput(root string, args ...string) (string, error) {
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git -C %s %s: %w: %s", root, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func looksLikeVersion(value string) bool {
	value = strings.TrimPrefix(value, "v")
	parts := strings.SplitN(value, "-", 2)
	numbers := strings.Split(parts[0], ".")
	if len(numbers) != 3 {
		return false
	}
	for _, number := range numbers {
		if number == "" || strings.Trim(number, "0123456789") != "" {
			return false
		}
	}
	return true
}

func pathInside(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
