package cli

import (
	"testing"

	sessionstore "github.com/tvdavies/docket/internal/session"
	"github.com/tvdavies/docket/internal/task"
	"github.com/tvdavies/docket/internal/workspace"
)

func TestGroupedAndLegacySessionCommandsRemainFunctional(t *testing.T) {
	rootDir := t.TempDir()
	ws, err := workspace.Init(rootDir)
	if err != nil {
		t.Fatal(err)
	}
	created, err := task.Create(ws, task.CreateOptions{Title: "Session compatibility"})
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(rootDir)

	runCLIForTest(t, "session", "attach", created.ID, "--session", "grouped")
	if got := sessionstore.Current(ws, "grouped"); got != created.ID {
		t.Fatalf("grouped attach current = %q", got)
	}
	runCLIForTest(t, "session", "detach", "--session", "grouped")
	if got := sessionstore.Current(ws, "grouped"); got != "" {
		t.Fatalf("grouped detach current = %q", got)
	}

	runCLIForTest(t, "attach", created.ID, "--session", "legacy")
	if got := sessionstore.Current(ws, "legacy"); got != created.ID {
		t.Fatalf("legacy attach current = %q", got)
	}
	runCLIForTest(t, "detach", "--session", "legacy")
	if got := sessionstore.Current(ws, "legacy"); got != "" {
		t.Fatalf("legacy detach current = %q", got)
	}
}

func runCLIForTest(t *testing.T, args ...string) {
	t.Helper()
	command := newRootCmd()
	command.SetArgs(args)
	if _, err := command.ExecuteC(); err != nil {
		t.Fatalf("docket %v: %v", args, err)
	}
}
