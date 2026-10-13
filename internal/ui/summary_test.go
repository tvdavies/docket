package ui

import (
	"testing"
	"time"

	"github.com/tvdavies/docket/internal/bundle"
	"github.com/tvdavies/docket/internal/task"
)

func TestSummarizeDescribesKnownActivityAsSentences(t *testing.T) {
	for _, test := range []struct {
		item bundle.ActivityView
		want string
	}{
		{bundle.ActivityView{Kind: "event", Type: "task.created"}, "created the task"},
		{bundle.ActivityView{Kind: "event", Type: "task.updated"}, "edited the task details"},
		{bundle.ActivityView{Kind: "event", Type: "task.moved", Data: map[string]any{"from": "backlog", "to": "in-progress"}}, "moved backlog → in-progress"},
		{bundle.ActivityView{Kind: "event", Type: "task.moved", Data: map[string]any{"to": "done"}}, "moved to done"},
		{bundle.ActivityView{Kind: "event", Type: "task.labeled", Data: map[string]any{"labels": []any{"bug", "auth"}}}, "set labels to bug, auth"},
		{bundle.ActivityView{Kind: "event", Type: "task.labeled", Data: map[string]any{"labels": []any{}}}, "cleared all labels"},
		{bundle.ActivityView{Kind: "event", Type: "task.linked", Data: map[string]any{"kind": "blocks", "to": "TASK-0002"}}, "linked blocks TASK-0002"},
		{bundle.ActivityView{Kind: "event", Type: "task.unlinked", Data: map[string]any{"kind": "blocks", "to": "TASK-0002"}}, "removed the blocks link to TASK-0002"},
		{bundle.ActivityView{Kind: "event", Type: "task.file_attached", Data: map[string]any{"file": "log.txt", "mime": "text/plain"}}, "attached log.txt (text/plain)"},
		{bundle.ActivityView{Kind: "event", Type: "task.assigned"}, "changed the assignee"},
		{bundle.ActivityView{Kind: "event", Type: "task.waiting", Data: map[string]any{"wait": map[string]any{"kind": "review", "reason": "needs approval"}}}, "started waiting on review: needs approval"},
		// In-memory structs are normalised the same way as decoded JSON.
		{bundle.ActivityView{Kind: "event", Type: "task.waiting", Data: map[string]any{"wait": task.Wait{Kind: "ci", Reason: "build"}}}, "started waiting on ci: build"},
		{bundle.ActivityView{Kind: "event", Type: "task.resumed", Data: map[string]any{"kind": "review", "result": "approved"}}, "resumed after the review wait: approved"},
		{bundle.ActivityView{Kind: "event", Type: "task.reference_added", Data: map[string]any{"reference": map[string]any{"kind": "pr", "url": "https://x/1", "title": "Fix"}}}, "added pr reference Fix"},
		{bundle.ActivityView{Kind: "event", Type: "task.reference_removed", Data: map[string]any{"reference": map[string]any{"kind": "pr", "url": "https://x/1"}}}, "removed pr reference https://x/1"},
		{bundle.ActivityView{Kind: "comment", Type: "comment", Body: "hi"}, "commented"},
		{bundle.ActivityView{Kind: "session", Type: "attach", Session: "s1"}, "attached session s1"},
		{bundle.ActivityView{Kind: "session", Type: "detach", Session: "s1"}, "detached session s1"},
		{bundle.ActivityView{Kind: "widget", Type: "test_run"}, "posted test run"},
	} {
		if got := Summarize(test.item).Text; got != test.want {
			t.Errorf("%s: got %q, want %q", test.item.Type, got, test.want)
		}
	}
}

func TestSummarizeFallsBackToTypeAndCompactData(t *testing.T) {
	item := bundle.ActivityView{Kind: "event", Type: "plugin.deploy_done", Data: map[string]any{"env": "prod", "count": 2.0, "tags": []any{"a"}}}
	got := Summarize(item)
	if got.Text != `plugin deploy done · count=2, env=prod, tags=["a"]` || got.Icon != "•" {
		t.Fatalf("got %+v", got)
	}
	if got := Summarize(bundle.ActivityView{Kind: "event", Type: "task.mystery"}).Text; got != "mystery" {
		t.Fatalf("no-data fallback = %q", got)
	}
}

func TestRelativeTimes(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		at   time.Time
		want string
	}{
		{now.Add(-10 * time.Second), "just now"},
		{now.Add(-5 * time.Minute), "5m ago"},
		{now.Add(-3 * time.Hour), "3h ago"},
		{now.Add(-50 * time.Hour), "2d ago"},
		{now.Add(-21 * 24 * time.Hour), "3w ago"},
		{now.Add(2 * time.Hour), "2h from now"},
		{time.Time{}, ""},
	} {
		if got := Relative(test.at, now); got != test.want {
			t.Errorf("Relative(%v) = %q, want %q", test.at, got, test.want)
		}
	}
	if got := Relative(time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC), now); got != "1 Mar 2024" {
		t.Errorf("old date = %q", got)
	}
}
