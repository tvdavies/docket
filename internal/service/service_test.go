package service_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/handlers"
	"github.com/tvdavies/docket/internal/plugin"
	"github.com/tvdavies/docket/internal/registry"
	"github.com/tvdavies/docket/internal/service"
	"github.com/tvdavies/docket/internal/workspace"
)

func createHandledWorkspace(t *testing.T) (string, *workspace.Workspace, string) {
	t.Helper()
	root := t.TempDir()
	ws, err := workspace.Init(root)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "handled.jsonl")
	if err := os.MkdirAll(filepath.Join(root, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\ncat >> " + shellQuote(output) + "\n"
	if err := os.WriteFile(filepath.Join(root, "hooks", "record"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(ws.Root, "config.yaml")
	file, err := os.OpenFile(configPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("handlers:\n  record:\n    on: [task.created]\n    run: hooks/record\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return root, ws, output
}

func TestManagerWatchesWorkspaceAndDrainsHandlers(t *testing.T) {
	root, ws, output := createHandledWorkspace(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := service.NewManager(ctx, io.Discard)
	defer manager.Stop()
	manager.SetWorkspaces([]registry.WorkspaceEntry{{Name: "test", Path: root}})
	waitFor(t, func() bool {
		statuses := manager.Statuses()
		return len(statuses) == 1 && statuses[0].State == "watching"
	})

	if err := events.Append(ws, events.Event{Type: events.TaskCreated, Task: "TASK-0001"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		data, err := os.ReadFile(output)
		statuses := manager.Statuses()
		return err == nil && strings.Contains(string(data), `"task":"TASK-0001"`) &&
			len(statuses) == 1 && statuses[0].EventCount == 1 && statuses[0].HandlerCount == 1
	})
	statuses := manager.Statuses()
	if statuses[0].EventCount != 1 || statuses[0].HandlerCount != 1 {
		t.Fatalf("unexpected status: %#v", statuses[0])
	}
}

func TestManagerCancellationStopsRunningHandler(t *testing.T) {
	root, ws, _ := createHandledWorkspace(t)
	startedFile := filepath.Join(root, "handler-started")
	script := "#!/bin/sh\ntouch " + shellQuote(startedFile) + "\nsleep 10\n"
	if err := os.WriteFile(filepath.Join(root, "hooks", "record"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := events.Append(ws, events.Event{Type: events.TaskCreated, Task: "TASK-0001"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := service.NewManager(ctx, io.Discard)
	manager.SetWorkspaces([]registry.WorkspaceEntry{{Name: "test", Path: root}})
	waitFor(t, func() bool { _, err := os.Stat(startedFile); return err == nil })

	started := time.Now()
	manager.Stop()
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("manager shutdown waited %s for cancelled handler", elapsed)
	}
}

func TestManagerPinsRegisteredWorkspaceAndRecoversAfterRecreation(t *testing.T) {
	parent := t.TempDir()
	if _, err := workspace.Init(parent); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(parent, "child")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	childWS, err := workspace.Init(child)
	if err != nil {
		t.Fatal(err)
	}
	if err := events.Append(childWS, events.Event{Type: events.TaskCreated, Task: "child"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := service.NewManager(ctx, io.Discard)
	defer manager.Stop()
	manager.SetWorkspaces([]registry.WorkspaceEntry{{Name: "child", Path: child}})
	waitFor(t, func() bool {
		statuses := manager.Statuses()
		return len(statuses) == 1 && statuses[0].State == "watching"
	})

	if err := os.RemoveAll(filepath.Join(child, workspace.DirName)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		statuses := manager.Statuses()
		return len(statuses) == 1 && statuses[0].State == "unavailable"
	})
	if _, err := workspace.Init(child); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		statuses := manager.Statuses()
		return len(statuses) == 1 && statuses[0].State == "watching"
	})
}

func TestManagerReloadsHandlerConfigWithoutNewEvent(t *testing.T) {
	root := t.TempDir()
	ws, err := workspace.Init(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := events.Append(ws, events.Event{Type: events.TaskCreated, Task: "backlog"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := service.NewManager(ctx, io.Discard)
	defer manager.Stop()
	manager.SetWorkspaces([]registry.WorkspaceEntry{{Name: "test", Path: root}})
	waitFor(t, func() bool {
		statuses := manager.Statuses()
		return len(statuses) == 1 && statuses[0].State == "watching" && statuses[0].HandlerCount == 0
	})

	output := filepath.Join(root, "config-delivery.jsonl")
	if err := os.MkdirAll(filepath.Join(root, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "hooks", "record"), []byte("#!/bin/sh\ncat >> "+shellQuote(output)+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(ws.Root, "config.yaml")
	file, err := os.OpenFile(configPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("handlers:\n  record:\n    on: [task.created]\n    run: hooks/record\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		data, readErr := os.ReadFile(output)
		statuses := manager.Statuses()
		return readErr == nil && strings.Contains(string(data), `"task":"backlog"`) && len(statuses) == 1 && statuses[0].HandlerCount == 1
	})
}

func TestManagerReconcilesWorkspaceSet(t *testing.T) {
	root, _, _ := createHandledWorkspace(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := service.NewManager(ctx, io.Discard)
	defer manager.Stop()
	manager.SetWorkspaces([]registry.WorkspaceEntry{{Name: "test", Path: root}})
	waitFor(t, func() bool { return len(manager.Statuses()) == 1 })
	manager.SetWorkspaces(nil)
	waitFor(t, func() bool { return len(manager.Statuses()) == 0 })
}

func TestHotReloadSeedsNewPluginHandlerWithoutHistoricalReplay(t *testing.T) {
	project := t.TempDir()
	ws, err := workspace.Init(project)
	if err != nil {
		t.Fatal(err)
	}
	pluginRoot := t.TempDir()
	output := filepath.Join(project, "new-handler-events.jsonl")
	if err := os.MkdirAll(filepath.Join(pluginRoot, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, "hooks", "record"), []byte("#!/bin/sh\ncat >> "+shellQuote(output)+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writePluginManifest(t, pluginRoot, "task.created", "")
	configPath := filepath.Join(t.TempDir(), "registry.yaml")
	t.Setenv("DOCKET_CONFIG", configPath)
	writeRegistryFixture(t, configPath, project, pluginRoot)
	appendPluginUse(t, ws)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := service.NewManager(ctx, io.Discard)
	defer manager.Stop()
	go manager.FollowRegistry(ctx, 20*time.Millisecond)
	waitFor(t, func() bool { return len(manager.Statuses()) == 1 && manager.Statuses()[0].State == "watching" })
	opened, err := workspace.OpenRoot(project)
	if err != nil {
		t.Fatal(err)
	}
	if err := events.Append(opened, events.Event{Type: events.TaskCreated, Task: "TASK-0001"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return handlers.Cursor(opened, "example/record") == 1 })

	manifest := `
name: example
version: 1.0.1
handlers:
  record: {on: [task.created], run: hooks/record, delivery: service}
  added: {on: [task.created], run: hooks/record, delivery: service}
`
	if err := os.WriteFile(filepath.Join(pluginRoot, plugin.ManifestFile), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		statuses := manager.Statuses()
		return len(statuses) == 1 && statuses[0].State == "watching" && statuses[0].HandlerCount == 2 && handlers.Cursor(opened, "example/added") == 1
	})
	before, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(strings.TrimSpace(string(before)), "\n") != 0 {
		t.Fatalf("new handler replayed history: %s", before)
	}
	if err := events.Append(opened, events.Event{Type: events.TaskCreated, Task: "TASK-0002"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		return handlers.Cursor(opened, "example/added") == 2 && handlers.Cursor(opened, "example/record") == 2
	})
	after, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(strings.TrimSpace(string(after)), "\n") != 2 {
		t.Fatalf("future event was not delivered once per handler: %s", after)
	}
}

func TestPluginManifestHotReloadPreservesCursorAndRecomposesHandler(t *testing.T) {
	project := t.TempDir()
	ws, err := workspace.Init(project)
	if err != nil {
		t.Fatal(err)
	}
	pluginRoot := t.TempDir()
	output := filepath.Join(project, "plugin-events.jsonl")
	if err := os.MkdirAll(filepath.Join(pluginRoot, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, "hooks", "record"), []byte("#!/bin/sh\ncat >> "+shellQuote(output)+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writePluginManifest(t, pluginRoot, "task.created", "")
	configPath := filepath.Join(t.TempDir(), "registry.yaml")
	t.Setenv("DOCKET_CONFIG", configPath)
	writeRegistryFixture(t, configPath, project, pluginRoot)
	appendPluginUse(t, ws)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := service.NewManager(ctx, io.Discard)
	defer manager.Stop()
	go manager.FollowRegistry(ctx, 20*time.Millisecond)
	waitFor(t, func() bool { return len(manager.Statuses()) == 1 && manager.Statuses()[0].State == "watching" })
	opened, err := workspace.OpenRoot(project)
	if err != nil {
		t.Fatal(err)
	}
	if err := events.Append(opened, events.Event{Type: events.TaskCreated, Task: "TASK-0001"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return handlers.Cursor(opened, "example/record") == 1 })

	writePluginManifest(t, pluginRoot, "task.commented", "description: reloaded\n")
	if err := events.Append(opened, events.Event{Type: events.TaskMoved, Task: "TASK-0001"}); err != nil {
		t.Fatal(err)
	}
	if err := events.Append(opened, events.Event{Type: events.TaskCommented, Task: "TASK-0001"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return handlers.Cursor(opened, "example/record") == 3 })
	lines, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(strings.TrimSpace(string(lines)), "\n") != 1 || !strings.Contains(string(lines), `"type":"task.commented"`) {
		t.Fatalf("hot reload deliveries = %s", lines)
	}
}

func TestSystemdUnitUsesOneMultiWorkspaceService(t *testing.T) {
	unit := service.BuildSystemdUnit("/home/tom/bin/docket", "/home/tom/.config/docket/config.yaml", "/usr/bin:/bin")
	for _, want := range []string{
		`ExecStart="/home/tom/bin/docket" run --all`,
		`Environment="DOCKET_CONFIG=/home/tom/.config/docket/config.yaml"`,
		`EnvironmentFile=-%h/.config/docket/environment`,
		`WantedBy=default.target`,
	} {
		if !strings.Contains(unit, want) {
			t.Fatalf("unit missing %q:\n%s", want, unit)
		}
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func writePluginManifest(t *testing.T, root, eventType, extra string) {
	t.Helper()
	body := "name: example\nversion: 1.0.0\n" + extra + "handlers:\n  record: {on: [" + eventType + "], run: hooks/record, delivery: service}\n"
	if err := os.WriteFile(filepath.Join(root, plugin.ManifestFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeRegistryFixture(t *testing.T, path, project, pluginRoot string) {
	t.Helper()
	body := "workspaces:\n  - {name: test, path: " + project + "}\nplugins:\n  - name: example\n    path: " + pluginRoot + "\n    source: {type: local}\n    version: 1.0.0\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendPluginUse(t *testing.T, ws *workspace.Workspace) {
	t.Helper()
	file, err := os.OpenFile(filepath.Join(ws.Root, "config.yaml"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("plugins:\n  example: {}\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestFollowRegistryPrunesLongMissingWorkspaces(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("DOCKET_CONFIG", configPath)
	root, _, _ := createHandledWorkspace(t)
	ghost := filepath.Join(t.TempDir(), "gone")
	registryYAML := "listen: 127.0.0.1:7463\nprune_after: 40ms\nworkspaces:\n" +
		"    - name: alive\n      path: " + root + "\n" +
		"    - name: ghost\n      path: " + ghost + "\n"
	if err := os.WriteFile(configPath, []byte(registryYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := service.NewManager(ctx, io.Discard)
	defer manager.Stop()
	go manager.FollowRegistry(ctx, 10*time.Millisecond)

	waitFor(t, func() bool {
		config, err := registry.Load()
		if err != nil || len(config.Workspaces) != 1 || config.Workspaces[0].Name != "alive" {
			return false
		}
		statuses := manager.Statuses()
		return len(statuses) == 1 && statuses[0].Name == "alive"
	})
}

func TestRunOnceDrainsBacklogAndLeavesFailuresPending(t *testing.T) {
	root, ws, output := createHandledWorkspace(t)
	if err := events.Append(ws, events.Event{Type: events.TaskCreated, Task: "TASK-0001"}); err != nil {
		t.Fatal(err)
	}
	entries := []registry.WorkspaceEntry{{Name: "test", Path: root}}
	results, err := service.RunOnce(context.Background(), entries, io.Discard, service.OnceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].EventCount != 1 || results[0].HandlerCount != 1 || results[0].Error != "" {
		t.Fatalf("results = %#v", results)
	}
	data, err := os.ReadFile(output)
	if err != nil || !strings.Contains(string(data), `"task":"TASK-0001"`) {
		t.Fatalf("handler output = %q, %v", data, err)
	}
	if handlers.Cursor(ws, "record") != 1 {
		t.Fatal("cursor did not advance after successful delivery")
	}

	// A failing handler keeps its batch pending and the drain reports it.
	if err := os.WriteFile(filepath.Join(root, "hooks", "record"), []byte("#!/bin/sh\ncat >/dev/null\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := events.Append(ws, events.Event{Type: events.TaskCreated, Task: "TASK-0002"}); err != nil {
		t.Fatal(err)
	}
	results, err = service.RunOnce(context.Background(), entries, io.Discard, service.OnceOptions{})
	if err == nil || len(results) != 1 || results[0].Error == "" {
		t.Fatalf("failing drain: results = %#v err = %v", results, err)
	}
	if handlers.Cursor(ws, "record") != 1 {
		t.Fatal("failed batch was acknowledged")
	}

	// Recovery on a later run delivers the pending event exactly once more.
	if err := os.WriteFile(filepath.Join(root, "hooks", "record"), []byte("#!/bin/sh\ncat >> "+shellQuote(output)+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RunOnce(context.Background(), entries, io.Discard, service.OnceOptions{}); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(output)
	if strings.Count(string(data), `"task":"TASK-0002"`) != 1 || handlers.Cursor(ws, "record") != 2 {
		t.Fatalf("recovered delivery = %q cursor=%d", data, handlers.Cursor(ws, "record"))
	}
}

func TestRunOnceReportsUnavailableWorkspaceAndContinues(t *testing.T) {
	root, ws, output := createHandledWorkspace(t)
	if err := events.Append(ws, events.Event{Type: events.TaskCreated, Task: "TASK-0001"}); err != nil {
		t.Fatal(err)
	}
	entries := []registry.WorkspaceEntry{
		{Name: "a-missing", Path: filepath.Join(t.TempDir(), "gone")},
		{Name: "b-present", Path: root},
	}
	results, err := service.RunOnce(context.Background(), entries, io.Discard, service.OnceOptions{})
	if err == nil || !strings.Contains(err.Error(), "a-missing") {
		t.Fatalf("err = %v", err)
	}
	if len(results) != 2 || results[0].Error == "" || results[1].Error != "" {
		t.Fatalf("results = %#v", results)
	}
	if data, _ := os.ReadFile(output); !strings.Contains(string(data), "TASK-0001") {
		t.Fatal("unavailable workspace prevented delivery elsewhere")
	}
}

func TestRunOnceCancellationStopsRunningHandler(t *testing.T) {
	root, ws, _ := createHandledWorkspace(t)
	startedFile := filepath.Join(root, "handler-started")
	if err := os.WriteFile(filepath.Join(root, "hooks", "record"), []byte("#!/bin/sh\ntouch "+shellQuote(startedFile)+"\nsleep 10\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := events.Append(ws, events.Event{Type: events.TaskCreated, Task: "TASK-0001"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		waitFor(t, func() bool { _, err := os.Stat(startedFile); return err == nil })
		cancel()
	}()
	started := time.Now()
	_, err := service.RunOnce(ctx, []registry.WorkspaceEntry{{Name: "test", Path: root}}, io.Discard, service.OnceOptions{})
	if err == nil {
		t.Fatal("cancelled drain reported success")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("cancelled drain took %s", elapsed)
	}
	if handlers.Cursor(ws, "record") != 0 {
		t.Fatal("cancelled delivery was acknowledged")
	}
}

func TestFollowRegistryWarnsAboutUnhostedServiceCommand(t *testing.T) {
	project := t.TempDir()
	ws, err := workspace.Init(project)
	if err != nil {
		t.Fatal(err)
	}
	pluginRoot := t.TempDir()
	output := filepath.Join(project, "plugin-events.jsonl")
	if err := os.MkdirAll(filepath.Join(pluginRoot, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, "hooks", "record"), []byte("#!/bin/sh\ncat >> "+shellQuote(output)+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A legacy manifest that expected Docket to launch its service process.
	writePluginManifest(t, pluginRoot, "task.created", "service: {url: 'http://127.0.0.1:9', command: [bin/serve]}\n")
	configPath := filepath.Join(t.TempDir(), "registry.yaml")
	t.Setenv("DOCKET_CONFIG", configPath)
	writeRegistryFixture(t, configPath, project, pluginRoot)
	appendPluginUse(t, ws)

	var logs lockedBuffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := service.NewManager(ctx, &logs)
	defer manager.Stop()
	go manager.FollowRegistry(ctx, 20*time.Millisecond)
	waitFor(t, func() bool { return len(manager.Statuses()) == 1 && manager.Statuses()[0].State == "watching" })
	// The hosting requirement is reported, and hooks still run.
	if err := events.Append(ws, events.Event{Type: events.TaskCreated, Task: "TASK-0001"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		data, _ := os.ReadFile(output)
		return strings.Contains(string(data), "TASK-0001")
	})
	if got := logs.String(); !strings.Contains(got, "plugin example: service.command") || !strings.Contains(got, "no longer launched") {
		t.Fatalf("runner log = %q", got)
	}
}

type lockedBuffer struct {
	mu   sync.Mutex
	data strings.Builder
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.String()
}

func TestRunOnceSkipMissingReportsWithoutFailingAndWarnsAboutHosting(t *testing.T) {
	root, ws, output := createHandledWorkspace(t)
	if err := events.Append(ws, events.Event{Type: events.TaskCreated, Task: "TASK-0001"}); err != nil {
		t.Fatal(err)
	}
	pluginRoot := t.TempDir()
	writePluginManifest(t, pluginRoot, "task.moved", "service: {url: 'http://127.0.0.1:9', command: [bin/serve]}\n")
	if err := os.MkdirAll(filepath.Join(pluginRoot, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, "hooks", "record"), []byte("#!/bin/sh\ncat >/dev/null\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "registry.yaml")
	t.Setenv("DOCKET_CONFIG", configPath)
	writeRegistryFixture(t, configPath, root, pluginRoot)
	appendPluginUse(t, ws)
	config, err := registry.Load()
	if err != nil {
		t.Fatal(err)
	}
	entries := []registry.WorkspaceEntry{{Name: "gone", Path: filepath.Join(t.TempDir(), "gone")}, {Name: "test", Path: root}}
	var logs lockedBuffer
	results, err := service.RunOnce(context.Background(), entries, &logs, service.OnceOptions{SkipMissing: true, Registry: config})
	if err != nil {
		t.Fatalf("missing registration failed the run: %v", err)
	}
	if len(results) != 2 || results[0].State != "missing" || results[1].State != "ok" {
		t.Fatalf("results = %#v", results)
	}
	if data, _ := os.ReadFile(output); !strings.Contains(string(data), "TASK-0001") {
		t.Fatal("present workspace was not drained")
	}
	got := logs.String()
	if !strings.Contains(got, "workspace gone missing") || !strings.Contains(got, "plugin example: service.command") {
		t.Fatalf("runner log = %q", got)
	}
}

func TestRunnerLogsBrokenPluginManifestThatStopsHooks(t *testing.T) {
	project := t.TempDir()
	ws, err := workspace.Init(project)
	if err != nil {
		t.Fatal(err)
	}
	pluginRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(pluginRoot, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, "hooks", "record"), []byte("#!/bin/sh\ncat >/dev/null\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writePluginManifest(t, pluginRoot, "task.created", "")
	configPath := filepath.Join(t.TempDir(), "registry.yaml")
	t.Setenv("DOCKET_CONFIG", configPath)
	writeRegistryFixture(t, configPath, project, pluginRoot)
	appendPluginUse(t, ws)

	var logs lockedBuffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := service.NewManager(ctx, &logs)
	defer manager.Stop()
	go manager.FollowRegistry(ctx, 20*time.Millisecond)
	waitFor(t, func() bool { return len(manager.Statuses()) == 1 && manager.Statuses()[0].State == "watching" })

	writePluginManifest(t, pluginRoot, "task.created", "surprise: true\n")
	waitFor(t, func() bool {
		got := logs.String()
		return strings.Contains(got, "plugin example:") && strings.Contains(got, "workspace test unavailable")
	})
	writePluginManifest(t, pluginRoot, "task.created", "")
	// Fixing the manifest changes the plugin generation, so the workspace
	// restarts and resumes watching.
	waitFor(t, func() bool { return len(manager.Statuses()) == 1 && manager.Statuses()[0].State == "watching" })
}
