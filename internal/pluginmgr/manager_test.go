package pluginmgr_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
	updated, err := pluginmgr.Update("example", "dev")
	if err != nil || len(updated) != 1 || updated[0].Version != "1.2.0" || updated[0].Path == entry.Path {
		t.Fatalf("successful update = %#v, %v", updated, err)
	}
	if _, err := os.Stat(entry.Path); !os.IsNotExist(err) {
		t.Fatalf("prior clone remains active at %s: %v", entry.Path, err)
	}
	if manifest, err := plugin.Load(updated[0].Path, "dev"); err != nil || manifest.Version != "1.2.0" {
		t.Fatalf("updated manifest = %#v, %v", manifest, err)
	}
}

func writePluginVersion(t *testing.T, root, version, extra string) {
	t.Helper()
	body := "name: example\nversion: " + version + "\n" + extra
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
