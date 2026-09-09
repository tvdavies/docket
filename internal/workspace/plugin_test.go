package workspace

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/tvdavies/docket/internal/plugin"
	"gopkg.in/yaml.v3"
)

func installFixturePlugin(t *testing.T, manifest string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, plugin.ManifestFile), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "registry.yaml")
	t.Setenv("DOCKET_CONFIG", configPath)
	registry := "plugins:\n  - name: example\n    path: " + root + "\n    source: {type: local}\n    version: 1.0.0\n"
	if err := os.WriteFile(configPath, []byte(registry), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestOpenComposesPluginStatusesHandlersAndConfig(t *testing.T) {
	root := installFixturePlugin(t, `
name: example
version: 1.0.0
handlers:
  notify: {on: [task.moved], run: hooks/notify}
statuses:
  - {name: merge, after: review}
config:
  workspace:
    endpoint: {type: string, default: local}
`)
	project := t.TempDir()
	ws, err := Init(project)
	if err != nil {
		t.Fatal(err)
	}
	declared := ws.Config
	declared.Statuses = []string{"todo", "review", "done"}
	declared.Plugins = PluginUses{Order: []string{"example"}, Values: map[string]PluginUse{"example": {}}}
	data, _ := yaml.Marshal(declared)
	if err := os.WriteFile(filepath.Join(ws.Root, "config.yaml"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	opened, err := OpenRoot(project)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(opened.Config.Statuses, []string{"todo", "review", "merge", "done"}) {
		t.Fatalf("statuses = %v", opened.Config.Statuses)
	}
	handler := opened.Config.Handlers["example/notify"]
	if handler.PluginName != "example" || handler.PluginRoot != root || handler.PluginConfig["endpoint"] != "local" {
		t.Fatalf("handler metadata = %#v", handler)
	}
	if len(opened.Plugins) != 1 || opened.Plugins[0].Manifest.Name != "example" {
		t.Fatalf("loaded plugins = %#v", opened.Plugins)
	}
}

func TestOpenFailsClosedForMissingPluginAndAnchor(t *testing.T) {
	project := t.TempDir()
	ws, err := Init(project)
	if err != nil {
		t.Fatal(err)
	}
	ws.Config.Plugins = PluginUses{Order: []string{"missing"}, Values: map[string]PluginUse{"missing": {}}}
	data, _ := yaml.Marshal(ws.Config)
	if err := os.WriteFile(filepath.Join(ws.Root, "config.yaml"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKET_CONFIG", filepath.Join(t.TempDir(), "empty.yaml"))
	if _, err := OpenRoot(project); err == nil {
		t.Fatal("expected missing plugin to fail closed")
	}
}

func TestCompositionRejectsDuplicateStatusContributions(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "registry.yaml")
	t.Setenv("DOCKET_CONFIG", configPath)
	var entries string
	for _, name := range []string{"first", "second"} {
		root := t.TempDir()
		manifest := "name: " + name + "\nversion: 1.0.0\nstatuses: [{name: merge, after: review}]\n"
		if err := os.WriteFile(filepath.Join(root, plugin.ManifestFile), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
		entries += "  - name: " + name + "\n    path: " + root + "\n    source: {type: local}\n    version: 1.0.0\n"
	}
	if err := os.WriteFile(configPath, []byte("plugins:\n"+entries), 0o644); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	ws, err := Init(project)
	if err != nil {
		t.Fatal(err)
	}
	ws.Config.Statuses = []string{"todo", "review", "done"}
	ws.Config.Plugins = PluginUses{
		Order:  []string{"first", "second"},
		Values: map[string]PluginUse{"first": {}, "second": {}},
	}
	data, _ := yaml.Marshal(ws.Config)
	if err := os.WriteFile(filepath.Join(ws.Root, "config.yaml"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenRoot(project); err == nil {
		t.Fatal("expected duplicate status contribution to fail")
	}
}

func TestPluginDeclarationOrderRoundTrips(t *testing.T) {
	var config Config
	if err := yaml.Unmarshal([]byte("plugins:\n  second: {}\n  first: {}\n"), &config); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(config.Plugins.Order, []string{"second", "first"}) {
		t.Fatalf("order = %v", config.Plugins.Order)
	}
	data, err := yaml.Marshal(&config)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("empty marshalled config")
	}
	var roundTrip Config
	if err := yaml.Unmarshal(data, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(roundTrip.Plugins.Order, config.Plugins.Order) {
		t.Fatalf("round-trip order = %v", roundTrip.Plugins.Order)
	}
}
