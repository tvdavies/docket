package service_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tvdavies/docket/internal/events"
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
		return err == nil && strings.Contains(string(data), `"task":"TASK-0001"`)
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

func TestHTTPStatusSurface(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := service.NewManager(ctx, io.Discard)
	defer manager.Stop()
	manager.SetWorkspaces([]registry.WorkspaceEntry{{Name: "missing", Path: filepath.Join(t.TempDir(), "gone")}})
	waitFor(t, func() bool {
		statuses := manager.Statuses()
		return len(statuses) == 1 && statuses[0].State == "unavailable"
	})

	server := httptest.NewServer(service.Handler(manager))
	defer server.Close()
	response, err := http.Get(server.URL + "/api/workspaces")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var statuses []service.WorkspaceStatus
	if err := json.NewDecoder(response.Body).Decode(&statuses); err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 || statuses[0].Name != "missing" || statuses[0].State != "unavailable" {
		t.Fatalf("unexpected API response: %#v", statuses)
	}
}

func TestValidateListenRequiresExplicitRemoteOptIn(t *testing.T) {
	if err := service.ValidateListen("127.0.0.1:7463", false); err != nil {
		t.Fatalf("loopback refused: %v", err)
	}
	if err := service.ValidateListen("0.0.0.0:7463", false); err == nil {
		t.Fatal("expected non-loopback address to be refused")
	}
	if err := service.ValidateListen("0.0.0.0:7463", true); err != nil {
		t.Fatalf("explicit remote bind refused: %v", err)
	}
}

func TestSystemdUnitUsesOneMultiWorkspaceService(t *testing.T) {
	unit := service.BuildSystemdUnit("/home/tom/bin/docket", "/home/tom/.config/docket/config.yaml", "/usr/bin:/bin")
	for _, want := range []string{
		`ExecStart="/home/tom/bin/docket" serve --all`,
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
