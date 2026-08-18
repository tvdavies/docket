package events_test

import (
	"sync"
	"testing"

	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/workspace"
)

func newWorkspace(t *testing.T) *workspace.Workspace {
	t.Helper()
	ws, err := workspace.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestAppendAndReadAll(t *testing.T) {
	ws := newWorkspace(t)
	for i := 0; i < 3; i++ {
		if err := events.Append(ws, events.Event{Type: events.TaskCreated, Task: "TASK-0001"}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := events.All(ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 events, got %d", len(all))
	}
	if all[0].Seq != 1 || all[2].Seq != 3 {
		t.Fatalf("seq numbering wrong: %d..%d", all[0].Seq, all[2].Seq)
	}
}

func TestInboxCursorDrains(t *testing.T) {
	ws := newWorkspace(t)
	_ = events.Append(ws, events.Event{Type: events.TaskAssigned, Task: "T1", Assignee: "agent:pi"})
	_ = events.Append(ws, events.Event{Type: events.TaskCommented, Task: "T1", Assignee: "someone-else"})
	_ = events.Append(ws, events.Event{Type: events.TaskMoved, Task: "T1", Assignee: "agent:pi"})

	first, err := events.Inbox(ws, events.InboxOptions{Actor: "agent:pi", MarkRead: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 {
		t.Fatalf("expected 2 events for agent:pi, got %d", len(first))
	}
	// Draining again yields nothing until new events arrive.
	second, _ := events.Inbox(ws, events.InboxOptions{Actor: "agent:pi", MarkRead: true})
	if len(second) != 0 {
		t.Fatalf("expected drained inbox, got %d", len(second))
	}
	_ = events.Append(ws, events.Event{Type: events.TaskMoved, Task: "T1", Assignee: "agent:pi"})
	third, _ := events.Inbox(ws, events.InboxOptions{Actor: "agent:pi"})
	if len(third) != 1 {
		t.Fatalf("expected 1 new event, got %d", len(third))
	}
}

func TestWatchWithSetupDoesNotMissEventsAppendedDuringSetup(t *testing.T) {
	ws := newWorkspace(t)
	_ = events.Append(ws, events.Event{Type: events.TaskCreated, Task: "before"})
	done := make(chan struct{})
	var received []events.Event
	var appendOnce sync.Once
	var appendErr error
	err := events.WatchWithSetup(ws, false, done, func() error {
		appendOnce.Do(func() {
			appendErr = events.Append(ws, events.Event{Type: events.TaskCreated, Task: "during"})
		})
		return appendErr
	}, func(event events.Event) error {
		received = append(received, event)
		close(done)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(received) != 1 || received[0].Task != "during" {
		t.Fatalf("received %#v, want only event appended during setup", received)
	}
}

func TestInboxAllIgnoresAssignee(t *testing.T) {
	ws := newWorkspace(t)
	_ = events.Append(ws, events.Event{Type: events.TaskCreated, Task: "T1"})
	_ = events.Append(ws, events.Event{Type: events.TaskCreated, Task: "T2", Assignee: "x"})
	all, _ := events.Inbox(ws, events.InboxOptions{Actor: "nobody", All: true})
	if len(all) != 2 {
		t.Fatalf("expected 2 with --all, got %d", len(all))
	}
}
