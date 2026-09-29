package cli_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/tvdavies/docket/internal/cli"
	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/workspace"
)

// runDocket executes the CLI in a child test process so stdout can be
// captured exactly as a caller sees it.
func runDocket(t *testing.T, dir string, args ...string) (string, string, error) {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestDocketHelperProcess$")
	command.Dir = dir
	encoded, _ := json.Marshal(args)
	command.Env = append(os.Environ(), "DOCKET_TEST_CLI_HELPER="+string(encoded), "DOCKET_HOME=", "DOCKET_CONFIG="+dir+"/no-registry.yaml")
	var stdout, stderr strings.Builder
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return stdout.String(), stderr.String(), err
}

func TestDocketHelperProcess(t *testing.T) {
	raw := os.Getenv("DOCKET_TEST_CLI_HELPER")
	if raw == "" {
		return
	}
	var args []string
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		os.Exit(2)
	}
	os.Args = append([]string{"docket"}, args...)
	os.Exit(cli.Execute())
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
