package plugin_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tvdavies/docket/internal/plugin"
)

func writeManifest(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, plugin.ManifestFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestLoadValidatesEveryV1ExtensionPoint(t *testing.T) {
	root := writeManifest(t, `
name: dispatch
version: 1.0.0
requires: {docket: ">=0.6.0"}
handlers:
  wake: {on: [task.moved], lua: hooks/wake.lua, delivery: service}
statuses:
  - {name: merge, after: review}
config:
  instance:
    endpoint: {type: string, default: http://127.0.0.1:7464}
  workspace:
    server_root: {type: string, required: true}
  status:
    agent: {type: string, enum: [planner, implementer]}
service: {url: http://127.0.0.1:7464, healthz: /healthz, auth: none}
cli: {run: bin/docket-dispatch}
ui:
  cards: [{type: dispatch/session, title: Session}]
  reference_resolvers:
    - {id: dispatch/session, pattern: "^https?://127\\.0\\.0\\.1:7464/sessions/", kinds: [session]}
`)
	manifest, err := plugin.Load(root, "0.6.0")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := manifest.ResolveConfig(nil, map[string]any{"server_root": "/srv/dispatch"}, map[string]map[string]any{
		"review": {"agent": "implementer"},
	}, []string{"todo", "review", "merge"})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Values["endpoint"] != "http://127.0.0.1:7464" || resolved.Values["server_root"] != "/srv/dispatch" {
		t.Fatalf("unexpected effective values: %#v", resolved.Values)
	}
	if resolved.Statuses["review"]["agent"] != "implementer" {
		t.Fatalf("unexpected status values: %#v", resolved.Statuses)
	}
}

func TestManifestIsStrictAndRequiresLoopbackService(t *testing.T) {
	for name, body := range map[string]struct{ body, want string }{
		"unknown field":  {"name: example\nversion: 1.0.0\nsurprise: true\n", "field surprise not found"},
		"remote service": {"name: example\nversion: 1.0.0\nservice: {url: http://example.com}\n", "loopback"},
		"engine floor":   {"name: example\nversion: 1.0.0\nrequires: {docket: '>=9.0.0'}\n", "requires docket"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := plugin.Load(writeManifest(t, body.body), "0.6.0")
			if err == nil || !strings.Contains(err.Error(), body.want) {
				t.Fatalf("error = %v, want substring %q", err, body.want)
			}
		})
	}
}

func TestSecretConfigIsDocumentedButNeverStored(t *testing.T) {
	manifest, err := plugin.Load(writeManifest(t, `
name: example
version: 1.0.0
config:
  instance:
    token: {type: string, secret: true, description: EXAMPLE_TOKEN}
`), "dev")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manifest.ResolveInstanceConfig(map[string]any{"token": "plaintext"}); err == nil {
		t.Fatal("secret value was accepted for YAML storage")
	}
	if values, err := manifest.ResolveInstanceConfig(nil); err != nil || len(values) != 0 {
		t.Fatalf("empty secret config = %#v, %v", values, err)
	}
}

func TestStatusConfigAppliesRequiredFieldsAndDefaultsToEveryStatus(t *testing.T) {
	manifest, err := plugin.Load(writeManifest(t, `
name: example
version: 1.0.0
config:
  status:
    agent: {type: string, required: true}
`), "dev")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manifest.ResolveConfig(nil, nil, nil, []string{"todo"}); err == nil || !strings.Contains(err.Error(), "config.status.todo.agent is required") {
		t.Fatalf("missing required status config error = %v", err)
	}

	manifest, err = plugin.Load(writeManifest(t, `
name: example
version: 1.0.0
config:
  status:
    retries: {type: number, default: 2}
`), "dev")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := manifest.ResolveConfig(nil, nil, nil, []string{"todo", "review"})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Statuses["todo"]["retries"] != 2 || resolved.Statuses["review"]["retries"] != 2 {
		t.Fatalf("status defaults = %#v", resolved.Statuses)
	}
}

func TestScopedConfigRejectsUnknownTypesAndStatuses(t *testing.T) {
	manifest, err := plugin.Load(writeManifest(t, `
name: example
version: 1.0.0
config:
  workspace:
    enabled: {type: boolean, required: true}
  status:
    retries: {type: number}
`), "dev")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manifest.ResolveConfig(nil, map[string]any{"enabled": "yes"}, nil, []string{"todo"}); err == nil {
		t.Fatal("expected type mismatch")
	}
	if _, err := manifest.ResolveConfig(nil, map[string]any{"enabled": true}, map[string]map[string]any{"gone": {"retries": 2}}, []string{"todo"}); err == nil {
		t.Fatal("expected unknown status")
	}
}
