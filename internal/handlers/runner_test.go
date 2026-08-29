package handlers_test

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/handlers"
	"github.com/tvdavies/docket/internal/luahook"
	"github.com/tvdavies/docket/internal/store"
	"github.com/tvdavies/docket/internal/workspace"
)

func newWorkspace(t *testing.T) (*workspace.Workspace, string) {
	t.Helper()
	root := t.TempDir()
	ws, err := workspace.Init(root)
	if err != nil {
		t.Fatal(err)
	}
	return ws, root
}

func writeScript(t *testing.T, root, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "hooks", name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nset -eu\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return "hooks/" + name
}

func appendEvent(t *testing.T, ws *workspace.Workspace, eventType string) {
	t.Helper()
	if err := events.Append(ws, events.Event{Type: eventType, Task: "TASK-0001"}); err != nil {
		t.Fatal(err)
	}
}

func nonEmptyLines(t *testing.T, path string) []string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	defer file.Close()
	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) != "" {
			lines = append(lines, scanner.Text())
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return lines
}

func TestPluginHandlerUsesNamespacedCursorRootAndEnvironment(t *testing.T) {
	ws, projectRoot := newWorkspace(t)
	pluginRoot := t.TempDir()
	output := filepath.Join(projectRoot, "plugin-env.txt")
	t.Setenv("HANDLER_OUTPUT", output)
	if err := os.MkdirAll(filepath.Join(pluginRoot, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
set -eu
printf '%s|%s|%s|%s|%s\n' "$DOCKET_PLUGIN" "$DOCKET_PLUGIN_ROOT" "$DOCKET_HANDLER" "$DOCKET_ACTOR" "$DOCKET_PLUGIN_CONFIG" >> "$HANDLER_OUTPUT"
cat >/dev/null
`
	if err := os.WriteFile(filepath.Join(pluginRoot, "hooks", "record"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	ws.Config.Handlers = map[string]workspace.HandlerConfig{
		"example/record": {
			On: []string{"*"}, Run: "hooks/record", PluginName: "example", PluginRoot: pluginRoot,
			PluginConfig: map[string]any{"endpoint": "local"}, PluginStatusConfig: map[string]map[string]any{"review": {"agent": "worker"}},
		},
	}
	appendEvent(t, ws, events.TaskCreated)
	if failures := handlers.DrainAll(ws, handlers.Options{}); len(failures) != 0 {
		t.Fatalf("plugin drain failed: %v", failures)
	}
	lines := nonEmptyLines(t, output)
	if len(lines) != 1 || !strings.Contains(lines[0], "example|"+pluginRoot+"|example/record|handler:example/record|") ||
		!strings.Contains(lines[0], `"endpoint":"local"`) || !strings.Contains(lines[0], `"status_config"`) {
		t.Fatalf("plugin environment = %q", lines)
	}
	if got := handlers.Cursor(ws, "example/record"); got != 1 {
		t.Fatalf("plugin cursor = %d", got)
	}
	if got := handlers.Cursor(ws, "record"); got != 0 {
		t.Fatalf("workspace cursor collided: %d", got)
	}
}

func TestWorkspaceHandlerClearsInheritedPluginContext(t *testing.T) {
	ws, root := newWorkspace(t)
	output := filepath.Join(root, "plain-plugin-env.txt")
	t.Setenv("HANDLER_OUTPUT", output)
	t.Setenv("DOCKET_PLUGIN", "outer")
	t.Setenv("DOCKET_PLUGIN_ROOT", "/outer")
	t.Setenv("DOCKET_PLUGIN_CONFIG", `{"config":{"leaked":true}}`)
	run := writeScript(t, root, "plain-env", `printf '%s|%s|%s' "$DOCKET_PLUGIN" "$DOCKET_PLUGIN_ROOT" "$DOCKET_PLUGIN_CONFIG" > "$HANDLER_OUTPUT"`+"\n")
	ws.Config.Handlers = map[string]workspace.HandlerConfig{"plain": {On: []string{"*"}, Run: run}}
	appendEvent(t, ws, events.TaskCreated)
	if failures := handlers.DrainAll(ws, handlers.Options{}); len(failures) != 0 {
		t.Fatalf("plain drain failed: %v", failures)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "||" {
		t.Fatalf("workspace handler inherited plugin context: %q", data)
	}
}

func TestDrainDeliversMatchingEventsAndAdvancesCursor(t *testing.T) {
	ws, root := newWorkspace(t)
	output := filepath.Join(root, "received.jsonl")
	t.Setenv("HANDLER_OUTPUT", output)
	run := writeScript(t, root, "record", `cat >> "$HANDLER_OUTPUT"`+"\n"+
		`printf '%s|%s|%s\n' "$DOCKET_HANDLER" "$DOCKET_ACTOR" "$PWD" >> "$HANDLER_OUTPUT.env"`+"\n")
	ws.Config.Handlers = map[string]workspace.HandlerConfig{
		"record": {On: []string{events.TaskMoved}, Run: run},
	}

	appendEvent(t, ws, events.TaskCreated)
	appendEvent(t, ws, events.TaskMoved)
	if failures := handlers.DrainAll(ws, handlers.Options{}); len(failures) != 0 {
		t.Fatalf("unexpected failures: %v", failures)
	}

	lines := nonEmptyLines(t, output)
	if len(lines) != 1 || !strings.Contains(lines[0], `"type":"task.moved"`) {
		t.Fatalf("expected only task.moved, got %q", lines)
	}
	if got := handlers.Cursor(ws, "record"); got != 2 {
		t.Fatalf("expected cursor 2, got %d", got)
	}
	if failures := handlers.DrainAll(ws, handlers.Options{}); len(failures) != 0 {
		t.Fatalf("second drain failed: %v", failures)
	}
	if got := len(nonEmptyLines(t, output)); got != 1 {
		t.Fatalf("already delivered event repeated; got %d lines", got)
	}

	envLine := nonEmptyLines(t, output+".env")
	want := "record|handler:record|" + root
	if len(envLine) != 1 || envLine[0] != want {
		t.Fatalf("handler environment/cwd = %q, want %q", envLine, want)
	}
}

func TestFailureLeavesCursorForRetry(t *testing.T) {
	ws, root := newWorkspace(t)
	output := filepath.Join(root, "retried.jsonl")
	t.Setenv("HANDLER_OUTPUT", output)
	run := writeScript(t, root, "retry", "exit 7\n")
	ws.Config.Handlers = map[string]workspace.HandlerConfig{
		"retry": {On: []string{events.TaskCommented}, Run: run},
	}
	appendEvent(t, ws, events.TaskCommented)

	failures := handlers.DrainAll(ws, handlers.Options{})
	if len(failures) != 1 {
		t.Fatalf("expected one failure, got %v", failures)
	}
	if got := handlers.Cursor(ws, "retry"); got != 0 {
		t.Fatalf("failed handler advanced cursor to %d", got)
	}

	writeScript(t, root, "retry", `cat >> "$HANDLER_OUTPUT"`+"\n")
	if failures := handlers.DrainAll(ws, handlers.Options{}); len(failures) != 0 {
		t.Fatalf("retry failed: %v", failures)
	}
	if got := len(nonEmptyLines(t, output)); got != 1 {
		t.Fatalf("expected one retried event, got %d", got)
	}
	if got := handlers.Cursor(ws, "retry"); got != 1 {
		t.Fatalf("retry cursor = %d, want 1", got)
	}
}

func TestLegacyPlainCursorReplaysAndUpgrades(t *testing.T) {
	ws, root := newWorkspace(t)
	output := filepath.Join(root, "legacy.jsonl")
	t.Setenv("HANDLER_OUTPUT", output)
	run := writeScript(t, root, "legacy", `cat >> "$HANDLER_OUTPUT"`+"\n")
	ws.Config.Handlers = map[string]workspace.HandlerConfig{
		"legacy": {On: []string{"*"}, Run: run},
	}
	appendEvent(t, ws, events.TaskCreated)
	if err := os.MkdirAll(ws.HandlerStateDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	cursorPath := filepath.Join(ws.HandlerStateDir(), "legacy.cursor")
	if err := os.WriteFile(cursorPath, []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if failures := handlers.DrainAll(ws, handlers.Options{}); len(failures) != 0 {
		t.Fatalf("legacy replay failed: %v", failures)
	}
	if got := len(nonEmptyLines(t, output)); got != 1 {
		t.Fatalf("legacy cursor silently skipped history; delivered %d events", got)
	}
	cursor, err := os.ReadFile(cursorPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cursor), `"prefix_hash"`) {
		t.Fatalf("legacy cursor was not upgraded: %s", cursor)
	}
}

func TestRewrittenEventLogResetsHandlerCursor(t *testing.T) {
	ws, root := newWorkspace(t)
	output := filepath.Join(root, "rewritten.jsonl")
	t.Setenv("HANDLER_OUTPUT", output)
	run := writeScript(t, root, "rewritten", `cat >> "$HANDLER_OUTPUT"`+"\n")
	ws.Config.Handlers = map[string]workspace.HandlerConfig{
		"rewritten": {On: []string{"*"}, Run: run},
	}
	if err := events.Append(ws, events.Event{Type: events.TaskCreated, Task: "AAAA"}); err != nil {
		t.Fatal(err)
	}
	if failures := handlers.DrainAll(ws, handlers.Options{}); len(failures) != 0 {
		t.Fatalf("first drain failed: %v", failures)
	}
	original, err := os.ReadFile(ws.EventsFile())
	if err != nil {
		t.Fatal(err)
	}
	rewritten := strings.Replace(string(original), `"task":"AAAA"`, `"task":"BBBB"`, 1)
	if len(rewritten) != len(original) {
		t.Fatalf("test replacement changed file size: %d != %d", len(rewritten), len(original))
	}
	if err := os.WriteFile(ws.EventsFile(), []byte(rewritten), 0o644); err != nil {
		t.Fatal(err)
	}

	if failures := handlers.DrainAll(ws, handlers.Options{}); len(failures) != 0 {
		t.Fatalf("replacement drain failed: %v", failures)
	}
	lines := nonEmptyLines(t, output)
	if len(lines) != 2 || !strings.Contains(lines[1], `"task":"BBBB"`) {
		t.Fatalf("replacement was not safely replayed: %q", lines)
	}
}

func TestHandlerCursorIsIsolatedFromActorInbox(t *testing.T) {
	ws, root := newWorkspace(t)
	output := filepath.Join(root, "isolated.jsonl")
	t.Setenv("HANDLER_OUTPUT", output)
	run := writeScript(t, root, "isolated", `cat >> "$HANDLER_OUTPUT"`+"\n")
	ws.Config.Handlers = map[string]workspace.HandlerConfig{
		"isolated": {On: []string{"*"}, Run: run},
	}
	appendEvent(t, ws, events.TaskCreated)
	if err := events.AdvanceCursor(ws, "handler:isolated", events.Count(ws)); err != nil {
		t.Fatal(err)
	}

	if failures := handlers.DrainAll(ws, handlers.Options{}); len(failures) != 0 {
		t.Fatalf("unexpected failures: %v", failures)
	}
	if got := len(nonEmptyLines(t, output)); got != 1 {
		t.Fatalf("actor inbox cursor acknowledged handler delivery; got %d events", got)
	}
}

func TestConcurrentDrainWaitsForHandlerLock(t *testing.T) {
	ws, root := newWorkspace(t)
	output := filepath.Join(root, "concurrent.jsonl")
	t.Setenv("HANDLER_OUTPUT", output)
	run := writeScript(t, root, "concurrent", `cat >> "$HANDLER_OUTPUT"`+"\n")
	ws.Config.Handlers = map[string]workspace.HandlerConfig{
		"concurrent": {On: []string{"*"}, Run: run},
	}
	appendEvent(t, ws, events.TaskCreated)

	locked := make(chan struct{})
	release := make(chan struct{})
	lockDone := make(chan error, 1)
	lockPath := filepath.Join(ws.HandlerStateDir(), "concurrent.lock")
	go func() {
		lockDone <- store.WithLock(lockPath, func() error {
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked

	drainDone := make(chan []handlers.Failure, 1)
	go func() { drainDone <- handlers.DrainAll(ws, handlers.Options{}) }()
	select {
	case <-drainDone:
		t.Fatal("unrelated concurrent drain skipped a busy handler")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-lockDone; err != nil {
		t.Fatal(err)
	}
	if failures := <-drainDone; len(failures) != 0 {
		t.Fatalf("unexpected failures: %v", failures)
	}
	if got := len(nonEmptyLines(t, output)); got != 1 {
		t.Fatalf("expected event after lock handoff, got %d", got)
	}
}

func TestQueuedDrainRechecksHandlerAfterConfigRemoval(t *testing.T) {
	ws, root := newWorkspace(t)
	output := filepath.Join(root, "removed.jsonl")
	t.Setenv("HANDLER_OUTPUT", output)
	run := writeScript(t, root, "removed", `cat >> "$HANDLER_OUTPUT"`+"\n")
	ws.Config.Handlers = map[string]workspace.HandlerConfig{"removed": {On: []string{"*"}, Run: run}}
	if err := workspace.WriteDeclaredConfig(ws.Root, ws.Config); err != nil {
		t.Fatal(err)
	}
	opened, err := workspace.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	appendEvent(t, opened, events.TaskCreated)

	locked := make(chan struct{})
	release := make(chan struct{})
	lockDone := make(chan error, 1)
	lockPath := filepath.Join(opened.HandlerStateDir(), "removed.lock")
	go func() {
		lockDone <- store.WithLock(lockPath, func() error {
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked
	drainDone := make(chan []handlers.Failure, 1)
	go func() { drainDone <- handlers.DrainAll(opened, handlers.Options{RefreshConfig: true}) }()
	if err := workspace.MutateDeclaredConfig(opened.Root, func(config *workspace.Config) error {
		delete(config.Handlers, "removed")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-lockDone; err != nil {
		t.Fatal(err)
	}
	if failures := <-drainDone; len(failures) != 0 {
		t.Fatalf("queued drain failed: %v", failures)
	}
	if got := len(nonEmptyLines(t, output)); got != 0 {
		t.Fatalf("removed handler delivered %d events", got)
	}
}

func TestNestedDrainSkipsAllBusyHandlerLocks(t *testing.T) {
	ws, root := newWorkspace(t)
	output := filepath.Join(root, "recursive.jsonl")
	t.Setenv("HANDLER_OUTPUT", output)
	t.Setenv("DOCKET_HANDLER_STACK", "first")
	firstRun := writeScript(t, root, "first", `cat >> "$HANDLER_OUTPUT"`+"\n")
	secondRun := writeScript(t, root, "second", `cat >> "$HANDLER_OUTPUT"`+"\n")
	ws.Config.Handlers = map[string]workspace.HandlerConfig{
		"first":  {On: []string{"*"}, Run: firstRun},
		"second": {On: []string{"*"}, Run: secondRun},
	}
	appendEvent(t, ws, events.TaskCreated)

	firstLock := filepath.Join(ws.HandlerStateDir(), "first.lock")
	secondLock := filepath.Join(ws.HandlerStateDir(), "second.lock")
	if err := store.WithLock(firstLock, func() error {
		return store.WithLock(secondLock, func() error {
			started := time.Now()
			if failures := handlers.DrainAll(ws, handlers.Options{}); len(failures) != 0 {
				t.Fatalf("nested drain failed: %v", failures)
			}
			if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
				t.Fatalf("nested drain blocked on another handler lock for %s", elapsed)
			}
			return nil
		})
	}); err != nil {
		t.Fatal(err)
	}
	if got := len(nonEmptyLines(t, output)); got != 0 {
		t.Fatalf("nested drain delivered while handler locks were busy")
	}
}

func TestBareRelativeRunPathUsesProjectRoot(t *testing.T) {
	ws, root := newWorkspace(t)
	output := filepath.Join(root, "bare.jsonl")
	t.Setenv("HANDLER_OUTPUT", output)
	if err := os.WriteFile(filepath.Join(root, "record"), []byte("#!/bin/sh\ncat >> \"$HANDLER_OUTPUT\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ws.Config.Handlers = map[string]workspace.HandlerConfig{
		"record": {On: []string{"*"}, Run: "record"},
	}
	appendEvent(t, ws, events.TaskCreated)

	if failures := handlers.DrainAll(ws, handlers.Options{}); len(failures) != 0 {
		t.Fatalf("unexpected failures: %v", failures)
	}
	if got := len(nonEmptyLines(t, output)); got != 1 {
		t.Fatalf("project-root executable received %d events", got)
	}
}

func TestHandlerTimeoutLeavesCursor(t *testing.T) {
	ws, root := newWorkspace(t)
	run := writeScript(t, root, "slow", "sleep 1\n")
	ws.Config.Handlers = map[string]workspace.HandlerConfig{
		"slow": {On: []string{"*"}, Run: run},
	}
	appendEvent(t, ws, events.TaskCreated)

	failures := handlers.DrainAll(ws, handlers.Options{Timeout: 10 * time.Millisecond})
	if len(failures) != 1 || !strings.Contains(failures[0].Error(), "timed out") {
		t.Fatalf("expected timeout failure, got %v", failures)
	}
	if got := handlers.Cursor(ws, "slow"); got != 0 {
		t.Fatalf("timed-out handler advanced cursor to %d", got)
	}
}

func TestServiceDeliveryStaysPendingForInlineDrain(t *testing.T) {
	ws, root := newWorkspace(t)
	output := filepath.Join(root, "service-delivery.jsonl")
	t.Setenv("HANDLER_OUTPUT", output)
	run := writeScript(t, root, "service-delivery", `cat >> "$HANDLER_OUTPUT"`+"\n")
	ws.Config.Handlers = map[string]workspace.HandlerConfig{
		"service-delivery": {On: []string{"*"}, Run: run, Delivery: "service"},
	}
	appendEvent(t, ws, events.TaskCreated)

	if failures := handlers.DrainAll(ws, handlers.Options{Scope: handlers.ScopeInline}); len(failures) != 0 {
		t.Fatalf("inline drain failed: %v", failures)
	}
	if got := len(nonEmptyLines(t, output)); got != 0 {
		t.Fatalf("service handler ran inline; got %d deliveries", got)
	}
	if got := handlers.Cursor(ws, "service-delivery"); got != 0 {
		t.Fatalf("inline drain advanced service cursor to %d", got)
	}

	if failures := handlers.DrainAll(ws, handlers.Options{Scope: handlers.ScopeAll}); len(failures) != 0 {
		t.Fatalf("service drain failed: %v", failures)
	}
	if got := len(nonEmptyLines(t, output)); got != 1 {
		t.Fatalf("service drain delivered %d events, want 1", got)
	}
}

func TestLuaHandlerRunsInChildAndAppliesExactMatches(t *testing.T) {
	ws, root := newWorkspace(t)
	output := filepath.Join(root, "lua-events.txt")
	t.Setenv("HANDLER_OUTPUT", output)
	t.Setenv("DOCKET_TEST_LUA_HELPER", "1")
	if err := os.MkdirAll(filepath.Join(root, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "hooks", "record.lua")
	source := `
function handle(event, docket)
    local file = assert(io.open(os.getenv("HANDLER_OUTPUT"), "a"))
    file:write(event.task .. "|" .. event.data.to .. "\n")
    file:close()
end
`
	if err := os.WriteFile(script, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	ws.Config.Handlers = map[string]workspace.HandlerConfig{
		"lua-record": {
			On: []string{events.TaskMoved}, Match: map[string]any{"data.to": "done"},
			Lua: "hooks/record.lua",
		},
	}
	if err := events.Append(ws, events.Event{Type: events.TaskMoved, Task: "TASK-0001", Data: map[string]any{"to": "ready"}}); err != nil {
		t.Fatal(err)
	}
	if err := events.Append(ws, events.Event{Type: events.TaskMoved, Task: "TASK-0001", Data: map[string]any{"to": "done"}}); err != nil {
		t.Fatal(err)
	}

	failures := handlers.DrainAll(ws, handlers.Options{LuaCommand: luaHelperCommand()})
	if len(failures) != 0 {
		t.Fatalf("Lua drain failed: %v", failures)
	}
	lines := nonEmptyLines(t, output)
	if len(lines) != 1 || lines[0] != "TASK-0001|done" {
		t.Fatalf("Lua match delivered %q", lines)
	}
	if got := handlers.Cursor(ws, "lua-record"); got != 2 {
		t.Fatalf("Lua handler cursor = %d, want 2", got)
	}
}

func TestLuaOsExitCannotTerminateParentOrSkipBatchEvents(t *testing.T) {
	ws, root := newWorkspace(t)
	output := filepath.Join(root, "exited-events.txt")
	t.Setenv("HANDLER_OUTPUT", output)
	t.Setenv("DOCKET_TEST_LUA_HELPER", "1")
	if err := os.MkdirAll(filepath.Join(root, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	source := `
function handle(event, docket)
    local file = assert(io.open(os.getenv("HANDLER_OUTPUT"), "a"))
    file:write(event.task .. "\n")
    file:close()
    os.exit(0)
end
`
	if err := os.WriteFile(filepath.Join(root, "hooks", "exit.lua"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	ws.Config.Handlers = map[string]workspace.HandlerConfig{
		"lua-exit": {On: []string{"*"}, Lua: "hooks/exit.lua"},
	}
	if err := events.Append(ws, events.Event{Type: events.TaskCreated, Task: "TASK-0001"}); err != nil {
		t.Fatal(err)
	}
	if err := events.Append(ws, events.Event{Type: events.TaskCreated, Task: "TASK-0002"}); err != nil {
		t.Fatal(err)
	}

	if failures := handlers.DrainAll(ws, handlers.Options{LuaCommand: luaHelperCommand()}); len(failures) != 0 {
		t.Fatalf("isolated os.exit failed: %v", failures)
	}
	if lines := nonEmptyLines(t, output); len(lines) != 2 || lines[0] != "TASK-0001" || lines[1] != "TASK-0002" {
		t.Fatalf("os.exit skipped events: %q", lines)
	}
	if got := handlers.Cursor(ws, "lua-exit"); got != 2 {
		t.Fatalf("isolated Lua exit cursor = %d, want 2", got)
	}
}

func TestLuaChildTimeoutLeavesCursor(t *testing.T) {
	ws, root := newWorkspace(t)
	t.Setenv("DOCKET_TEST_LUA_HELPER", "1")
	if err := os.MkdirAll(filepath.Join(root, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "hooks", "loop.lua"), []byte("function handle(event, docket) while true do end end\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws.Config.Handlers = map[string]workspace.HandlerConfig{
		"lua-loop": {On: []string{"*"}, Lua: "hooks/loop.lua"},
	}
	appendEvent(t, ws, events.TaskCreated)

	failures := handlers.DrainAll(ws, handlers.Options{LuaCommand: luaHelperCommand(), Timeout: 100 * time.Millisecond})
	if len(failures) != 1 || !strings.Contains(failures[0].Error(), "timed out") {
		t.Fatalf("expected isolated Lua timeout, got %v", failures)
	}
	if got := handlers.Cursor(ws, "lua-loop"); got != 0 {
		t.Fatalf("timed-out Lua handler advanced cursor to %d", got)
	}
}

func TestLuaHookHelperProcess(t *testing.T) {
	if os.Getenv("DOCKET_TEST_LUA_HELPER") != "1" {
		return
	}
	separator := -1
	for index, arg := range os.Args {
		if arg == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		t.Fatal("missing Lua script argument")
	}
	ws, err := workspace.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := luahook.Run(luahook.Options{
		Workspace: ws, Script: os.Args[separator+1], Input: os.Stdin,
		Output: os.Stdout, Error: os.Stderr,
	}); err != nil {
		t.Fatal(err)
	}
}

func luaHelperCommand() []string {
	return []string{os.Args[0], "-test.run=TestLuaHookHelperProcess", "--"}
}

func TestWildcardHandlerReceivesEveryEvent(t *testing.T) {
	ws, root := newWorkspace(t)
	output := filepath.Join(root, "all.jsonl")
	t.Setenv("HANDLER_OUTPUT", output)
	run := writeScript(t, root, "all", `cat >> "$HANDLER_OUTPUT"`+"\n")
	ws.Config.Handlers = map[string]workspace.HandlerConfig{
		"all": {On: []string{"*"}, Run: run},
	}
	appendEvent(t, ws, events.TaskCreated)
	appendEvent(t, ws, events.TaskAssigned)

	if failures := handlers.DrainAll(ws, handlers.Options{}); len(failures) != 0 {
		t.Fatalf("unexpected failures: %v", failures)
	}
	if got := len(nonEmptyLines(t, output)); got != 2 {
		t.Fatalf("expected two events, got %d", got)
	}
}
