package service_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestWorkspaceLeaseBlocksPathReplacementUntilRequestCompletes(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	if _, err := workspace.Init(first); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.Init(second); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := service.NewManager(ctx, io.Discard)
	defer manager.Stop()
	manager.SetWorkspaces([]registry.WorkspaceEntry{{Name: "test", Path: first}})

	leased, release, err := manager.LeaseWorkspace("test")
	if err != nil {
		t.Fatal(err)
	}
	if leased.Root != filepath.Join(first, workspace.DirName) {
		t.Fatalf("leased root = %s", leased.Root)
	}
	replaced := make(chan struct{})
	go func() {
		manager.SetWorkspaces([]registry.WorkspaceEntry{{Name: "test", Path: second}})
		close(replaced)
	}()
	select {
	case <-replaced:
		t.Fatal("workspace path was replaced while request lease was active")
	case <-time.After(50 * time.Millisecond):
	}
	release()
	select {
	case <-replaced:
	case <-time.After(time.Second):
		t.Fatal("workspace replacement did not continue after lease release")
	}

	leased, release, err = manager.LeaseWorkspace("test")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if leased.Root != filepath.Join(second, workspace.DirName) {
		t.Fatalf("replacement root = %s", leased.Root)
	}
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

func TestPluginProxyRewritesHostPrefixAndStripsReservedHeaders(t *testing.T) {
	var targetHost string
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Host != targetHost {
			http.Error(writer, "bad host: "+request.Host, http.StatusForbidden)
			return
		}
		if request.Header.Get("X-Docket-User") != "" {
			http.Error(writer, "spoofed docket header", http.StatusForbidden)
			return
		}
		if request.Header.Get("X-Forwarded-Prefix") != "/plugins/example" {
			http.Error(writer, "missing prefix", http.StatusBadRequest)
			return
		}
		writer.Write([]byte("proxied:" + request.URL.Path))
	}))
	defer target.Close()
	targetHost = strings.TrimPrefix(target.URL, "http://")
	project, pluginRoot, configPath := pluginServiceFixture(t, target.URL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := service.NewManager(ctx, io.Discard)
	defer manager.Stop()
	manager.SetWorkspaces([]registry.WorkspaceEntry{{Name: "test", Path: project}})
	server := httptest.NewServer(service.Handler(manager))
	defer server.Close()
	request, _ := http.NewRequest(http.MethodGet, server.URL+"/plugins/example/sessions/one", nil)
	request.Header.Set("X-Docket-User", "attacker")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || string(body) != "proxied:/sessions/one" {
		t.Fatalf("proxy = %d %q", response.StatusCode, body)
	}

	target.Close()
	response, err = http.Get(server.URL + "/plugins/example/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var failure map[string]string
	if err := json.NewDecoder(response.Body).Decode(&failure); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadGateway || failure["plugin"] != "example" || failure["target"] != target.URL {
		t.Fatalf("dead target response = %d %#v", response.StatusCode, failure)
	}
	_ = pluginRoot
	_ = configPath
}

func TestPluginProxyPassesWebSocketUpgrades(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !strings.EqualFold(request.Header.Get("Upgrade"), "websocket") {
			http.Error(writer, "upgrade required", http.StatusBadRequest)
			return
		}
		hijacker, ok := writer.(http.Hijacker)
		if !ok {
			http.Error(writer, "hijacking unavailable", http.StatusInternalServerError)
			return
		}
		connection, buffered, err := hijacker.Hijack()
		if err != nil {
			return
		}
		defer connection.Close()
		_, _ = buffered.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		_ = buffered.Flush()
		line, _ := buffered.ReadString('\n')
		_, _ = buffered.WriteString("echo:" + line)
		_ = buffered.Flush()
	}))
	defer target.Close()
	project, _, _ := pluginServiceFixture(t, target.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := service.NewManager(ctx, io.Discard)
	defer manager.Stop()
	manager.SetWorkspaces([]registry.WorkspaceEntry{{Name: "test", Path: project}})
	server := httptest.NewServer(service.Handler(manager))
	defer server.Close()

	address := strings.TrimPrefix(server.URL, "http://")
	connection, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_, _ = fmt.Fprintf(connection, "GET /plugins/example/socket HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n", address)
	reader := bufio.NewReader(connection)
	status, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(status, "101") {
		t.Fatalf("upgrade status = %q, %v", status, err)
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\r\n" {
			break
		}
	}
	_, _ = connection.Write([]byte("ping\n"))
	echo, err := reader.ReadString('\n')
	if err != nil || echo != "echo:ping\n" {
		t.Fatalf("websocket tunnel = %q, %v", echo, err)
	}
}

func TestPluginConfigAPIValidatesAndWritesEachScope(t *testing.T) {
	project := t.TempDir()
	ws, err := workspace.Init(project)
	if err != nil {
		t.Fatal(err)
	}
	pluginRoot := t.TempDir()
	manifest := `
name: example
version: 1.0.0
config:
  instance:
    retries: {type: number}
  workspace:
    enabled: {type: boolean}
  status:
    agent: {type: string}
`
	if err := os.WriteFile(filepath.Join(pluginRoot, plugin.ManifestFile), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "registry.yaml")
	t.Setenv("DOCKET_CONFIG", configPath)
	writeRegistryFixture(t, configPath, project, pluginRoot)
	appendPluginUse(t, ws)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := service.NewManager(ctx, io.Discard)
	defer manager.Stop()
	manager.SetWorkspaces([]registry.WorkspaceEntry{{Name: "test", Path: project}})
	server := httptest.NewServer(service.Handler(manager))
	defer server.Close()

	patch := func(path, body string) *http.Response {
		request, _ := http.NewRequest(http.MethodPatch, server.URL+path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	response := patch("/api/plugins/example/config", `{"values":{"retries":"many"}}`)
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid instance config status = %d", response.StatusCode)
	}
	response = patch("/api/plugins/example/config", `{"values":{"retries":3}}`)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("instance config status = %d", response.StatusCode)
	}
	response = patch("/api/workspaces/test/plugins/example/config", `{"values":{"enabled":true}}`)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("workspace config status = %d", response.StatusCode)
	}
	response = patch("/api/workspaces/test/plugins/example/statuses/backlog", `{"values":{"agent":"worker"}}`)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status config status = %d", response.StatusCode)
	}
	config, err := registry.Load()
	if err != nil {
		t.Fatal(err)
	}
	if config.Plugins[0].Config["retries"] != 3 {
		t.Fatalf("instance values = %#v", config.Plugins[0].Config)
	}
	opened, err := workspace.OpenRoot(project)
	if err != nil {
		t.Fatal(err)
	}
	use := opened.DeclaredConfig.Plugins.Values["example"]
	if use.Config["enabled"] != true || use.Statuses["backlog"]["agent"] != "worker" {
		t.Fatalf("workspace values = %#v", use)
	}
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

func pluginServiceFixture(t *testing.T, target string) (string, string, string) {
	t.Helper()
	project := t.TempDir()
	ws, err := workspace.Init(project)
	if err != nil {
		t.Fatal(err)
	}
	pluginRoot := t.TempDir()
	writePluginManifest(t, pluginRoot, "task.created", "service: {url: "+target+", healthz: /healthz}\n")
	configPath := filepath.Join(t.TempDir(), "registry.yaml")
	t.Setenv("DOCKET_CONFIG", configPath)
	writeRegistryFixture(t, configPath, project, pluginRoot)
	appendPluginUse(t, ws)
	return project, pluginRoot, configPath
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
