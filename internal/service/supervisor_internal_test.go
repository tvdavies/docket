package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/tvdavies/docket/internal/plugin"
	"github.com/tvdavies/docket/internal/registry"
	"github.com/tvdavies/docket/internal/workspace"
)

// fastSupervision shortens every supervision timing for the test.
func fastSupervision(t *testing.T) {
	t.Helper()
	saved := []time.Duration{serviceStopGrace, serviceInitialBackoff, serviceHealthInterval, serviceStableAfter, serviceMaxBackoff, serviceWatchDebounce}
	serviceStopGrace, serviceInitialBackoff, serviceHealthInterval = 500*time.Millisecond, 20*time.Millisecond, 30*time.Millisecond
	serviceStableAfter, serviceMaxBackoff, serviceWatchDebounce = time.Hour, 80*time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() {
		serviceStopGrace, serviceInitialBackoff, serviceHealthInterval = saved[0], saved[1], saved[2]
		serviceStableAfter, serviceMaxBackoff, serviceWatchDebounce = saved[3], saved[4], saved[5]
	})
	t.Setenv("DOCKET_STATE_DIR", t.TempDir())
}

// serviceScript writes a plugin whose service records each start (pid and
// environment) in starts.log and then runs body.
func serviceScript(t *testing.T, body string) (string, string) {
	t.Helper()
	root := t.TempDir()
	starts := filepath.Join(root, "starts.log")
	script := "#!/bin/sh\necho \"$$ $DOCKET_PLUGIN $PORT $DOCKET_PLUGIN_CONFIG $(pwd)\" >> " + starts + "\n" + body + "\n"
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "serve"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return root, starts
}

func starts(path string) []string {
	data, _ := os.ReadFile(path)
	text := strings.TrimSpace(string(data))
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func eventually(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func alive(pid string) bool {
	var value int
	for _, r := range pid {
		value = value*10 + int(r-'0')
	}
	return syscall.Kill(value, 0) == nil
}

func pidOf(line string) string { return strings.Fields(line)[0] }

func newTestSupervisor(t *testing.T) (*supervisor, *atomic.Int32) {
	t.Helper()
	var changes atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	s := newSupervisor(ctx, func() { changes.Add(1) })
	t.Cleanup(func() {
		s.stop()
		cancel()
	})
	return s, &changes
}

func TestSupervisorRunsCommandWithPluginEnvironmentAndStops(t *testing.T) {
	fastSupervision(t)
	root, log := serviceScript(t, "exec sleep 60")
	s, changes := newTestSupervisor(t)
	spec := serviceSpec{name: "example", root: root, instance: map[string]any{"token": "x"},
		service: plugin.Service{URL: "http://127.0.0.1:9123", Command: []string{"bin/serve"}}}
	s.sync([]serviceSpec{spec})
	eventually(t, "service start", func() bool { return len(starts(log)) == 1 })
	line := starts(log)[0]
	for _, want := range []string{" example 9123 ", `{"config":{"token":"x"}}`, root} {
		if !strings.Contains(line, want) {
			t.Fatalf("start line %q missing %q", line, want)
		}
	}
	eventually(t, "running status", func() bool { return s.statuses()["example"].State == "running" })
	if changes.Load() == 0 {
		t.Fatal("status changes were not published")
	}

	// An unchanged spec keeps the process; removing it stops the group.
	s.sync([]serviceSpec{spec})
	if got := len(starts(log)); got != 1 {
		t.Fatalf("unchanged sync restarted the service (%d starts)", got)
	}
	s.sync(nil)
	if alive(pidOf(line)) {
		t.Fatal("service still running after it was removed")
	}
	if _, ok := s.statuses()["example"]; ok {
		t.Fatal("removed service still reported")
	}
	output, _ := PluginLogPath("example")
	data, _ := os.ReadFile(output)
	if !strings.Contains(string(data), "docket: started bin/serve") {
		t.Fatalf("service log = %q", data)
	}
}

func TestSupervisorRestartsCrashesWithBackoff(t *testing.T) {
	fastSupervision(t)
	root, log := serviceScript(t, "echo boom >&2; exit 3")
	s, _ := newTestSupervisor(t)
	s.sync([]serviceSpec{{name: "example", root: root, service: plugin.Service{URL: "http://127.0.0.1:9123", Command: []string{"bin/serve"}}}})
	eventually(t, "repeated restarts", func() bool { return len(starts(log)) >= 4 })
	status := s.statuses()["example"]
	if status.Restarts < 3 || !strings.Contains(status.LastError, "exit status 3") {
		t.Fatalf("status = %+v", status)
	}
	output, _ := PluginLogPath("example")
	data, _ := os.ReadFile(output)
	if !strings.Contains(string(data), "boom") {
		t.Fatalf("stderr not captured: %q", data)
	}
}

func TestSupervisorRestartsOnWatchedChangeOnly(t *testing.T) {
	fastSupervision(t)
	root, log := serviceScript(t, "exec sleep 60")
	if err := os.MkdirAll(filepath.Join(root, "server", "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	s, _ := newTestSupervisor(t)
	s.sync([]serviceSpec{{name: "example", root: root, service: plugin.Service{
		URL: "http://127.0.0.1:9123", Command: []string{"bin/serve"}, Watch: []string{"server/**/*.mjs"},
	}}})
	eventually(t, "service start", func() bool { return len(starts(log)) == 1 })
	first := pidOf(starts(log)[0])

	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if got := len(starts(log)); got != 1 {
		t.Fatalf("unwatched edit restarted the service (%d starts)", got)
	}
	if err := os.WriteFile(filepath.Join(root, "server", "lib", "app.mjs"), []byte("export {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	eventually(t, "watch restart", func() bool { return len(starts(log)) == 2 })
	if alive(first) {
		t.Fatal("previous process survived a watch restart")
	}
	eventually(t, "running after restart", func() bool { return s.statuses()["example"].State == "running" })
	if status := s.statuses()["example"]; !strings.Contains(status.LastError, "server/lib/app.mjs") {
		t.Fatalf("status = %+v", status)
	}
}

func TestSupervisorRestartsUnhealthyService(t *testing.T) {
	fastSupervision(t)
	var healthy atomic.Bool
	healthy.Store(true)
	health := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if !healthy.Load() {
			writer.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer health.Close()
	root, log := serviceScript(t, "exec sleep 60")
	s, _ := newTestSupervisor(t)
	s.sync([]serviceSpec{{name: "example", root: root, service: plugin.Service{
		URL: health.URL, Healthz: "/healthz", Command: []string{"bin/serve"},
	}}})
	eventually(t, "healthy", func() bool { return s.statuses()["example"].State == "healthy" })
	healthy.Store(false)
	eventually(t, "unhealthy restart", func() bool { return len(starts(log)) >= 2 })
	if status := s.statuses()["example"]; status.Restarts == 0 || !strings.Contains(status.LastError, "503") {
		t.Fatalf("status = %+v", status)
	}
}

func TestServiceSpecsOnlyIncludeEnabledPluginsWithCommands(t *testing.T) {
	project := t.TempDir()
	ws, err := workspace.Init(project)
	if err != nil {
		t.Fatal(err)
	}
	manifest := func(name, service string) string {
		root := t.TempDir()
		body := "name: " + name + "\nversion: 1.0.0\n" + service
		if err := os.WriteFile(filepath.Join(root, plugin.ManifestFile), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return root
	}
	commanded := manifest("commanded", "config:\n  instance:\n    port: {type: number, default: 9000}\nservice:\n  url: http://127.0.0.1:9000\n  command: [node, server.mjs]\n")
	proxied := manifest("proxied", "service:\n  url: http://127.0.0.1:9001\n")
	disabled := manifest("disabled", "service:\n  url: http://127.0.0.1:9002\n  command: [node, server.mjs]\n")
	file, err := os.OpenFile(filepath.Join(ws.Root, "config.yaml"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("plugins:\n  commanded: {}\n  proxied: {}\n"); err != nil {
		t.Fatal(err)
	}
	file.Close()
	config := &registry.Config{
		Workspaces: []registry.WorkspaceEntry{{Name: "test", Path: project}},
		Plugins: []registry.PluginEntry{
			{Name: "commanded", Path: commanded}, {Name: "proxied", Path: proxied}, {Name: "disabled", Path: disabled},
		},
	}
	specs, problems := serviceSpecs(config, config.Workspaces)
	if len(problems) != 0 || len(specs) != 1 || specs[0].name != "commanded" || specs[0].instance["port"] != 9000 {
		t.Fatalf("specs = %+v, problems = %v", specs, problems)
	}
	if specs, _ := serviceSpecs(config, nil); len(specs) != 0 {
		t.Fatalf("specs without workspaces = %+v", specs)
	}
}
