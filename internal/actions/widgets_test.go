package actions_test

import (
	"bytes"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/tvdavies/docket/internal/actions"
	"github.com/tvdavies/docket/internal/bundle"
	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/plugin"
	"github.com/tvdavies/docket/internal/task"
	"github.com/tvdavies/docket/internal/widget"
	"github.com/tvdavies/docket/internal/workspace"
)

func widgetFixture(t *testing.T) (*workspace.Workspace, *task.Task, widget.Record) {
	t.Helper()
	ws, err := workspace.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws.Plugins = []workspace.LoadedPlugin{{Manifest: &plugin.Manifest{Name: "fixture", Version: "1.0.0", UI: plugin.UI{APIVersion: 2, Cards: []plugin.Card{{Type: "fixture/progress", Locations: []string{"board", "activity"}}}}}}}
	value, err := task.Create(ws, task.CreateOptions{Title: "Widget fixture", Assignee: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	return ws, value, widget.Record{Version: 1, WidgetType: "fixture/progress", InstanceID: "run-1", TaskID: value.ID, CreatedAt: "2026-09-10T10:00:00Z", Revision: 1, Phase: "created", Fallback: widget.Fallback{Label: "Progress", StatusLabel: "Queued", Priority: "active"}}
}
func TestWidgetLifecycleRetriesAndTerminalRecovery(t *testing.T) {
	ws, value, create := widgetFixture(t)
	original, _ := os.ReadFile(value.TaskFile())
	ops := actions.Tasks{Workspace: ws, Actor: "publisher"}
	finish := create
	finish.Phase = "finalised"
	finish.Revision = 12
	finish.Fallback.StatusLabel = "Finished"
	finish.Fallback.Summary = "Saved outcome"
	if _, _, err := ops.Widget("demo", value.ID, finish); err == nil {
		t.Fatal("finalise before create accepted")
	}
	var appended atomic.Int32
	var group sync.WaitGroup
	for range 20 {
		group.Add(1)
		go func() {
			defer group.Done()
			_, w, err := ops.Widget("demo", value.ID, create)
			if err != nil {
				t.Error(err)
			}
			if w {
				appended.Add(1)
			}
		}()
	}
	group.Wait()
	if appended.Load() != 1 {
		t.Fatalf("creates=%d", appended.Load())
	}
	bad := create
	bad.Fallback.Label = "Changed"
	if _, _, err := ops.Widget("demo", value.ID, bad); err == nil {
		t.Fatal("changed retry accepted")
	}
	if _, w, err := ops.Widget("demo", value.ID, finish); err != nil || !w {
		t.Fatalf("finish: %v %v", w, err)
	}
	for _, retry := range []widget.Record{create, finish} {
		current, w, err := ops.Widget("demo", value.ID, retry)
		if err != nil || w || current.Phase != "finalised" {
			t.Fatalf("retry: %+v %v %v", current, w, err)
		}
	}
	bad = finish
	bad.Revision++
	if _, _, err := ops.Widget("demo", value.ID, bad); err == nil {
		t.Fatal("terminal overwrite accepted")
	}
	bad = finish
	bad.CreatedAt = "2026-09-11T10:00:00Z"
	if _, _, err := ops.Widget("demo", value.ID, bad); err == nil {
		t.Fatal("creation time changed")
	}
	log, err := events.All(ws)
	if err != nil || len(log) != 2 {
		t.Fatalf("ledger=%d %v", len(log), err)
	}
	after, _ := os.ReadFile(value.TaskFile())
	if !bytes.Equal(original, after) {
		t.Fatal("widget mutated dossier")
	}
	reopened, err := workspace.OpenRoot(ws.Root)
	if err != nil {
		t.Fatal(err)
	} // disabled/no module, empty process state
	b, err := bundle.Build(reopened, value.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Widgets) != 1 || len(b.Activity) != 1 || b.Activity[0].Kind != "widget" || b.Widgets[0].Fallback.Summary != "Saved outcome" || b.Activity[0].At != create.CreatedAt {
		t.Fatalf("recovery=%+v", b)
	}
}
func TestWidgetAppendFailureAndValidation(t *testing.T) {
	ws, value, create := widgetFixture(t)
	failure := errors.New("disk unavailable")
	called := false
	ops := actions.Tasks{Workspace: ws, Append: func(events.Event) error { called = true; return failure }}
	if _, w, err := ops.Widget("demo", value.ID, create); !errors.Is(err, failure) || w || !called {
		t.Fatalf("append result %v %v", w, err)
	}
	index, _ := widget.Load(ws)
	if len(index) != 0 {
		t.Fatal("failed append projected record")
	}
	for _, mutate := range []func(*widget.Record){func(r *widget.Record) { r.Version = 2 }, func(r *widget.Record) { r.Revision = widget.MaxRevision + 1 }, func(r *widget.Record) { r.InstanceID = "../escape" }, func(r *widget.Record) {
		r.Fallback.References = []widget.Reference{{Kind: "session", URL: "https://evil.example/run", Title: "Unsafe"}}
	}, func(r *widget.Record) { r.Fallback.Summary = string(bytes.Repeat([]byte("a"), 2001)) }} {
		r := create
		mutate(&r)
		if widget.Validate(r, "demo") == nil {
			t.Fatalf("invalid record accepted: %+v", r)
		}
	}
	ws.Plugins = nil
	called = false
	if _, _, err := ops.Widget("demo", value.ID, create); err == nil || called {
		t.Fatal("disabled publisher appended")
	}
}
