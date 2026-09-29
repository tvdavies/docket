package cli_test

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/handlers"
	"github.com/tvdavies/docket/internal/workspace"
)

func handledProject(t *testing.T, script string) (string, *workspace.Workspace) {
	t.Helper()
	dir := t.TempDir()
	ws, err := workspace.Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hooks", "record"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(filepath.Join(ws.Root, "config.yaml"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("handlers:\n  record: {on: [task.created], run: hooks/record, delivery: service}\n"); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	return dir, ws
}

func TestRunOnceDeliversServiceHooksAndReportsFailure(t *testing.T) {
	output := filepath.Join(t.TempDir(), "delivered.jsonl")
	dir, ws := handledProject(t, "#!/bin/sh\ncat >> '"+output+"'\n")
	// A mutation leaves a service-delivered hook for the runner.
	if _, stderr, err := runDocket(t, dir, "new", "--title", "Deliver me"); err != nil {
		t.Fatalf("new: %v: %s", err, stderr)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("service-delivered hook ran inline")
	}
	out, stderr, err := runDocket(t, dir, "run", "--once", "--json")
	if err != nil {
		t.Fatalf("run --once: %v: %s", err, stderr)
	}
	var results []struct {
		EventCount   int    `json:"event_count"`
		HandlerCount int    `json:"handler_count"`
		Error        string `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &results); err != nil || len(results) != 1 || results[0].Error != "" || results[0].HandlerCount != 1 {
		t.Fatalf("results %q: %v", out, err)
	}
	if data, _ := os.ReadFile(output); !strings.Contains(string(data), "task.created") {
		t.Fatalf("hook output = %q", data)
	}

	// A failing hook: non-zero exit, and the event stays pending.
	if err := os.WriteFile(filepath.Join(dir, "hooks", "record"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, stderr, err := runDocket(t, dir, "new", "--title", "Fail me"); err != nil {
		t.Fatalf("new: %v: %s", err, stderr)
	}
	position := handlers.Cursor(ws, "record")
	if _, stderr, err := runDocket(t, dir, "run", "--once"); err == nil || !strings.Contains(stderr, "stay pending") {
		t.Fatalf("failing run --once: err=%v stderr=%q", err, stderr)
	}
	if handlers.Cursor(ws, "record") != position {
		t.Fatal("failed delivery was acknowledged")
	}
}

func TestServeAliasRejectsRemovedListenFlags(t *testing.T) {
	dir, _ := handledProject(t, "#!/bin/sh\ncat >/dev/null\n")
	_, stderr, err := runDocket(t, dir, "serve", "--listen", "127.0.0.1:7463")
	if err == nil || !strings.Contains(stderr, "no longer serves a web board") {
		t.Fatalf("serve --listen: err=%v stderr=%q", err, stderr)
	}
}

// The foreground runner delivers events as they are appended and exits
// cleanly on SIGTERM, terminating an in-flight hook rather than waiting for it.
func TestRunForegroundDeliversAndStopsOnSignal(t *testing.T) {
	output := filepath.Join(t.TempDir(), "delivered.jsonl")
	started := filepath.Join(t.TempDir(), "slow-started")
	script := "#!/bin/sh\nbatch=$(cat)\nprintf '%s\\n' \"$batch\" >> '" + output + "'\ncase \"$batch\" in *SLOW*) touch '" + started + "'; sleep 30;; esac\n"
	dir, ws := handledProject(t, script)
	{
		command := exec.Command(docketBinary(t), "run")
		command.Dir = dir
		command.Env = append(os.Environ(), "DOCKET_HOME=", "DOCKET_CONFIG="+dir+"/no-registry.yaml")
		stderr, err := command.StderrPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		lines := bufio.NewScanner(stderr)
		if !lines.Scan() || !strings.Contains(lines.Text(), "event runner started") {
			t.Fatalf("runner banner = %q", lines.Text())
		}
		go func() {
			for lines.Scan() {
			}
		}()
		waitUntil(t, func() bool { return handlers.Cursor(ws, "record") == events.Count(ws) })
		if err := events.Append(ws, events.Event{Type: events.TaskCreated, Task: "TASK-0001"}); err != nil {
			t.Fatal(err)
		}
		waitUntil(t, func() bool {
			data, _ := os.ReadFile(output)
			return strings.Contains(string(data), "TASK-0001")
		})
		if err := events.Append(ws, events.Event{Type: events.TaskCreated, Task: "SLOW"}); err != nil {
			t.Fatal(err)
		}
		waitUntil(t, func() bool { _, err := os.Stat(started); return err == nil })
		stopAt := time.Now()
		if err := command.Process.Signal(syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		if err := command.Wait(); err != nil {
			t.Fatalf("runner exit: %v", err)
		}
		if elapsed := time.Since(stopAt); elapsed > 5*time.Second {
			t.Fatalf("runner took %s to stop", elapsed)
		}
		// The interrupted batch was not acknowledged; the next run retries it.
		if handlers.Cursor(ws, "record") == events.Count(ws) {
			t.Fatal("interrupted delivery was acknowledged")
		}
	}
}

func waitUntil(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}
