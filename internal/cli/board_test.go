package cli_test

import (
	"strings"
	"testing"

	"github.com/tvdavies/docket/internal/task"
	"github.com/tvdavies/docket/internal/workspace"
)

func TestBareDocketWithoutTerminalPrintsHelp(t *testing.T) {
	dir := t.TempDir()
	out, stderr, err := runDocket(t, dir)
	if err != nil {
		t.Fatalf("docket: %v: %s", err, stderr)
	}
	if !strings.Contains(out, "Tasks:") || !strings.Contains(out, "board") {
		t.Fatalf("help output = %q", out)
	}
	if strings.Contains(out, "\x1b[") {
		t.Fatalf("piped help contains escape codes: %q", out)
	}
}

func TestBoardRefusesWithoutTerminal(t *testing.T) {
	dir := t.TempDir()
	if _, err := workspace.Init(dir); err != nil {
		t.Fatal(err)
	}
	_, stderr, err := runDocket(t, dir, "board")
	if err == nil || !strings.Contains(stderr, "needs an interactive terminal") {
		t.Fatalf("err=%v stderr=%q", err, stderr)
	}
	if _, stderr, err := runDocket(t, dir, "tui"); err == nil || !strings.Contains(stderr, "needs an interactive terminal") {
		t.Fatalf("tui alias: err=%v stderr=%q", err, stderr)
	}
}

func TestPipedShowStaysPlainText(t *testing.T) {
	dir := t.TempDir()
	ws, err := workspace.Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	created, err := task.Create(ws, task.CreateOptions{Title: "Plain please", Labels: []string{"bug"}, Description: "Some **markdown**."})
	if err != nil {
		t.Fatal(err)
	}
	if _, stderr, err := runDocket(t, dir, "move", created.ID, "ready"); err != nil {
		t.Fatalf("move: %v: %s", err, stderr)
	}
	out, stderr, err := runDocket(t, dir, "show", created.ID, "--no-pager")
	if err != nil {
		t.Fatalf("show: %v: %s", err, stderr)
	}
	if !strings.HasPrefix(out, "# "+created.ID+" — Plain please\nstatus: ready\nlabels: bug\n\nSome **markdown**.\n\n## Activity (1)\n") {
		t.Fatalf("show output changed:\n%s", out)
	}
	if strings.Contains(out, "\x1b[") || strings.Contains(out, "→") {
		t.Fatalf("piped show was styled:\n%s", out)
	}
}
