package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tvdavies/docket/internal/task"
	"github.com/tvdavies/docket/internal/workspace"
)

func TestBoundedShowIsReadOnlyAndCompact(t *testing.T) {
	dir := t.TempDir()
	ws, err := workspace.Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	created, err := task.Create(ws, task.CreateOptions{Title: "Read me"})
	if err != nil {
		t.Fatal(err)
	}
	for _, note := range []string{"one", "two", "three"} {
		if _, stderr, err := runDocket(t, dir, "comment", created.ID, note); err != nil {
			t.Fatalf("comment: %v: %s", err, stderr)
		}
	}
	snapshot := func() string {
		var all strings.Builder
		_ = filepath.Walk(ws.Root, func(path string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && !strings.HasSuffix(path, ".lock") {
				data, _ := os.ReadFile(path)
				all.WriteString(path + "\n" + string(data) + "\n")
			}
			return nil
		})
		return all.String()
	}
	before := snapshot()
	out, stderr, err := runDocket(t, dir, "show", created.ID, "--view", "agent", "--activity", "1", "--json", "--compact")
	if err != nil {
		t.Fatalf("show: %v: %s", err, stderr)
	}
	if strings.Count(strings.TrimSpace(out), "\n") != 0 {
		t.Fatalf("compact output spans lines: %q", out)
	}
	if !strings.Contains(out, `"activity_page":{"total":3,"start":2,"end":3,"truncated":true,"next_before":2}`) {
		t.Fatalf("output = %s", out)
	}
	if _, _, err := runDocket(t, dir, "show", created.ID, "--view", "current", "--activity", "1"); err == nil {
		t.Fatal("current view accepted an activity limit")
	}
	if after := snapshot(); after != before {
		t.Fatal("a bounded read changed workspace files")
	}
}

func TestPipedListPrintsFullTitlesAsTabSeparatedColumns(t *testing.T) {
	dir := t.TempDir()
	ws, err := workspace.Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	title := "Investigate the intermittent login cache invalidation failure on staging servers"
	if _, err := task.Create(ws, task.CreateOptions{Title: title, Labels: []string{"bug"}}); err != nil {
		t.Fatal(err)
	}
	out, stderr, err := runDocket(t, dir, "list")
	if err != nil {
		t.Fatalf("list: %v: %s", err, stderr)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 || lines[0] != "ID\tSTATUS\tTITLE\tLABELS\tWAITING" {
		t.Fatalf("output = %q", out)
	}
	if columns := strings.Split(lines[1], "\t"); len(columns) != 5 || columns[2] != title || columns[3] != "bug" {
		t.Fatalf("row = %q", lines[1])
	}
}
