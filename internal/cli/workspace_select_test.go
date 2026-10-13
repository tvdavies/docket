package cli_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// selectFixture is two registered workspaces (alpha, beta) and a directory
// outside both, sharing one isolated registry.
type selectFixture struct {
	alpha, beta, outside, config string
}

func newSelectFixture(t *testing.T) selectFixture {
	t.Helper()
	base := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(base); err == nil {
		base = resolved
	}
	f := selectFixture{
		alpha: filepath.Join(base, "alpha"), beta: filepath.Join(base, "beta"),
		outside: filepath.Join(base, "outside"), config: filepath.Join(base, "registry.yaml"),
	}
	for _, dir := range []string{f.alpha, f.beta, f.outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, project := range []string{f.alpha, f.beta} {
		if _, stderr, err := f.run(t, project, nil, "init"); err != nil {
			t.Fatalf("init %s: %v: %s", project, err, stderr)
		}
	}
	return f
}

func (f selectFixture) run(t *testing.T, dir string, env []string, args ...string) (string, string, error) {
	t.Helper()
	command := exec.Command(docketBinary(t), args...)
	command.Dir = dir
	command.Env = append(os.Environ(), "DOCKET_HOME=", "DOCKET_WORKSPACE=", "DOCKET_CONFIG="+f.config)
	command.Env = append(command.Env, env...)
	var stdout, stderr strings.Builder
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return stdout.String(), stderr.String(), err
}

func (f selectFixture) mustRun(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	out, stderr, err := f.run(t, dir, env, args...)
	if err != nil {
		t.Fatalf("docket %v: %v: %s", args, err, stderr)
	}
	return out
}

type currentJSON struct {
	Name, Path, Root, Source, Selector, Variable string
}

func (f selectFixture) current(t *testing.T, dir string, env []string, args ...string) currentJSON {
	t.Helper()
	var got currentJSON
	out := f.mustRun(t, dir, env, append(args, "workspace", "current", "--json")...)
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	return got
}

func TestWorkspaceSelectionOrderFromCLI(t *testing.T) {
	f := newSelectFixture(t)

	if got := f.current(t, f.alpha, nil); got.Source != "cwd" || got.Name != "alpha" || got.Path != f.alpha {
		t.Fatalf("cwd = %#v", got)
	}
	if got := f.current(t, f.alpha, []string{"DOCKET_WORKSPACE=beta"}); got.Source != "env" || got.Variable != "DOCKET_WORKSPACE" || got.Name != "beta" {
		t.Fatalf("env = %#v", got)
	}
	if got := f.current(t, f.alpha, []string{"DOCKET_WORKSPACE=beta"}, "-w", "alpha"); got.Source != "flag" || got.Name != "alpha" {
		t.Fatalf("flag = %#v", got)
	}
	if got := f.current(t, f.outside, nil, "--workspace", f.beta); got.Source != "flag" || got.Name != "beta" || got.Path != f.beta {
		t.Fatalf("flag path = %#v", got)
	}

	// Outside any workspace with nothing selected, the error names both fixes.
	_, stderr, err := f.run(t, f.outside, nil, "list")
	if err == nil || !strings.Contains(stderr, "-w NAME") || !strings.Contains(stderr, "docket workspace use NAME") {
		t.Fatalf("unselected list: err=%v stderr=%s", err, stderr)
	}

	// The saved context applies outside a workspace but never inside one.
	f.mustRun(t, f.outside, nil, "workspace", "use", "beta")
	if got := f.current(t, f.outside, nil); got.Source != "context" || got.Name != "beta" {
		t.Fatalf("context = %#v", got)
	}
	if got := f.current(t, f.alpha, nil); got.Source != "cwd" || got.Name != "alpha" {
		t.Fatalf("cwd with context = %#v", got)
	}
	human := f.mustRun(t, f.outside, nil, "workspace", "current")
	if !strings.Contains(human, "Workspace: beta") || !strings.Contains(human, "Source: context") {
		t.Fatalf("human current = %q", human)
	}

	// Commands act on the selected workspace from anywhere.
	f.mustRun(t, f.outside, nil, "-w", "alpha", "new", "--title", "In alpha")
	f.mustRun(t, f.outside, nil, "new", "--title", "In beta")
	if out := f.mustRun(t, f.outside, nil, "list", "-w", "alpha"); !strings.Contains(out, "In alpha") || strings.Contains(out, "In beta") {
		t.Fatalf("alpha list = %q", out)
	}
	if out := f.mustRun(t, f.outside, nil, "list"); !strings.Contains(out, "In beta") || strings.Contains(out, "In alpha") {
		t.Fatalf("context list = %q", out)
	}

	// init keeps working on its own directory despite the saved context.
	fresh := filepath.Join(f.outside, "fresh")
	if err := os.Mkdir(fresh, 0o755); err != nil {
		t.Fatal(err)
	}
	f.mustRun(t, fresh, nil, "init")
	if _, err := os.Stat(filepath.Join(fresh, ".docket", "config.yaml")); err != nil {
		t.Fatalf("init did not create its own workspace: %v", err)
	}

	f.mustRun(t, f.outside, nil, "workspace", "use", "--clear")
	if _, _, err := f.run(t, f.outside, nil, "workspace", "current"); err == nil {
		t.Fatal("current succeeded after clearing the context")
	}
}

func TestWorkspaceUnknownNamesFail(t *testing.T) {
	f := newSelectFixture(t)
	for _, args := range [][]string{
		{"-w", "nope", "list"},
		{"workspace", "use", "nope"},
		{"workspace", "path", "nope"},
	} {
		_, stderr, err := f.run(t, f.outside, nil, args...)
		if err == nil || !strings.Contains(stderr, `unknown workspace "nope"`) {
			t.Fatalf("docket %v: err=%v stderr=%s", args, err, stderr)
		}
	}
	if _, _, err := f.run(t, f.outside, nil, "workspace", "use"); err == nil {
		t.Fatal("use without NAME or --clear succeeded")
	}
	if _, _, err := f.run(t, f.outside, nil, "workspace", "use", "--clear", "alpha"); err == nil {
		t.Fatal("use --clear with NAME succeeded")
	}
}

func TestWorkspacePathAndListMarkCurrent(t *testing.T) {
	f := newSelectFixture(t)
	if out := f.mustRun(t, f.outside, nil, "workspace", "path", "beta"); out != f.beta+"\n" {
		t.Fatalf("path = %q", out)
	}
	if out := f.mustRun(t, filepath.Join(f.alpha, ".docket", "tasks"), nil, "workspace", "path"); out != f.alpha+"\n" {
		t.Fatalf("path in effect = %q", out)
	}

	// Piped list stays tab-separated with NAME first; CURRENT is the last column.
	out := f.mustRun(t, f.alpha, nil, "workspace", "list")
	want := "NAME\tPATH\tCURRENT\nalpha\t" + f.alpha + "\t*\nbeta\t" + f.beta + "\t\n"
	if out != want {
		t.Fatalf("list = %q, want %q", out, want)
	}

	f.mustRun(t, f.outside, nil, "workspace", "use", "beta")
	var entries []struct {
		Name    string `json:"name"`
		Current bool   `json:"current"`
		Default bool   `json:"default"`
	}
	if err := json.Unmarshal([]byte(f.mustRun(t, f.alpha, nil, "workspace", "list", "--json")), &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || !entries[0].Current || entries[0].Default || entries[1].Current || !entries[1].Default {
		t.Fatalf("list json = %#v", entries)
	}
}

func TestWorkspaceCompletionListsRegisteredNames(t *testing.T) {
	f := newSelectFixture(t)
	for _, args := range [][]string{
		{"__complete", "-w", ""},
		{"__complete", "workspace", "use", ""},
		{"__complete", "workspace", "path", "b"},
	} {
		out := f.mustRun(t, f.outside, nil, args...)
		if !strings.Contains(out, "beta\t"+f.beta) {
			t.Fatalf("completion %v = %q", args, out)
		}
		if args[len(args)-1] == "" && !strings.Contains(out, "alpha\t") {
			t.Fatalf("completion %v missing alpha: %q", args, out)
		}
	}
}
