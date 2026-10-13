package registry_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tvdavies/docket/internal/registry"
	"github.com/tvdavies/docket/internal/workspace"
)

// resolveFixture registers two workspaces, alpha and beta, and returns their
// project directories plus a directory outside any workspace.
func resolveFixture(t *testing.T) (alpha, beta, outside string) {
	t.Helper()
	t.Setenv("DOCKET_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	t.Setenv("DOCKET_HOME", "")
	t.Setenv(registry.EnvWorkspace, "")
	base := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(base); err == nil {
		base = resolved
	}
	alpha, beta, outside = filepath.Join(base, "alpha"), filepath.Join(base, "beta"), filepath.Join(base, "outside")
	for _, project := range []string{alpha, beta} {
		if err := os.MkdirAll(filepath.Join(project, "src"), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := workspace.Init(project); err != nil {
			t.Fatal(err)
		}
		if _, err := registry.Add(project, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	return alpha, beta, outside
}

func mustResolve(t *testing.T, selector string) registry.Resolution {
	t.Helper()
	resolution, err := registry.Resolve(selector)
	if err != nil {
		t.Fatalf("resolve %q: %v", selector, err)
	}
	return resolution
}

func TestResolvePrecedenceFlagEnvCwdContext(t *testing.T) {
	alpha, beta, outside := resolveFixture(t)
	if _, err := registry.Use("beta"); err != nil {
		t.Fatal(err)
	}

	// Inside alpha, the working directory beats the saved context.
	t.Chdir(filepath.Join(alpha, "src"))
	if got := mustResolve(t, ""); got.Source != registry.SourceCwd || got.Project != alpha {
		t.Fatalf("cwd resolution = %#v", got)
	}

	// DOCKET_WORKSPACE beats the working directory.
	t.Setenv(registry.EnvWorkspace, "beta")
	if got := mustResolve(t, ""); got.Source != registry.SourceEnv || got.Variable != registry.EnvWorkspace || got.Name != "beta" || got.Project != beta {
		t.Fatalf("env resolution = %#v", got)
	}

	// The flag beats the environment.
	if got := mustResolve(t, "alpha"); got.Source != registry.SourceFlag || got.Name != "alpha" || got.Root != filepath.Join(alpha, workspace.DirName) {
		t.Fatalf("flag resolution = %#v", got)
	}

	// DOCKET_HOME pins handler and plugin subprocesses ahead of DOCKET_WORKSPACE.
	t.Setenv("DOCKET_HOME", alpha)
	if got := mustResolve(t, ""); got.Source != registry.SourceEnv || got.Variable != "DOCKET_HOME" || got.Project != alpha {
		t.Fatalf("DOCKET_HOME resolution = %#v", got)
	}
	t.Setenv("DOCKET_HOME", "")
	t.Setenv(registry.EnvWorkspace, "")

	// Outside any workspace the saved context applies.
	t.Chdir(outside)
	if got := mustResolve(t, ""); got.Source != registry.SourceContext || got.Name != "beta" || got.Project != beta {
		t.Fatalf("context resolution = %#v", got)
	}

	// With no context, the error explains both ways to select a workspace.
	if _, err := registry.ClearCurrent(); err != nil {
		t.Fatal(err)
	}
	_, err := registry.Resolve("")
	if !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	for _, hint := range []string{"-w NAME", "docket workspace use NAME"} {
		if !strings.Contains(err.Error(), hint) {
			t.Fatalf("error %q does not mention %q", err, hint)
		}
	}
}

func TestResolveSelectorAcceptsPaths(t *testing.T) {
	alpha, _, outside := resolveFixture(t)
	t.Chdir(outside)
	for _, selector := range []string{alpha, filepath.Join(alpha, "src"), filepath.Join(alpha, workspace.DirName)} {
		got := mustResolve(t, selector)
		if got.Project != alpha || got.Name != "alpha" || got.Source != registry.SourceFlag {
			t.Fatalf("resolve %s = %#v", selector, got)
		}
	}

	// A registered name wins over a same-named directory; ./NAME is the path.
	// An unregistered directory is accepted by bare relative name too.
	for _, dir := range []string{"alpha", "other"} {
		if _, err := workspace.Init(filepath.Join(outside, dir)); err != nil {
			t.Fatal(err)
		}
	}
	if got := mustResolve(t, "alpha"); got.Name != "alpha" || got.Project != alpha {
		t.Fatalf("name selector = %#v", got)
	}
	if got := mustResolve(t, "./alpha"); got.Name != "" || got.Project != filepath.Join(outside, "alpha") {
		t.Fatalf("./NAME selector = %#v", got)
	}
	if got := mustResolve(t, "other"); got.Name != "" || got.Project != filepath.Join(outside, "other") {
		t.Fatalf("unregistered directory selector = %#v", got)
	}
}

func TestResolveUnknownAndStaleSelectors(t *testing.T) {
	_, beta, outside := resolveFixture(t)
	t.Chdir(outside)
	if _, err := registry.Resolve("nope"); err == nil || !strings.Contains(err.Error(), `unknown workspace "nope"`) {
		t.Fatalf("unknown name error = %v", err)
	}
	t.Setenv(registry.EnvWorkspace, "nope")
	if _, err := registry.Resolve(""); err == nil || !strings.Contains(err.Error(), `unknown workspace "nope"`) {
		t.Fatalf("unknown env error = %v", err)
	}
	t.Setenv(registry.EnvWorkspace, "")
	if _, err := registry.Resolve(filepath.Join(outside, "missing")); err == nil {
		t.Fatal("missing path resolved")
	}

	// A context whose registration disappeared is reported, not ignored.
	if err := registry.Update(func(config *registry.Config) error {
		config.CurrentWorkspace = "ghost"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Resolve(""); err == nil || !strings.Contains(err.Error(), `"ghost" is no longer registered`) {
		t.Fatalf("stale context error = %v", err)
	}

	// A registered workspace whose directory is gone fails with its path.
	if err := os.RemoveAll(beta); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Resolve("beta"); err == nil || !strings.Contains(err.Error(), beta) {
		t.Fatalf("missing registered workspace error = %v", err)
	}
}

func TestUseAndClearPersistContext(t *testing.T) {
	alpha, _, outside := resolveFixture(t)
	t.Chdir(outside)

	entry, err := registry.Use("beta")
	if err != nil || entry.Name != "beta" {
		t.Fatalf("use beta = %#v, %v", entry, err)
	}
	config, err := registry.Load()
	if err != nil || config.CurrentWorkspace != "beta" {
		t.Fatalf("persisted context = %q, %v", config.CurrentWorkspace, err)
	}

	// A path selects the registered workspace containing it.
	if entry, err := registry.Use(filepath.Join(alpha, "src")); err != nil || entry.Name != "alpha" {
		t.Fatalf("use path = %#v, %v", entry, err)
	}
	if _, err := registry.Use("nope"); err == nil || !strings.Contains(err.Error(), `unknown workspace "nope"`) {
		t.Fatalf("use unknown error = %v", err)
	}
	if config, _ := registry.Load(); config.CurrentWorkspace != "alpha" {
		t.Fatalf("failed use changed the context to %q", config.CurrentWorkspace)
	}

	previous, err := registry.ClearCurrent()
	if err != nil || previous != "alpha" {
		t.Fatalf("clear = %q, %v", previous, err)
	}
	if config, _ := registry.Load(); config.CurrentWorkspace != "" {
		t.Fatalf("context remains after clear: %q", config.CurrentWorkspace)
	}

	// Unregistering the selected workspace also clears the context.
	if _, err := registry.Use("beta"); err != nil {
		t.Fatal(err)
	}
	if removed, err := registry.Remove("beta"); err != nil || !removed {
		t.Fatalf("remove = %v, %v", removed, err)
	}
	if config, _ := registry.Load(); config.CurrentWorkspace != "" {
		t.Fatalf("context names removed workspace: %q", config.CurrentWorkspace)
	}
}
