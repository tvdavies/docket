package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tvdavies/docket/internal/plugin"
	"github.com/tvdavies/docket/internal/registry"
	"github.com/tvdavies/docket/internal/workspace"
)

// tryPluginPassthrough resolves git-style plugin commands before Cobra parses
// plugin-owned flags. Builtins always win.
func tryPluginPassthrough(args []string) (bool, error) {
	selector, args := leadingWorkspaceFlag(args)
	if len(args) == 0 || strings.HasPrefix(args[0], "-") || builtinCommand(args[0]) {
		return false, nil
	}
	name := args[0]
	entries, err := plugin.LoadInstalled()
	if err != nil {
		return false, err
	}
	// The plugin is pinned to the selected workspace through DOCKET_HOME. An
	// explicit -w that cannot be resolved is an error once the command is
	// known to be a plugin; otherwise the plugin runs without a workspace.
	var ws *workspace.Workspace
	resolution, resolveErr := registry.Resolve(selector)
	if resolveErr == nil {
		ws, resolveErr = workspace.OpenRoot(resolution.Root)
	}
	if resolveErr != nil && selector != "" {
		if _, lookErr := exec.LookPath("docket-" + name); lookErr == nil || installedPlugin(entries, name) {
			return true, resolveErr
		}
	}
	baseEnvironment := os.Environ()
	if ws != nil {
		baseEnvironment = withEnvironment(baseEnvironment, map[string]string{"DOCKET_HOME": ws.Root})
	}
	for _, entry := range entries {
		if entry.Name != name {
			continue
		}
		manifest, err := plugin.Load(entry.Path, Version)
		if err != nil {
			return true, err
		}
		if manifest.CLI == nil {
			break
		}
		program := filepath.Join(manifest.Root, manifest.CLI.Run)
		info, err := os.Stat(program)
		if err != nil {
			return true, fmt.Errorf("plugin %q CLI: %w", name, err)
		}
		if info.Mode()&0o111 == 0 {
			return true, fmt.Errorf("plugin %q CLI is not executable: %s", name, program)
		}
		environment := map[string]string{
			"DOCKET_PLUGIN": name, "DOCKET_PLUGIN_ROOT": manifest.Root, "DOCKET_PLUGIN_CONFIG": "",
		}
		if ws != nil {
			for _, loaded := range ws.Plugins {
				if loaded.Manifest.Name != name {
					continue
				}
				payload, _ := json.Marshal(map[string]any{"config": loaded.Effective.Values, "status_config": loaded.Effective.Statuses})
				environment["DOCKET_PLUGIN_CONFIG"] = string(payload)
				break
			}
		}
		return true, execPassthrough(program, append([]string{program}, args[1:]...), withEnvironment(baseEnvironment, environment))
	}
	program, err := exec.LookPath("docket-" + name)
	if err != nil {
		return false, nil
	}
	return true, execPassthrough(program, append([]string{program}, args[1:]...), baseEnvironment)
}

// leadingWorkspaceFlag removes a -w/--workspace flag written before a plugin
// command name (docket -w NAME my-plugin ...), returning its value.
func leadingWorkspaceFlag(args []string) (string, []string) {
	if len(args) == 0 {
		return "", args
	}
	switch first := args[0]; {
	case (first == "-w" || first == "--workspace") && len(args) > 1:
		return args[1], args[2:]
	case strings.HasPrefix(first, "--workspace="):
		return strings.TrimPrefix(first, "--workspace="), args[1:]
	case strings.HasPrefix(first, "-w") && len(first) > 2:
		return strings.TrimPrefix(strings.TrimPrefix(first, "-w"), "="), args[1:]
	}
	return "", args
}

func installedPlugin(entries []plugin.Installed, name string) bool {
	for _, entry := range entries {
		if entry.Name == name {
			return true
		}
	}
	return false
}

func withEnvironment(existing []string, values map[string]string) []string {
	result := make([]string, 0, len(existing)+len(values))
	for _, item := range existing {
		key, _, ok := strings.Cut(item, "=")
		if ok {
			if _, replaced := values[key]; replaced {
				continue
			}
		}
		result = append(result, item)
	}
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	return result
}
