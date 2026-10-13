package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/tvdavies/docket/internal/workspace"
)

func TestRootHelpGroupsTheReducedCommandSurface(t *testing.T) {
	root := newRootCmd()
	root.SetArgs([]string{"--help"})
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, expected := range []string{
		"Tasks:",
		"Relationships and context:",
		"Events and automation:",
		"Setup and help:",
		"\n  session    ",
		"\n  run        ",
		"\n  events     ",
		`Run "docket COMMAND --help"`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("help missing %q:\n%s", expected, text)
		}
	}
	if strings.Contains(text, "\x1b[") {
		t.Fatalf("help written to a buffer must not contain colour codes:\n%q", text)
	}
	for _, removed := range []string{"attach", "detach", "current", "context", "serve", "watch", "files", "unlink", "reindex", "__lua-hook"} {
		if strings.Contains(text, "\n  "+removed+" ") {
			t.Fatalf("command %q is listed in root help:\n%s", removed, text)
		}
	}
}

func TestRemovedCommandsAreUnknown(t *testing.T) {
	for _, args := range [][]string{
		{"serve"}, {"watch"}, {"files", "TASK-0001"}, {"unlink", "TASK-0001"}, {"reindex"},
		{"attach", "TASK-0001"}, {"detach"}, {"current"}, {"context", "TASK-0001"},
	} {
		root := newRootCmd()
		root.SetArgs(args)
		root.SetOut(&bytes.Buffer{})
		_, err := root.ExecuteC()
		if err == nil || !strings.Contains(err.Error(), "unknown command") {
			t.Fatalf("docket %v: err = %v, want unknown command", args, err)
		}
	}
	for _, args := range [][]string{{"wait", "show", "TASK-0001"}, {"reference", "list", "TASK-0001"}} {
		root := newRootCmd()
		root.SetArgs(args)
		var output bytes.Buffer
		root.SetOut(&output)
		root.SetErr(&output)
		// A parent without RunE prints its help for unknown subcommands
		// rather than running anything.
		if _, err := root.ExecuteC(); err == nil && strings.Contains(output.String(), "TASK-0001") {
			t.Fatalf("docket %v still ran:\n%s", args, output.String())
		}
	}
}

func TestHelpStyleIsPlainOffTerminalOrWithNoColor(t *testing.T) {
	if helpStyleFor(&bytes.Buffer{}).colour {
		t.Fatal("non-stdout writer must not be coloured")
	}
	t.Setenv("NO_COLOR", "1")
	if helpStyleFor(os.Stdout).colour {
		t.Fatal("NO_COLOR must disable colour")
	}
	styled := helpStyle{colour: true}
	if got := styled.heading("Usage:"); got != "\x1b[1mUsage:\x1b[0m" {
		t.Fatalf("heading = %q", got)
	}
}

func TestCommandErrorPrintsExactUsageAndRecoveryHelp(t *testing.T) {
	root := newRootCmd()
	root.SetArgs([]string{"move"})
	var output bytes.Buffer
	if code := executeRoot(root, &output); code != 1 {
		t.Fatalf("exit code = %d", code)
	}
	text := output.String()
	if !strings.Contains(text, "Usage: docket move [TASK-ID] STATUS [flags]") {
		t.Fatalf("missing exact usage:\n%s", text)
	}
	if !strings.Contains(text, "Run 'docket move --help' for examples and flags.") {
		t.Fatalf("missing recovery instruction:\n%s", text)
	}
}

func TestOperationalErrorDoesNotPretendArgumentsAreWrong(t *testing.T) {
	t.Setenv("DOCKET_HOME", "")
	rootDir := t.TempDir()
	if _, err := workspace.Init(rootDir); err != nil {
		t.Fatal(err)
	}
	t.Chdir(rootDir)
	root := newRootCmd()
	root.SetArgs([]string{"show", "TASK-9999"})
	var output bytes.Buffer
	if code := executeRoot(root, &output); code != 1 {
		t.Fatalf("exit code = %d", code)
	}
	text := output.String()
	if !strings.Contains(text, `task "TASK-9999" not found`) {
		t.Fatalf("missing operational error:\n%s", text)
	}
	if strings.Contains(text, "Usage:") || strings.Contains(text, "--help") {
		t.Fatalf("operational error was misclassified as usage:\n%s", text)
	}
}

func TestNoOpEditAndLabelErrorsExplainRequiredFlags(t *testing.T) {
	for _, test := range []struct {
		args []string
		want string
	}{
		{args: []string{"edit", "TASK-0001"}, want: "supply --title, --desc, --desc-file, or --assignee"},
		{args: []string{"label", "TASK-0001"}, want: "supply --add LABEL or --remove LABEL"},
	} {
		root := newRootCmd()
		root.SetArgs(test.args)
		_, err := root.ExecuteC()
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("%v error = %v, want %q", test.args, err, test.want)
		}
	}
}
