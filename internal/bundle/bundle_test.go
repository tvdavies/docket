package bundle_test

import (
	"slices"
	"testing"
	"time"

	"github.com/tvdavies/docket/internal/actions"
	"github.com/tvdavies/docket/internal/bundle"
	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/project"
	"github.com/tvdavies/docket/internal/session"
	"github.com/tvdavies/docket/internal/task"
	"github.com/tvdavies/docket/internal/workspace"
)

func TestBundleIncludesWaitReferencesSessionsAndUnifiedActivity(t *testing.T) {
	ws, err := workspace.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	operations := actions.Tasks{Workspace: ws, Actor: "planner", Session: "run-42"}
	created, err := operations.Create(task.CreateOptions{Title: "Build a plan"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Attach(ws, created.ID, "run-42", "planner"); err != nil {
		t.Fatal(err)
	}
	if err := events.Append(ws, events.Event{
		Type: events.TaskAttached, Task: created.ID, Actor: "planner",
		Data: map[string]any{"session": "run-42"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.Comment(created.ID, "Drafted the first plan"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := operations.AddReference(created.ID, "plan", "https://example.com/plan", "Plan v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.SetWait(created.ID, actions.SetWaitOptions{Kind: "plan_feedback", Reason: "Awaiting review"}); err != nil {
		t.Fatal(err)
	}

	result, err := bundle.Build(ws, created.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.Wait == nil || result.Wait.Kind != "plan_feedback" {
		t.Fatalf("wait = %#v", result.Wait)
	}
	if len(result.References) != 1 || result.References[0].Kind != "plan" {
		t.Fatalf("references = %#v", result.References)
	}
	if len(result.Sessions) != 1 || result.Sessions[0].Session != "run-42" {
		t.Fatalf("sessions = %#v", result.Sessions)
	}
	types := make([]string, 0, len(result.Activity))
	var previous time.Time
	for _, activity := range result.Activity {
		types = append(types, activity.Type)
		at, err := time.Parse(time.RFC3339Nano, activity.At)
		if err != nil {
			t.Fatalf("activity timestamp %q: %v", activity.At, err)
		}
		if !previous.IsZero() && at.Before(previous) {
			t.Fatalf("activity timestamps are not chronological: %#v", result.Activity)
		}
		previous = at
	}
	for _, expected := range []string{events.TaskCreated, "attach", "comment", events.TaskReferenceAdded, events.TaskWaiting} {
		if !slices.Contains(types, expected) {
			t.Fatalf("activity types %v do not include %q", types, expected)
		}
	}
	if slices.Contains(types, events.TaskCommented) {
		t.Fatalf("comment event duplicated rich comment activity: %v", types)
	}
}

func TestBundleResolvesTitlesAndProject(t *testing.T) {
	ws, err := workspace.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, _ := project.Create(ws, "Website", "")
	main, _ := task.Create(ws, task.CreateOptions{Title: "Fix cache", Project: p.ID})
	dep, _ := task.Create(ws, task.CreateOptions{Title: "Auth hardening"})
	_ = task.Link(ws, main.ID, "blocks", dep.ID)
	_, _ = task.AddComment(ws, main.ID, "agent:pi", "s", "note one")
	_, _ = task.AddComment(ws, main.ID, "agent:pi", "s", "note two")

	b, err := bundle.Build(ws, main.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if b.Project == nil || b.Project.Name != "Website" {
		t.Fatalf("project not resolved: %+v", b.Project)
	}
	refs := b.Relationships["blocks"]
	if len(refs) != 1 || refs[0].Title != "Auth hardening" {
		t.Fatalf("relationship title not resolved: %+v", refs)
	}
	if len(b.Comments) != 1 || b.Comments[0].Body != "note two" {
		t.Fatalf("comment limit not applied: %+v", b.Comments)
	}
}
