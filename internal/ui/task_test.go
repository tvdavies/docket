package ui

import (
	"os"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/tvdavies/docket/internal/bundle"
	"github.com/tvdavies/docket/internal/task"
)

var testNow = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

// TestMain pins local time so day headings and clock times are stable.
func TestMain(m *testing.M) {
	time.Local = time.UTC
	os.Exit(m.Run())
}

func sampleTask() Task {
	return Task{
		View: bundle.ViewFull, ID: "TASK-0007", Title: "Fix login cache invalidation", Status: "in-progress",
		CreatedAt: "2026-10-10T09:00:00Z", UpdatedAt: "2026-10-13T09:00:00Z",
		Project:  &bundle.ProjectRef{ID: "PROJ-0001", Name: "Website"},
		Labels:   []string{"bug", "auth"},
		Assignee: "alice",
		Wait:     &task.Wait{ID: "wait-1", Kind: "review", Reason: "PR needs approval", Reference: "https://example.com/pr/1", Since: testNow.Add(-2 * time.Hour)},
		References: []task.Reference{
			{ID: "ref-1", Kind: "pr", URL: "https://example.com/pr/1", Title: "Fix cache key"},
		},
		Description:   "## Context\n\nUsers see **stale** sessions.",
		Relationships: map[string][]bundle.TaskRef{"blocks": {{ID: "TASK-0008", Title: "Release"}}},
		Attachments:   []*task.Attachment{{File: "trace.log", Mime: "text/plain", Bytes: 2048, Caption: "Production trace"}},
		Activity: []bundle.ActivityView{
			{At: "2026-10-12T09:00:00Z", Kind: "event", Type: "task.created", Actor: "alice"},
			{At: "2026-10-12T09:05:00Z", Kind: "event", Type: "task.moved", Actor: "alice", Data: map[string]any{"from": "backlog", "to": "in-progress"}},
			{At: "2026-10-13T10:00:00Z", Kind: "comment", Type: "comment", Actor: "bob", Session: "s1", Body: "Root cause is the **tenant** key."},
			{At: "2026-10-13T11:00:00Z", Kind: "event", Type: "task.plugin_ping", Data: map[string]any{"ok": true}},
		},
	}
}

func plainRenderer(width int) Renderer {
	theme := NewTheme(true)
	theme.Plain = true
	return Renderer{Theme: theme, Width: width, Now: testNow}
}

func TestDocumentRendersEverySectionInOrder(t *testing.T) {
	out := stripANSI(plainRenderer(80).Document(sampleTask()))
	order := []string{
		"TASK-0007", "● in-progress", "Fix login cache invalidation",
		"@alice", "project Website (PROJ-0001)", "created 3d ago", "updated 3h ago",
		"[bug] [auth]",
		"◷ Waiting on review", "since 2h ago", "PR needs approval", "wait wait-1", "↗ https://example.com/pr/1",
		"Description", "Context", "stale",
		"Relationships", "blocks", "TASK-0008", "Release",
		"References", "↗ pr", "Fix cache key", "ref-1",
		"Attachments", "attachments/trace.log", "(text/plain, 2.0 KB)", "Production trace",
		"Activity · 4",
		"alice created the task",
		"alice moved backlog → in-progress",
		"bob commented · session s1", "Root cause is the", "tenant",
		"system plugin ping · ok=true",
	}
	position := 0
	for _, want := range order {
		index := strings.Index(out[position:], want)
		if index < 0 {
			t.Fatalf("missing %q after byte %d:\n%s", want, position, out)
		}
		position += index + len(want)
	}
	for _, day := range []string{"Mon 12 Oct 2026", "Tue 13 Oct 2026"} {
		if strings.Count(out, day) != 1 {
			t.Fatalf("day heading %q should appear once:\n%s", day, out)
		}
	}
	if strings.Contains(out, `{"ok":true}`) {
		t.Fatalf("raw JSON leaked into the timeline:\n%s", out)
	}
}

func TestDocumentFitsWidth(t *testing.T) {
	for _, width := range []int{50, 80} {
		out := stripANSI(plainRenderer(width).Document(sampleTask()))
		for _, line := range strings.Split(out, "\n") {
			if w := lipgloss.Width(line); w > width {
				t.Fatalf("width %d: line is %d cells: %q", width, w, line)
			}
		}
	}
}

func TestColouredOutputStripsToTheSameText(t *testing.T) {
	coloured := Renderer{Theme: NewTheme(false), Width: 80, Now: testNow}.Document(sampleTask())
	if !strings.Contains(coloured, "\x1b[") {
		t.Fatal("coloured theme produced no styling")
	}
	plain := stripANSI(coloured)
	for _, want := range []string{"moved backlog → in-progress", "◷ Waiting on review", "bug", "auth"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("missing %q:\n%s", want, plain)
		}
	}
}

func TestTimelineShowsPagingHintsForBoundedViews(t *testing.T) {
	agent := sampleTask()
	agent.View = bundle.ViewAgent
	agent.Activity = agent.Activity[2:]
	agent.Page = &bundle.ActivityPage{Total: 10, Start: 4, End: 6, Truncated: true, NextBefore: 4}
	agent.CommentsOmitted = 3
	out := stripANSI(plainRenderer(120).Timeline(agent))
	for _, want := range []string{
		"Activity · 5–6 of 10",
		"4 older items not shown; read them with: docket show TASK-0007 --view agent --activity-before 4",
		"4 newer items not shown.",
		"3 older comments omitted by --comments.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}

	current := sampleTask()
	current.View = bundle.ViewCurrent
	current.Activity = nil
	current.Page = &bundle.ActivityPage{Total: 4, Start: 4, End: 4, Truncated: true, NextBefore: 4}
	out = stripANSI(plainRenderer(120).Timeline(current))
	if !strings.Contains(out, "4 items not shown; read them with: docket show TASK-0007 --view agent") {
		t.Fatalf("current view hint missing:\n%s", out)
	}
}

func TestFromContextAndBundleCarryActivityPages(t *testing.T) {
	b := &bundle.Bundle{ID: "TASK-1", Title: "T", Status: "ready", Activity: []bundle.ActivityView{{Type: "task.created"}}}
	if got := FromBundle(b); got.Page != nil || len(got.Activity) != 1 || got.View != bundle.ViewFull {
		t.Fatalf("FromBundle = %+v", got)
	}
	c := &bundle.Context{View: bundle.ViewAgent, ID: "TASK-1", ActivityPage: bundle.ActivityPage{Total: 3, End: 3}}
	if got := FromContext(c); got.Page == nil || got.Page.Total != 3 || got.View != bundle.ViewAgent {
		t.Fatalf("FromContext = %+v", got)
	}
}

func TestCommentsTabListsOnlyComments(t *testing.T) {
	out := stripANSI(plainRenderer(80).Comments(sampleTask()))
	if !strings.Contains(out, "Comments · 1") || !strings.Contains(out, "bob commented") || strings.Contains(out, "created the task") {
		t.Fatalf("comments:\n%s", out)
	}
	empty := sampleTask()
	empty.Activity = nil
	if out := stripANSI(plainRenderer(80).Comments(empty)); !strings.Contains(out, "No comments yet.") {
		t.Fatalf("empty comments:\n%s", out)
	}
}

func TestTaskTableFitsWidthAndTruncatesTitles(t *testing.T) {
	tasks := []*task.Task{
		{ID: "TASK-0001", Status: "in-progress", Title: "A very long title that will certainly not fit in a narrow terminal window", Labels: []string{"bug"}, Wait: &task.Wait{Kind: "review"}},
		{ID: "TASK-0002", Status: "done", Title: "Short"},
	}
	theme := NewTheme(true)
	out := stripANSI(theme.TaskTable(tasks, 70))
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "ID") {
		t.Fatalf("table:\n%s", out)
	}
	for _, line := range lines {
		if w := lipgloss.Width(line); w > 70 {
			t.Fatalf("line is %d cells: %q", w, line)
		}
	}
	if !strings.Contains(lines[1], "● in-progress") || !strings.Contains(lines[1], "…") || !strings.Contains(lines[1], "◷ review") {
		t.Fatalf("row: %q", lines[1])
	}
}
