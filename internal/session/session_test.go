package session

import (
	"sync"
	"testing"

	"github.com/tvdavies/docket/internal/task"
	"github.com/tvdavies/docket/internal/workspace"
)

func TestAttachMovesSessionBetweenTasksAndDetachClearsIt(t *testing.T) {
	ws, err := workspace.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, _ := task.Create(ws, task.CreateOptions{Title: "First"})
	second, _ := task.Create(ws, task.CreateOptions{Title: "Second"})

	if _, err := Attach(ws, first.ID, "run-1", "planner"); err != nil {
		t.Fatal(err)
	}
	if _, err := Attach(ws, second.ID, "run-1", "implementer"); err != nil {
		t.Fatal(err)
	}
	if action := latestSessionAction(t, first, "run-1"); action != "detach" {
		t.Fatalf("first task latest action = %q, want detach", action)
	}
	if action := latestSessionAction(t, second, "run-1"); action != "attach" {
		t.Fatalf("second task latest action = %q, want attach", action)
	}
	if Current(ws, "run-1") != second.ID {
		t.Fatalf("current = %q", Current(ws, "run-1"))
	}

	if detached, err := Detach(ws, "run-1", "implementer"); err != nil || detached != second.ID {
		t.Fatalf("detach = %q, %v", detached, err)
	}
	if action := latestSessionAction(t, second, "run-1"); action != "detach" {
		t.Fatalf("second task latest action after detach = %q", action)
	}
	if Current(ws, "run-1") != "" {
		t.Fatalf("current after detach = %q", Current(ws, "run-1"))
	}
}

func TestConcurrentAttachLeavesOneCurrentTask(t *testing.T) {
	ws, err := workspace.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, _ := task.Create(ws, task.CreateOptions{Title: "First"})
	second, _ := task.Create(ws, task.CreateOptions{Title: "Second"})

	var wait sync.WaitGroup
	errors := make(chan error, 2)
	for _, target := range []string{first.ID, second.ID} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := Attach(ws, target, "shared-run", "worker")
			errors <- err
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}

	current := Current(ws, "shared-run")
	if current != first.ID && current != second.ID {
		t.Fatalf("current = %q, want one of %q or %q", current, first.ID, second.ID)
	}
	currentTask, otherTask := first, second
	if current == second.ID {
		currentTask, otherTask = second, first
	}
	if action := latestSessionAction(t, currentTask, "shared-run"); action != "attach" {
		t.Fatalf("current task latest action = %q, want attach", action)
	}
	if action := latestSessionAction(t, otherTask, "shared-run"); action != "detach" {
		t.Fatalf("other task latest action = %q, want detach", action)
	}
}

func latestSessionAction(t *testing.T, value *task.Task, sessionID string) string {
	t.Helper()
	entries, err := Entries(value)
	if err != nil {
		t.Fatal(err)
	}
	latest := ""
	for _, entry := range entries {
		if entry.Session == sessionID {
			latest = entry.Action
		}
	}
	return latest
}
