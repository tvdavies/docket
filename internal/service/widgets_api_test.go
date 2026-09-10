package service_test

import (
	"encoding/json"
	"github.com/tvdavies/docket/internal/bundle"
	"github.com/tvdavies/docket/internal/plugin"
	"github.com/tvdavies/docket/internal/task"
	"github.com/tvdavies/docket/internal/widget"
	"github.com/tvdavies/docket/internal/workspace"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestWidgetPublisherAPIAndReadProjection(t *testing.T) {
	root := t.TempDir()
	ws, err := workspace.Init(root)
	if err != nil {
		t.Fatal(err)
	}
	value, err := task.Create(ws, task.CreateOptions{Title: "Publisher API"})
	if err != nil {
		t.Fatal(err)
	}
	pluginRoot := t.TempDir()
	manifest := "name: example\nversion: 1.0.0\nui:\n  api_version: 2\n  cards:\n    - {type: example/job, title: Job, locations: [board, activity]}\n  reference_resolvers:\n    - {id: example/plan, pattern: '^https://example.test', kinds: [plan]}\n"
	if err := os.WriteFile(filepath.Join(pluginRoot, plugin.ManifestFile), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "registry.yaml")
	t.Setenv("DOCKET_CONFIG", path)
	writeRegistryFixture(t, path, root, pluginRoot)
	appendPluginUse(t, ws)
	manager, server := newBoardServer(t, "test", root)
	defer manager.Stop()
	defer server.Close()
	base := server.URL + "/api/workspaces/test/tasks/" + value.ID
	record := widget.Record{Version: 1, WidgetType: "example/job", TaskID: value.ID, InstanceID: "one", CreatedAt: "2026-09-10T10:00:00Z", Revision: 1, Phase: "created", Fallback: widget.Fallback{Label: "Job", StatusLabel: "Starting", Priority: "active"}}
	post := func(suffix string, r any, want int) map[string]any {
		t.Helper()
		response := sendJSON(t, http.MethodPost, base+suffix, r, nil)
		if response.StatusCode != want {
			t.Fatalf("%s = %d, want %d: %s", suffix, response.StatusCode, want, readBody(t, response))
		}
		var body map[string]any
		decodeResponse(t, response, &body)
		return body
	}
	receipt := post("/widgets/create", record, 201)
	if receipt["cursor"] == "" || receipt["cursor"] == nil {
		t.Fatal("no mutation cursor")
	}
	post("/widgets/create", record, 200)
	changed := record
	changed.Fallback.Label = "Conflict"
	post("/widgets/create", changed, 409)
	bad := record
	bad.TaskID = "TASK-other"
	post("/widgets/create", bad, 400)
	response := sendJSON(t, http.MethodPost, base+"/widgets/create", record, map[string]string{"Origin": "https://evil.test"})
	if response.StatusCode != 403 {
		t.Fatal("origin accepted")
	}
	response.Body.Close()
	response = getJSON(t, server.URL+"/api/workspaces/test/board")
	var board struct {
		Tasks []struct {
			Widgets  []widget.Record `json:"widget_summaries"`
			Revision string          `json:"widget_revision"`
		}
		Plugins []struct {
			API int `json:"api_version"`
		}
	}
	decodeResponse(t, response, &board)
	if len(board.Tasks[0].Widgets) != 1 || board.Tasks[0].Revision == "" || board.Plugins[0].API != 2 {
		t.Fatalf("board %+v", board)
	}
	record.Phase = "finalised"
	record.Revision = 3
	record.Fallback.Summary = "Final durable summary"
	post("/widgets/finalise", record, 200)
	post("/widgets/finalise", record, 200)
	response = getJSON(t, base)
	var detail bundle.Bundle
	decodeResponse(t, response, &detail)
	count := 0
	for _, entry := range detail.Activity {
		if entry.Kind == "widget" {
			count++
		}
	}
	if count != 1 || len(detail.Widgets) != 1 || detail.Widgets[0].Fallback.Summary != "Final durable summary" {
		t.Fatalf("detail %+v", detail)
	}
	live := map[string]any{"kind": "example/job", "task": value.ID, "session": "one", "ttl_ms": 30000, "payload": json.RawMessage(`{"widget_version":1,"revision":4,"data":{"version":1,"value":"late"}}`)}
	response = sendJSON(t, http.MethodPost, server.URL+"/api/workspaces/test/live", live, nil)
	if response.StatusCode != 409 {
		t.Fatalf("terminal ingress = %d: %s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()
}
