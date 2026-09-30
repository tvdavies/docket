package actions_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/tvdavies/docket/internal/actions"
	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/store"
	"github.com/tvdavies/docket/internal/task"
	"github.com/tvdavies/docket/internal/workspace"
)

func TestMutationsLeaveDossierUntouchedWhenEventFails(t *testing.T) {
	ws, err := workspace.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created, err := task.Create(ws, task.CreateOptions{Title: "Stay put", Status: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := task.Create(ws, task.CreateOptions{Title: "Link target"})
	if err != nil {
		t.Fatal(err)
	}
	commitFailure := errors.New("event disk unavailable")
	committed := 0
	failing := actions.Tasks{
		Workspace: ws, Actor: "agent",
		Append:    func(events.Event) error { return commitFailure },
		Committed: func() { committed++ },
	}
	title := "Changed"
	mutations := map[string]func() error{
		"edit":   func() error { _, err := failing.Edit(created.ID, actions.EditOptions{Title: &title}); return err },
		"move":   func() error { _, _, err := failing.Move(created.ID, "done"); return err },
		"assign": func() error { _, err := failing.Assign(created.ID, "reviewer"); return err },
		"label":  func() error { _, err := failing.Label(created.ID, []string{"bug"}, nil); return err },
		"link":   func() error { return failing.Link(created.ID, "blocks", other.ID) },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			before := readDossiers(t, created, other)
			if err := mutate(); !errors.Is(err, commitFailure) {
				t.Fatalf("error = %v", err)
			}
			if after := readDossiers(t, created, other); !bytes.Equal(before, after) {
				t.Fatalf("dossier changed after failed commit:\n%s\n---\n%s", before, after)
			}
		})
	}
	if committed != 0 {
		t.Fatalf("post-commit callback ran %d times for failed mutations", committed)
	}
}

func TestIndeterminateEventFailureKeepsMutation(t *testing.T) {
	ws, err := workspace.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created, err := task.Create(ws, task.CreateOptions{Title: "Maybe recorded", Status: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	committed := 0
	operations := actions.Tasks{
		Workspace: ws, Actor: "agent",
		Append: func(event events.Event) error {
			if err := events.Append(ws, event); err != nil {
				return err
			}
			return fmt.Errorf("sync failed: %w", store.ErrAppendIndeterminate)
		},
		Committed: func() { committed++ },
	}
	if _, _, err := operations.Move(created.ID, "done"); !errors.Is(err, store.ErrAppendIndeterminate) {
		t.Fatalf("move error = %v", err)
	}
	reloaded, err := task.Load(ws, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Status != "done" {
		t.Fatalf("status = %q; a visible event must not be contradicted by a rolled-back dossier", reloaded.Status)
	}
	if committed != 1 {
		t.Fatalf("post-commit callback ran %d times", committed)
	}
}

func TestConcurrentMutationsRecordEventsInCommitOrder(t *testing.T) {
	ws, err := workspace.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created, err := task.Create(ws, task.CreateOptions{Title: "Contended"})
	if err != nil {
		t.Fatal(err)
	}
	operations := actions.Tasks{Workspace: ws, Actor: "agent"}
	const writers = 12
	var group sync.WaitGroup
	failures := make(chan error, writers)
	for index := 0; index < writers; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			if _, err := operations.Label(created.ID, []string{fmt.Sprintf("l%02d", index)}, nil); err != nil {
				failures <- err
			}
		}(index)
	}
	group.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	log, err := events.All(ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(log) != writers {
		t.Fatalf("events = %d, want %d", len(log), writers)
	}
	// Each label event carries the full label set it committed. In commit order
	// every set extends the previous one by exactly one label.
	for index, event := range log {
		labels, _ := event.Data["labels"].([]any)
		if len(labels) != index+1 {
			t.Fatalf("event %d carries %d labels; log order disagrees with dossier commits: %#v", index, len(labels), log)
		}
	}
}

func readDossiers(t *testing.T, values ...*task.Task) []byte {
	t.Helper()
	var all []byte
	for _, value := range values {
		data, err := os.ReadFile(value.TaskFile())
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, data...)
	}
	return all
}
