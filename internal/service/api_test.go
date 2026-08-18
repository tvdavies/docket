package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tvdavies/docket/internal/bundle"
	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/registry"
	"github.com/tvdavies/docket/internal/service"
	"github.com/tvdavies/docket/internal/task"
	"github.com/tvdavies/docket/internal/workspace"
)

func TestBoardAPICreatesReadsAndUpdatesTasks(t *testing.T) {
	root := t.TempDir()
	ws, err := workspace.Init(root)
	if err != nil {
		t.Fatal(err)
	}
	seed, err := task.Create(ws, task.CreateOptions{Title: "Existing card", Status: "ready"})
	if err != nil {
		t.Fatal(err)
	}

	manager, server := newBoardServer(t, "demo", root)
	defer manager.Stop()
	defer server.Close()

	response := getJSON(t, server.URL+"/api/workspaces/demo/board")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("board status = %d", response.StatusCode)
	}
	var board struct {
		Workspace string   `json:"workspace"`
		Statuses  []string `json:"statuses"`
		Tasks     []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"tasks"`
	}
	decodeResponse(t, response, &board)
	if board.Workspace != "demo" || len(board.Statuses) == 0 || len(board.Tasks) != 1 || board.Tasks[0].ID != seed.ID {
		t.Fatalf("board = %#v", board)
	}

	response = sendJSON(t, http.MethodPost, server.URL+"/api/workspaces/demo/tasks", map[string]any{
		"title": "Created on board", "description": "Initial context", "status": "ready", "labels": []string{"feature", "feature"},
	}, map[string]string{"X-Docket-Actor": "web-ui"})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d: %s", response.StatusCode, readBody(t, response))
	}
	var created bundle.Bundle
	decodeResponse(t, response, &created)
	if created.ID == "" || created.Title != "Created on board" {
		t.Fatalf("created = %#v", created)
	}

	response = sendJSON(t, http.MethodPatch, server.URL+"/api/workspaces/demo/tasks/"+created.ID, map[string]any{
		"title": "Edited on board", "description": "Updated context", "assignee": "reviewer", "labels": []string{"urgent"}, "status": "done",
	}, map[string]string{"X-Docket-Actor": "web-ui"})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d: %s", response.StatusCode, readBody(t, response))
	}
	var updated bundle.Bundle
	decodeResponse(t, response, &updated)
	if updated.Title != "Edited on board" || updated.Status != "done" || updated.Assignee != "reviewer" || len(updated.Labels) != 1 || updated.Labels[0] != "urgent" {
		t.Fatalf("updated = %#v", updated)
	}

	response = sendJSON(t, http.MethodPost, server.URL+"/api/workspaces/demo/tasks/"+created.ID+"/comments", map[string]any{
		"text": "Added from the board",
	}, map[string]string{"X-Docket-Actor": "web-ui"})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("comment status = %d: %s", response.StatusCode, readBody(t, response))
	}
	var commented bundle.Bundle
	decodeResponse(t, response, &commented)
	if len(commented.Comments) != 1 || commented.Comments[0].Author != "web-ui" || commented.Comments[0].Body != "Added from the board" {
		t.Fatalf("comments = %#v", commented.Comments)
	}

	log, err := events.All(ws)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{events.TaskCreated, events.TaskAssigned, events.TaskLabeled, events.TaskMoved, events.TaskCommented}
	if len(log) != len(want) {
		t.Fatalf("event log = %#v", log)
	}
	for index, eventType := range want {
		if log[index].Type != eventType || log[index].Actor != "web-ui" {
			t.Fatalf("event %d = %#v", index, log[index])
		}
	}
}

func TestBoardAPIValidatesBeforeMutationAndProtectsWrites(t *testing.T) {
	root := t.TempDir()
	ws, err := workspace.Init(root)
	if err != nil {
		t.Fatal(err)
	}
	created, err := task.Create(ws, task.CreateOptions{Title: "Must remain", Status: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	manager, server := newBoardServer(t, "demo", root)
	defer manager.Stop()
	defer server.Close()

	response := sendJSON(t, http.MethodPatch, server.URL+"/api/workspaces/demo/tasks/"+created.ID, map[string]any{
		"title": "Must not persist", "status": "not-configured",
	}, nil)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid status response = %d", response.StatusCode)
	}
	unchanged, err := task.Load(ws, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Title != "Must remain" || unchanged.Status != "ready" {
		t.Fatalf("invalid request partially mutated task: %#v", unchanged)
	}

	response = sendJSON(t, http.MethodPatch, server.URL+"/api/workspaces/demo/tasks/"+created.ID, map[string]any{"unexpected": true}, nil)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown field response = %d", response.StatusCode)
	}

	request, err := http.NewRequest(http.MethodPatch, server.URL+"/api/workspaces/demo/tasks/"+created.ID, strings.NewReader(`{"status":"done"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://malicious.example")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin response = %d", response.StatusCode)
	}
	response.Body.Close()

	request, err = http.NewRequest(http.MethodPatch, server.URL+"/api/workspaces/demo/tasks/"+created.ID, strings.NewReader(`{"status":"done"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "attacker.example"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://attacker.example")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("DNS-rebinding Host response = %d", response.StatusCode)
	}
	response.Body.Close()

	request, err = http.NewRequest(http.MethodGet, server.URL+"/api/workspaces", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "attacker.example"
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("DNS-rebinding read response = %d", response.StatusCode)
	}
	response.Body.Close()

	request, err = http.NewRequest(http.MethodPost, server.URL+"/api/workspaces/demo/tasks", strings.NewReader(`{"title":"wrong content type"}`))
	if err != nil {
		t.Fatal(err)
	}
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("content-type response = %d", response.StatusCode)
	}
	response.Body.Close()

	response = getJSON(t, server.URL+"/api/workspaces/unknown/board")
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown workspace response = %d", response.StatusCode)
	}
	response.Body.Close()

	response = getJSON(t, server.URL+"/api/workspaces/demo/tasks/%2e%2e%2foutside")
	if response.StatusCode == http.StatusOK {
		t.Fatal("encoded task traversal unexpectedly succeeded")
	}
	response.Body.Close()
}

func TestSlowRequestBodyDoesNotHoldWorkspaceLease(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
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

	reader, writer := io.Pipe()
	request := httptest.NewRequest(http.MethodPost, "/api/workspaces/demo/tasks", reader)
	request.Host = "127.0.0.1:7463"
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	requestDone := make(chan struct{})
	go func() {
		service.Handler(manager).ServeHTTP(response, request)
		close(requestDone)
	}()
	if _, err := writer.Write([]byte("{")); err != nil {
		t.Fatal(err)
	}

	replaced := make(chan struct{})
	go func() {
		manager.SetWorkspaces([]registry.WorkspaceEntry{{Name: "demo", Path: second}})
		close(replaced)
	}()
	select {
	case <-replaced:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("slow request body held the manager workspace lease")
	}
	_ = writer.Close()
	select {
	case <-requestDone:
	case <-time.After(time.Second):
		t.Fatal("request did not stop after body closed")
	}
}

func TestBoardAssetsAreEmbeddedWithStrictCSP(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := service.NewManager(ctx, io.Discard)
	defer manager.Stop()
	server := httptest.NewServer(service.Handler(manager))
	defer server.Close()

	response, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, response)
	if response.StatusCode != http.StatusOK || !strings.Contains(body, "Kanban board") || !strings.Contains(body, "/assets/app.js") {
		t.Fatalf("index response = %d %q", response.StatusCode, body)
	}
	csp := response.Header.Get("Content-Security-Policy")
	if strings.Contains(csp, "unsafe-inline") || !strings.Contains(csp, "script-src 'self'") {
		t.Fatalf("unexpected CSP: %s", csp)
	}

	response, err = http.Get(server.URL + "/assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	asset := readBody(t, response)
	if response.StatusCode != http.StatusOK || !strings.Contains(asset, "async function loadBoard") {
		t.Fatalf("JS asset response = %d", response.StatusCode)
	}
}

func newBoardServer(t *testing.T, name, root string) (*service.Manager, *httptest.Server) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	manager := service.NewManager(ctx, io.Discard)
	manager.SetWorkspaces([]registry.WorkspaceEntry{{Name: name, Path: root}})
	waitFor(t, func() bool {
		statuses := manager.Statuses()
		return len(statuses) == 1 && statuses[0].State == "watching"
	})
	return manager, httptest.NewServer(service.Handler(manager))
}

func sendJSON(t *testing.T, method, target string, value any, headers map[string]string) *http.Response {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(method, target, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	for key, header := range headers {
		request.Header.Set(key, header)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func getJSON(t *testing.T, target string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func decodeResponse(t *testing.T, response *http.Response, destination any) {
	t.Helper()
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(destination); err != nil {
		t.Fatal(err)
	}
}

func readBody(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
