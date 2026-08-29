package service_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tvdavies/docket/internal/registry"
	"github.com/tvdavies/docket/internal/service"
	"github.com/tvdavies/docket/internal/task"
	"github.com/tvdavies/docket/internal/workspace"
)

type sseEvent struct {
	Type string
	ID   string
	Data []byte
}

func TestWorkspaceStreamSendsSnapshotThenMutationPatch(t *testing.T) {
	root := t.TempDir()
	ws, err := workspace.Init(root)
	if err != nil {
		t.Fatal(err)
	}
	seed, err := task.Create(ws, task.CreateOptions{Title: "Existing", Status: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	manager, server := newBoardServer(t, "demo", root)
	defer manager.Stop()
	defer server.Close()

	response, events := openSSE(t, server.URL+"/api/workspaces/demo/stream", "")
	defer response.Body.Close()
	initial := nextSSE(t, events)
	if initial.Type != "init" || initial.ID == "" {
		t.Fatalf("initial event = %#v", initial)
	}
	var snapshot struct {
		Workspace string `json:"workspace"`
		Tasks     []struct {
			ID string `json:"id"`
		} `json:"tasks"`
		Cursor string `json:"cursor"`
	}
	if err := json.Unmarshal(initial.Data, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Workspace != "demo" || snapshot.Cursor != initial.ID || len(snapshot.Tasks) != 1 || snapshot.Tasks[0].ID != seed.ID {
		t.Fatalf("snapshot = %#v", snapshot)
	}

	started := time.Now()
	createdResponse := sendJSON(t, http.MethodPost, server.URL+"/api/workspaces/demo/tasks", map[string]any{
		"title": "Live card", "status": "ready",
	}, map[string]string{"X-Docket-Actor": "stream-test"})
	if createdResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d: %s", createdResponse.StatusCode, readBody(t, createdResponse))
	}
	var created struct {
		ID     string `json:"id"`
		Cursor string `json:"cursor"`
	}
	decodeResponse(t, createdResponse, &created)
	patch := nextSSE(t, events)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("patch arrived after %s", elapsed)
	}
	if patch.Type != "patch" || patch.ID == "" || patch.ID != created.Cursor {
		t.Fatalf("patch = %#v cursor=%q", patch, created.Cursor)
	}
	var payload struct {
		Task struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"task"`
	}
	if err := json.Unmarshal(patch.Data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Task.ID != created.ID || payload.Task.Title != "Live card" {
		t.Fatalf("patch payload = %#v", payload)
	}
}

func TestMutationCursorNamesEndOfWholeEventGroup(t *testing.T) {
	root := t.TempDir()
	if _, err := workspace.Init(root); err != nil {
		t.Fatal(err)
	}
	manager, server := newBoardServer(t, "demo", root)
	defer manager.Stop()
	defer server.Close()
	streamResponse, streamEvents := openSSE(t, server.URL+"/api/workspaces/demo/stream", "")
	defer streamResponse.Body.Close()
	_ = nextSSE(t, streamEvents)
	response := sendJSON(t, http.MethodPost, server.URL+"/api/workspaces/demo/tasks", map[string]any{"title": "Grouped", "status": "ready"}, nil)
	var created struct {
		ID string `json:"id"`
	}
	decodeResponse(t, response, &created)
	_ = nextSSE(t, streamEvents)
	response = sendJSON(t, http.MethodPatch, server.URL+"/api/workspaces/demo/tasks/"+created.ID, map[string]any{
		"assignee": "reviewer", "labels": []string{"urgent"}, "status": "done",
	}, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("patch status = %d: %s", response.StatusCode, readBody(t, response))
	}
	var updated struct {
		Cursor string `json:"cursor"`
	}
	decodeResponse(t, response, &updated)
	var last sseEvent
	for range 3 {
		last = nextSSE(t, streamEvents)
	}
	if last.ID != updated.Cursor {
		t.Fatalf("last group cursor = %q, mutation cursor = %q", last.ID, updated.Cursor)
	}
}

func TestWorkspaceStreamTruncationForcesFreshInit(t *testing.T) {
	root := t.TempDir()
	ws, err := workspace.Init(root)
	if err != nil {
		t.Fatal(err)
	}
	manager, server := newBoardServer(t, "demo", root)
	defer manager.Stop()
	defer server.Close()
	streamResponse, streamEvents := openSSE(t, server.URL+"/api/workspaces/demo/stream", "")
	initial := nextSSE(t, streamEvents)
	response := sendJSON(t, http.MethodPost, server.URL+"/api/workspaces/demo/tasks", map[string]any{"title": "Reset me", "status": "ready"}, nil)
	var created struct {
		ID string `json:"id"`
	}
	decodeResponse(t, response, &created)
	beforeReset := nextSSE(t, streamEvents)
	if err := os.Truncate(ws.EventsFile(), 0); err != nil {
		t.Fatal(err)
	}
	response = sendJSON(t, http.MethodPatch, server.URL+"/api/workspaces/demo/tasks/"+created.ID, map[string]any{"status": "done"}, nil)
	response.Body.Close()
	select {
	case event, open := <-streamEvents:
		if open {
			t.Fatalf("reset stream emitted stale-state event instead of disconnecting: %#v", event)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("reset did not disconnect existing stream")
	}
	streamResponse.Body.Close()
	resumed, resumedEvents := openSSE(t, server.URL+"/api/workspaces/demo/stream", beforeReset.ID)
	defer resumed.Body.Close()
	if event := nextSSE(t, resumedEvents); event.Type != "init" || event.ID == initial.ID || event.ID == beforeReset.ID {
		t.Fatalf("stale pre-truncation cursor resumed with %#v", event)
	}
}

func TestWorkspaceStreamPrefixRewriteForcesFreshInit(t *testing.T) {
	root := t.TempDir()
	ws, err := workspace.Init(root)
	if err != nil {
		t.Fatal(err)
	}
	manager, server := newBoardServer(t, "demo", root)
	defer manager.Stop()
	defer server.Close()
	response, streamEvents := openSSE(t, server.URL+"/api/workspaces/demo/stream", "")
	_ = nextSSE(t, streamEvents)
	created := sendJSON(t, http.MethodPost, server.URL+"/api/workspaces/demo/tasks", map[string]any{"title": "Rewrite"}, nil)
	created.Body.Close()
	cursor := nextSSE(t, streamEvents).ID
	data, err := os.ReadFile(ws.EventsFile())
	if err != nil {
		t.Fatal(err)
	}
	data[0] ^= 1
	if err := os.WriteFile(ws.EventsFile(), data, 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case event, open := <-streamEvents:
		if open {
			t.Fatalf("rewritten prefix emitted stale-state event: %#v", event)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("rewritten prefix did not disconnect existing stream")
	}
	response.Body.Close()
	resumed, resumedEvents := openSSE(t, server.URL+"/api/workspaces/demo/stream", cursor)
	defer resumed.Body.Close()
	if event := nextSSE(t, resumedEvents); event.Type != "init" {
		t.Fatalf("rewritten prefix resumed with %#v", event)
	}
}

func TestWorkspaceStreamResumesAfterLastEventID(t *testing.T) {
	root := t.TempDir()
	if _, err := workspace.Init(root); err != nil {
		t.Fatal(err)
	}
	manager, server := newBoardServer(t, "demo", root)
	defer manager.Stop()
	defer server.Close()

	firstResponse, firstEvents := openSSE(t, server.URL+"/api/workspaces/demo/stream", "")
	_ = nextSSE(t, firstEvents) // init
	response := sendJSON(t, http.MethodPost, server.URL+"/api/workspaces/demo/tasks", map[string]any{"title": "First"}, nil)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("first create = %d: %s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()
	firstPatch := nextSSE(t, firstEvents)
	firstResponse.Body.Close()

	response = sendJSON(t, http.MethodPost, server.URL+"/api/workspaces/demo/tasks", map[string]any{"title": "Second"}, nil)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("second create = %d: %s", response.StatusCode, readBody(t, response))
	}
	var second struct {
		ID string `json:"id"`
	}
	decodeResponse(t, response, &second)

	resumedResponse, resumedEvents := openSSE(t, server.URL+"/api/workspaces/demo/stream", firstPatch.ID)
	defer resumedResponse.Body.Close()
	config := nextSSE(t, resumedEvents)
	if config.Type != "config" {
		t.Fatalf("first resumed event = %#v", config)
	}
	patch := nextSSE(t, resumedEvents)
	if patch.Type != "patch" || patch.ID == firstPatch.ID {
		t.Fatalf("resumed patch = %#v", patch)
	}
	var payload struct {
		Task struct {
			ID string `json:"id"`
		} `json:"task"`
	}
	if err := json.Unmarshal(patch.Data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Task.ID != second.ID {
		t.Fatalf("resumed task = %q, want %q", payload.Task.ID, second.ID)
	}
}

func TestWorkspaceStreamInvalidCursorFallsBackToInit(t *testing.T) {
	root := t.TempDir()
	if _, err := workspace.Init(root); err != nil {
		t.Fatal(err)
	}
	manager, server := newBoardServer(t, "demo", root)
	defer manager.Stop()
	defer server.Close()

	response, events := openSSE(t, server.URL+"/api/workspaces/demo/stream", "not-a-cursor")
	defer response.Body.Close()
	if event := nextSSE(t, events); event.Type != "init" || event.ID == "" {
		t.Fatalf("fallback event = %#v", event)
	}
}

func TestLiveIngestExpiresWithoutWritingLedger(t *testing.T) {
	root := t.TempDir()
	ws, err := workspace.Init(root)
	if err != nil {
		t.Fatal(err)
	}
	manager, server := newBoardServer(t, "demo", root)
	defer manager.Stop()
	defer server.Close()
	before, err := os.ReadFile(ws.EventsFile())
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}

	response := sendJSON(t, http.MethodPost, server.URL+"/api/workspaces/demo/live", map[string]any{
		"kind": "dispatch/session", "task": "TASK-0001", "session": "abc", "payload": map[string]any{"state": "working"}, "ttl_ms": 40,
	}, nil)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("live status = %d: %s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()

	streamResponse, streamEvents := openSSE(t, server.URL+"/api/workspaces/demo/stream", "")
	_ = nextSSE(t, streamEvents) // init
	live := nextSSE(t, streamEvents)
	streamResponse.Body.Close()
	if live.Type != "live" || !bytes.Contains(live.Data, []byte(`"working"`)) {
		t.Fatalf("live event = %#v", live)
	}

	time.Sleep(70 * time.Millisecond)
	streamResponse, streamEvents = openSSE(t, server.URL+"/api/workspaces/demo/stream", "")
	defer streamResponse.Body.Close()
	_ = nextSSE(t, streamEvents)
	select {
	case event := <-streamEvents:
		if event.Type == "live" {
			t.Fatalf("expired item replayed: %#v", event)
		}
	case <-time.After(80 * time.Millisecond):
	}
	after, err := os.ReadFile(ws.EventsFile())
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("live ingest changed events.jsonl: before=%q after=%q", before, after)
	}
}

func TestActiveStreamDoesNotBlockWorkspaceReplacement(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	if _, err := workspace.Init(first); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.Init(second); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := service.NewManager(ctx, io.Discard)
	defer manager.Stop()
	manager.SetWorkspaces([]registry.WorkspaceEntry{{Name: "demo", Path: first}})
	waitFor(t, func() bool {
		statuses := manager.Statuses()
		return len(statuses) == 1 && statuses[0].State == "watching"
	})
	server := httptest.NewServer(service.Handler(manager))
	defer server.Close()
	response, events := openSSE(t, server.URL+"/api/workspaces/demo/stream", "")
	_ = nextSSE(t, events)
	replaced := make(chan struct{})
	go func() {
		manager.SetWorkspaces([]registry.WorkspaceEntry{{Name: "demo", Path: second}})
		close(replaced)
	}()
	select {
	case <-replaced:
	case <-time.After(time.Second):
		t.Fatal("active SSE blocked workspace replacement")
	}
	response.Body.Close()
}

func TestLiveIngestValidatesBoundsAndOrigin(t *testing.T) {
	root := t.TempDir()
	if _, err := workspace.Init(root); err != nil {
		t.Fatal(err)
	}
	manager, server := newBoardServer(t, "demo", root)
	defer manager.Stop()
	defer server.Close()
	base := map[string]any{"kind": "session", "payload": map[string]any{"ok": true}, "ttl_ms": 1000}
	oversizedTTL := maps.Clone(base)
	oversizedTTL["ttl_ms"] = int64(^uint64(0) >> 1)
	response := sendJSON(t, http.MethodPost, server.URL+"/api/workspaces/demo/live", oversizedTTL, nil)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("oversized ttl status = %d", response.StatusCode)
	}
	response.Body.Close()
	longTask := maps.Clone(base)
	longTask["task"] = strings.Repeat("x", 201)
	response = sendJSON(t, http.MethodPost, server.URL+"/api/workspaces/demo/live", longTask, nil)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("long task status = %d", response.StatusCode)
	}
	response.Body.Close()
	response = sendJSON(t, http.MethodPost, server.URL+"/api/workspaces/demo/live", base, map[string]string{"Origin": "https://malicious.example"})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin live status = %d", response.StatusCode)
	}
	response.Body.Close()
}

func TestStreamRejectsHostHeaderLikeOtherReads(t *testing.T) {
	root := t.TempDir()
	if _, err := workspace.Init(root); err != nil {
		t.Fatal(err)
	}
	manager, server := newBoardServer(t, "demo", root)
	defer manager.Stop()
	defer server.Close()
	request, err := http.NewRequest(http.MethodGet, server.URL+"/api/workspaces/demo/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "attacker.example"
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d", response.StatusCode)
	}
}

func openSSE(t *testing.T, target, lastEventID string) (*http.Response, <-chan sseEvent) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Accept", "text/event-stream")
	if lastEventID != "" {
		request.Header.Set("Last-Event-ID", lastEventID)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d: %s", response.StatusCode, readBody(t, response))
	}
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") || response.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("stream headers = %#v", response.Header)
	}
	result := make(chan sseEvent, 16)
	go func() {
		defer close(result)
		scanner := bufio.NewScanner(response.Body)
		var event sseEvent
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				if event.Type != "" || event.ID != "" || len(event.Data) != 0 {
					result <- event
					event = sseEvent{}
				}
				continue
			}
			switch {
			case strings.HasPrefix(line, "event: "):
				event.Type = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "id: "):
				event.ID = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "data: "):
				event.Data = append(event.Data, strings.TrimPrefix(line, "data: ")...)
			}
		}
	}()
	return response, result
}

func nextSSE(t *testing.T, events <-chan sseEvent) sseEvent {
	t.Helper()
	select {
	case event, ok := <-events:
		if !ok {
			t.Fatal("stream closed before next event")
		}
		return event
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for stream event")
		return sseEvent{}
	}
}
