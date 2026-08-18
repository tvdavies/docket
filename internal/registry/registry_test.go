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
	if _, err := registry.Add(project, "second"); err == nil {
		t.Fatal("expected duplicate path to fail")
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
