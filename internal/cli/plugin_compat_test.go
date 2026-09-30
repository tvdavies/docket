package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tvdavies/docket/internal/handlers"
	"github.com/tvdavies/docket/internal/plugin"
	"github.com/tvdavies/docket/internal/workspace"
)

// dispatchShapedManifest mirrors the headless contract the installed Dispatch
// plugin relies on, plus the legacy ui, service and options_from metadata it
// still declares. Its ui directory deliberately does not exist and nothing
// listens on its service URL.
const dispatchShapedManifest = `name: fleet
version: 1.4.0
handlers:
  wake: {on: [task.moved], match: {data.to: in-review}, lua: hooks/wake.lua, delivery: service}
statuses:
  - {name: merge, after: in-review}
config:
  instance:
    endpoint: {type: string, default: "http://127.0.0.1:9"}
  workspace:
    model: {type: string, options_from: /api/models}
  status:
    agent: {type: string}
service: {url: "http://127.0.0.1:9", healthz: /healthz}
cli: {run: bin/docket-fleet}
ui:
  dir: ui
  capabilities: [task.read, service.fetch]
  widgets:
    - {type: fleet/session, title: Session, entry: session.html}
  pages:
    - {id: fleet, title: Fleet, entry: page.html}
`

func TestDispatchShapedPluginKeepsHeadlessContributions(t *testing.T) {
	pluginRoot := t.TempDir()
	files := map[string]string{
		plugin.ManifestFile: dispatchShapedManifest,
		// The hook records its plugin config and the status config for the
		// destination lane as a comment, proving scoped config reaches it.
		"hooks/wake.lua": `function handle(event, docket)
    local lane = docket.plugin.status_config["in-review"] or {}
    docket.task.comment(event.task, "woken model=" .. tostring(docket.plugin.config.model) .. " agent=" .. tostring(lane.agent))
end
`,
		"bin/docket-fleet": "#!/bin/sh\nprintf 'fleet-cli %s %s\\n' \"$DOCKET_PLUGIN\" \"$*\"\n",
	}
	for path, body := range files {
		full := filepath.Join(pluginRoot, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	project := t.TempDir()
	registryPath := filepath.Join(project, "no-registry.yaml")
	registryBody := "workspaces:\n  - {name: proj, path: " + project + "}\nplugins:\n  - name: fleet\n    path: " + pluginRoot + "\n    source: {type: local}\n    version: 1.4.0\n"
	if err := os.WriteFile(registryPath, []byte(registryBody), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKET_CONFIG", registryPath)
	t.Setenv("DOCKET_HOME", "")
	ws, err := workspace.Init(project)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(filepath.Join(ws.Root, "config.yaml"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("plugins:\n  fleet: {}\n"); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()

	docket := func(args ...string) string {
		t.Helper()
		out, stderr, err := runDocket(t, project, args...)
		if err != nil {
			t.Fatalf("docket %v: %v\n%s", args, err, stderr)
		}
		return out
	}

	docket("plugin", "config", "set", "fleet", "model=large")
	docket("plugin", "config", "set", "fleet", "--status", "in-review", "agent=reviewer")
	got := docket("plugin", "config", "get", "fleet", "--json")
	var settings []struct {
		Workspaces map[string]struct {
			Config   map[string]any            `json:"config"`
			Statuses map[string]map[string]any `json:"statuses"`
		} `json:"workspace_values"`
	}
	if err := json.Unmarshal([]byte(got), &settings); err != nil || len(settings) != 1 ||
		settings[0].Workspaces["proj"].Config["model"] != "large" || settings[0].Workspaces["proj"].Statuses["in-review"]["agent"] != "reviewer" {
		t.Fatalf("plugin config get = %s (%v)", got, err)
	}

	docket("new", "--title", "Ship it")
	docket("move", "TASK-0001", "in-review")
	// The contributed status composes into the workspace.
	docket("move", "TASK-0001", "merge")
	docket("run", "--once")

	var bundle struct {
		Status   string `json:"status"`
		Comments []struct {
			Body string `json:"body"`
		} `json:"comments"`
	}
	if err := json.Unmarshal([]byte(docket("show", "TASK-0001", "--json")), &bundle); err != nil {
		t.Fatal(err)
	}
	if bundle.Status != "merge" || len(bundle.Comments) != 1 || bundle.Comments[0].Body != "woken model=large agent=reviewer" {
		t.Fatalf("bundle = %#v", bundle)
	}
	opened, err := workspace.OpenRoot(project)
	if err != nil {
		t.Fatal(err)
	}
	// Plugin handler identities and checkpoints are unchanged.
	if _, ok := opened.Config.Handlers["fleet/wake"]; !ok {
		t.Fatalf("handlers = %v", opened.Config.HandlerNames())
	}
	if _, err := handlers.ReadCheckpoint(opened, "fleet/wake"); err != nil {
		t.Fatalf("plugin checkpoint: %v", err)
	}

	if out := docket("fleet", "status", "--all"); strings.TrimSpace(out) != "fleet-cli fleet status --all" {
		t.Fatalf("passthrough = %q", out)
	}
	if out, stderr, err := runDocket(t, project, "plugin", "validate", pluginRoot); err != nil || !strings.Contains(out, "fleet 1.4.0: ok") || strings.Contains(stderr, "warning") {
		t.Fatalf("validate: %v out=%q stderr=%q", err, out, stderr)
	}
}
