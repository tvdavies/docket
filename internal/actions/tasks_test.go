package actions_test

import (
	"errors"
	"testing"

	"github.com/tvdavies/docket/internal/actions"
	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/task"
	"github.com/tvdavies/docket/internal/workspace"
)

func TestTaskActionsCreateAndEdit(t *testing.T) {
	ws, err := workspace.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	operations := actions.Tasks{Workspace: ws, Actor: "web"}
	created, err := operations.Create(task.CreateOptions{Title: "Created through actions", Status: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	title := "Edited title"
	description := "Edited description\n"
	edited, err := operations.Edit(created.ID, actions.EditOptions{Title: &title, Description: &description})
	if err != nil {
		t.Fatal(err)
	}
	if edited.Title != title || edited.Description != "Edited description" {
		t.Fatalf("edited task = %#v", edited)
	}
	assignee := "reviewer"
	if _, err := operations.Edit(created.ID, actions.EditOptions{Assignee: &assignee}); err != nil {
		t.Fatal(err)
	}
	log, err := events.All(ws)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{events.TaskCreated, events.TaskUpdated, events.TaskAssigned}
	if len(log) != len(want) {
		t.Fatalf("events = %#v", log)
	}
	for index, eventType := range want {
		if log[index].Type != eventType {
			t.Fatalf("event %d = %#v", index, log[index])
		}
	}
}

func TestCreateAndCommentRollBackWhenEventsFail(t *testing.T) {
	ws, err := workspace.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	commitFailure := errors.New("event disk unavailable")
	failing := actions.Tasks{
		Workspace: ws, Actor: "web",
		Append: func(events.Event) error { return commitFailure },
	}
	if _, err := failing.Create(task.CreateOptions{Title: "Must roll back"}); !errors.Is(err, commitFailure) {
		t.Fatalf("create error = %v", err)
	}
	all, err := task.All(ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Fatalf("failed create left tasks: %#v", all)
	}

	created, err := task.Create(ws, task.CreateOptions{Title: "Existing"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := failing.Comment(created.ID, "Must roll back"); !errors.Is(err, commitFailure) {
		t.Fatalf("comment error = %v", err)
	}
	comments, err := created.Comments()
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 0 {
		t.Fatalf("failed comment left files: %#v", comments)
	}
}

func TestTaskPatchIsAtomicAndRollsBackWhenEventsFail(t *testing.T) {
	ws, err := workspace.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created, err := task.Create(ws, task.CreateOptions{Title: "Original", Status: "ready", Labels: []string{"old"}})
	if err != nil {
		t.Fatal(err)
	}
	commitFailure := errors.New("event disk unavailable")
	operations := actions.Tasks{
		Workspace: ws, Actor: "web",
		Append: func(events.Event) error { return commitFailure },
	}
	title, status, labels := "Changed", "done", []string{"new"}
	if _, err := operations.Patch(created.ID, actions.PatchOptions{Title: &title, Status: &status, Labels: &labels}); !errors.Is(err, commitFailure) {
		t.Fatalf("patch error = %v", err)
	}
	reloaded, err := task.Load(ws, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Title != "Original" || reloaded.Status != "ready" || len(reloaded.Labels) != 1 || reloaded.Labels[0] != "old" {
		t.Fatalf("failed patch was not rolled back: %#v", reloaded)
	}
	if got := events.Count(ws); got != 0 {
		t.Fatalf("failed patch appended %d events", got)
	}
}

func TestTaskPatchWritesOneDossierAndOrderedEventGroup(t *testing.T) {
	ws, err := workspace.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created, err := task.Create(ws, task.CreateOptions{Title: "Original", Status: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	operations := actions.Tasks{Workspace: ws, Actor: "web"}
	title, assignee, status, labels := "Changed", "reviewer", "done", []string{"new"}
	updated, err := operations.Patch(created.ID, actions.PatchOptions{
		Title: &title, Assignee: &assignee, Status: &status, Labels: &labels,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != title || updated.Assignee != assignee || updated.Status != status {
		t.Fatalf("patch result = %#v", updated)
	}
	log, err := events.All(ws)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{events.TaskAssigned, events.TaskLabeled, events.TaskMoved}
	if len(log) != len(want) {
		t.Fatalf("events = %#v", log)
	}
	for index, eventType := range want {
		if log[index].Type != eventType {
			t.Fatalf("event %d = %#v", index, log[index])
		}
	}
}

func TestWaitAndReferenceLifecycle(t *testing.T) {
	ws, err := workspace.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created, err := task.Create(ws, task.CreateOptions{Title: "Plan durable waits"})
	if err != nil {
		t.Fatal(err)
	}
	operations := actions.Tasks{Workspace: ws, Actor: "planner"}
	waiting, err := operations.SetWait(created.ID, actions.SetWaitOptions{
		Kind: "plan_feedback", Reason: "Awaiting plan review", Reference: "https://example.com/plan",
	})
	if err != nil {
		t.Fatal(err)
	}
	if waiting.Wait == nil || waiting.Wait.ID == "" || waiting.Wait.Kind != "plan_feedback" {
		t.Fatalf("wait = %#v", waiting.Wait)
	}
	if _, err := operations.SetWait(created.ID, actions.SetWaitOptions{Kind: "other", Reason: "Must fail"}); err == nil {
		t.Fatal("second active wait unexpectedly succeeded")
	}
	if _, err := operations.ResolveWait(created.ID, actions.ResolveWaitOptions{WaitID: "wait-stale"}); err == nil {
		t.Fatal("stale wait resolver unexpectedly succeeded")
	}

	withReference, reference, err := operations.AddReference(created.ID, "plan", "https://example.com/plan", "Plan v1")
	if err != nil {
		t.Fatal(err)
	}
	if len(withReference.References) != 1 || reference.ID == "" {
		t.Fatalf("reference = %#v task = %#v", reference, withReference)
	}
	resumed, err := operations.ResolveWait(created.ID, actions.ResolveWaitOptions{WaitID: waiting.Wait.ID, Result: "approved"})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Wait != nil {
		t.Fatalf("resolved wait remains: %#v", resumed.Wait)
	}
	withoutReference, removed, err := operations.RemoveReference(created.ID, reference.ID)
	if err != nil {
		t.Fatal(err)
	}
	if removed.ID != reference.ID || len(withoutReference.References) != 0 {
		t.Fatalf("removed = %#v task = %#v", removed, withoutReference)
	}

	log, err := events.All(ws)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{events.TaskWaiting, events.TaskReferenceAdded, events.TaskResumed, events.TaskReferenceRemoved}
	if len(log) != len(want) {
		t.Fatalf("events = %#v", log)
	}
	for index, eventType := range want {
		if log[index].Type != eventType {
			t.Fatalf("event %d = %#v", index, log[index])
		}
	}
}

func TestWaitAndReferenceRejectUnsafeURLSchemes(t *testing.T) {
	ws, err := workspace.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created, err := task.Create(ws, task.CreateOptions{Title: "Safe links only"})
	if err != nil {
		t.Fatal(err)
	}
	operations := actions.Tasks{Workspace: ws, Actor: "planner"}
	for _, unsafe := range []string{"javascript:alert(1)", "data:text/html,<script>alert(1)</script>", "mailto:test@example.com", "file:relative"} {
		t.Run(unsafe, func(t *testing.T) {
			if _, _, err := operations.AddReference(created.ID, "plan", unsafe, ""); err == nil {
				t.Fatalf("unsafe reference %q was accepted", unsafe)
			}
			if _, err := operations.SetWait(created.ID, actions.SetWaitOptions{Kind: "feedback", Reason: "Review", Reference: unsafe}); err == nil {
				t.Fatalf("unsafe wait reference %q was accepted", unsafe)
			}
		})
	}
}

func TestWaitMutationRollsBackWhenEventFails(t *testing.T) {
	ws, err := workspace.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created, err := task.Create(ws, task.CreateOptions{Title: "Remain runnable"})
	if err != nil {
		t.Fatal(err)
	}
	commitFailure := errors.New("event disk unavailable")
	operations := actions.Tasks{
		Workspace: ws, Actor: "planner",
		Append: func(events.Event) error { return commitFailure },
	}
	if _, err := operations.SetWait(created.ID, actions.SetWaitOptions{Kind: "ci", Reason: "Awaiting CI"}); !errors.Is(err, commitFailure) {
		t.Fatalf("set wait error = %v", err)
	}
	reloaded, err := task.Load(ws, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Wait != nil {
		t.Fatalf("failed wait mutation persisted: %#v", reloaded.Wait)
	}
}

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
