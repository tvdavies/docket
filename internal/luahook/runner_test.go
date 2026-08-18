package luahook_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/luahook"
	"github.com/tvdavies/docket/internal/task"
	"github.com/tvdavies/docket/internal/workspace"
)

func TestRunProvidesFullLuaLibrariesEventAndSDK(t *testing.T) {
	root := t.TempDir()
	ws, err := workspace.Init(root)
	if err != nil {
		t.Fatal(err)
	}
	created, err := task.Create(ws, task.CreateOptions{Title: "Lua SDK task", Labels: []string{"demo"}})
	if err != nil {
		t.Fatal(err)
	}
	hooks := filepath.Join(root, "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(hooks, "record.lua")
	source := `
assert(type(io) == "table" and type(io.open) == "function")
assert(type(os) == "table" and type(os.execute) == "function")
assert(type(package) == "table" and type(require) == "function")
assert(type(debug) == "table")
assert(type(coroutine) == "table")
assert(type(channel) == "table")
assert(type(string) == "table" and type(table) == "table" and type(math) == "table")

function handle(event, docket)
    assert(event.type == "task.moved")
    assert(event.data.to == "done")
    local current = docket.task.get(event.task)
    assert(current.title == "Lua SDK task")
    assert(current.labels[1] == "demo")
    assert(docket.paths.workspace:match("/.docket$"))
    assert(docket.asset("sound.mp3"):match("/hooks/sound.mp3$"))

    local file = assert(io.open(docket.path("from-io.txt"), "w"))
    file:write(event.task .. "|" .. event.data.to)
    file:close()

    docket.fs.write_atomic("atomic.txt", "atomic:" .. event.actor)
    docket.process.run("sh", {"-c", "printf process > from-process.txt"})

    local moved, from = docket.task.move(event.task, "done")
    assert(from == "backlog" and moved.status == "done")
    assert(docket.task.assign(event.task, "researcher").assignee == "researcher")
    assert(docket.task.label(event.task, {"lua"}, {"demo"}).labels[1] == "lua")
    local waiting = docket.task.wait(event.task, {kind="ci", reason="Awaiting checks", reference="https://example.com/pr"})
    assert(waiting.wait.kind == "ci")
    local referenced, reference = docket.task.reference_add(event.task, "pr", "https://example.com/pr", "Pull request")
    assert(referenced.references[1].kind == "pr" and reference.id:match("^ref%-"))
    assert(docket.task.resume(event.task, waiting.wait.id, "green").wait == nil)
    assert(#docket.task.reference_remove(event.task, reference.id).references == 0)
    assert(docket.task.comment(event.task, "from Lua"):match("%.md$"))
    docket.log.info("handled", event.task)
end
`
	if err := os.WriteFile(script, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("DOCKET_ACTOR", "handler:lua-test")
	t.Setenv("DOCKET_SESSION", "lua-session")
	input := strings.NewReader(`{"seq":1,"type":"task.moved","task":"` + created.ID + `","actor":"tester","data":{"from":"ready","to":"done"}}` + "\n")
	var output bytes.Buffer
	if err := luahook.Run(luahook.Options{
		Context: context.Background(), Workspace: ws, Script: script,
		Input: input, Output: &output, Error: &output,
	}); err != nil {
		t.Fatalf("run Lua hook: %v", err)
	}

	assertFile(t, filepath.Join(root, "from-io.txt"), created.ID+"|done")
	assertFile(t, filepath.Join(root, "atomic.txt"), "atomic:tester")
	assertFile(t, filepath.Join(root, "from-process.txt"), "process")
	updated, err := task.Load(ws, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != "done" || updated.Assignee != "researcher" || len(updated.Labels) != 1 || updated.Labels[0] != "lua" || updated.Wait != nil || len(updated.References) != 0 {
		t.Fatalf("SDK task mutations produced %#v", updated)
	}
	eventLog, err := events.All(ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(eventLog) != 8 || eventLog[0].Actor != "handler:lua-test" {
		t.Fatalf("SDK events = %#v", eventLog)
	}
	if !strings.Contains(output.String(), "info: handled "+created.ID) {
		t.Fatalf("missing SDK log output: %q", output.String())
	}
}

func TestRunRequiresHandleFunction(t *testing.T) {
	root := t.TempDir()
	ws, err := workspace.Init(root)
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "empty.lua")
	if err := os.WriteFile(script, []byte("value = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = luahook.Run(luahook.Options{Workspace: ws, Script: script})
	if err == nil || !strings.Contains(err.Error(), "must define function handle") {
		t.Fatalf("expected missing handle error, got %v", err)
	}
}

func TestRunReportsEventThatFailed(t *testing.T) {
	root := t.TempDir()
	ws, err := workspace.Init(root)
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "fail.lua")
	if err := os.WriteFile(script, []byte("function handle(event, docket) error('boom') end\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = luahook.Run(luahook.Options{
		Workspace: ws, Script: script,
		Input: strings.NewReader("{\"seq\":42,\"type\":\"task.created\"}\n"),
	})
	if err == nil || !strings.Contains(err.Error(), "event 42") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected contextual Lua error, got %v", err)
	}
}

func assertFile(t *testing.T, path, expected string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != expected {
		t.Fatalf("%s = %q, want %q", path, data, expected)
	}
}
