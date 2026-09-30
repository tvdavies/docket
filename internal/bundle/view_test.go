package bundle_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/tvdavies/docket/internal/actions"
	"github.com/tvdavies/docket/internal/bundle"
	"github.com/tvdavies/docket/internal/task"
	"github.com/tvdavies/docket/internal/workspace"
)

func historyTask(t *testing.T, comments int) (*workspace.Workspace, *task.Task, *task.Task) {
	t.Helper()
	ws, err := workspace.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	operations := actions.Tasks{Workspace: ws, Actor: "agent"}
	created, err := operations.Create(task.CreateOptions{Title: "Long history", Description: "Keep keys versioned."})
	if err != nil {
		t.Fatal(err)
	}
	other, err := operations.Create(task.CreateOptions{Title: "Follow-up"})
	if err != nil {
		t.Fatal(err)
	}
	if err := operations.Link(created.ID, "blocks", other.ID); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < comments; index++ {
		if _, err := operations.Comment(created.ID, fmt.Sprintf("note %d", index)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := operations.Attach(created.ID, "repro.log", []byte("trace"), "failing run"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := operations.AddReference(created.ID, "decision", "https://example.com/adr/7", "ADR 7"); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.SetWait(created.ID, actions.SetWaitOptions{Kind: "review", Reason: "Awaiting review"}); err != nil {
		t.Fatal(err)
	}
	return ws, created, other
}

func TestCurrentViewKeepsStateAndReportsOmittedHistory(t *testing.T) {
	ws, created, other := historyTask(t, 30)
	value, err := bundle.BuildContext(ws, created.ID, bundle.Options{View: bundle.ViewCurrent})
	if err != nil {
		t.Fatal(err)
	}
	if value.Wait == nil || value.Wait.Kind != "review" {
		t.Fatalf("wait = %#v", value.Wait)
	}
	if len(value.References) != 1 || value.References[0].Kind != "decision" {
		t.Fatalf("references = %#v", value.References)
	}
	if refs := value.Relationships["blocks"]; len(refs) != 1 || refs[0].ID != other.ID || refs[0].Title != "Follow-up" {
		t.Fatalf("relationships = %#v", value.Relationships)
	}
	if len(value.Attachments) != 1 || value.Attachments[0].File != "repro.log" {
		t.Fatalf("attachments = %#v", value.Attachments)
	}
	if value.Description != "Keep keys versioned." || len(value.Activity) != 0 {
		t.Fatalf("context = %#v", value)
	}
	page := value.ActivityPage
	if page.Total == 0 || !page.Truncated || page.Start != page.Total || page.End != page.Total {
		t.Fatalf("page = %#v", page)
	}
}

func TestAgentViewPagesActivityWithoutDuplicates(t *testing.T) {
	ws, created, _ := historyTask(t, 30)
	full, err := bundle.Build(ws, created.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	total := len(full.Activity)

	newest, err := bundle.BuildContext(ws, created.ID, bundle.Options{View: bundle.ViewAgent, ActivityLimit: 10})
	if err != nil {
		t.Fatal(err)
	}
	page := newest.ActivityPage
	if page.Total != total || page.Start != total-10 || page.End != total || !page.Truncated || page.NextBefore != total-10 {
		t.Fatalf("page = %#v (total %d)", page, total)
	}
	if !reflect.DeepEqual(activityKeys(newest.Activity), activityKeys(full.Activity[total-10:])) {
		t.Fatal("agent page does not match the newest full-timeline items")
	}
	older, err := bundle.BuildContext(ws, created.ID, bundle.Options{View: bundle.ViewAgent, ActivityLimit: 10, ActivityBefore: page.NextBefore})
	if err != nil {
		t.Fatal(err)
	}
	if older.ActivityPage.End != page.Start || !reflect.DeepEqual(activityKeys(older.Activity), activityKeys(full.Activity[total-20:total-10])) {
		t.Fatalf("older page = %#v", older.ActivityPage)
	}

	data, err := json.Marshal(newest)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	for _, duplicated := range []string{"comments", "sessions", "active_sessions", "widgets"} {
		if _, ok := fields[duplicated]; ok {
			t.Fatalf("agent view repeats %q", duplicated)
		}
	}
	fullData, _ := json.Marshal(full)
	if len(data) >= len(fullData) {
		t.Fatalf("agent view is %d bytes, full is %d", len(data), len(fullData))
	}
}

func TestContextReadsAreDeterministicAndFullContractUnchanged(t *testing.T) {
	ws, created, _ := historyTask(t, 5)
	first, err := bundle.BuildWith(ws, created.ID, bundle.Options{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := bundle.BuildWith(ws, created.ID, bundle.Options{})
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if string(firstJSON) != string(secondJSON) {
		t.Fatal("repeated full reads differ")
	}
	if first.ActivityPage != nil || first.CommentsOmitted != 0 {
		t.Fatalf("unbounded full read gained paging fields: %#v", first.ActivityPage)
	}

	limited, err := bundle.BuildWith(ws, created.ID, bundle.Options{CommentLimit: 2, ActivityLimit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if limited.CommentsOmitted != 3 || len(limited.Comments) != 2 {
		t.Fatalf("comments = %d omitted %d", len(limited.Comments), limited.CommentsOmitted)
	}
	if limited.ActivityPage == nil || len(limited.Activity) != 3 || !limited.ActivityPage.Truncated {
		t.Fatalf("activity page = %#v", limited.ActivityPage)
	}
}

func activityKeys(items []bundle.ActivityView) []string {
	keys := make([]string, 0, len(items))
	for _, item := range items {
		keys = append(keys, item.At+" "+item.Type+" "+item.Body)
	}
	return keys
}
