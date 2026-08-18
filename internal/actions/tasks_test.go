package actions_test

import (
	"testing"

	"github.com/tvdavies/docket/internal/actions"
	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/task"
	"github.com/tvdavies/docket/internal/workspace"
)

func TestTaskActionsMutateAndEmitEvents(t *testing.T) {
	ws, err := workspace.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created, err := task.Create(ws, task.CreateOptions{Title: "Action task", Status: "ready", Labels: []string{"old"}})
	if err != nil {
		t.Fatal(err)
	}
	operations := actions.Tasks{Workspace: ws, Actor: "handler:test", Session: "hook-session"}

	moved, from, err := operations.Move(created.ID, "done")
	if err != nil {
		t.Fatal(err)
	}
	if from != "ready" || moved.Status != "done" {
		t.Fatalf("move = %s -> %s", from, moved.Status)
	}
	assigned, err := operations.Assign(created.ID, "researcher")
	if err != nil {
		t.Fatal(err)
	}
	if assigned.Assignee != "researcher" {
		t.Fatalf("assignee = %q", assigned.Assignee)
	}
	labelled, err := operations.Label(created.ID, []string{"new"}, []string{"old"})
	if err != nil {
		t.Fatal(err)
	}
	if len(labelled.Labels) != 1 || labelled.Labels[0] != "new" {
		t.Fatalf("labels = %v", labelled.Labels)
	}
	comment, err := operations.Comment(created.ID, "from Lua")
	if err != nil {
		t.Fatal(err)
	}
	if comment.Author != "handler:test" || comment.Session != "hook-session" {
		t.Fatalf("comment identity = %#v", comment)
	}

	log, err := events.All(ws)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{events.TaskMoved, events.TaskAssigned, events.TaskLabeled, events.TaskCommented}
	if len(log) != len(want) {
		t.Fatalf("events = %v", log)
	}
	for index, eventType := range want {
		if log[index].Type != eventType || log[index].Actor != "handler:test" {
			t.Fatalf("event %d = %#v", index, log[index])
		}
	}
}

func TestTaskActionUsesCustomAppender(t *testing.T) {
	ws, err := workspace.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created, err := task.Create(ws, task.CreateOptions{Title: "Custom append", Status: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	var emitted []events.Event
	operations := actions.Tasks{
		Workspace: ws, Actor: "test",
		Append: func(event events.Event) error {
			emitted = append(emitted, event)
			return nil
		},
	}
	if _, _, err := operations.Move(created.ID, "done"); err != nil {
		t.Fatal(err)
	}
	if len(emitted) != 1 || emitted[0].Type != events.TaskMoved {
		t.Fatalf("custom events = %#v", emitted)
	}
	if got := events.Count(ws); got != 0 {
		t.Fatalf("default event appender also ran; count = %d", got)
	}
}
