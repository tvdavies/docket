package events

import (
	"sync"
	"testing"

	"github.com/tvdavies/docket/internal/workspace"
)

func TestInboxMarkReadDoesNotAcknowledgeEventAppendedAfterSnapshot(t *testing.T) {
	ws, err := workspace.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := Append(ws, Event{Type: TaskAssigned, Task: "T1", Assignee: "sal"}); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	afterInboxSnapshot = func() {
		once.Do(func() {
			if err := Append(ws, Event{Type: TaskMoved, Task: "T1", Assignee: "sal"}); err != nil {
				t.Error(err)
			}
		})
	}
	t.Cleanup(func() { afterInboxSnapshot = func() {} })

	first, err := Inbox(ws, InboxOptions{Actor: "sal", MarkRead: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || first[0].Type != TaskAssigned {
		t.Fatalf("first read = %#v", first)
	}
	if got := Cursor(ws, "sal"); got != 1 {
		t.Fatalf("cursor advanced to %d, beyond the returned snapshot", got)
	}
	second, err := Inbox(ws, InboxOptions{Actor: "sal", MarkRead: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || second[0].Type != TaskMoved {
		t.Fatalf("event appended between read and acknowledgement was lost: %#v", second)
	}
}

func TestInboxMarkReadIsSerialisedPerActor(t *testing.T) {
	ws, err := workspace.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := Append(ws, Event{Type: TaskAssigned, Task: "T1", Assignee: "sal"}); err != nil {
		t.Fatal(err)
	}
	// While one reader holds the actor lock between snapshot and
	// acknowledgement, a second reader and an append run. Without
	// serialisation the second reader's larger position could be overwritten
	// by the first reader's smaller one, returning events twice.
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	afterInboxSnapshot = func() {
		once.Do(func() {
			close(entered)
			<-release
		})
	}
	t.Cleanup(func() { afterInboxSnapshot = func() {} })

	type result struct {
		events []Event
		err    error
	}
	firstDone := make(chan result, 1)
	go func() {
		evs, err := Inbox(ws, InboxOptions{Actor: "sal", MarkRead: true})
		firstDone <- result{evs, err}
	}()
	<-entered
	if err := Append(ws, Event{Type: TaskMoved, Task: "T1", Assignee: "sal"}); err != nil {
		t.Fatal(err)
	}
	secondDone := make(chan result, 1)
	go func() {
		evs, err := Inbox(ws, InboxOptions{Actor: "sal", MarkRead: true})
		secondDone <- result{evs, err}
	}()
	close(release)
	first, second := <-firstDone, <-secondDone
	if first.err != nil || second.err != nil {
		t.Fatal(first.err, second.err)
	}
	if len(first.events) != 1 || len(second.events) != 1 || second.events[0].Type != TaskMoved {
		t.Fatalf("first=%#v second=%#v", first.events, second.events)
	}
	if got := Cursor(ws, "sal"); got != 2 {
		t.Fatalf("cursor = %d, want 2", got)
	}
}
