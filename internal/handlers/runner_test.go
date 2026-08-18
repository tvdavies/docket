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
