package registry_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tvdavies/docket/internal/registry"
	"github.com/tvdavies/docket/internal/workspace"
)

func setup(t *testing.T) (string, string) {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("DOCKET_CONFIG", configPath)
	project := filepath.Join(t.TempDir(), "My Project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.Init(project); err != nil {
		t.Fatal(err)
	}
	return configPath, project
}

func TestAddLoadRemove(t *testing.T) {
	configPath, project := setup(t)
	entry, err := registry.Add(project, "")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Name != "my-project" || entry.Path != project {
		t.Fatalf("unexpected entry: %#v", entry)
	}
	config, err := registry.Load()
	if err != nil {
		t.Fatal(err)
	}
	if config.Listen != registry.DefaultListen || len(config.Workspaces) != 1 {
		t.Fatalf("unexpected config: %#v", config)
	}
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("registry not written: %v", err)
	}

	removed, err := registry.Remove("my-project")
	if err != nil || !removed {
		t.Fatalf("remove = %v, %v", removed, err)
	}
	config, err = registry.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Workspaces) != 0 {
		t.Fatalf("workspace remains after remove: %#v", config.Workspaces)
	}
}

func TestAddIsIdempotentAndRejectsCollisions(t *testing.T) {
	_, project := setup(t)
	if _, err := registry.Add(project, "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Add(project, "first"); err != nil {
		t.Fatalf("idempotent add failed: %v", err)
	}
	entry, err := registry.Add(project, "")
	if err != nil || entry.Name != "first" {
		t.Fatalf("default-name re-registration = %#v, %v", entry, err)
	}
	if _, err := registry.Add(project, "second"); err == nil {
		t.Fatal("expected duplicate path to fail")
	}
}

func TestDefaultNameGetsStableSuffixOnCollision(t *testing.T) {
	_, first := setup(t)
	firstEntry, err := registry.Add(first, "")
	if err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(t.TempDir(), "My Project")
	if err := os.MkdirAll(second, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.Init(second); err != nil {
		t.Fatal(err)
	}
	secondEntry, err := registry.Add(second, "")
	if err != nil {
		t.Fatal(err)
	}
	if firstEntry.Name != "my-project" || secondEntry.Name != "my-project-2" {
		t.Fatalf("collision names = %q, %q", firstEntry.Name, secondEntry.Name)
	}
}

func TestRegistryStoresPluginEntriesAndRemainsLenient(t *testing.T) {
	configPath, _ := setup(t)
	pluginRoot := t.TempDir()
	if err := registry.Update(func(config *registry.Config) error {
		config.Plugins = append(config.Plugins, registry.PluginEntry{
			Name: "example", Path: pluginRoot, Version: "1.0.0",
			Source: registry.PluginSource{Type: "local"}, Config: map[string]any{"enabled": true},
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(configPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.WriteString("future_registry_key: accepted\n")
	_ = file.Close()
	config, err := registry.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Plugins) != 1 || config.Plugins[0].Name != "example" || config.Plugins[0].Config["enabled"] != true {
		t.Fatalf("plugins = %#v", config.Plugins)
	}
}

func TestRegistryRejectsInvalidPluginEntries(t *testing.T) {
	for name, entry := range map[string]registry.PluginEntry{
		"unsafe name":   {Name: "../bad", Path: t.TempDir(), Source: registry.PluginSource{Type: "local"}},
		"relative path": {Name: "example", Path: "relative", Source: registry.PluginSource{Type: "local"}},
		"source":        {Name: "example", Path: t.TempDir(), Source: registry.PluginSource{Type: "remote-magic"}},
	} {
		t.Run(name, func(t *testing.T) {
			config := &registry.Config{Plugins: []registry.PluginEntry{entry}}
			if err := config.Validate(); err == nil {
				t.Fatal("expected invalid plugin entry")
			}
		})
	}
}

func TestAddAcceptsDocketDirectoryAndDescendant(t *testing.T) {
	_, project := setup(t)
	nested := filepath.Join(project, "src", "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	entry, err := registry.Add(nested, "nested-test")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Path != project {
		t.Fatalf("resolved path = %s, want %s", entry.Path, project)
	}
}
