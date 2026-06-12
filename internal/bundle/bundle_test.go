package bundle_test

import (
	"testing"

	"github.com/tvdavies/tadu/internal/bundle"
	"github.com/tvdavies/tadu/internal/project"
	"github.com/tvdavies/tadu/internal/task"
	"github.com/tvdavies/tadu/internal/workspace"
)

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
