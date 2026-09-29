package service_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tvdavies/docket/internal/service"
)

type pluginsSnapshot struct {
	Plugins []struct {
		Name         string `json:"name"`
		ManifestHash string `json:"manifest_hash"`
		UIHash       string `json:"ui_hash"`
		UIBase       string `json:"ui_base"`
		Error        string `json:"error"`
	} `json:"plugins"`
}

func nextOfType(t *testing.T, events <-chan sseEvent, kind string) sseEvent {
	t.Helper()
	for {
		event := nextSSE(t, events)
		if event.Type == kind {
			return event
		}
	}
}

func decodePlugins(t *testing.T, event sseEvent) pluginsSnapshot {
	t.Helper()
	var snapshot pluginsSnapshot
	if err := json.Unmarshal(event.Data, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Plugins) != 1 || snapshot.Plugins[0].Name != "example" {
		t.Fatalf("plugins = %s", event.Data)
	}
	return snapshot
}

func configUIBase(t *testing.T, event sseEvent) string {
	t.Helper()
	var config struct {
		Plugins []struct {
			UIBase string `json:"ui_base"`
			Pages  []struct {
				ID string `json:"id"`
			} `json:"pages"`
		} `json:"plugins"`
	}
	if event.Type == "init" {
		var init struct {
			Config json.RawMessage `json:"config"`
		}
		if err := json.Unmarshal(event.Data, &init); err != nil {
			t.Fatal(err)
		}
		event.Data = init.Config
	}
	if err := json.Unmarshal(event.Data, &config); err != nil || len(config.Plugins) != 1 {
		t.Fatalf("config = %s (%v)", event.Data, err)
	}
	return config.Plugins[0].UIBase
}

// A long poll interval proves the fsnotify path, and an open workspace stream
// proves UI-only edits never restart the runtime (a restart closes it).
func TestPluginUIEditsHotReloadWithoutRuntimeRestart(t *testing.T) {
	_, pluginRoot, _ := pluginServiceFixture(t, "http://127.0.0.1:1")
	uiManifest := "ui:\n  dir: ui\n  pages:\n    - {id: fleet, title: Fleet, entry: page.html}\n"
	writePluginManifest(t, pluginRoot, "task.created", uiManifest)
	if err := os.MkdirAll(filepath.Join(pluginRoot, "ui", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(pluginRoot, "ui", "page.html"), "<p>one</p>")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := service.NewManager(ctx, io.Discard)
	defer manager.Stop()
	go manager.FollowRegistry(ctx, time.Hour)
	waitFor(t, func() bool { return len(manager.Statuses()) == 1 && manager.Statuses()[0].State == "watching" })
	server := httptest.NewServer(service.Handler(manager))
	defer server.Close()

	instance, plugins := openSSE(t, server.URL+"/api/stream", "")
	defer instance.Body.Close()
	first := decodePlugins(t, nextOfType(t, plugins, "plugins"))
	if first.Plugins[0].UIHash == "" || first.Plugins[0].UIBase != "/plugin-ui/example/"+first.Plugins[0].UIHash {
		t.Fatalf("initial plugins = %#v", first)
	}
	board, boardEvents := openSSE(t, server.URL+"/api/workspaces/test/stream", "")
	defer board.Body.Close()
	if base := configUIBase(t, nextOfType(t, boardEvents, "init")); base != first.Plugins[0].UIBase {
		t.Fatalf("board ui_base = %q, want %q", base, first.Plugins[0].UIBase)
	}

	// An asset edit in a nested directory changes only the UI hash.
	writeFile(t, filepath.Join(pluginRoot, "ui", "nested", "app.js"), "export const edited = true")
	edited := decodePlugins(t, nextOfType(t, plugins, "plugins"))
	if edited.Plugins[0].UIHash == first.Plugins[0].UIHash || edited.Plugins[0].ManifestHash != first.Plugins[0].ManifestHash {
		t.Fatalf("after asset edit = %#v, before %#v", edited, first)
	}
	if base := configUIBase(t, nextOfType(t, boardEvents, "config")); base != edited.Plugins[0].UIBase {
		t.Fatalf("board ui_base = %q, want %q", base, edited.Plugins[0].UIBase)
	}

	// A ui-only manifest edit republishes config on the same stream too.
	writePluginManifest(t, pluginRoot, "task.created", uiManifest+"    - {id: more, title: More, entry: page.html}\n")
	manifestEdit := decodePlugins(t, nextOfType(t, plugins, "plugins"))
	if manifestEdit.Plugins[0].ManifestHash == first.Plugins[0].ManifestHash {
		t.Fatalf("manifest hash unchanged: %#v", manifestEdit)
	}
	config := nextOfType(t, boardEvents, "config")
	if !strings.Contains(string(config.Data), `"id":"more"`) {
		t.Fatalf("config after manifest edit = %s", config.Data)
	}
}

// Handler changes still restart the runtime: the old workspace stream ends
// and the board reconnects to the new generation.
func TestPluginRuntimeManifestEditRestartsWorkspaceStream(t *testing.T) {
	_, pluginRoot, _ := pluginServiceFixture(t, "http://127.0.0.1:1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := service.NewManager(ctx, io.Discard)
	defer manager.Stop()
	go manager.FollowRegistry(ctx, time.Hour)
	waitFor(t, func() bool { return len(manager.Statuses()) == 1 && manager.Statuses()[0].State == "watching" })
	server := httptest.NewServer(service.Handler(manager))
	defer server.Close()

	board, boardEvents := openSSE(t, server.URL+"/api/workspaces/test/stream", "")
	defer board.Body.Close()
	nextOfType(t, boardEvents, "init")
	writePluginManifest(t, pluginRoot, "task.commented", "service: {url: 'http://127.0.0.1:1'}\n")
	deadline := time.After(3 * time.Second)
	for {
		select {
		case _, open := <-boardEvents:
			if !open {
				return
			}
		case <-deadline:
			t.Fatal("workspace stream survived a handler change")
		}
	}
}

func TestInstanceStreamReportsBrokenManifest(t *testing.T) {
	_, pluginRoot, _ := pluginServiceFixture(t, "http://127.0.0.1:1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := service.NewManager(ctx, io.Discard)
	defer manager.Stop()
	go manager.FollowRegistry(ctx, time.Hour)
	waitFor(t, func() bool { return len(manager.Statuses()) == 1 })
	server := httptest.NewServer(service.Handler(manager))
	defer server.Close()

	response, plugins := openSSE(t, server.URL+"/api/stream", "")
	defer response.Body.Close()
	if first := decodePlugins(t, nextOfType(t, plugins, "plugins")); first.Plugins[0].Error != "" {
		t.Fatalf("unexpected error: %#v", first)
	}
	writeFile(t, filepath.Join(pluginRoot, "docket-plugin.yaml"), "name: example\nversion: 1.0.0\nbogus: true\n")
	broken := decodePlugins(t, nextOfType(t, plugins, "plugins"))
	if !strings.Contains(broken.Plugins[0].Error, "bogus") {
		t.Fatalf("broken manifest = %#v", broken)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
