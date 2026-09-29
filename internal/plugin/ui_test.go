package plugin_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tvdavies/docket/internal/plugin"
)

func TestFrameUIManifestValidation(t *testing.T) {
	base := "name: demo\nversion: 1.0.0\n"
	service := "service: {url: 'http://127.0.0.1:9000'}\n"
	for _, test := range []struct {
		name, body, want string
	}{
		{"full", service + "ui:\n  dir: ui\n  capabilities: [task.read, service.fetch]\n  widgets:\n    - {type: demo/job, title: Job, entry: job.html, slots: [board, activity]}\n  panels:\n    - {id: logs, title: Logs, entry: panels/logs.html}\n  pages:\n    - {id: fleet, title: Fleet, entry: fleet.html}\n", ""},
		{"presentation only", "ui:\n  widgets:\n    - {type: demo/job, title: Job}\n", ""},
		{"namespace", "ui:\n  widgets:\n    - {type: other/job, title: Job}\n", "namespaced"},
		{"duplicate with card", "ui:\n  api_version: 2\n  cards: [{type: demo/job, title: J, locations: [board]}]\n  widgets:\n    - {type: demo/job, title: Job}\n", "duplicated"},
		{"slot", "ui:\n  widgets:\n    - {type: demo/job, title: Job, slots: [sidebar]}\n", "slot"},
		{"entry without dir", "ui:\n  widgets:\n    - {type: demo/job, title: Job, entry: job.html}\n", "requires ui.dir"},
		{"entry traversal", "ui:\n  dir: ui\n  pages:\n    - {id: fleet, title: Fleet, entry: ../secret.html}\n", "clean relative"},
		{"entry query", "ui:\n  dir: ui\n  pages:\n    - {id: fleet, title: Fleet, entry: 'a.html?x=1'}\n", "clean relative"},
		{"dir traversal", "ui:\n  dir: ../outside\n", "inside the plugin"},
		{"page id", "ui:\n  dir: ui\n  pages:\n    - {id: Fleet/One, title: Fleet, entry: a.html}\n", "must be unique"},
		{"capability", "ui:\n  capabilities: [task.delete]\n", "unknown"},
		{"service capability", "ui:\n  capabilities: [service.stream]\n", "requires a service"},
		{"resolver endpoint", "ui:\n  reference_resolvers:\n    - {id: demo/r, pattern: x, endpoint: /resolve}\n", "requires a service"},
		{"resolver endpoint path", service + "ui:\n  reference_resolvers:\n    - {id: demo/r, pattern: x, endpoint: ../resolve}\n", "service path"},
		{"unknown field", "ui:\n  widgets:\n    - {type: demo/job, title: Job, mount: x}\n", "field mount not found"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := plugin.Load(writeManifest(t, base+test.body), "dev")
			if test.want == "" && err != nil {
				t.Fatal(err)
			}
			if test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestDeclaredWidgetsIncludeLegacyV2CardsAndDefaultSlots(t *testing.T) {
	ui := plugin.UI{APIVersion: 2, Cards: []plugin.Card{{Type: "demo/card", Title: "Card", Locations: []string{"activity"}}}, Widgets: []plugin.Widget{{Type: "demo/job", Title: "Job"}}}
	got := ui.DeclaredWidgets()
	if len(got) != 2 || strings.Join(got[0].Slots, ",") != "board,activity" || strings.Join(got[1].Slots, ",") != "activity" {
		t.Fatalf("widgets %+v", got)
	}
	if !ui.DeclaresWidget("demo/card") || (plugin.UI{Cards: ui.Cards}).DeclaresWidget("demo/card") {
		t.Fatal("v1 cards are not ledger widgets")
	}
}

func TestUIHashTracksDirectoryChanges(t *testing.T) {
	root := writeManifest(t, "name: demo\nversion: 1.0.0\nui: {dir: ui}\n")
	manifest, err := plugin.Load(root, "dev")
	if err != nil {
		t.Fatal(err)
	}
	if hash, err := manifest.UIHash(); err != nil || hash != "" {
		t.Fatalf("missing dir hash = %q %v", hash, err)
	}
	file := filepath.Join(root, "ui", "index.html")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := manifest.UIHash()
	if err != nil || len(first) != 16 {
		t.Fatalf("hash = %q %v", first, err)
	}
	if again, _ := manifest.UIHash(); again != first {
		t.Fatal("hash is unstable")
	}
	if err := os.WriteFile(file, []byte("two!"), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Second)
	_ = os.Chtimes(file, later, later)
	if next, _ := manifest.UIHash(); next == first {
		t.Fatal("hash ignored an edit")
	}
}
