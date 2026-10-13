package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatchSeesNestedAndNewTaskDirectories(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "tasks", "TASK-0001", "comments")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	refreshed := make(chan struct{}, 16)
	stop := watch(root, func() { refreshed <- struct{}{} })
	defer stop()

	expect := func(what string) {
		t.Helper()
		select {
		case <-refreshed:
		case <-time.After(3 * time.Second):
			t.Fatalf("no refresh after %s", what)
		}
		// Let the debounce settle so the next step starts clean.
		time.Sleep(250 * time.Millisecond)
		for len(refreshed) > 0 {
			<-refreshed
		}
	}
	write := func(path string) {
		t.Helper()
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write(filepath.Join(existing, "0001.md"))
	expect("a comment in an existing task")

	write(filepath.Join(root, "tasks", "TASK-0001", "task.md"))
	expect("a task file edit")

	fresh := filepath.Join(root, "tasks", "TASK-0002")
	if err := os.MkdirAll(filepath.Join(fresh, "attachments"), 0o755); err != nil {
		t.Fatal(err)
	}
	expect("a new task directory")
	write(filepath.Join(fresh, "attachments", "manifest.yaml"))
	expect("an attachment in a new task")
}

func TestWatchStopsCleanly(t *testing.T) {
	root := t.TempDir()
	stop := watch(root, func() {})
	stop()
}
