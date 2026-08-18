package session

import (
	"sync"
	"testing"

	"github.com/tvdavies/docket/internal/task"
	"github.com/tvdavies/docket/internal/workspace"
)

func TestActiveEntriesTracksLatestUnmatchedAttach(t *testing.T) {
	entries := []Entry{
		{Action: "attach", Session: "finished", Actor: "planner", At: "2026-01-01T10:00:00Z"},
		{Action: "attach", Session: "active", Actor: "implementer", At: "2026-01-01T10:01:00.1Z"},
		{Action: "detach", Session: "finished", Actor: "planner", At: "2026-01-01T10:02:00Z"},
		{Action: "attach", Session: "active", Actor: "implementer-v2", At: "2026-01-01T10:03:00Z"},
		{Action: "attach", Session: "earlier", Actor: "reviewer", At: "2026-01-01T10:03:00Z"},
	}

	active := ActiveEntries(entries)
	if len(active) != 2 || active[0].Session != "active" || active[0].Actor != "implementer-v2" || active[1].Session != "earlier" {
		t.Fatalf("active entries = %#v", active)
	}
}

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
	assertActiveSessionCount(t, first, 0)
	assertActiveSessionCount(t, second, 1)
	if Current(ws, "run-1") != second.ID {
		t.Fatalf("current = %q", Current(ws, "run-1"))
	}

	if detached, err := Detach(ws, "run-1", "implementer"); err != nil || detached != second.ID {
		t.Fatalf("detach = %q, %v", detached, err)
	}
	assertActiveSessionCount(t, second, 0)
}

func TestConcurrentAttachLeavesSessionActiveOnOnlyCurrentTask(t *testing.T) {
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
			_, err := Attach(ws, target, "shared-run", "agent")
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
	firstActive := activeSessionCount(t, first)
	secondActive := activeSessionCount(t, second)
	if firstActive+secondActive != 1 {
		t.Fatalf("active counts = first:%d second:%d", firstActive, secondActive)
	}
	if (current == first.ID) != (firstActive == 1) || (current == second.ID) != (secondActive == 1) {
		t.Fatalf("current %q disagrees with active counts first:%d second:%d", current, firstActive, secondActive)
	}
}

func assertActiveSessionCount(t *testing.T, value *task.Task, expected int) {
	t.Helper()
	if count := activeSessionCount(t, value); count != expected {
		t.Fatalf("active session count for %s = %d, want %d", value.ID, count, expected)
	}
}

func activeSessionCount(t *testing.T, value *task.Task) int {
	t.Helper()
	entries, err := Entries(value)
	if err != nil {
		t.Fatal(err)
	}
	return len(ActiveEntries(entries))
}
