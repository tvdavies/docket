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

	"github.com/tvdavies/docket/internal/registry"
	"github.com/tvdavies/docket/internal/service"
)

func TestPluginUIAssetsAreSandboxedAndHashAddressed(t *testing.T) {
	project, pluginRoot, _ := pluginServiceFixture(t, "http://127.0.0.1:1")
	writePluginManifest(t, pluginRoot, "task.created", "service: {url: 'http://127.0.0.1:1'}\nui:\n  dir: ui\n  capabilities: [task.read]\n  widgets:\n    - {type: example/job, title: Job, entry: job.html}\n  pages:\n    - {id: fleet, title: Fleet, entry: fleet.html}\n")
	if err := os.MkdirAll(filepath.Join(pluginRoot, "ui", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"ui/job.html": "<p>job</p>", "ui/nested/app.js": "export {}", "secret.txt": "no"} {
		if err := os.WriteFile(filepath.Join(pluginRoot, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(pluginRoot, "secret.txt"), filepath.Join(pluginRoot, "ui", "escape.txt")); err != nil {
		t.Fatal(err)
	}
	manager, server := newBoardServer(t, "test", project)
	defer manager.Stop()
	defer server.Close()

	response := getJSON(t, server.URL+"/api/workspaces/test/board")
	var board struct {
		Plugins []struct {
			UIBase       string `json:"ui_base"`
			Capabilities []string
			Widgets      []struct {
				Type  string
				Entry string
				Slots []string
			}
			Pages []struct{ ID, Entry string }
		}
	}
	decodeResponse(t, response, &board)
	if len(board.Plugins) != 1 {
		t.Fatalf("plugins %+v", board.Plugins)
	}
	metadata := board.Plugins[0]
	if !strings.HasPrefix(metadata.UIBase, "/plugin-ui/example/") || len(metadata.Widgets) != 1 || metadata.Widgets[0].Entry != "job.html" || len(metadata.Widgets[0].Slots) != 2 || len(metadata.Pages) != 1 || metadata.Capabilities[0] != "task.read" {
		t.Fatalf("metadata %+v", metadata)
	}

	get := func(path string) (*http.Response, string) {
		t.Helper()
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		return response, string(body)
	}
	response, body := get(metadata.UIBase + "/job.html")
	if response.StatusCode != 200 || body != "<p>job</p>" {
		t.Fatalf("asset = %d %q", response.StatusCode, body)
	}
	csp := response.Header.Get("Content-Security-Policy")
	if !strings.HasPrefix(csp, "sandbox allow-scripts") || strings.Contains(csp, "allow-same-origin") || !strings.Contains(csp, "connect-src 'none'") {
		t.Fatalf("csp = %q", csp)
	}
	if !strings.Contains(response.Header.Get("Cache-Control"), "immutable") || response.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("headers = %v", response.Header)
	}
	if response, _ := get(metadata.UIBase + "/nested/app.js"); response.StatusCode != 200 {
		t.Fatalf("nested = %d", response.StatusCode)
	}
	if response, _ := get("/plugin-ui/example/stale/job.html"); response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("stale hash = %d %q", response.StatusCode, response.Header.Get("Cache-Control"))
	}
	for _, path := range []string{"/escape.txt", "/../secret.txt", "/%2e%2e/secret.txt", "/nested", "/missing.html"} {
		if response, body := get(metadata.UIBase + path); response.StatusCode == 200 || strings.Contains(body, "no") && response.StatusCode < 400 {
			t.Fatalf("%s served %d %q", path, response.StatusCode, body)
		}
	}
	if response, _ := get("/plugin-ui/unknown/x/job.html"); response.StatusCode != 404 {
		t.Fatalf("unknown plugin = %d", response.StatusCode)
	}
	response, _ = get("/workspaces/test")
	if !strings.Contains(response.Header.Get("Content-Security-Policy"), "frame-src 'self'") {
		t.Fatalf("board csp = %q", response.Header.Get("Content-Security-Policy"))
	}
	response, body = get("/plugin-sdk/v1/client.js")
	if response.StatusCode != 200 || !strings.Contains(body, "connect") || response.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("sdk client = %d %v", response.StatusCode, response.Header)
	}
}

func TestPluginProxyStripsDocketCredentialsAndSandboxesResponses(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]string{"cookie": request.Header.Get("Cookie"), "authorization": request.Header.Get("Authorization")})
		http.SetCookie(writer, &http.Cookie{Name: "plugin", Value: "x"})
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
	request, _ := http.NewRequest(http.MethodGet, server.URL+"/plugins/example/echo", nil)
	request.Header.Set("Cookie", "docket=secret")
	request.Header.Set("Authorization", "Bearer secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var echoed map[string]string
	if err := json.NewDecoder(response.Body).Decode(&echoed); err != nil {
		t.Fatal(err)
	}
	if echoed["cookie"] != "" || echoed["authorization"] != "" {
		t.Fatalf("credentials forwarded: %v", echoed)
	}
	if response.Header.Get("Set-Cookie") != "" || !strings.HasPrefix(response.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("response headers = %v", response.Header)
	}
}
