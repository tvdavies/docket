package bundle_test

import (
	"bytes"
	"os"
	"slices"
	"strings"
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
	if result.CreatedAt == "" || result.UpdatedAt == "" {
		t.Fatalf("bundle timestamps = created %q updated %q", result.CreatedAt, result.UpdatedAt)
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
	if result.ActiveSessions == nil || len(result.ActiveSessions) != 0 {
		t.Fatalf("deprecated active sessions field = %#v", result.ActiveSessions)
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

// A workspace written by a release that published widget records keeps that
// evidence readable: nothing produces widgets now, and no plugin needs to be
// enabled for the recorded summary and references to appear.
func TestBundleKeepsHistoricalWidgetEvidence(t *testing.T) {
	ws, err := workspace.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created, err := task.Create(ws, task.CreateOptions{Title: "Old run"})
	if err != nil {
		t.Fatal(err)
	}
	record := func(phase string, revision int, status, summary string) map[string]any {
		fallback := map[string]any{"label": "Session", "status_label": status, "priority": "history",
			"references": []any{map[string]any{"kind": "log", "url": "https://example.com/run/1", "title": "Run log"}}}
		if summary != "" {
			fallback["summary"] = summary
		}
		return map[string]any{"version": 1, "widget_type": "dispatch/session", "instance_id": "run-1", "task_id": created.ID,
			"created_at": "2026-09-10T10:00:00Z", "revision": revision, "phase": phase, "fallback": fallback}
	}
	for _, event := range []events.Event{
		{Type: "task.widget_created", Task: created.ID, Actor: "dispatch", Data: map[string]any{"record": record("created", 1, "Running", "")}},
		{Type: "task.widget_finalised", Task: created.ID, Actor: "dispatch", Data: map[string]any{"record": record("finalised", 4, "Finished", "Merged PR #12")}},
	} {
		if err := events.Append(ws, event); err != nil {
			t.Fatal(err)
		}
	}
	before, err := os.ReadFile(ws.EventsFile())
	if err != nil {
		t.Fatal(err)
	}
	result, err := bundle.Build(ws, created.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Widgets) != 1 || result.Widgets[0].Fallback.Summary != "Merged PR #12" || result.WidgetRevision == "" {
		t.Fatalf("widgets = %#v", result.Widgets)
	}
	var widgetActivity []bundle.ActivityView
	for _, activity := range result.Activity {
		if activity.Kind == "widget" {
			widgetActivity = append(widgetActivity, activity)
		}
		if activity.Type == "task.widget_created" || activity.Type == "task.widget_finalised" {
			t.Fatalf("raw widget event duplicated in activity: %#v", activity)
		}
	}
	if len(widgetActivity) != 1 || !strings.Contains(widgetActivity[0].Body, "Merged PR #12") || !strings.Contains(widgetActivity[0].Body, "https://example.com/run/1") {
		t.Fatalf("widget activity = %#v", widgetActivity)
	}
	after, _ := os.ReadFile(ws.EventsFile())
	if !bytes.Equal(before, after) {
		t.Fatal("reading history changed event bytes")
	}
}
