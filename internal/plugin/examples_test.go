package plugin

import (
	"path/filepath"
	"testing"
)

// The shipped examples are documentation; keep them valid.
func TestExamplePluginsValidate(t *testing.T) {
	matches, err := filepath.Glob("../../examples/plugins/*/docket-plugin.yaml")
	if err != nil || len(matches) == 0 {
		t.Fatalf("no example plugins found: %v", err)
	}
	for _, path := range matches {
		manifest, err := Load(filepath.Dir(path), "dev")
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if hash, err := manifest.UIHash(); manifest.UIDir() != "" && (err != nil || hash == "") {
			t.Fatalf("%s: ui hash %q: %v", path, hash, err)
		}
	}
}
