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

func TestInitTwiceFails(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(root); err == nil {
		t.Fatal("expected second init to fail")
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
