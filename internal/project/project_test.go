package project_test

import (
	"strings"
	"testing"

	"github.com/tvdavies/docket/internal/project"
	"github.com/tvdavies/docket/internal/workspace"
)

func TestLoadRejectsNonCanonicalAndTraversingIDs(t *testing.T) {
	ws, err := workspace.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"../outside", "PROJ-1", "PROJ-0000", "OTHER-0001", "PROJ-0001/file"} {
		t.Run(id, func(t *testing.T) {
			if _, err := project.Load(ws, id); err == nil || !strings.Contains(err.Error(), "invalid project id") {
				t.Fatalf("project.Load(%q) error = %v", id, err)
			}
		})
	}
}
