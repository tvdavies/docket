package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tvdavies/docket/internal/plugin"
	"github.com/tvdavies/docket/internal/registry"
	"github.com/tvdavies/docket/internal/service"
	"github.com/tvdavies/docket/internal/workspace"
)

// settingsFixtureRoot is the harmless plugin the generated settings UI is
// built and browser-tested against. Sharing it with the Go contract tests keeps
// the API and the forms honest about the same schema.
const settingsFixtureRoot = "../../web/tests/fixtures/plugin-settings"

type settingsFixture struct {
	server       *httptest.Server
	registryPath string
	alpha, beta  string // project roots
}

func newSettingsFixture(t *testing.T) *settingsFixture {
	t.Helper()
	fixtureRoot, err := filepath.Abs(settingsFixtureRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plugin.Load(fixtureRoot, "dev"); err != nil {
		t.Fatalf("fixture manifest must be valid: %v", err)
	}
	registryPath := filepath.Join(t.TempDir(), "registry.yaml")
	t.Setenv("DOCKET_CONFIG", registryPath)
	t.Setenv("DOCKET_HOME", "")
	body := "workspaces:\n  - {name: alpha, path: " + filepath.Join(t.TempDir(), "alpha") + "}\n  - {name: beta, path: " + filepath.Join(t.TempDir(), "beta") + "}\nplugins:\n  - name: settings-fixture\n    path: " + fixtureRoot + "\n    source: {type: local}\n    version: 1.2.0\n"
	if err := os.WriteFile(registryPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	config, err := registry.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range config.Workspaces {
		ws, err := workspace.Init(entry.Path)
		if err != nil {
			t.Fatal(err)
		}
		file, err := os.OpenFile(filepath.Join(ws.Root, "config.yaml"), os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.WriteString("plugins:\n  settings-fixture:\n    config: {board_label: " + entry.Name + "}\n"); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	manager := service.NewManager(ctx, io.Discard)
	t.Cleanup(manager.Stop)
	manager.SetWorkspaces(config.Workspaces)
	waitFor(t, func() bool {
		statuses := manager.Statuses()
		return len(statuses) == 2 && statuses[0].State == "watching" && statuses[1].State == "watching"
	})
	server := httptest.NewServer(service.Handler(manager))
	t.Cleanup(server.Close)
	return &settingsFixture{server: server, registryPath: registryPath, alpha: config.Workspaces[0].Path, beta: config.Workspaces[1].Path}
}

func (f *settingsFixture) patch(t *testing.T, path, body string) (int, map[string]any) {
	t.Helper()
	request, _ := http.NewRequest(http.MethodPatch, f.server.URL+path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var payload map[string]any
	_ = json.NewDecoder(response.Body).Decode(&payload)
	return response.StatusCode, payload
}

func (f *settingsFixture) catalogue(t *testing.T) []map[string]any {
	t.Helper()
	response, err := http.Get(f.server.URL + "/api/plugins")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("catalogue status = %d", response.StatusCode)
	}
	var entries []map[string]any
	if err := json.NewDecoder(response.Body).Decode(&entries); err != nil {
		t.Fatal(err)
	}
	return entries
}

func (f *settingsFixture) workspaceValues(t *testing.T, name string) map[string]any {
	t.Helper()
	entries := f.catalogue(t)
	if len(entries) != 1 {
		t.Fatalf("catalogue = %#v", entries)
	}
	return entries[0]["workspace_values"].(map[string]any)[name].(map[string]any)
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestPluginSettingsCatalogueExposesSchemasAndScopedValues(t *testing.T) {
	fixture := newSettingsFixture(t)
	entries := fixture.catalogue(t)
	if len(entries) != 1 || entries[0]["name"] != "settings-fixture" || entries[0]["version"] != "1.2.0" {
		t.Fatalf("catalogue = %#v", entries)
	}
	schemas := entries[0]["schemas"].(map[string]any)
	for _, scope := range []string{"instance", "workspace", "status"} {
		fields := schemas[scope].(map[string]any)
		kinds := map[string]bool{}
		for _, raw := range fields {
			kinds[raw.(map[string]any)["type"].(string)] = true
		}
		for _, kind := range []string{"string", "number", "boolean", "list", "map"} {
			if !kinds[kind] {
				t.Fatalf("schema %s lacks a %s field: %#v", scope, kind, fields)
			}
		}
	}
	secret := schemas["instance"].(map[string]any)["api_token"].(map[string]any)
	if secret["secret"] != true || secret["default"] != nil {
		t.Fatalf("secret declaration = %#v", secret)
	}
	values := entries[0]["instance_values"].(map[string]any)
	// Instance values arrive default-resolved and never include secrets, so
	// the UI cannot tell a stored value from a default here.
	if values["api_base"] != "https://example.invalid" || values["max_parallel"] != float64(2) || values["log_level"] != "info" {
		t.Fatalf("instance defaults = %#v", values)
	}
	if _, present := values["api_token"]; present {
		t.Fatalf("secret leaked through the catalogue: %#v", values)
	}
	if _, present := values["telemetry"]; present {
		t.Fatalf("unset optional instance value should be absent: %#v", values)
	}
	// Workspace and status values are the raw stored keys only: no defaults.
	for _, name := range []string{"alpha", "beta"} {
		ws := fixture.workspaceValues(t, name)
		if !reflect.DeepEqual(ws["config"], map[string]any{"board_label": name}) || !reflect.DeepEqual(ws["statuses"], map[string]any{}) {
			t.Fatalf("workspace %s values = %#v", name, ws)
		}
	}
}

func TestPluginSettingsPatchesPersistLiteralValuesAtEveryScope(t *testing.T) {
	fixture := newSettingsFixture(t)
	instance := `{"values":{"api_base":"","max_parallel":0,"telemetry":false,"log_level":"debug","retry_backoff":5,"allowed_hosts":["10.0.0.1",{"cidr":"10.0.0.0/8"}],"headers":{"X-Trace":"on","nested":{"depth":2}},"greeting":"  spaced  "}}`
	if status, payload := fixture.patch(t, "/api/plugins/settings-fixture/config", instance); status != http.StatusOK {
		t.Fatalf("instance patch = %d %v", status, payload)
	}
	config, err := registry.Load()
	if err != nil {
		t.Fatal(err)
	}
	stored := config.Plugins[0].Config
	if stored["api_base"] != "" || stored["max_parallel"] != 0 || stored["telemetry"] != false || stored["greeting"] != "  spaced  " || stored["retry_backoff"] != 5 {
		t.Fatalf("literal instance values were not stored verbatim: %#v", stored)
	}
	if hosts := stored["allowed_hosts"].([]any); len(hosts) != 2 || hosts[0] != "10.0.0.1" || hosts[1].(map[string]any)["cidr"] != "10.0.0.0/8" {
		t.Fatalf("nested list lost shape: %#v", stored["allowed_hosts"])
	}
	if headers := stored["headers"].(map[string]any); headers["nested"].(map[string]any)["depth"] != 2 {
		t.Fatalf("nested map lost shape: %#v", stored["headers"])
	}
	catalogue := fixture.catalogue(t)[0]["instance_values"].(map[string]any)
	if catalogue["api_base"] != "" || catalogue["max_parallel"] != float64(0) || catalogue["telemetry"] != false {
		t.Fatalf("catalogue does not echo literal empty/zero/false: %#v", catalogue)
	}

	board := `{"values":{"max_parallel":0,"telemetry":false,"auto_assign":true,"review_mode":"strict","priority":3,"reviewers":[],"routing":{}}}`
	if status, payload := fixture.patch(t, "/api/workspaces/alpha/plugins/settings-fixture/config", board); status != http.StatusOK {
		t.Fatalf("board patch = %d %v", status, payload)
	}
	lane := `{"values":{"agent":"","wip_limit":0,"autostart":false,"mode":"auto","channels":["email","slack"],"watchers":["a"],"env":{"K":"v"}}}`
	if status, payload := fixture.patch(t, "/api/workspaces/alpha/plugins/settings-fixture/statuses/in-review", lane); status != http.StatusOK {
		t.Fatalf("lane patch = %d %v", status, payload)
	}
	alpha := fixture.workspaceValues(t, "alpha")
	wantBoard := map[string]any{"board_label": "alpha", "max_parallel": float64(0), "telemetry": false, "auto_assign": true, "review_mode": "strict", "priority": float64(3), "reviewers": []any{}, "routing": map[string]any{}}
	if !reflect.DeepEqual(alpha["config"], wantBoard) {
		t.Fatalf("board values = %#v", alpha["config"])
	}
	wantLane := map[string]any{"agent": "", "wip_limit": float64(0), "autostart": false, "mode": "auto", "channels": []any{"email", "slack"}, "watchers": []any{"a"}, "env": map[string]any{"K": "v"}}
	if !reflect.DeepEqual(alpha["statuses"].(map[string]any)["in-review"], wantLane) {
		t.Fatalf("lane values = %#v", alpha["statuses"])
	}
	// Two-workspace isolation: beta's stored keys are untouched by alpha's saves.
	beta := fixture.workspaceValues(t, "beta")
	if !reflect.DeepEqual(beta["config"], map[string]any{"board_label": "beta"}) || !reflect.DeepEqual(beta["statuses"], map[string]any{}) {
		t.Fatalf("beta changed: %#v", beta)
	}
	// Other lanes on alpha are independent too.
	if _, present := alpha["statuses"].(map[string]any)["backlog"]; present {
		t.Fatalf("unrelated lane received values: %#v", alpha["statuses"])
	}

	// Lists and maps replace the whole stored value; omitted keys are retained.
	replace := `{"values":{"watchers":["b","c"],"env":{"ONLY":"this"}}}`
	if status, payload := fixture.patch(t, "/api/workspaces/alpha/plugins/settings-fixture/statuses/in-review", replace); status != http.StatusOK {
		t.Fatalf("replace patch = %d %v", status, payload)
	}
	replaced := fixture.workspaceValues(t, "alpha")["statuses"].(map[string]any)["in-review"].(map[string]any)
	if !reflect.DeepEqual(replaced["watchers"], []any{"b", "c"}) || !reflect.DeepEqual(replaced["env"], map[string]any{"ONLY": "this"}) || replaced["mode"] != "auto" {
		t.Fatalf("whole-field replacement = %#v", replaced)
	}
	// The effective composition matches manifest.go: board default overrides
	// the instance value for a same-named key, and lane defaults apply
	// independently to every composed lane.
	opened, err := workspace.OpenRoot(fixture.beta)
	if err != nil {
		t.Fatal(err)
	}
	effective := opened.Plugins[0].Effective
	if effective.Values["max_parallel"] != 4 || effective.Values["telemetry"] != false {
		t.Fatalf("effective beta values = %#v", effective.Values)
	}
	for _, status := range opened.Config.Statuses {
		if effective.Statuses[status]["agent"] != "worker" || effective.Statuses[status]["autostart"] != true {
			t.Fatalf("lane %s defaults = %#v", status, effective.Statuses[status])
		}
	}
}

func TestPluginSettingsRejectionsPreserveStoredBytes(t *testing.T) {
	fixture := newSettingsFixture(t)
	alphaConfig := filepath.Join(fixture.alpha, ".docket", "config.yaml")
	registryBefore := readFile(t, fixture.registryPath)
	alphaBefore := readFile(t, alphaConfig)
	cases := []struct{ name, path, body, wantError string }{
		{"instance type", "/api/plugins/settings-fixture/config", `{"values":{"max_parallel":"many"}}`, "must be number"},
		{"instance enum", "/api/plugins/settings-fixture/config", `{"values":{"log_level":"loud"}}`, "must be one of"},
		{"instance unknown key", "/api/plugins/settings-fixture/config", `{"values":{"mystery":1}}`, "is not declared by the plugin"},
		{"instance secret write", "/api/plugins/settings-fixture/config", `{"values":{"api_token":"leak"}}`, "is secret"},
		{"board type", "/api/workspaces/alpha/plugins/settings-fixture/config", `{"values":{"auto_assign":"yes"}}`, "must be boolean"},
		{"board enum", "/api/workspaces/alpha/plugins/settings-fixture/config", `{"values":{"priority":9}}`, "must be one of"},
		{"board unknown key", "/api/workspaces/alpha/plugins/settings-fixture/config", `{"values":{"colour":"red"}}`, "is not declared by the plugin"},
		{"board list kind", "/api/workspaces/alpha/plugins/settings-fixture/config", `{"values":{"reviewers":{"not":"a list"}}}`, "must be list"},
		{"board null is not deletion", "/api/workspaces/alpha/plugins/settings-fixture/config", `{"values":{"board_label":null}}`, "must be string"},
		{"lane type", "/api/workspaces/alpha/plugins/settings-fixture/statuses/backlog", `{"values":{"wip_limit":"3"}}`, "must be number"},
		{"lane compound enum", "/api/workspaces/alpha/plugins/settings-fixture/statuses/backlog", `{"values":{"channels":["slack"]}}`, "must be one of"},
		{"lane unknown key", "/api/workspaces/alpha/plugins/settings-fixture/statuses/backlog", `{"values":{"owner":"x"}}`, "is not declared by the plugin"},
		{"lane unknown status", "/api/workspaces/alpha/plugins/settings-fixture/statuses/nowhere", `{"values":{"agent":"x"}}`, "unknown composed status"},
	}
	for _, testCase := range cases {
		status, payload := fixture.patch(t, testCase.path, testCase.body)
		if status != http.StatusBadRequest || !strings.Contains(payload["error"].(string), testCase.wantError) {
			t.Fatalf("%s: status = %d payload = %v", testCase.name, status, payload)
		}
	}
	// Missing plugin and unmanaged workspace are 404s, malformed bodies 400,
	// wrong media type 415, foreign origin 403.
	if status, _ := fixture.patch(t, "/api/plugins/absent/config", `{"values":{}}`); status != http.StatusNotFound {
		t.Fatalf("missing plugin = %d", status)
	}
	if status, _ := fixture.patch(t, "/api/workspaces/gamma/plugins/settings-fixture/config", `{"values":{}}`); status != http.StatusNotFound {
		t.Fatalf("unmanaged workspace = %d", status)
	}
	if status, _ := fixture.patch(t, "/api/plugins/settings-fixture/config", `{"values":{"greeting":"x"}} trailing`); status != http.StatusBadRequest {
		t.Fatalf("trailing body = %d", status)
	}
	request, _ := http.NewRequest(http.MethodPatch, fixture.server.URL+"/api/plugins/settings-fixture/config", strings.NewReader(`{"values":{}}`))
	request.Header.Set("Content-Type", "text/plain")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("wrong media type = %d", response.StatusCode)
	}
	request, _ = http.NewRequest(http.MethodPatch, fixture.server.URL+"/api/plugins/settings-fixture/config", strings.NewReader(`{"values":{}}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://evil.example")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin = %d", response.StatusCode)
	}

	if !bytes.Equal(readFile(t, fixture.registryPath), registryBefore) {
		t.Fatalf("registry bytes changed after rejections:\n%s", readFile(t, fixture.registryPath))
	}
	if !bytes.Equal(readFile(t, alphaConfig), alphaBefore) {
		t.Fatalf("alpha config bytes changed after rejections:\n%s", readFile(t, alphaConfig))
	}
	if _, err := os.Stat(filepath.Join(fixture.alpha, ".docket", "config.yaml.tmp")); !os.IsNotExist(err) {
		t.Fatal("rejected save left a temporary file")
	}

	// A required key can only be absent from a deliberately incomplete
	// candidate: a third enabling workspace that never stored board_label makes
	// every instance save fail validation against that workspace.
	gamma := filepath.Join(t.TempDir(), "gamma")
	gammaWS, err := workspace.Init(gamma)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(filepath.Join(gammaWS.Root, "config.yaml"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("plugins:\n  settings-fixture: {}\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := registry.Update(func(config *registry.Config) error {
		config.Workspaces = append(config.Workspaces, registry.WorkspaceEntry{Name: "gamma", Path: gamma})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	registryWithGamma := readFile(t, fixture.registryPath)
	status, payload := fixture.patch(t, "/api/plugins/settings-fixture/config", `{"values":{"greeting":"hello"}}`)
	if status != http.StatusBadRequest || !strings.Contains(payload["error"].(string), "board_label is required") {
		t.Fatalf("required absence = %d %v", status, payload)
	}
	if !bytes.Equal(readFile(t, fixture.registryPath), registryWithGamma) {
		t.Fatal("rejected instance save changed the registry")
	}
	if !bytes.Equal(readFile(t, alphaConfig), alphaBefore) {
		t.Fatal("rejected instance save changed an enabling workspace")
	}
}

func TestSettingsRoutesServeTheBoardShell(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := service.NewManager(ctx, io.Discard)
	defer manager.Stop()
	server := httptest.NewServer(service.Handler(manager))
	defer server.Close()
	for _, path := range []string{"/settings/plugins", "/workspaces/demo/settings/plugins", "/workspaces/demo/settings/plugins/statuses/in-review", "/next/settings/plugins"} {
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body := readBody(t, response)
		if response.StatusCode != http.StatusOK || !strings.Contains(body, "<div id=\"root\"></div>") || response.Header.Get("Content-Security-Policy") == "" {
			t.Fatalf("settings shell %s = %d %q", path, response.StatusCode, body)
		}
	}
	for _, path := range []string{"/settings", "/settings/", "/settings/plugins/extra", "/settings/other"} {
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("non-allowlisted path %s = %d", path, response.StatusCode)
		}
	}
}
