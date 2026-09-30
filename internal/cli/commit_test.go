package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/task"
	"github.com/tvdavies/docket/internal/workspace"
)

func TestMoveLeavesDossierUnchangedWhenEventAppendFails(t *testing.T) {
	dir := t.TempDir()
	ws, err := workspace.Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	created, err := task.Create(ws, task.CreateOptions{Title: "Stay ready", Status: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(created.TaskFile())
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(ws.EventsFile())
	if err := os.Mkdir(ws.EventsFile(), 0o755); err != nil {
		t.Fatal(err)
	}
	_, stderr, err := runDocket(t, dir, "move", created.ID, "done")
	if err == nil || !strings.Contains(stderr, "append event") {
		t.Fatalf("move: err=%v stderr=%q", err, stderr)
	}
	after, err := os.ReadFile(created.TaskFile())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("dossier changed after failed append:\n%s\n---\n%s", before, after)
	}
}

func TestInlineHookCanMutateTheTaskThatTriggeredIt(t *testing.T) {
	dir := t.TempDir()
	ws, err := workspace.Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	created, err := task.Create(ws, task.CreateOptions{Title: "Hooked"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nexec '" + docketBinary(t) + "' label " + created.ID + " --add hooked\n"
	if err := os.WriteFile(filepath.Join(dir, "hooks", "relabel"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(filepath.Join(ws.Root, "config.yaml"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("handlers:\n  relabel: {on: [task.waiting], run: hooks/relabel}\n"); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()

	started := time.Now()
	if _, stderr, err := runDocket(t, dir, "wait", "set", created.ID, "--kind", "ci", "--reason", "Awaiting CI"); err != nil {
		t.Fatalf("wait set: %v: %s", err, stderr)
	}
	// The task lock times out after 30 seconds; a hook that had to wait for it
	// would have failed or stalled.
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("wait set took %s", elapsed)
	}
	reloaded, err := task.Load(ws, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Wait == nil || !reloaded.HasLabel("hooked") {
		t.Fatalf("task = %#v", reloaded)
	}
	log, err := events.All(ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(log) != 2 || log[0].Type != events.TaskWaiting || log[1].Type != events.TaskLabeled {
		t.Fatalf("events = %#v", log)
	}
}
