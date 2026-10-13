package cli

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
)

func TestPagerCommandHonoursPAGER(t *testing.T) {
	t.Setenv("PAGER", "")
	os.Unsetenv("PAGER")
	if pager, ok := pagerCommand(); !ok || pager != "less -R" {
		t.Fatalf("default pager = %q, %v", pager, ok)
	}
	t.Setenv("PAGER", "most -s")
	if pager, ok := pagerCommand(); !ok || pager != "most -s" {
		t.Fatalf("custom pager = %q, %v", pager, ok)
	}
	for _, disabled := range []string{"", "  ", "cat"} {
		t.Setenv("PAGER", disabled)
		if _, ok := pagerCommand(); ok {
			t.Fatalf("PAGER=%q should disable paging", disabled)
		}
	}
}

func TestDevNullIsNotATerminal(t *testing.T) {
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()
	stdin, stdout := os.Stdin, os.Stdout
	defer func() { os.Stdin, os.Stdout = stdin, stdout }()
	os.Stdin, os.Stdout = devNull, devNull
	if stdinIsTerminal() || stdoutIsTerminal() {
		t.Fatalf("%s counted as a terminal: stdin=%v stdout=%v", os.DevNull, stdinIsTerminal(), stdoutIsTerminal())
	}
}

// pageWith runs pageTo with PAGER set and content taller than the screen.
func pageWith(t *testing.T, pager, content string) (string, error) {
	t.Helper()
	t.Setenv("PAGER", pager)
	t.Setenv("TERM", "xterm")
	var out bytes.Buffer
	err := pageTo(&out, content, colorprofile.NoTTY, 2, false)
	return out.String(), err
}

func TestPagerFallsBackToStdoutWhenUnavailable(t *testing.T) {
	content := "one\ntwo\nthree\nfour\n"
	// Not installed: found before running anything.
	out, err := pageWith(t, "docket-no-such-pager -R", content)
	if err != nil || out != content {
		t.Fatalf("missing pager: out=%q err=%v", out, err)
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	// The shell cannot run it (exit 127/126): nothing was shown, so the
	// content is written directly.
	for _, pager := range []string{"sh -c 'exit 127'", "sh -c 'exit 126'"} {
		out, err := pageWith(t, pager, content)
		if err != nil || out != content {
			t.Fatalf("%s: out=%q err=%v", pager, out, err)
		}
	}
}

func TestPagerReportsFailureButNotEarlyQuit(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	big := strings.Repeat("line of task output\n", 20000) // beyond a pipe buffer
	// Reading part of the input and quitting, like q in less.
	out, err := pageWith(t, "head -c 4", big)
	if err != nil || out != "line" {
		t.Fatalf("early quit: out=%q err=%v", out, err)
	}
	// A pager that runs and fails is an error.
	if _, err := pageWith(t, "sh -c 'cat >/dev/null; exit 3'", big); err == nil || !strings.Contains(err.Error(), "status 3") {
		t.Fatalf("failing pager: err=%v", err)
	}
	// A working pager receives the content.
	if out, err := pageWith(t, "sh -c 'cat'", "a\nb\nc\nd\n"); err != nil || out != "a\nb\nc\nd\n" {
		t.Fatalf("working pager: out=%q err=%v", out, err)
	}
}
