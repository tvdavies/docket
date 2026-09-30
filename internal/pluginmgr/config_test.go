package pluginmgr_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/tvdavies/docket/internal/plugin"
	"github.com/tvdavies/docket/internal/pluginmgr"
	"github.com/tvdavies/docket/internal/registry"
	"github.com/tvdavies/docket/internal/workspace"
)

// settingsFixtureRoot declares every config field kind at every scope. These
// tests carry the persistence, rejection and concurrency guarantees the
// retired HTTP settings API used to provide.
const settingsFixtureRoot = "testdata/plugin-settings"

type settingsFixture struct {
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
		appendConfig(t, ws, "plugins:\n  settings-fixture:\n    config: {board_label: "+entry.Name+"}\n")
	}
	return &settingsFixture{registryPath: registryPath, alpha: config.Workspaces[0].Path, beta: config.Workspaces[1].Path}
}

func appendConfig(t *testing.T, ws *workspace.Workspace, text string) {
	t.Helper()
	file, err := os.OpenFile(filepath.Join(ws.Root, "config.yaml"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(text); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

// set decodes values from JSON, as the CLI does, so numbers and nested values
// arrive with the same types a caller would send.
func set(t *testing.T, target pluginmgr.ConfigTarget, values string) error {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(values), &decoded); err != nil {
		t.Fatal(err)
	}
	return pluginmgr.SetConfig(target, decoded)
}

func instance() pluginmgr.ConfigTarget {
	return pluginmgr.ConfigTarget{Plugin: "settings-fixture", Scope: pluginmgr.ScopeInstance}
}

func board(path string) pluginmgr.ConfigTarget {
	return pluginmgr.ConfigTarget{Plugin: "settings-fixture", Scope: pluginmgr.ScopeWorkspace, WorkspacePath: path}
}

func lane(path, status string) pluginmgr.ConfigTarget {
	return pluginmgr.ConfigTarget{Plugin: "settings-fixture", Scope: pluginmgr.ScopeStatus, WorkspacePath: path, Status: status}
}

func describe(t *testing.T) pluginmgr.PluginSettings {
	t.Helper()
	entries, err := pluginmgr.DescribeConfig("settings-fixture")
	if err != nil || len(entries) != 1 {
		t.Fatalf("describe = %#v, %v", entries, err)
	}
	// Round-trip through JSON so assertions match what `--json` prints.
	encoded, _ := json.Marshal(entries[0])
	var result pluginmgr.PluginSettings
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestPluginSettingsDescribeExposesSchemasAndScopedValues(t *testing.T) {
	fixture := newSettingsFixture(t)
	entry := describe(t)
	if entry.Name != "settings-fixture" || entry.Version != "1.2.0" {
		t.Fatalf("entry = %#v", entry)
	}
	for scope, fields := range map[string]map[string]plugin.ConfigField{
		"instance": entry.Schemas.Instance, "workspace": entry.Schemas.Workspace, "status": entry.Schemas.Status,
	} {
		kinds := map[string]bool{}
		for _, field := range fields {
			kinds[field.Type] = true
		}
		for _, kind := range []string{"string", "number", "boolean", "list", "map"} {
			if !kinds[kind] {
				t.Fatalf("schema %s lacks a %s field", scope, kind)
			}
		}
	}
	if secret := entry.Schemas.Instance["api_token"]; !secret.Secret || secret.Default != nil {
		t.Fatalf("secret declaration = %#v", secret)
	}
	values := entry.InstanceValues
	// Instance values are default-resolved and never include secrets.
	if values["api_base"] != "https://example.invalid" || values["max_parallel"] != float64(2) || values["log_level"] != "info" {
		t.Fatalf("instance defaults = %#v", values)
	}
	if _, present := values["api_token"]; present {
		t.Fatalf("secret leaked: %#v", values)
	}
	if _, present := values["telemetry"]; present {
		t.Fatalf("unset optional instance value should be absent: %#v", values)
	}
	// Workspace and status values are the raw stored keys only: no defaults.
	for name, path := range map[string]string{"alpha": fixture.alpha, "beta": fixture.beta} {
		ws := entry.Workspaces[name]
		if ws.Path != path || !reflect.DeepEqual(ws.Config, map[string]any{"board_label": name}) || len(ws.Statuses) != 0 {
			t.Fatalf("workspace %s values = %#v", name, ws)
		}
	}
	if _, err := pluginmgr.DescribeConfig("absent"); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("absent plugin describe err = %v", err)
	}
}

func TestPluginSettingsPersistLiteralValuesAtEveryScope(t *testing.T) {
	fixture := newSettingsFixture(t)
	if err := set(t, instance(), `{"api_base":"","max_parallel":0,"telemetry":false,"log_level":"debug","retry_backoff":5,"allowed_hosts":["10.0.0.1",{"cidr":"10.0.0.0/8"}],"headers":{"X-Trace":"on","nested":{"depth":2}},"greeting":"  spaced  "}`); err != nil {
		t.Fatal(err)
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
	described := describe(t).InstanceValues
	if described["api_base"] != "" || described["max_parallel"] != float64(0) || described["telemetry"] != false {
		t.Fatalf("describe does not echo literal empty/zero/false: %#v", described)
	}

	if err := set(t, board(fixture.alpha), `{"max_parallel":0,"telemetry":false,"auto_assign":true,"review_mode":"strict","priority":3,"reviewers":[],"routing":{}}`); err != nil {
		t.Fatal(err)
	}
	if err := set(t, lane(fixture.alpha, "in-review"), `{"agent":"","wip_limit":0,"autostart":false,"mode":"auto","channels":["email","slack"],"watchers":["a"],"env":{"K":"v"}}`); err != nil {
		t.Fatal(err)
	}
	alpha := describe(t).Workspaces["alpha"]
	wantBoard := map[string]any{"board_label": "alpha", "max_parallel": float64(0), "telemetry": false, "auto_assign": true, "review_mode": "strict", "priority": float64(3), "reviewers": []any{}, "routing": map[string]any{}}
	if !reflect.DeepEqual(alpha.Config, wantBoard) {
		t.Fatalf("board values = %#v", alpha.Config)
	}
	wantLane := map[string]any{"agent": "", "wip_limit": float64(0), "autostart": false, "mode": "auto", "channels": []any{"email", "slack"}, "watchers": []any{"a"}, "env": map[string]any{"K": "v"}}
	if !reflect.DeepEqual(alpha.Statuses["in-review"], wantLane) {
		t.Fatalf("lane values = %#v", alpha.Statuses)
	}
	// Two-workspace isolation: beta's stored keys are untouched by alpha's saves.
	beta := describe(t).Workspaces["beta"]
	if !reflect.DeepEqual(beta.Config, map[string]any{"board_label": "beta"}) || len(beta.Statuses) != 0 {
		t.Fatalf("beta changed: %#v", beta)
	}
	if _, present := alpha.Statuses["backlog"]; present {
		t.Fatalf("unrelated lane received values: %#v", alpha.Statuses)
	}

	// Lists and maps replace the whole stored value; omitted keys are retained.
	if err := set(t, lane(fixture.alpha, "in-review"), `{"watchers":["b","c"],"env":{"ONLY":"this"}}`); err != nil {
		t.Fatal(err)
	}
	replaced := describe(t).Workspaces["alpha"].Statuses["in-review"]
	if !reflect.DeepEqual(replaced["watchers"], []any{"b", "c"}) || !reflect.DeepEqual(replaced["env"], map[string]any{"ONLY": "this"}) || replaced["mode"] != "auto" {
		t.Fatalf("whole-field replacement = %#v", replaced)
	}
	// The effective composition: board default overrides the instance value
	// for a same-named key, and lane defaults apply to every composed lane.
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
	cases := []struct {
		name      string
		target    pluginmgr.ConfigTarget
		body      string
		wantError string
	}{
		{"instance type", instance(), `{"max_parallel":"many"}`, "must be number"},
		{"instance enum", instance(), `{"log_level":"loud"}`, "must be one of"},
		{"instance unknown key", instance(), `{"mystery":1}`, "is not declared by the plugin"},
		{"instance secret write", instance(), `{"api_token":"leak"}`, "is secret"},
		{"board type", board(fixture.alpha), `{"auto_assign":"yes"}`, "must be boolean"},
		{"board enum", board(fixture.alpha), `{"priority":9}`, "must be one of"},
		{"board unknown key", board(fixture.alpha), `{"colour":"red"}`, "is not declared by the plugin"},
		{"board list kind", board(fixture.alpha), `{"reviewers":{"not":"a list"}}`, "must be list"},
		{"board null is not deletion", board(fixture.alpha), `{"board_label":null}`, "must be string"},
		{"lane type", lane(fixture.alpha, "backlog"), `{"wip_limit":"3"}`, "must be number"},
		{"lane compound enum", lane(fixture.alpha, "backlog"), `{"channels":["slack"]}`, "must be one of"},
		{"lane unknown key", lane(fixture.alpha, "backlog"), `{"owner":"x"}`, "is not declared by the plugin"},
		{"lane unknown status", lane(fixture.alpha, "nowhere"), `{"agent":"x"}`, "unknown composed status"},
		{"missing plugin", pluginmgr.ConfigTarget{Plugin: "absent", Scope: pluginmgr.ScopeInstance}, `{}`, "not installed"},
		{"not enabled", pluginmgr.ConfigTarget{Plugin: "absent", Scope: pluginmgr.ScopeWorkspace, WorkspacePath: fixture.alpha}, `{}`, "not enabled"},
		{"unknown scope", pluginmgr.ConfigTarget{Plugin: "settings-fixture", Scope: "board"}, `{}`, "unknown config scope"},
		{"status without name", pluginmgr.ConfigTarget{Plugin: "settings-fixture", Scope: pluginmgr.ScopeStatus, WorkspacePath: fixture.alpha}, `{}`, "requires a status"},
	}
	for _, testCase := range cases {
		if err := set(t, testCase.target, testCase.body); err == nil || !strings.Contains(err.Error(), testCase.wantError) {
			t.Fatalf("%s: err = %v, want %q", testCase.name, err, testCase.wantError)
		}
	}
	if !bytes.Equal(readFile(t, fixture.registryPath), registryBefore) {
		t.Fatalf("registry bytes changed after rejections:\n%s", readFile(t, fixture.registryPath))
	}
	if !bytes.Equal(readFile(t, alphaConfig), alphaBefore) {
		t.Fatalf("alpha config bytes changed after rejections:\n%s", readFile(t, alphaConfig))
	}
	if _, err := os.Stat(alphaConfig + ".tmp"); !os.IsNotExist(err) {
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
	appendConfig(t, gammaWS, "plugins:\n  settings-fixture: {}\n")
	if err := registry.Update(func(config *registry.Config) error {
		config.Workspaces = append(config.Workspaces, registry.WorkspaceEntry{Name: "gamma", Path: gamma})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	registryWithGamma := readFile(t, fixture.registryPath)
	if err := set(t, instance(), `{"greeting":"hello"}`); err == nil || !strings.Contains(err.Error(), "board_label is required") {
		t.Fatalf("required absence err = %v", err)
	}
	if !bytes.Equal(readFile(t, fixture.registryPath), registryWithGamma) {
		t.Fatal("rejected instance save changed the registry")
	}
	if !bytes.Equal(readFile(t, alphaConfig), alphaBefore) {
		t.Fatal("rejected instance save changed an enabling workspace")
	}
}

func TestPluginSettingsIgnoreUnrelatedInvalidWorkspace(t *testing.T) {
	fixture := newSettingsFixture(t)
	invalid := t.TempDir()
	invalidWS, err := workspace.Init(invalid)
	if err != nil {
		t.Fatal(err)
	}
	appendConfig(t, invalidWS, "handlers:\n  broken: {on: [task.created]}\n")
	if err := registry.Update(func(config *registry.Config) error {
		config.Workspaces = append(config.Workspaces, registry.WorkspaceEntry{Name: "invalid-unrelated", Path: invalid})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := set(t, instance(), `{"max_parallel":3}`); err != nil {
		t.Fatalf("unrelated invalid workspace blocked instance config: %v", err)
	}
	if err := set(t, board(fixture.alpha), `{"auto_assign":true}`); err != nil {
		t.Fatal(err)
	}
}

func TestPluginSettingsConcurrentInstanceUpdatesPreserveDisjointKeys(t *testing.T) {
	project := t.TempDir()
	ws, err := workspace.Init(project)
	if err != nil {
		t.Fatal(err)
	}
	pluginRoot := t.TempDir()
	var manifest strings.Builder
	manifest.WriteString("name: example\nversion: 1.0.0\nconfig:\n  instance:\n")
	const count = 16
	for index := 0; index < count; index++ {
		fmt.Fprintf(&manifest, "    key%d: {type: number}\n", index)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, plugin.ManifestFile), []byte(manifest.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "registry.yaml")
	t.Setenv("DOCKET_CONFIG", configPath)
	registryBody := "workspaces:\n  - {name: test, path: " + project + "}\nplugins:\n  - name: example\n    path: " + pluginRoot + "\n    source: {type: local}\n    version: 1.0.0\n"
	if err := os.WriteFile(configPath, []byte(registryBody), 0o644); err != nil {
		t.Fatal(err)
	}
	appendConfig(t, ws, "plugins:\n  example: {}\n")

	start := make(chan struct{})
	var group sync.WaitGroup
	failures := make(chan error, count)
	for index := 0; index < count; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			failures <- pluginmgr.SetConfig(pluginmgr.ConfigTarget{Plugin: "example", Scope: pluginmgr.ScopeInstance}, map[string]any{fmt.Sprintf("key%d", index): index})
		}()
	}
	close(start)
	group.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Error(err)
		}
	}
	config, err := registry.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Plugins) != 1 || len(config.Plugins[0].Config) != count {
		t.Fatalf("concurrent instance values = %#v", config.Plugins)
	}
	for index := 0; index < count; index++ {
		if config.Plugins[0].Config[fmt.Sprintf("key%d", index)] != index {
			t.Fatalf("key%d missing from %#v", index, config.Plugins[0].Config)
		}
	}
}
