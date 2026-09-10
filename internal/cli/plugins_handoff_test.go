package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/handlers"
	"github.com/tvdavies/docket/internal/pluginmgr"
	"github.com/tvdavies/docket/internal/workspace"
)

func TestPluginHandoffFlagCombinations(t *testing.T) {
	for _, args := range [][]string{
		{"enable", "x", "--adopt-cursors", "--from-start"},
		{"enable", "x", "--receipt-dir", "new"},
		{"enable", "x", "--expect-config-sha256", "hash"},
		{"disable", "x", "--legacy-config", "template"},
		{"disable", "x", "--expect-config-sha256", "hash"},
		{"disable", "x", "--receipt-dir", "new"},
	} {
		root := newRootCmd()
		root.SetArgs(append([]string{"plugin"}, args...))
		_, err := root.ExecuteC()
		if err == nil || !(strings.Contains(err.Error(), "mutually exclusive") || strings.Contains(err.Error(), "require")) {
			t.Fatalf("%v: %v", args, err)
		}
	}
}

func TestPluginHandoffCLIForwardReverseAndInspection(t *testing.T) {
	home := t.TempDir()
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("DOCKET_PLUGIN_DIR", filepath.Join(home, "plugins"))
	for _, name := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
		t.Setenv(name, filepath.Join(home, name))
	}
	t.Setenv("HOME", home)
	t.Setenv("DOCKET_HOME", "")
	t.Setenv("DOCKET_CONFIG", filepath.Join(home, "registry.yaml"))
	t.Setenv("DOCKET_HANDLER_STACK", "")
	t.Chdir(home)
	project := filepath.Join(home, "project")
	ws, err := workspace.Init(project)
	if err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(home, "package")
	for _, base := range []string{project, plugin} {
		if err := os.MkdirAll(filepath.Join(base, "hooks"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(base, "hooks", "h"), []byte("#!/bin/sh\ncat >/dev/null\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(plugin, "docket-plugin.yaml"), []byte("name: fixture\nversion: 1.0.0\nhandlers:\n  h: {on: ['*'], run: hooks/h}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := pluginmgr.Add(plugin, "", "dev"); err != nil {
		t.Fatal(err)
	}
	cfg := workspace.DefaultConfig()
	cfg.Handlers = map[string]workspace.HandlerConfig{"h": {On: []string{"*"}, Run: "hooks/h"}}
	if err := workspace.WriteDeclaredConfig(ws.Root, cfg); err != nil {
		t.Fatal(err)
	}
	if err := events.Append(ws, events.Event{Type: events.TaskCreated}); err != nil {
		t.Fatal(err)
	}
	if err := handlers.SeedCursorAtEnd(ws, "h"); err != nil {
		t.Fatal(err)
	}
	template := filepath.Join(home, "template.yaml")
	data, err := os.ReadFile(filepath.Join(ws.Root, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(template, data, 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		root := newRootCmd()
		root.SetContext(context.Background())
		root.SetArgs(append([]string{"plugin"}, args...))
		if _, err := root.ExecuteC(); err != nil {
			t.Fatal(err)
		}
	}
	forward := filepath.Join(home, "forward")
	run("enable", "fixture", "--workspace", project, "--adopt-cursors", "--receipt-dir", forward)
	data, err = os.ReadFile(filepath.Join(ws.Root, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	reverse := filepath.Join(home, "reverse")
	run("disable", "fixture", "--workspace", project, "--adopt-cursors", "--legacy-config", template, "--expect-config-sha256", workspace.ConfigHash(data), "--receipt-dir", reverse)
	before, err := os.ReadFile(filepath.Join(ws.Root, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	// Existing receipts need no old template/hash and can never reapply a direction.
	run("disable", "fixture", "--workspace", project, "--adopt-cursors", "--receipt-dir", reverse)
	after, err := os.ReadFile(filepath.Join(ws.Root, "config.yaml"))
	if err != nil || string(before) != string(after) {
		t.Fatal("inspection mutated config")
	}
	if handlers.Cursor(ws, "h") != 1 || handlers.Cursor(ws, "fixture/h") != 1 {
		t.Fatal("CLI lost transferred position")
	}
}
