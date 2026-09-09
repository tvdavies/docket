package pluginmgr_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/handlers"
	"github.com/tvdavies/docket/internal/plugin"
	"github.com/tvdavies/docket/internal/pluginmgr"
	"github.com/tvdavies/docket/internal/registry"
	"github.com/tvdavies/docket/internal/workspace"
	"gopkg.in/yaml.v3"
)

func TestAddAndEnableAdoptsLegacyCursorAtomically(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "registry.yaml")
	t.Setenv("DOCKET_CONFIG", configPath)
	pluginRoot := t.TempDir()
	manifest := `
name: example
version: 1.0.0
handlers:
  notify: {on: [task.moved], run: hooks/notify}
statuses:
  - {name: merge, after: review}
`
	if err := os.WriteFile(filepath.Join(pluginRoot, plugin.ManifestFile), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	entry, err := pluginmgr.Add(pluginRoot, "", "dev")
	if err != nil || entry.Source.Type != "local" {
		t.Fatalf("add = %#v, %v", entry, err)
	}
	project := t.TempDir()
	ws, err := workspace.Init(project)
	if err != nil {
		t.Fatal(err)
	}
	ws.Config.Statuses = []string{"todo", "review", "merge", "done"}
	ws.Config.Handlers = map[string]workspace.HandlerConfig{"notify": {On: []string{"task.moved"}, Run: "hooks/notify"}}
	data, _ := yaml.Marshal(ws.Config)
	if err := os.WriteFile(filepath.Join(ws.Root, "config.yaml"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := events.Append(ws, events.Event{Type: events.TaskMoved, Task: "TASK-0001"}); err != nil {
		t.Fatal(err)
	}
	if err := handlers.SeedCursorAtEnd(ws, "notify"); err != nil {
		t.Fatal(err)
	}
	if err := pluginmgr.Enable(project, "example", nil, true, false, "dev"); err != nil {
		t.Fatal(err)
	}
	opened, err := workspace.OpenRoot(project)
	if err != nil {
		t.Fatal(err)
	}
	if _, legacyStillConfigured := opened.DeclaredConfig.Handlers["notify"]; legacyStillConfigured {
		t.Fatal("legacy handler remained configured")
	}
	if handlers.Cursor(opened, "notify") != handlers.Cursor(opened, "example/notify") {
		t.Fatal("adopted cursor position changed")
	}
	if strings.Join(opened.Config.Statuses, ",") != "todo,review,merge,done" {
		t.Fatalf("composed statuses = %v", opened.Config.Statuses)
	}
	if err := pluginmgr.Disable(project, "example"); err != nil {
		t.Fatal(err)
	}
	if err := pluginmgr.Enable(project, "example", nil, false, true, "dev"); err != nil {
		t.Fatal(err)
	}
	replayed, err := workspace.OpenRoot(project)
	if err != nil {
		t.Fatal(err)
	}
	if handlers.Cursor(replayed, "example/notify") != 0 {
		t.Fatal("--from-start did not reset the cursor")
	}
	if _, err := os.Stat(filepath.Join(replayed.HandlerStateDir(), "example", "notify.cursor")); err != nil {
		t.Fatalf("explicit zero cursor marker missing: %v", err)
	}
}

func TestEnableAdoptionWaitsForInFlightLegacyHandler(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "registry.yaml")
	t.Setenv("DOCKET_CONFIG", configPath)
	pluginRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(pluginRoot, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "deliveries.txt")
	pluginScript := "#!/bin/sh\ncat >/dev/null\nprintf 'plugin\\n' >> " + shellQuote(output) + "\n"
	if err := os.WriteFile(filepath.Join(pluginRoot, "hooks", "notify"), []byte(pluginScript), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "name: example\nversion: 1.0.0\nhandlers:\n  notify: {on: [task.moved], run: hooks/notify}\n"
	if err := os.WriteFile(filepath.Join(pluginRoot, plugin.ManifestFile), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := pluginmgr.Add(pluginRoot, "", "dev"); err != nil {
		t.Fatal(err)
	}

	project := t.TempDir()
	ws, err := workspace.Init(project)
	if err != nil {
		t.Fatal(err)
	}
	started := filepath.Join(project, "legacy-started")
	release := filepath.Join(project, "legacy-release")
	if err := os.MkdirAll(filepath.Join(project, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	legacyScript := "#!/bin/sh\nprintf 'legacy\\n' >> " + shellQuote(output) + "\ntouch " + shellQuote(started) + "\nwhile [ ! -f " + shellQuote(release) + " ]; do sleep 0.01; done\n"
	if err := os.WriteFile(filepath.Join(project, "hooks", "notify"), []byte(legacyScript), 0o755); err != nil {
		t.Fatal(err)
	}
	ws.Config.Handlers = map[string]workspace.HandlerConfig{"notify": {On: []string{events.TaskMoved}, Run: "hooks/notify"}}
	if err := workspace.WriteDeclaredConfig(ws.Root, ws.Config); err != nil {
		t.Fatal(err)
	}
	if err := events.Append(ws, events.Event{Type: events.TaskMoved, Task: "TASK-0001"}); err != nil {
		t.Fatal(err)
	}
	legacy, err := workspace.OpenRoot(project)
	if err != nil {
		t.Fatal(err)
	}
	drainDone := make(chan []handlers.Failure, 1)
	go func() { drainDone <- handlers.DrainAll(legacy, handlers.Options{RefreshConfig: true}) }()
	waitFile(t, started)

	enableDone := make(chan error, 1)
	go func() { enableDone <- pluginmgr.Enable(project, "example", nil, true, false, "dev") }()
	select {
	case err := <-enableDone:
		t.Fatalf("adoption completed while legacy handler was in flight: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := os.WriteFile(release, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if failures := <-drainDone; len(failures) != 0 {
		t.Fatalf("legacy drain failed: %v", failures)
	}
	if err := <-enableDone; err != nil {
		t.Fatal(err)
	}

	opened, err := workspace.OpenRoot(project)
	if err != nil {
		t.Fatal(err)
	}
	if handlers.Cursor(opened, "notify") != 1 || handlers.Cursor(opened, "example/notify") != 1 {
		t.Fatalf("adopted cursors = legacy %d plugin %d", handlers.Cursor(opened, "notify"), handlers.Cursor(opened, "example/notify"))
	}
	if err := events.Append(opened, events.Event{Type: events.TaskMoved, Task: "TASK-0002"}); err != nil {
		t.Fatal(err)
	}
	if failures := handlers.DrainAll(opened, handlers.Options{RefreshConfig: true}); len(failures) != 0 {
		t.Fatalf("plugin drain failed: %v", failures)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "legacy\nplugin\n" {
		t.Fatalf("deliveries = %q", data)
	}
}

func TestDisableRecoversWorkspaceAfterPluginIsUnregistered(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "registry.yaml")
	t.Setenv("DOCKET_CONFIG", configPath)
	pluginRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(pluginRoot, plugin.ManifestFile), []byte("name: example\nversion: 1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := pluginmgr.Add(pluginRoot, "", "dev"); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	if _, err := workspace.Init(project); err != nil {
		t.Fatal(err)
	}
	if err := pluginmgr.Enable(project, "example", nil, false, false, "dev"); err != nil {
		t.Fatal(err)
	}
	if _, err := pluginmgr.Remove("example"); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.OpenRoot(project); err == nil {
		t.Fatal("workspace unexpectedly remained available with missing plugin")
	}
	if err := pluginmgr.Disable(project, "example"); err != nil {
		t.Fatal(err)
	}
	declared, err := workspace.LoadDeclaredRoot(project)
	if err != nil {
		t.Fatal(err)
	}
	if _, enabled := declared.Plugins.Values["example"]; enabled {
		t.Fatal("plugin remained enabled after recovery")
	}
}

func TestGitUpdateRejectsInvalidCandidateAndKeepsPriorVersion(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	configPath := filepath.Join(t.TempDir(), "registry.yaml")
	t.Setenv("DOCKET_CONFIG", configPath)
	t.Setenv("DOCKET_PLUGIN_DIR", filepath.Join(t.TempDir(), "managed"))
	repository := t.TempDir()
	git(t, repository, "init", "-b", "main")
	git(t, repository, "config", "user.email", "test@example.com")
	git(t, repository, "config", "user.name", "Test")
	writePluginVersion(t, repository, "1.0.0", "")
	git(t, repository, "add", plugin.ManifestFile)
	git(t, repository, "commit", "-m", "v1")
	git(t, repository, "tag", "v1.0.0")

	entry, err := pluginmgr.Add("file://"+repository, "", "dev")
	if err != nil || entry.Version != "1.0.0" {
		t.Fatalf("git add = %#v, %v", entry, err)
	}
	writePluginVersion(t, repository, "1.1.0", "unknown_key: true\n")
	git(t, repository, "add", plugin.ManifestFile)
	git(t, repository, "commit", "-m", "bad v1.1")
	git(t, repository, "tag", "v1.1.0")
	if _, err := pluginmgr.Update("example", "dev"); err == nil {
		t.Fatal("invalid candidate unexpectedly activated")
	}
	manifest, err := plugin.Load(entry.Path, "dev")
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Version != "1.0.0" {
		t.Fatalf("active version = %s", manifest.Version)
	}
	config, err := registry.Load()
	if err != nil {
		t.Fatal(err)
	}
	if config.Plugins[0].Version != "1.0.0" {
		t.Fatalf("registry version changed: %#v", config.Plugins[0])
	}

	writePluginVersion(t, repository, "1.2.0", "")
	git(t, repository, "add", plugin.ManifestFile)
	git(t, repository, "commit", "-m", "good v1.2")
	git(t, repository, "tag", "v1.2.0")

	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	wrapperDir := t.TempDir()
	cloneReady := filepath.Join(wrapperDir, "clone-ready")
	cloneRelease := filepath.Join(wrapperDir, "clone-release")
	wrapper := "#!/bin/sh\nif [ \"$1\" = clone ]; then\n  \"$REAL_GIT\" \"$@\" || exit $?\n  touch \"$CLONE_READY\"\n  while [ ! -f \"$CLONE_RELEASE\" ]; do sleep 0.01; done\n  exit 0\nfi\nexec \"$REAL_GIT\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(wrapperDir, "git"), []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REAL_GIT", realGit)
	t.Setenv("CLONE_READY", cloneReady)
	t.Setenv("CLONE_RELEASE", cloneRelease)
	t.Setenv("PATH", wrapperDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	updateDone := make(chan struct {
		entries []registry.PluginEntry
		err     error
	}, 1)
	go func() {
		entries, err := pluginmgr.Update("example", "dev")
		updateDone <- struct {
			entries []registry.PluginEntry
			err     error
		}{entries: entries, err: err}
	}()
	waitFile(t, cloneReady)
	if err := registry.Update(func(config *registry.Config) error {
		config.Plugins[0].Config = map[string]any{"marker": "kept"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cloneRelease, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := <-updateDone
	updated, err := result.entries, result.err
	if err != nil || len(updated) != 1 || updated[0].Version != "1.2.0" || updated[0].Path == entry.Path {
		t.Fatalf("successful update = %#v, %v", updated, err)
	}
	if updated[0].Config["marker"] != "kept" {
		t.Fatalf("update lost concurrent instance config: %#v", updated[0].Config)
	}
	if _, err := os.Stat(entry.Path); !os.IsNotExist(err) {
		t.Fatalf("prior clone remains active at %s: %v", entry.Path, err)
	}
	if manifest, err := plugin.Load(updated[0].Path, "dev"); err != nil || manifest.Version != "1.2.0" {
		t.Fatalf("updated manifest = %#v, %v", manifest, err)
	}
}

func waitFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("file %s was not created", path)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func writePluginVersion(t *testing.T, root, version, extra string) {
	t.Helper()
	body := "name: example\nversion: " + version + "\nconfig:\n  instance:\n    marker: {type: string}\n" + extra
	if err := os.WriteFile(filepath.Join(root, plugin.ManifestFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func git(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
}
