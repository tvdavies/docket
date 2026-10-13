package cli

import (
	"testing"

	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/task"
	"github.com/tvdavies/docket/internal/workspace"
)

func TestBoardSourceUsesCommandCodePaths(t *testing.T) {
	t.Setenv("DOCKET_ACTOR", "board-user")
	dir := t.TempDir()
	ws, err := workspace.Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	created, err := task.Create(ws, task.CreateOptions{Title: "From the board"})
	if err != nil {
		t.Fatal(err)
	}
	source := &boardSource{root: ws.Root, name: "demo"}

	snap, err := source.Load()
	if err != nil || len(snap.Tasks) != 1 || len(snap.Statuses) == 0 || snap.Statuses[0] != ws.Config.Statuses[0] {
		t.Fatalf("load: %+v, %v", snap, err)
	}
	if _, err := source.Move(created.ID, "in-progress"); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Move(created.ID, "nonsense"); err == nil {
		t.Fatal("unknown status accepted")
	}
	if _, err := source.Comment(created.ID, "Noted from the board"); err != nil {
		t.Fatal(err)
	}

	log, err := events.All(ws)
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, event := range log {
		if event.Actor != "board-user" {
			t.Fatalf("event actor = %q", event.Actor)
		}
		types = append(types, event.Type)
	}
	if len(types) != 2 || types[0] != events.TaskMoved || types[1] != events.TaskCommented {
		t.Fatalf("events = %v", types)
	}
	detail, err := source.Detail(created.ID)
	if err != nil || detail.Status != "in-progress" || len(detail.Comments) != 1 {
		t.Fatalf("detail = %+v, %v", detail, err)
	}
}
