package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManWritesOnePagePerVisibleCommand(t *testing.T) {
	dir := t.TempDir()
	root := newRootCmd()
	root.SetArgs([]string{"man", "--dir", dir})
	root.SetErr(&bytes.Buffer{})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, page := range []string{"docket.1", "docket-show.1", "docket-link.1", "docket-events.1", "docket-workspace-reindex.1"} {
		data, err := os.ReadFile(filepath.Join(dir, page))
		if err != nil {
			t.Fatalf("missing %s: %v", page, err)
		}
		if !strings.HasPrefix(string(data), ".nh\n.TH ") || strings.Contains(string(data), "Auto generated") {
			t.Fatalf("%s is not a clean roff page:\n%.200s", page, data)
		}
	}
	for _, hidden := range []string{"docket-man.1", "docket-__lua-hook.1", "docket-help.1", "docket-completion.1"} {
		if _, err := os.Stat(filepath.Join(dir, hidden)); err == nil {
			t.Fatalf("hidden or runtime command page %s was generated", hidden)
		}
	}
}

func TestManPrintsRootPageToStdout(t *testing.T) {
	root := newRootCmd()
	root.SetArgs([]string{"man"})
	var output bytes.Buffer
	root.SetOut(&output)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if text := output.String(); !strings.Contains(text, `.TH "DOCKET" "1"`) || !strings.Contains(text, "docket-show(1)") {
		t.Fatalf("unexpected root page:\n%.400s", text)
	}
}

func TestIndentBlocksForMarkdownKeepsRelativeAlignment(t *testing.T) {
	got := indentBlocksForMarkdown("Intro:\n  a  one\n     two\n\nOutro\n      deep")
	want := "Intro:\n    a  one\n       two\n\nOutro\n      deep"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}
