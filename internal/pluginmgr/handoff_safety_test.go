package pluginmgr_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tvdavies/docket/internal/pluginmgr"
	"github.com/tvdavies/docket/internal/workspace"
	"gopkg.in/yaml.v3"
)

func TestHandoffRejectsStateAliasesBeforeLocking(t *testing.T) {
	for _, kind := range []string{"cursor-symlink", "cursor-hardlink", "lock-symlink", "cursor-directory", "receipt-parent"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			f.appendMove("done")
			if failures := f.drain(); len(failures) != 0 {
				t.Fatal(failures)
			}
			before := f.configHash()
			protected := filepath.Join(f.home, "untouched")
			if err := os.WriteFile(protected, []byte("preserve me"), 0o600); err != nil {
				t.Fatal(err)
			}
			cursors := f.open().HandlerStateDir()
			receipt := f.receiptDir("alias")
			var err error
			switch kind {
			case "cursor-symlink":
				err = os.Symlink(protected, filepath.Join(cursors, "stub", "alpha.cursor"))
			case "cursor-hardlink":
				err = os.Link(protected, filepath.Join(cursors, "stub", "alpha.cursor"))
			case "lock-symlink":
				err = os.Symlink(protected, filepath.Join(cursors, "stub", "alpha.lock"))
			case "cursor-directory":
				err = os.Symlink(f.home, filepath.Join(cursors, "stub"))
			case "receipt-parent":
				alias := filepath.Join(f.home, "alias")
				err = os.Symlink(f.home, alias)
				receipt = filepath.Join(alias, "attempt")
			}
			// Leaf aliases require a real namespace directory first.
			if os.IsNotExist(err) && strings.HasPrefix(kind, "cursor-") && kind != "cursor-directory" || os.IsNotExist(err) && kind == "lock-symlink" {
				if e := os.MkdirAll(filepath.Join(cursors, "stub"), 0o700); e != nil {
					t.Fatal(e)
				}
				if kind == "cursor-hardlink" {
					err = os.Link(protected, filepath.Join(cursors, "stub", "alpha.cursor"))
				} else {
					suffix := ".cursor"
					if kind == "lock-symlink" {
						suffix = ".lock"
					}
					err = os.Symlink(protected, filepath.Join(cursors, "stub", "alpha"+suffix))
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			result, err := f.forward(receipt)
			if err == nil || result.Status != pluginmgr.StatusRejected {
				t.Fatalf("alias accepted: %+v %v", result, err)
			}
			if f.configHash() != before {
				t.Fatal("config changed")
			}
			data, err := os.ReadFile(protected)
			if err != nil || string(data) != "preserve me" {
				t.Fatal("aliased file changed")
			}
		})
	}
}

func TestHandoffDetectsObservedOutOfBandDrift(t *testing.T) {
	for _, kind := range []string{"config", "manifest", "script", "ledger", "source"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			f.appendMove("done")
			if failures := f.drain(); len(failures) != 0 {
				t.Fatal(failures)
			}
			pluginmgr.SetCrashHook(func(stage string) {
				if stage != "before_publish" {
					return
				}
				var path string
				switch kind {
				case "config":
					path = filepath.Join(f.root(), "config.yaml")
				case "manifest":
					path = filepath.Join(f.pluginRoot, "docket-plugin.yaml")
				case "script":
					path = filepath.Join(f.pluginRoot, "hooks", "alpha")
				case "ledger":
					path = f.open().EventsFile()
				case "source":
					path = filepath.Join(f.open().HandlerStateDir(), "alpha.cursor")
				}
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if kind == "ledger" || kind == "source" {
					data = []byte("corrupt fixture data\n")
				} else {
					data = append(data, []byte("\n# concurrent edit\n")...)
				}
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
			})
			defer pluginmgr.SetCrashHook(nil)
			result, err := f.forward(f.receiptDir(kind))
			if err == nil || result.Status != pluginmgr.StatusSourceActive {
				t.Fatalf("drift accepted: %+v %v", result, err)
			}
			f.assertLegacyActive()
		})
	}
}

func TestReceiptInspectionValidatesAllEvidenceAndNeverWrites(t *testing.T) {
	for _, kind := range []string{"before-hash", "map", "source-bytes", "pending", "direction", "committed", "snapshot", "active-checkpoint", "active-rewind"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			f.appendMove("done")
			if failures := f.drain(); len(failures) != 0 {
				t.Fatal(failures)
			}
			dir := f.receiptDir("receipt")
			result, err := f.forward(dir)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "prepared.json")
			var data map[string]any
			readJSON(t, path, &data)
			switch kind {
			case "before-hash":
				data["before_config_hash"] = strings.Repeat("0", 64)
			case "map":
				data["map"] = map[string]string{"stub/../../escape": "../../escape"}
			case "source-bytes":
				data["sources"].([]any)[0].(map[string]any)["raw"] = `{"position":0,"prefix_hash":""}`
			case "pending":
				data["ledger"].(map[string]any)["pending"] = map[string]any{}
			case "direction":
				data["direction"] = "sideways"
			case "committed":
				path = filepath.Join(dir, "committed.json")
				data = map[string]any{"version": 1}
			case "snapshot":
				path = filepath.Join(dir, "config-target.yaml")
				data = map[string]any{"invalid": "snapshot"}
			case "active-rewind":
				path = filepath.Join(f.open().HandlerStateDir(), "stub", "alpha.cursor")
				data = map[string]any{"position": 0, "prefix_hash": ""}
			case "active-checkpoint":
				path = filepath.Join(f.open().HandlerStateDir(), "stub", "alpha.cursor")
				data = map[string]any{"position": 99, "prefix_hash": strings.Repeat("0", 64)}
			}
			bytes, err := json.Marshal(data)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, bytes, 0o600); err != nil {
				t.Fatal(err)
			}
			before := f.configHash()
			cursor := f.cursorBytes("stub/alpha")
			inspection, err := f.forward(dir)
			if err == nil || inspection.Status != pluginmgr.StatusNeedsInspection {
				t.Fatalf("%s was trusted: %+v %v (original %s)", kind, inspection, err, result.Status)
			}
			if f.configHash() != before || f.cursorBytes("stub/alpha") != cursor {
				t.Fatal("inspection mutated active state")
			}
		})
	}
}

func TestReceiptInspectionAcceptsOmittedConfigDefaults(t *testing.T) {
	f := newFixture(t)
	var declared map[string]any
	data, err := os.ReadFile(filepath.Join(f.root(), "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(data, &declared); err != nil {
		t.Fatal(err)
	}
	delete(declared, "settings")
	data, err = yaml.Marshal(declared)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.root(), "config.yaml"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	f.appendMove("done")
	if failures := f.drain(); len(failures) != 0 {
		t.Fatal(failures)
	}
	dir := f.receiptDir("defaults")
	if _, err := f.forward(dir); err != nil {
		t.Fatal(err)
	}
	result, err := f.forward(dir)
	if err != nil || result.Status != pluginmgr.StatusAlreadyCommitted {
		t.Fatalf("omitted defaults: %+v %v", result, err)
	}
}

func TestHandoffDoesNotExportInvalidYAMLScalars(t *testing.T) {
	f := newFixture(t)
	if err := os.WriteFile(filepath.Join(f.root(), "config.yaml"), []byte("private-secret-scalar"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := f.forward(f.receiptDir("private"))
	if err == nil {
		t.Fatal("invalid YAML accepted")
	}
	data, _ := json.Marshal(result)
	if strings.Contains(string(data)+err.Error(), "private-secret") {
		t.Fatalf("private input leaked: %s %v", data, err)
	}
}

func TestHandoffRetainsUnrelatedConfigurationWithTemplateExtras(t *testing.T) {
	f := newFixture(t)
	if err := workspace.MutateDeclaredConfig(f.root(), func(c *workspace.Config) error { c.Labels = []string{"unrelated"}; return nil }); err != nil {
		t.Fatal(err)
	}
	f.appendMove("done")
	if failures := f.drain(); len(failures) != 0 {
		t.Fatal(failures)
	}
	if _, err := f.forward(f.receiptDir("f")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.reverse(f.receiptDir("r")); err != nil {
		t.Fatal(err)
	}
	if got := f.declared().Labels; len(got) != 1 || got[0] != "unrelated" {
		t.Fatalf("labels: %v", got)
	}
}
