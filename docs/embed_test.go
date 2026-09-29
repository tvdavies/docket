package docs

import (
	"strings"
	"testing"
)

func TestTopicsIncludeThePluginGuides(t *testing.T) {
	for _, name := range []string{"plugins", "plugins/authoring", "plugins/ui", "plugin-sdk.d.ts", "cli"} {
		topic, content, ok := Read(name)
		if !ok || strings.TrimSpace(content) == "" || topic.Title == "" {
			t.Fatalf("topic %q: ok=%v title=%q", name, ok, topic.Title)
		}
	}
	if topic, _, ok := Read("docs/plugins/authoring.md"); !ok || topic.Name != "plugins/authoring" {
		t.Fatalf("path form did not resolve: %+v", topic)
	}
	if _, _, ok := Read("missing"); ok {
		t.Fatal("unknown topic resolved")
	}
}
