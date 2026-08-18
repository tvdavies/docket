package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tvdavies/docket/internal/workspace"
)

func TestRootHelpGroupsCommandsAndDemotesLegacySessionSurface(t *testing.T) {
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
		"Task workflow:",
		"Optional session shorthand:",
		"Automation and event diagnostics:",
		"session     Optional task pointer",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("help missing %q:\n%s", expected, text)
		}
	}
	for _, hidden := range []string{"\n  attach      ", "\n  detach      ", "\n  current     ", "\n  context     "} {
		if strings.Contains(text, hidden) {
			t.Fatalf("legacy command %q remained in root help:\n%s", hidden, text)
		}
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
