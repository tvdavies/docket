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

func TestTaduHomeOverride(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TADU_HOME", root)
	// cwd is elsewhere, but TADU_HOME points at the project root.
	other := t.TempDir()
	t.Chdir(other)
	ws, err := Open()
	if err != nil {
		t.Fatalf("open via TADU_HOME: %v", err)
	}
	if ws.Root != filepath.Join(root, DirName) {
		t.Fatalf("wrong root via TADU_HOME: %s", ws.Root)
	}
}
