package task_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tvdavies/docket/internal/task"
	"github.com/tvdavies/docket/internal/workspace"
)

func newWorkspace(t *testing.T) *workspace.Workspace {
	t.Helper()
	dir := t.TempDir()
	ws, err := workspace.Init(dir)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	return ws
}

func TestLoadRejectsNonCanonicalAndTraversingIDs(t *testing.T) {
	ws := newWorkspace(t)
	for _, id := range []string{"../outside", "TASK-1", "TASK-0000", "OTHER-0001", "TASK-0001/child"} {
		t.Run(id, func(t *testing.T) {
			if _, err := task.Load(ws, id); err == nil || !strings.Contains(err.Error(), "invalid task id") {
				t.Fatalf("task.Load(%q) error = %v", id, err)
			}
		})
	}
}

func TestCreateAndLoad(t *testing.T) {
	ws := newWorkspace(t)
	created, err := task.Create(ws, task.CreateOptions{
		Title:       "Fix login cache",
		Description: "body text",
		Labels:      []string{"bug"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != "TASK-0001" {
		t.Fatalf("expected TASK-0001, got %s", created.ID)
	}
	if created.Status != "backlog" {
		t.Fatalf("expected default status backlog, got %s", created.Status)
	}

	loaded, err := task.Load(ws, "TASK-0001")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Title != "Fix login cache" || loaded.Description != "body text" {
		t.Fatalf("round-trip mismatch: %+v", loaded)
	}
	if !loaded.HasLabel("bug") {
		t.Fatal("missing label")
	}
}

func TestIDReconcileWhenCounterLost(t *testing.T) {
	ws := newWorkspace(t)
	if _, err := task.Create(ws, task.CreateOptions{Title: "one"}); err != nil {
		t.Fatal(err)
	}
	if _, err := task.Create(ws, task.CreateOptions{Title: "two"}); err != nil {
		t.Fatal(err)
	}
	// Delete the counter; next id must still advance past existing ids.
	_ = os.Remove(ws.Path(".next-id"))
	third, err := task.Create(ws, task.CreateOptions{Title: "three"})
	if err != nil {
		t.Fatal(err)
	}
	if third.ID != "TASK-0003" {
		t.Fatalf("expected self-heal to TASK-0003, got %s", third.ID)
	}
}

func TestUpdateBumpsTimestamp(t *testing.T) {
	ws := newWorkspace(t)
	created, _ := task.Create(ws, task.CreateOptions{Title: "x"})
	updated, err := task.Update(ws, created.ID, func(tk *task.Task) error {
		tk.Status = "in-progress"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != "in-progress" {
		t.Fatal("status not updated")
	}
	if updated.UpdatedAt.Before(created.UpdatedAt) {
		t.Fatal("updated_at not bumped")
	}
}

func TestCommentsAppendOnly(t *testing.T) {
	ws := newWorkspace(t)
	created, _ := task.Create(ws, task.CreateOptions{Title: "x"})
	if _, err := task.AddComment(ws, created.ID, "tom", "sess-1", "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := task.AddComment(ws, created.ID, "agent:pi", "sess-2", "second"); err != nil {
		t.Fatal(err)
	}
	reloaded, _ := task.Load(ws, created.ID)
	comments, err := reloaded.Comments()
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 2 {
		t.Fatalf("expected 2 comments, got %d", len(comments))
	}
	if comments[0].Body != "first" || comments[1].Body != "second" {
		t.Fatalf("comment order wrong: %+v", comments)
	}
}

func TestConcurrentComments(t *testing.T) {
	ws := newWorkspace(t)
	created, _ := task.Create(ws, task.CreateOptions{Title: "x"})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, _ = task.AddComment(ws, created.ID, "actor", "sess", "c")
		}(i)
	}
	wg.Wait()
	reloaded, _ := task.Load(ws, created.ID)
	comments, _ := reloaded.Comments()
	if len(comments) != 20 {
		t.Fatalf("expected 20 unique comments, got %d", len(comments))
	}
}

func TestAttachFile(t *testing.T) {
	ws := newWorkspace(t)
	created, _ := task.Create(ws, task.CreateOptions{Title: "x"})
	src := filepath.Join(t.TempDir(), "repro.log")
	if err := os.WriteFile(src, []byte("log data"), 0o644); err != nil {
		t.Fatal(err)
	}
	att, err := task.AttachFile(ws, created.ID, src, "caption", "tom")
	if err != nil {
		t.Fatal(err)
	}
	if att.Bytes != 8 {
		t.Fatalf("expected 8 bytes, got %d", att.Bytes)
	}
	reloaded, _ := task.Load(ws, created.ID)
	atts, _ := reloaded.Attachments()
	if len(atts) != 1 || atts[0].File != "repro.log" {
		t.Fatalf("manifest mismatch: %+v", atts)
	}
	if _, err := os.Stat(filepath.Join(reloaded.AttachmentsDir(), "repro.log")); err != nil {
		t.Fatal("attached file missing on disk")
	}
	duplicate, err := task.AttachData(ws, created.ID, "repro.log", []byte("new"), "", "tom")
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.File != "repro-1.log" {
		t.Fatalf("duplicate filename = %q", duplicate.File)
	}
	if _, err := task.AttachData(ws, created.ID, "../outside.txt", []byte("bad"), "", "tom"); err == nil {
		t.Fatal("attachment traversal unexpectedly accepted")
	}
	if _, _, err := reloaded.AttachmentPath("../repro.log"); err == nil {
		t.Fatal("attachment download traversal unexpectedly accepted")
	}
}

func TestAttachDataWithCommitHoldsTaskLockThroughCommit(t *testing.T) {
	ws := newWorkspace(t)
	created, err := task.Create(ws, task.CreateOptions{Title: "locked attachment"})
	if err != nil {
		t.Fatal(err)
	}
	commitStarted := make(chan struct{})
	releaseCommit := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		_, err := task.AttachDataWithCommit(ws, created.ID, "first.txt", []byte("one"), "", "actor", func(value *task.Task, attachment *task.Attachment) error {
			if value.Title != created.Title || attachment.File != "first.txt" {
				return os.ErrInvalid
			}
			close(commitStarted)
			<-releaseCommit
			return nil
		})
		firstDone <- err
	}()
	<-commitStarted
	secondDone := make(chan error, 1)
	go func() {
		_, err := task.AttachData(ws, created.ID, "second.txt", []byte("two"), "", "actor")
		secondDone <- err
	}()
	select {
	case err := <-secondDone:
		t.Fatalf("second attachment completed before commit released the task lock: %v", err)
	case <-time.After(75 * time.Millisecond):
	}
	close(releaseCommit)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	reloaded, err := task.Load(ws, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	attachments, err := reloaded.Attachments()
	if err != nil || len(attachments) != 2 {
		t.Fatalf("attachments after serialized commits = %#v, %v", attachments, err)
	}
}

func TestLinkMaintainsInverse(t *testing.T) {
	ws := newWorkspace(t)
	a, _ := task.Create(ws, task.CreateOptions{Title: "a"})
	b, _ := task.Create(ws, task.CreateOptions{Title: "b"})
	if err := task.Link(ws, a.ID, "blocks", b.ID); err != nil {
		t.Fatal(err)
	}
	ra, _ := task.Load(ws, a.ID)
	rb, _ := task.Load(ws, b.ID)
	if got := ra.Relationships["blocks"]; len(got) != 1 || got[0] != b.ID {
		t.Fatalf("forward edge wrong: %+v", ra.Relationships)
	}
	if got := rb.Relationships["blocked-by"]; len(got) != 1 || got[0] != a.ID {
		t.Fatalf("inverse edge wrong: %+v", rb.Relationships)
	}
	// Unlink removes both.
	if err := task.Unlink(ws, a.ID, "blocks", b.ID); err != nil {
		t.Fatal(err)
	}
	ra, _ = task.Load(ws, a.ID)
	rb, _ = task.Load(ws, b.ID)
	if len(ra.Relationships["blocks"]) != 0 || len(rb.Relationships["blocked-by"]) != 0 {
		t.Fatal("unlink left dangling edges")
	}
}
