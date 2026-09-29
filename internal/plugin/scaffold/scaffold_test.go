package scaffold

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tvdavies/docket/internal/plugin"
)

func TestCreateWritesValidPlugins(t *testing.T) {
	for name, options := range map[string]Options{
		"default":     {Name: "my-plugin"},
		"widget only": {Name: "widgets", Widget: true},
		"page only":   {Name: "pages", Page: true},
		"service":     {Name: "live_thing", Page: true, Widget: true, Service: true},
	} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), options.Name)
			written, err := Create(dir, options)
			if err != nil {
				t.Fatal(err)
			}
			manifest, err := plugin.Load(dir, "")
			if err != nil {
				t.Fatal(err)
			}
			// Only the service command's interpreter may be missing on a test machine.
			for _, problem := range manifest.Problems() {
				if !strings.Contains(problem, "not on PATH") {
					t.Fatalf("problem: %s", problem)
				}
			}
			hasWidget := len(manifest.UI.Widgets) > 0
			if hasWidget != slices.Contains(written, "bin/"+options.Name) || hasWidget != (manifest.CLI != nil) {
				t.Fatalf("widget %v but written %v, cli %+v", hasWidget, written, manifest.CLI)
			}
			if options.Service != (manifest.Service != nil) || options.Service != slices.Contains(written, "server/index.mjs") {
				t.Fatalf("service %v but manifest %+v, written %v", options.Service, manifest.Service, written)
			}
			if options.Service && manifest.Config.Instance["greeting"].OptionsFrom != "/options/greetings" {
				t.Fatalf("config = %+v", manifest.Config)
			}
			if options.Service && !slices.Contains(manifest.UI.Capabilities, "service.fetch") {
				t.Fatalf("capabilities = %v", manifest.UI.Capabilities)
			}
		})
	}
}

func TestCreateRefusesNonEmptyDirectoriesAndBadNames(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "keep"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(dir, Options{Name: "example"}); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("error = %v", err)
	}
	if _, err := Create(filepath.Join(t.TempDir(), "x"), Options{Name: "Bad Name"}); err == nil {
		t.Fatal("accepted an invalid name")
	}
}
