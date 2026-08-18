package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitAndDiscoverUpward(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root); err != nil {
		t.Fatal(err)
	}
	// Discovery should find the workspace from a nested subdirectory.
	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(nested)

	ws, err := Open()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if ws.Root != filepath.Join(root, DirName) {
		t.Fatalf("discovered wrong root: %s", ws.Root)
	}
	if !ws.Config.HasStatus("backlog") {
		t.Fatal("default config not loaded")
	}
}

func TestOpenAtProjectDocketAndDescendant(t *testing.T) {
	root := t.TempDir()
	ws, err := Init(root)
	if err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "src", "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{root, ws.Root, nested} {
		opened, err := OpenAt(path)
		if err != nil {
			t.Fatalf("OpenAt(%s): %v", path, err)
		}
		if opened.Root != ws.Root {
			t.Fatalf("OpenAt(%s) root = %s, want %s", path, opened.Root, ws.Root)
		}
	}
}

func TestOpenRootNeverFallsBackToParentWorkspace(t *testing.T) {
	parent := t.TempDir()
	if _, err := Init(parent); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(parent, "child")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenRoot(child); err == nil {
		t.Fatal("OpenRoot silently fell back to parent workspace")
	}
}

func TestInitTwiceIsIdempotent(t *testing.T) {
	root := t.TempDir()
	first, err := Init(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Init(root)
	if err != nil {
		t.Fatalf("second init failed: %v", err)
	}
	if second.Root != first.Root {
		t.Fatalf("second init opened %s, want %s", second.Root, first.Root)
	}
}

func TestConfigLoadsHandlers(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, DirName, "config.yaml")
	file, err := os.OpenFile(configPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("handlers:\n  notify:\n    on: [task.moved]\n    run: hooks/notify\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	ws, err := Open()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	handler, ok := ws.Config.Handlers["notify"]
	if !ok || handler.Run != "hooks/notify" || !handler.Matches("task.moved") {
		t.Fatalf("handler not loaded: %#v", handler)
	}
}

func TestConfigLoadsLuaHandlerAndMatch(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, DirName, "config.yaml")
	file, err := os.OpenFile(configPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("handlers:\n  notify:\n    on: [task.moved]\n    match:\n      data.to: done\n    lua: hooks/notify.lua\n    delivery: service\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	ws, err := OpenRoot(root)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	handler := ws.Config.Handlers["notify"]
	if handler.Lua != "hooks/notify.lua" || handler.Match["data.to"] != "done" || handler.Delivery != "service" {
		t.Fatalf("Lua handler not loaded: %#v", handler)
	}
}

func TestConfigRequiresExactlyOneHandlerRuntime(t *testing.T) {
	for name, handler := range map[string]HandlerConfig{
		"neither": {On: []string{"*"}},
		"both":    {On: []string{"*"}, Run: "hooks/noop", Lua: "hooks/noop.lua"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Handlers = map[string]HandlerConfig{"notify": handler}
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected invalid runtime selection to fail validation")
			}
		})
	}
}

func TestConfigRejectsInvalidMatchPath(t *testing.T) {
	for _, path := range []string{"data..to", "date.to", "task.id"} {
		t.Run(path, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Handlers = map[string]HandlerConfig{
				"notify": {On: []string{"*"}, Lua: "hooks/noop.lua", Match: map[string]any{path: "done"}},
			}
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected invalid match path to fail validation")
			}
		})
	}
}

func TestConfigRejectsUnsafeIDPrefixes(t *testing.T) {
	for name, mutate := range map[string]func(*Config){
		"task path":    func(config *Config) { config.Settings.IDPrefix = "../TASK" },
		"project path": func(config *Config) { config.Settings.ProjectPrefix = "PROJ/ECT" },
		"padding":      func(config *Config) { config.Settings.IDPadding = -1 },
	} {
		t.Run(name, func(t *testing.T) {
			config := DefaultConfig()
			mutate(config)
			if err := config.Validate(); err == nil {
				t.Fatal("expected unsafe ID settings to fail validation")
			}
		})
	}
}

func TestConfigRejectsUnsafeHandlerName(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Handlers = map[string]HandlerConfig{
		"../../outside": {On: []string{"*"}, Run: "hooks/noop"},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected unsafe handler name to fail validation")
	}
}

func TestConfigRejectsCaseCollidingHandlerNames(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Handlers = map[string]HandlerConfig{
		"Notify": {On: []string{"*"}, Run: "hooks/noop"},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected uppercase handler name to fail validation")
	}
}

func TestConfigRejectsUnknownHandlerDelivery(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Handlers = map[string]HandlerConfig{
		"notify": {On: []string{"*"}, Run: "hooks/noop", Delivery: "eventually-maybe"},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected unknown handler delivery to fail validation")
	}
}

func TestDocketHomeOverride(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKET_HOME", root)
	// cwd is elsewhere, but DOCKET_HOME points at the project root.
	other := t.TempDir()
	t.Chdir(other)
	ws, err := Open()
	if err != nil {
		t.Fatalf("open via DOCKET_HOME: %v", err)
	}
	if ws.Root != filepath.Join(root, DirName) {
		t.Fatalf("wrong root via DOCKET_HOME: %s", ws.Root)
	}
}
