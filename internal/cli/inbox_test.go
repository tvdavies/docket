package cli_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/workspace"
)

// runDocket executes a real docket binary built from this tree, so stdout,
// stderr, exit status and child processes (such as Lua hooks, which re-exec
// the running binary) behave exactly as they do for a caller.
func runDocket(t *testing.T, dir string, args ...string) (string, string, error) {
	t.Helper()
	command := exec.Command(docketBinary(t), args...)
	command.Dir = dir
	command.Env = append(os.Environ(), "DOCKET_HOME=", "DOCKET_CONFIG="+dir+"/no-registry.yaml")
	var stdout, stderr strings.Builder
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return stdout.String(), stderr.String(), err
}

var (
	buildOnce   sync.Once
	builtDocket string
	buildErr    error
)

func docketBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "docket-cli-test-")
		if err != nil {
			buildErr = err
			return
		}
		builtDocket = filepath.Join(dir, "docket")
		output, err := exec.Command("go", "build", "-o", builtDocket, "github.com/tvdavies/docket").CombinedOutput()
		if err != nil {
			buildErr = fmt.Errorf("build docket: %v: %s", err, output)
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return builtDocket
}

func TestMain(m *testing.M) {
	code := m.Run()
	if builtDocket != "" {
		_ = os.RemoveAll(filepath.Dir(builtDocket))
	}
	os.Exit(code)
}

func TestInboxPeekAckContract(t *testing.T) {
	dir := t.TempDir()
	ws, err := workspace.Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = events.Append(ws, events.Event{Type: events.TaskAssigned, Task: "TASK-0001", Assignee: "sal"})

	// Legacy array output is unchanged.
	out, stderr, err := runDocket(t, dir, "inbox", "--actor", "sal", "--json")
	if err != nil {
		t.Fatalf("inbox: %v: %s", err, stderr)
	}
	var legacy []events.Event
	if err := json.Unmarshal([]byte(out), &legacy); err != nil || len(legacy) != 1 {
		t.Fatalf("legacy output %q: %v", out, err)
	}

	out, stderr, err = runDocket(t, dir, "inbox", "--actor", "sal", "--peek", "--json")
	if err != nil {
		t.Fatalf("peek: %v: %s", err, stderr)
	}
	var batch events.InboxBatch
	if err := json.Unmarshal([]byte(out), &batch); err != nil {
		t.Fatalf("peek output %q: %v", out, err)
	}
	if len(batch.Events) != 1 || !strings.HasPrefix(batch.Checkpoint, "dkinbox1.") {
		t.Fatalf("batch = %#v", batch)
	}
	if events.Cursor(ws, "sal") != 0 {
		t.Fatal("peek moved the cursor")
	}

	if _, stderr, err := runDocket(t, dir, "inbox", "ack", "--actor", "other", batch.Checkpoint); err == nil {
		t.Fatal("ack for another actor succeeded")
	} else if !strings.Contains(stderr, "belongs to actor") {
		t.Fatalf("wrong-actor stderr = %q", stderr)
	}
	for i := 0; i < 2; i++ {
		out, stderr, err = runDocket(t, dir, "inbox", "ack", "--actor", "sal", "--json", batch.Checkpoint)
		if err != nil {
			t.Fatalf("ack %d: %v: %s", i, err, stderr)
		}
		var ack events.InboxAck
		if err := json.Unmarshal([]byte(out), &ack); err != nil || ack.Position != 1 || ack.Applied != (i == 0) {
			t.Fatalf("ack %d output %q: %v", i, out, err)
		}
	}

	if _, stderr, err := runDocket(t, dir, "inbox", "--peek", "--mark-read"); err == nil || !strings.Contains(stderr, "none of the others can be") {
		t.Fatalf("combined --peek --mark-read: err=%v stderr=%q", err, stderr)
	}
}
